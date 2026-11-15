// Package hostagentapi is the host-agent-api Component: it pairs with the
// Host Agents the Operator adds on LXCs and VMs, by the Workload's VMID,
// and reaches them over HTTPS, pinned to the certificate each showed at
// pairing, with the token both keep. It refuses every operation but
// Update Agent on an Agent of another Release, so the Agents never drift
// from the core.
package hostagentapi

import (
	"bufio"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/jobproto"
	pb "github.com/itsKontra/ddash/components/host-agent-api/hostagentapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// PairInterval is how often an enrolling Agent is tried.
const PairInterval = 10 * time.Second

// downloads fetches the Host Agent's SHA256SUMS from ddash; tests swap it.
var downloads = &http.Client{Timeout: 20 * time.Second}

// Definition is host-agent-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.HostAgentApiConfig]{
		Manifest:   manifest,
		Service:    &pb.HostAgentApiService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*pb.HostAgentApiConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedHostAgentApiServiceServer
	db        *sql.DB
	log       *slog.Logger
	publicURL string

	// reads caches every paired Agent's info and packages.
	reads *sdk.Cache[[]reading]

	// pairing is held while one Agent is paired.
	pairing sync.Mutex

	mu sync.Mutex
	// pairErr is why the last pairing attempt of an enrolling Agent failed.
	pairErr map[uint32]string
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *pb.HostAgentApiConfig) error {
	c.db, c.log, c.publicURL = env.DB, env.Log, strings.TrimRight(env.PublicURL, "/")
	c.pairErr = map[uint32]string{}
	c.reads = sdk.Cached(sdk.TTLFast, c.readAll)
	sdk.Every(ctx, PairInterval, c.pairPending)
	return nil
}

// Health is degraded while a paired Agent can't be read or runs another
// Release; ok without any Agent.
func (c *component) Health(ctx context.Context) sdk.Health {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return sdk.Health{State: sdk.Down, Message: err.Error()}
	}
	var problems []string
	for _, r := range rs {
		if r.state != pb.AgentState_AGENT_STATE_PAIRED && r.state != pb.AgentState_AGENT_STATE_ENROLLING {
			problems = append(problems, fmt.Sprintf("%d: %s", r.vmid, r.message))
		}
	}
	if len(problems) > 0 {
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(problems, "; ")}
	}
	return sdk.Health{State: sdk.OK}
}

// row is one Agent as stored.
type row struct {
	vmid                                uint32
	name, url, code, token, fingerprint string
	added, paired                       time.Time
}

func (c *component) rows(ctx context.Context) ([]row, error) {
	rs, err := c.db.QueryContext(ctx, `SELECT vmid, name, url, code, token, fingerprint, added, paired FROM agent ORDER BY vmid`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		var added, paired string
		if err := rs.Scan(&r.vmid, &r.name, &r.url, &r.code, &r.token, &r.fingerprint, &added, &paired); err != nil {
			return nil, err
		}
		r.added, _ = time.Parse(time.RFC3339, added)
		r.paired, _ = time.Parse(time.RFC3339, paired)
		out = append(out, r)
	}
	return out, rs.Err()
}

func (c *component) row(ctx context.Context, vmid uint32) (row, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return row{}, err
	}
	for _, r := range rows {
		if r.vmid == vmid {
			return r, nil
		}
	}
	return row{}, status.Errorf(codes.NotFound, "Workload %d has no Host Agent", vmid)
}

func (r row) client() *agent.Client { return agent.Dial(r.url, r.token, r.fingerprint) }

// reading is one Agent as last read.
type reading struct {
	row
	state    pb.AgentState
	message  string
	info     agent.Info
	packages agent.PackagesResponse
}

// readTimeout bounds one Agent's read, so a dead one can't hold a getter.
const readTimeout = 5 * time.Second

// readAll reads every paired Agent in parallel; one that fails is listed
// with why, so readAll fails only when the DB does.
func (c *component) readAll(ctx context.Context) ([]reading, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]reading, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		out[i].row = r
		if r.token == "" {
			out[i].state = pb.AgentState_AGENT_STATE_ENROLLING
			c.mu.Lock()
			out[i].message = c.pairErr[r.vmid]
			c.mu.Unlock()
			continue
		}
		wg.Go(func() { c.read(ctx, &out[i]) })
	}
	wg.Wait()
	return out, nil
}

func (c *component) read(ctx context.Context, r *reading) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	cl := r.client()
	if err := cl.Get(ctx, agent.PathInfo, nil, &r.info); err != nil {
		r.state, r.message = stateOf(err)
		return
	}
	if r.info.Release != sdk.Release {
		r.state = pb.AgentState_AGENT_STATE_OUTDATED
		r.message = fmt.Sprintf("it runs Release %s and ddash runs %s: update the Agent", r.info.Release, sdk.Release)
		return
	}
	if err := cl.Get(ctx, agent.PathPackages, nil, &r.packages); err != nil {
		r.state, r.message = stateOf(err)
		return
	}
	r.state = pb.AgentState_AGENT_STATE_PAIRED
}

// stateOf is the state an Agent is in when a call to it failed with err.
func stateOf(err error) (pb.AgentState, string) {
	switch {
	case errors.Is(err, agent.ErrFingerprint):
		return pb.AgentState_AGENT_STATE_FINGERPRINT_CHANGED, "its certificate isn't the one pinned at pairing: if you reinstalled it, pair it again"
	case agent.CodeOf(err) == agent.CodeBadToken || agent.CodeOf(err) == agent.CodeUnpaired:
		return pb.AgentState_AGENT_STATE_REFUSED, "it refuses ddash's token: its state was reset, so pair it again"
	}
	return pb.AgentState_AGENT_STATE_UNREACHABLE, "unreachable: " + err.Error()
}

func (c *component) ListAgents(ctx context.Context, _ *pb.ListAgentsRequest) (*pb.ListAgentsResponse, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListAgentsResponse{}
	for _, r := range rs {
		out.Agents = append(out.Agents, r.proto())
	}
	return out, nil
}

func (r reading) proto() *pb.Agent {
	a := &pb.Agent{
		Vmid: r.vmid, Name: r.name, Url: r.url, State: r.state, Message: r.message,
		Fingerprint: r.fingerprint, Added: timestamppb.New(r.added),
		Release: r.info.Release, Hostname: r.info.Hostname, Arch: r.info.Arch,
	}
	if o := r.info.OS; o != nil {
		a.Os = &pb.OS{Id: o.ID, Version: o.Version, Pretty: o.Pretty, PackageManager: o.PackageManager, Init: o.Init, RebootRequired: o.RebootRequired}
	}
	if !r.paired.IsZero() {
		a.Paired = timestamppb.New(r.paired)
	}
	return a
}

func (c *component) AddAgent(ctx context.Context, req *pb.AddAgentRequest) (*pb.AddAgentResponse, error) {
	var errs sdk.FieldErrors
	if req.GetVmid() == 0 {
		errs.Add("vmid", "is the Workload's VMID")
	}
	if u, err := url.Parse(req.GetUrl()); err != nil || u.Scheme != "https" || u.Hostname() == "" {
		errs.Add("url", "must be the Agent's https URL, like https://198.18.2.10:%d", agent.HostPort)
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	code := agent.NewCode()
	_, err := c.db.ExecContext(ctx, `INSERT INTO agent (vmid, name, url, code, added) VALUES (?, ?, ?, ?, ?)`,
		req.GetVmid(), req.GetName(), strings.TrimRight(req.GetUrl(), "/"), code, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		if migrate.IsTaken(err) {
			return nil, status.Errorf(codes.AlreadyExists, "Workload %d already has a Host Agent", req.GetVmid())
		}
		return nil, err
	}
	c.reads.Invalidate()
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	return &pb.AddAgentResponse{Agent: reading{row: r, state: pb.AgentState_AGENT_STATE_ENROLLING}.proto(), Setup: c.setup(r)}, nil
}

func (c *component) GetAgentSetup(ctx context.Context, req *pb.GetAgentSetupRequest) (*pb.GetAgentSetupResponse, error) {
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	return &pb.GetAgentSetupResponse{Setup: c.setup(r)}, nil
}

// setup is what installs r's Agent: install.sh from this ddash, which
// checks the binary against the SHA-256 it pins, with the enrolment code
// until it is paired.
func (c *component) setup(r row) *pb.AgentSetup {
	script := c.publicURL + "/agent/host/install.sh"
	s := &pb.AgentSetup{
		EnrolmentCode:    r.code,
		ScriptUrl:        script,
		ChecksumsUrl:     c.publicURL + "/agent/host/SHA256SUMS",
		UninstallCommand: uninstall(c.publicURL),
	}
	if r.code != "" {
		s.InstallCommand = fmt.Sprintf("curl -fsSL %s | sudo sh -s -- --code %s", script, r.code)
	}
	return s
}

func uninstall(publicURL string) string {
	return fmt.Sprintf("curl -fsSL %s/agent/host/install.sh | sudo sh -s -- --uninstall", publicURL)
}

// pairPending tries every enrolling Agent once.
func (c *component) pairPending(ctx context.Context) {
	rows, err := c.rows(ctx)
	if err != nil {
		c.log.Warn("can't read the Agents", "err", err)
		return
	}
	for _, r := range rows {
		if r.token == "" && r.code != "" {
			c.pair(ctx, r)
		}
	}
}

// pair pairs with r's Agent, keeping why it failed for ListAgents. One
// pairing at a time, each re-reading the row: the loop and PairAgent
// mustn't both pair, or the second token replaces the first on the Agent.
func (c *component) pair(ctx context.Context, r row) {
	c.pairing.Lock()
	defer c.pairing.Unlock()
	if now, err := c.row(ctx, r.vmid); err != nil || now.token != "" || now.code != r.code {
		return
	}
	err := func() error {
		ctx, cancel := context.WithTimeout(ctx, readTimeout)
		defer cancel()
		hello, fp, err := agent.Probe(ctx, r.url)
		switch {
		case err != nil:
			return fmt.Errorf("waiting for the Agent at %s: %v", r.url, err)
		case hello.Kind != agent.KindHost:
			return fmt.Errorf("%s answers as a %s Agent, not a Host Agent", r.url, hello.Kind)
		case hello.Paired:
			return errors.New("the Agent is already paired (with another ddash, or before a reset): install it again with the command above")
		}
		token, _, err := agent.Pair(ctx, r.url, fp, r.code)
		if err != nil {
			return fmt.Errorf("pairing refused: %v", err)
		}
		_, err = c.db.ExecContext(ctx, `UPDATE agent SET token = ?, fingerprint = ?, code = '', paired = ? WHERE vmid = ? AND token = ''`,
			token, fp, time.Now().UTC().Format(time.RFC3339), r.vmid)
		return err
	}()
	c.mu.Lock()
	if err != nil {
		c.pairErr[r.vmid] = err.Error()
	} else {
		delete(c.pairErr, r.vmid)
		c.log.Info("paired with a Host Agent", "vmid", r.vmid)
	}
	c.mu.Unlock()
	c.reads.Invalidate()
}

func (c *component) PairAgent(ctx context.Context, req *pb.PairAgentRequest) (*pb.PairAgentResponse, error) {
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	if req.GetReset_() {
		r.code, r.token, r.fingerprint = agent.NewCode(), "", ""
		if _, err := c.db.ExecContext(ctx, `UPDATE agent SET code = ?, token = '', fingerprint = '', paired = '' WHERE vmid = ?`, r.code, r.vmid); err != nil {
			return nil, err
		}
	}
	if r.token != "" {
		return nil, status.Errorf(codes.FailedPrecondition, "the Host Agent on %d is already paired", r.vmid)
	}
	c.pair(ctx, r)
	a, err := c.reading(ctx, r.vmid)
	if err != nil {
		return nil, err
	}
	return &pb.PairAgentResponse{Agent: a.proto()}, nil
}

func (c *component) reading(ctx context.Context, vmid uint32) (reading, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return reading{}, err
	}
	for _, x := range rs {
		if x.vmid == vmid {
			return x, nil
		}
	}
	return reading{}, status.Errorf(codes.NotFound, "Workload %d has no Host Agent", vmid)
}

func (c *component) RemoveAgent(ctx context.Context, req *pb.RemoveAgentRequest) (*pb.RemoveAgentResponse, error) {
	if _, err := c.row(ctx, req.GetVmid()); err != nil {
		return nil, err
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM agent WHERE vmid = ?`, req.GetVmid()); err != nil {
		return nil, err
	}
	c.reads.Invalidate()
	return &pb.RemoveAgentResponse{Uninstall: []string{uninstall(c.publicURL)}}, nil
}

// paired returns vmid's Agent ready for operations: paired, reachable,
// on ddash's Release.
func (c *component) paired(ctx context.Context, vmid uint32) (*agent.Client, error) {
	r, err := c.reading(ctx, vmid)
	if err != nil {
		return nil, err
	}
	switch r.state {
	case pb.AgentState_AGENT_STATE_PAIRED:
		return r.client(), nil
	case pb.AgentState_AGENT_STATE_ENROLLING:
		return nil, status.Errorf(codes.FailedPrecondition, "the Host Agent on %d isn't paired yet", vmid)
	case pb.AgentState_AGENT_STATE_UNREACHABLE:
		return nil, status.Errorf(codes.Unavailable, "the Host Agent on %d is %s", vmid, r.message)
	}
	return nil, status.Errorf(codes.FailedPrecondition, "the Host Agent on %d: %s", vmid, r.message)
}

func (c *component) UpdateAgent(ctx context.Context, req *pb.UpdateAgentRequest) (*pb.UpdateAgentResponse, error) {
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	if r.token == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "the Host Agent on %d isn't paired yet", r.vmid)
	}
	cl := r.client()
	var info agent.Info
	if err := cl.Get(ctx, agent.PathInfo, nil, &info); err != nil {
		return nil, agentErr(err)
	}
	arch := cmpOr(info.Arch, "amd64")
	sum, err := c.checksum(ctx, arch)
	if err != nil {
		return nil, err
	}
	var ref agent.JobRef
	update := agent.UpdateRequest{Release: sdk.Release, URL: c.publicURL + "/agent/host/" + arch, SHA256: sum}
	if err := cl.Post(ctx, agent.PathUpdate, update, &ref); err != nil {
		return nil, agentErr(err)
	}
	c.reads.Invalidate()
	return &pb.UpdateAgentResponse{Job: jobproto.Of(ref.Job, vmidText(r.vmid))}, nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// checksum reads arch's Host Agent's SHA-256 from ddash's SHA256SUMS.
func (c *component) checksum(ctx context.Context, arch string) (string, error) {
	sums := c.publicURL + "/agent/host/SHA256SUMS"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sums, nil)
	if err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "ddash's public URL %q: %v", c.publicURL, err)
	}
	res, err := downloads.Do(req)
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "can't read the Host Agent's checksums at %s: %v", sums, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", status.Errorf(codes.Unavailable, "%s answered %s", sums, res.Status)
	}
	file := "ddash-host-agent-linux-" + arch
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file && len(f[0]) == 64 {
			return f[0], nil
		}
	}
	return "", status.Errorf(codes.FailedPrecondition, "%s lists no %s", sums, file)
}

// agentErr is an Agent's refusal as a gRPC error.
func agentErr(err error) error {
	var e *agent.APIError
	if !errors.As(err, &e) {
		if errors.Is(err, agent.ErrFingerprint) {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		return status.Error(codes.Unavailable, "the Host Agent: "+err.Error())
	}
	code := codes.Internal
	switch e.Code {
	case agent.CodeConflict:
		code = codes.FailedPrecondition
	case agent.CodeInvalid:
		code = codes.InvalidArgument
	case agent.CodeNotFound:
		code = codes.NotFound
	case agent.CodeBusy:
		code = codes.FailedPrecondition
	case agent.CodeBadToken, agent.CodeUnpaired:
		code = codes.Unauthenticated
	}
	return status.Error(code, e.Message)
}

func vmidText(vmid uint32) string { return strconv.FormatUint(uint64(vmid), 10) }

func (c *component) ListPackages(ctx context.Context, _ *pb.ListPackagesRequest) (*pb.ListPackagesResponse, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListPackagesResponse{}
	for _, r := range rs {
		if r.state == pb.AgentState_AGENT_STATE_ENROLLING {
			continue
		}
		h := &pb.HostPackages{Vmid: r.vmid, Error: r.packages.Error, RebootRequired: r.packages.RebootRequired}
		if r.state != pb.AgentState_AGENT_STATE_PAIRED {
			h.Error = r.message
		}
		if !r.packages.Read.IsZero() {
			h.Read = timestamppb.New(r.packages.Read)
		}
		for _, p := range r.packages.Packages {
			h.Packages = append(h.Packages, &pb.Package{Name: p.Name, Current: p.Current, New: p.New})
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

// job starts a job on vmid's Agent by POSTing in (nil: no body) to path.
func (c *component) job(ctx context.Context, vmid uint32, path string, in any) (*agent.Job, error) {
	cl, err := c.paired(ctx, vmid)
	if err != nil {
		return nil, err
	}
	defer c.reads.Invalidate()
	var ref agent.JobRef
	if err := cl.Post(ctx, path, in, &ref); err != nil {
		return nil, agentErr(err)
	}
	return &ref.Job, nil
}

func (c *component) RefreshPackages(ctx context.Context, req *pb.RefreshPackagesRequest) (*pb.RefreshPackagesResponse, error) {
	j, err := c.job(ctx, req.GetVmid(), agent.PathPackagesRefresh, nil)
	if err != nil {
		return nil, err
	}
	return &pb.RefreshPackagesResponse{Job: jobproto.Of(*j, vmidText(req.GetVmid()))}, nil
}

func (c *component) UpgradePackages(ctx context.Context, req *pb.UpgradePackagesRequest) (*pb.UpgradePackagesResponse, error) {
	j, err := c.job(ctx, req.GetVmid(), agent.PathUpgrade, nil)
	if err != nil {
		return nil, err
	}
	return &pb.UpgradePackagesResponse{Job: jobproto.Of(*j, vmidText(req.GetVmid()))}, nil
}

func (c *component) EnableSSH(ctx context.Context, req *pb.EnableSSHRequest) (*pb.EnableSSHResponse, error) {
	j, err := c.job(ctx, req.GetVmid(), agent.PathSSHEnable, nil)
	if err != nil {
		return nil, err
	}
	return &pb.EnableSSHResponse{Job: jobproto.Of(*j, vmidText(req.GetVmid()))}, nil
}

func (c *component) GetSSH(ctx context.Context, req *pb.GetSSHRequest) (*pb.GetSSHResponse, error) {
	cl, err := c.paired(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	var st agent.SSHState
	if err := cl.Get(ctx, agent.PathSSH, nil, &st); err != nil {
		return nil, agentErr(err)
	}
	return &pb.GetSSHResponse{Ssh: sshProto(st)}, nil
}

func sshProto(st agent.SSHState) *pb.SSHState {
	out := &pb.SSHState{
		Installed: st.Installed, Running: st.Running, Enabled: st.Enabled,
		PasswordAuthentication: st.PasswordAuthentication, PermitRootLogin: st.PermitRootLogin, Users: st.Users,
	}
	for _, k := range st.Keys {
		out.Keys = append(out.Keys, &pb.AuthorizedKey{User: k.User, Name: k.Name, Type: k.Type, Fingerprint: k.Fingerprint, Comment: k.Comment})
	}
	return out
}

func (c *component) InstallKey(ctx context.Context, req *pb.InstallKeyRequest) (*pb.InstallKeyResponse, error) {
	cl, err := c.paired(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	err = cl.Post(ctx, agent.PathSSHKeys, agent.KeyRequest{User: req.GetUser(), Name: req.GetName(), PublicKey: req.GetPublicKey()}, nil)
	if agent.CodeOf(err) == agent.CodeConflict {
		return nil, status.Error(codes.AlreadyExists, err.Error())
	}
	if err != nil {
		return nil, agentErr(err)
	}
	return &pb.InstallKeyResponse{}, nil
}

func (c *component) RemoveKey(ctx context.Context, req *pb.RemoveKeyRequest) (*pb.RemoveKeyResponse, error) {
	cl, err := c.paired(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	if err := cl.Delete(ctx, agent.PathSSHKeys, agent.KeyRequest{User: req.GetUser(), Name: req.GetName()}, nil); err != nil {
		return nil, agentErr(err)
	}
	return &pb.RemoveKeyResponse{}, nil
}

func (c *component) SetSSHSettings(ctx context.Context, req *pb.SetSSHSettingsRequest) (*pb.SetSSHSettingsResponse, error) {
	cl, err := c.paired(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	var st agent.SSHState
	if err := cl.Post(ctx, agent.PathSSHSettings, agent.SSHSettings{PasswordAuthentication: req.GetPasswordAuthentication(), PermitRootLogin: req.GetPermitRootLogin()}, &st); err != nil {
		return nil, agentErr(err)
	}
	return &pb.SetSSHSettingsResponse{Ssh: sshProto(st)}, nil
}

func (c *component) ListJobs(ctx context.Context, _ *pb.ListJobsRequest) (*pb.ListJobsResponse, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListJobsResponse{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range rows {
		if r.token == "" {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, readTimeout)
			defer cancel()
			h := &pb.HostJobs{Vmid: r.vmid}
			var jobs []agent.Job
			if err := r.client().Get(ctx, agent.PathJobs, nil, &jobs); err != nil {
				_, h.Error = stateOf(err)
			}
			for _, j := range jobs {
				h.Jobs = append(h.Jobs, jobproto.Of(j, vmidText(r.vmid)))
			}
			mu.Lock()
			out.Hosts = append(out.Hosts, h)
			mu.Unlock()
		})
	}
	wg.Wait()
	slices.SortFunc(out.Hosts, func(a, b *pb.HostJobs) int { return int(a.GetVmid()) - int(b.GetVmid()) })
	return out, nil
}

func (c *component) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	var j agent.Job
	if err := r.client().Get(ctx, agent.PathJobs+"/"+url.PathEscape(req.GetId()), nil, &j); err != nil {
		return nil, agentErr(err)
	}
	return &pb.GetJobResponse{Job: jobproto.Of(j, vmidText(r.vmid))}, nil
}

func (c *component) CollectJob(ctx context.Context, req *pb.CollectJobRequest) (*pb.CollectJobResponse, error) {
	r, err := c.row(ctx, req.GetVmid())
	if err != nil {
		return nil, err
	}
	var j agent.Job
	if err := r.client().Delete(ctx, agent.PathJobs+"/"+url.PathEscape(req.GetId()), nil, &j); err != nil {
		return nil, agentErr(err)
	}
	c.reads.Invalidate()
	if j.State == agent.Failed {
		return nil, status.Errorf(codes.Aborted, "%s failed: %s", j.Title, j.Error)
	}
	return &pb.CollectJobResponse{Job: jobproto.Of(j, vmidText(r.vmid))}, nil
}
