// Package apps is the apps Component: it keeps a record per App, under an
// opaque id that outlives URL changes, discovered from Nginx Proxy
// Manager's Proxy Hosts and pfSense's Host Overrides or added as Link
// only, with the Operator's settings and Ignores.
//
// Every SyncInterval it reads nginx-proxy-manager-api's Proxy Hosts and
// the Reverse Proxy's address, pfsense-api's Host Overrides (both cached
// there for sdk.TTLSlow) and the Workloads proxmox-api and docker-api
// list, attaches every source to a record (attach), and declares every
// App's Check to checks when the set changed. ListApps derives each App's
// Reach, Workload and Warnings from the records and the last good reads
// (derive), and each Workload's network info from network by address;
// only the records and Ignores are stored.
package apps

import (
	"context"
	"embed"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// SyncInterval is how often apps re-reads its sources, so a change made in
// Nginx Proxy Manager or pfSense reaches Apps within a minute.
const SyncInterval = 60 * time.Second

// The sources' names in Health and errors.
const (
	npmName     = "nginx-proxy-manager-api"
	pfsenseName = "pfsense-api"
)

// ResyncTimeout bounds the sync ending an App Action: its slice of the
// host's 60 s Action deadline.
const ResyncTimeout = 10 * time.Second

// Definition is apps, ready for sdk.Serve.
func Definition() sdk.Definition { return define(ResyncTimeout) }

// define is apps bounding the sync that ends an App Action to resync.
func define(resync time.Duration) sdk.Definition {
	return sdk.Define(sdk.Spec[*appspb.AppsConfig]{
		Manifest:   manifest,
		Service:    &appspb.AppsService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*appspb.AppsConfig] { return &component{resync: resync} },
	})
}

type component struct {
	appspb.UnimplementedAppsServiceServer

	npm     npmpb.NginxProxyManagerApiServiceClient
	pfsense pfpb.PfsenseApiServiceClient
	network networkpb.NetworkServiceClient
	checks  checkspb.ChecksServiceClient
	proxmox proxmoxapipb.ProxmoxApiServiceClient
	docker  dockerapipb.DockerApiServiceClient
	store   store
	log     *slog.Logger
	// resync bounds the sync ending an App Action.
	resync time.Duration

	// synced is closed once the first sync ended, so ListApps and Health
	// never answer before the sources were first read.
	synced    chan struct{}
	firstSync sync.Once

	// turn makes syncs and Actions take turns: each reads the records,
	// and writes them and the App Checks, alone.
	turn sync.Mutex

	// runs is the progress of the App Actions running now, for GetRun.
	runs runs

	mu sync.Mutex
	// hosts is the last good read of the Proxy Hosts, by id; nil before
	// the first since Start.
	hosts     map[int64]*npmpb.ProxyHost
	hostsRead sourceRead
	// overrides is the last good read of the Host Overrides; nil before
	// the first since Start.
	overrides     []*pfpb.HostOverride
	overridesRead sourceRead
	// rpAddress is the Reverse Proxy's address as last read; empty
	// before the first.
	rpAddress string
	// hostsReadOnly is whether Nginx Proxy Manager refuses ddash's
	// Proxy Host writes, as last read.
	hostsReadOnly bool
	// certs is the last good read of the certificates, by id, as apps
	// names them; nil before the first.
	certs map[int64]*appspb.Certificate
	// src is the last read of the Workload sources.
	src sources
	// checksErr is why the last SetAppChecks failed; empty once one worked.
	checksErr string
	// sent is the set checks last accepted, nil before the first since
	// Start.
	sent []*checkspb.AppCheck
}

// sourceRead is how a source was last read.
type sourceRead struct {
	// at is when the last good read was; zero before the first.
	at time.Time
	// err is why the reads since failed, since failingSince.
	err          error
	failingSince time.Time
}

func (s *sourceRead) note(err error) {
	switch {
	case err == nil:
		s.err, s.at = nil, time.Now()
	case s.err == nil:
		s.err, s.failingSince = err, time.Now()
	default:
		s.err = err
	}
}

func (s sourceRead) proto() *appspb.SourceRead {
	out := &appspb.SourceRead{}
	if !s.at.IsZero() {
		out.ReadAt = timestamppb.New(s.at)
	}
	if s.err != nil {
		out.Error = status.Convert(s.err).Message()
		out.FailingSince = timestamppb.New(s.failingSince)
		out.NotConfigured = notConfigured(s.err)
	}
	return out
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *appspb.AppsConfig) error {
	c.npm = sdk.Require(env, npmpb.NewNginxProxyManagerApiServiceClient)
	c.pfsense = sdk.Require(env, pfpb.NewPfsenseApiServiceClient)
	c.network = sdk.Require(env, networkpb.NewNetworkServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.proxmox = sdk.Require(env, proxmoxapipb.NewProxmoxApiServiceClient)
	c.docker = sdk.Require(env, dockerapipb.NewDockerApiServiceClient)
	c.store = store{db: env.DB}
	c.log = env.Log
	c.synced = make(chan struct{})
	sdk.Every(ctx, SyncInterval, func(ctx context.Context) {
		defer c.firstSync.Do(func() { close(c.synced) })
		_ = c.sync(ctx, false)
	})
	return nil
}

// Health is degraded, naming each, while a required Component can't be
// read: Apps are then those of its last good read, or listed without the
// Workloads of an unavailable source. It waits for the first sync.
func (c *component) Health(ctx context.Context) sdk.Health {
	if err := c.waitSynced(ctx); err != nil {
		return sdk.Health{State: sdk.Degraded, Message: "apps hasn't read its sources yet"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var problems []string
	if c.hostsRead.err != nil {
		problems = append(problems, sourceProblem(npmName, c.hostsRead.err))
	}
	if c.overridesRead.err != nil {
		problems = append(problems, sourceProblem(pfsenseName, c.overridesRead.err))
	}
	if c.checksErr != "" {
		problems = append(problems, "checks unavailable: "+c.checksErr)
	}
	problems = append(problems, c.src.problems...)
	if len(problems) > 0 {
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(problems, "; ")}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) waitSynced(ctx context.Context) error {
	select {
	case <-c.synced:
		return nil
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
}

// sync reads the sources (fresh, bypassing their caches, when fresh),
// attaches what the last good reads hold to the records, and declares
// the App Checks. It returns why each source that couldn't be read
// failed, naming it; its last good read then stays.
func (c *component) sync(ctx context.Context, fresh bool) error {
	c.turn.Lock()
	defer c.turn.Unlock()
	if failed := c.syncLocked(ctx, fresh); len(failed) > 0 {
		return status.Errorf(codes.Unavailable, "couldn't read %s", strings.Join(failed, "; "))
	}
	return nil
}

// syncLocked is sync for a caller holding c.turn. It returns each source
// it couldn't read, named, with why.
func (c *component) syncLocked(ctx context.Context, fresh bool) (failed []string) {
	hosts, hostsErr := c.npm.ListProxyHosts(ctx, &npmpb.ListProxyHostsRequest{Fresh: fresh})
	// The Reverse Proxy's address comes from the config, so it is known
	// while Nginx Proxy Manager can't be read too.
	st, stErr := c.npm.GetStatus(ctx, &npmpb.GetStatusRequest{})
	overrides, overridesErr := c.pfsense.ListHostOverrides(ctx, &pfpb.ListHostOverridesRequest{Fresh: fresh})
	src := c.readSources(ctx)

	c.mu.Lock()
	c.src = src
	c.hostsRead.note(hostsErr)
	if hostsErr == nil {
		c.hosts = map[int64]*npmpb.ProxyHost{}
		for _, h := range hosts.GetProxyHosts() {
			c.hosts[h.GetId()] = h
		}
	}
	c.overridesRead.note(overridesErr)
	if overridesErr == nil {
		c.overrides = overrides.GetHostOverrides()
		if c.overrides == nil {
			c.overrides = []*pfpb.HostOverride{}
		}
	}
	if stErr == nil && st.GetAddress() != "" {
		c.rpAddress = st.GetAddress()
	}
	if stErr == nil {
		c.hostsReadOnly = st.GetProxyHostsReadOnly()
	}
	c.mu.Unlock()

	if hostsErr == nil || overridesErr == nil {
		if err := c.attach(ctx); err != nil {
			c.log.Error("attach the sources to App records", "err", err)
		}
	}
	c.syncChecks(ctx)

	if hostsErr != nil {
		failed = append(failed, npmName+": "+status.Convert(hostsErr).Message())
	}
	if overridesErr != nil {
		failed = append(failed, pfsenseName+": "+status.Convert(overridesErr).Message())
	}
	return failed
}

// attach attaches the last good reads' sources to the records and saves
// the result.
func (c *component) attach(ctx context.Context) error {
	recs, err := c.store.records(ctx)
	if err != nil {
		return err
	}
	ch := attach(recs, c.view())
	return c.store.save(ctx, ch.put, ch.drop)
}

// view is a copy of what the Apps are derived from besides their records.
// The Host Overrides count as read only once the Reverse Proxy's address
// is known too: without it, they can't be told apart.
func (c *component) view() view {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := view{hosts: c.hosts, src: c.src}
	if c.overrides != nil && c.rpAddress != "" {
		v.dns = newDNS(c.overrides, c.rpAddress)
	}
	return v
}

// apps derives every App from the records now.
func (c *component) apps(ctx context.Context) ([]*appspb.App, error) {
	recs, err := c.store.records(ctx)
	if err != nil {
		return nil, err
	}
	return derive(recs, c.view()), nil
}

func (c *component) ListApps(ctx context.Context, _ *appspb.ListAppsRequest) (*appspb.ListAppsResponse, error) {
	if err := c.waitSynced(ctx); err != nil {
		return nil, err
	}
	recs, err := c.store.records(ctx)
	if err != nil {
		return nil, err
	}
	v := c.view()
	apps := derive(recs, v)
	networkErr, adguardErr := c.fillNetwork(ctx, apps, v.src)
	c.fillCertificates(ctx, apps)
	c.mu.Lock()
	defer c.mu.Unlock()
	hosts := c.hostsRead.proto()
	hosts.ReadOnly = c.hostsReadOnly
	return &appspb.ListAppsResponse{
		Apps:          apps,
		ProxyHosts:    hosts,
		HostOverrides: c.overridesRead.proto(),
		ReverseProxy:  reverseProxyOf(v.dns, c.rpAddress),
		NetworkError:  networkErr,
		AdguardError:  adguardErr,
	}, nil
}

// certificateStates are nginx-proxy-manager-api's certificate states as
// apps names them.
var certificateStates = map[npmpb.CertificateState]appspb.CertificateState{
	npmpb.CertificateState_CERTIFICATE_STATE_OK:           appspb.CertificateState_CERTIFICATE_STATE_OK,
	npmpb.CertificateState_CERTIFICATE_STATE_NOT_RENEWING: appspb.CertificateState_CERTIFICATE_STATE_NOT_RENEWING,
	npmpb.CertificateState_CERTIFICATE_STATE_EXPIRES_SOON: appspb.CertificateState_CERTIFICATE_STATE_EXPIRES_SOON,
	npmpb.CertificateState_CERTIFICATE_STATE_EXPIRED:      appspb.CertificateState_CERTIFICATE_STATE_EXPIRED,
}

// fillCertificates gives each App's Proxy Host its certificate, as Nginx
// Proxy Manager's (cached) certificates list it now, or as last read while
// they can't be read.
func (c *component) fillCertificates(ctx context.Context, apps []*appspb.App) {
	resp, err := c.npm.ListCertificates(ctx, &npmpb.ListCertificatesRequest{})
	c.mu.Lock()
	if err == nil {
		c.certs = map[int64]*appspb.Certificate{}
		for _, x := range resp.GetCertificates() {
			c.certs[x.GetId()] = &appspb.Certificate{Name: x.GetNiceName(), State: certificateStates[x.GetState()], Message: x.GetStateMessage()}
		}
	}
	certs := c.certs
	c.mu.Unlock()
	for _, a := range apps {
		if p := a.GetProxyHost(); p != nil {
			p.Certificate = proto.CloneOf(certs[p.GetCertificateId()])
		}
	}
}

func (c *component) Refresh(ctx context.Context, _ *appspb.RefreshRequest) (*appspb.RefreshResponse, error) {
	if _, err := c.npm.Refresh(ctx, &npmpb.RefreshRequest{}); err != nil {
		return nil, err
	}
	// The fresh reads drop and refill pfSense's Host Override cache.
	if err := c.sync(ctx, true); err != nil {
		return nil, err
	}
	return &appspb.RefreshResponse{}, nil
}
