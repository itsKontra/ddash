package compose

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	containerpb "github.com/itsKontra/ddash/proto/ddash/container"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/compose/composepb"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
)

// target is the Compose Project an Action acts on, as read now.
type target struct {
	host    *dapb.HostProjects
	project *dapb.Project
	// containers are the Project's, by id.
	containers []*containerpb.Container
}

func (t target) file() string {
	if len(t.project.GetConfigFiles()) == 0 {
		return ""
	}
	return t.project.GetConfigFiles()[0]
}

func (t target) composeTarget(services []string) *dapb.ComposeTarget {
	return &dapb.ComposeTarget{Host: t.host.GetHost(), Project: t.project.GetName(), WorkingDir: t.project.GetWorkingDir(), File: t.file(), Services: services}
}

// protected lists the Project's Protected containers among services
// (all when empty).
func (t target) protected(services []string) []string {
	var out []string
	for _, ct := range t.containers {
		if ct.GetLabels()["ddash.protected"] == "true" && (len(services) == 0 || slices.Contains(services, ct.GetService())) {
			out = append(out, ct.GetName())
		}
	}
	return out
}

// in lists the Project's containers among services (all when empty).
func (t target) in(services []string) []*containerpb.Container {
	var out []*containerpb.Container
	for _, ct := range t.containers {
		if len(services) == 0 || slices.Contains(services, ct.GetService()) {
			out = append(out, ct)
		}
	}
	return out
}

// running lists the Project's running containers among services.
func (t target) running(services []string) []*containerpb.Container {
	var out []*containerpb.Container
	for _, ct := range t.containers {
		if ct.GetState() == containerpb.State_STATE_RUNNING && (len(services) == 0 || slices.Contains(services, ct.GetService())) {
			out = append(out, ct)
		}
	}
	return out
}

// resolve reads host's Agent and finds project on it.
func (c *component) resolve(ctx context.Context, host, project string) (target, error) {
	res, err := c.agents.ListComposeProjects(ctx, &dapb.ListComposeProjectsRequest{})
	if err != nil {
		return target{}, err
	}
	for _, h := range res.GetHosts() {
		if h.GetHost() != host {
			continue
		}
		if h.GetError() != "" {
			return target{host: h}, status.Errorf(codes.Unavailable, "the Docker Agent on %s: %s", host, h.GetError())
		}
		if project == "" {
			return target{host: h}, nil
		}
		for _, p := range h.GetProjects() {
			if p.GetName() != project {
				continue
			}
			t := target{host: h, project: p}
			for _, ct := range h.GetContainers() {
				if ct.GetProject() == p.GetName() {
					t.containers = append(t.containers, ct)
				}
			}
			return t, nil
		}
		return target{host: h}, status.Errorf(codes.NotFound, "%s has no Compose Project %s", host, project)
	}
	return target{}, status.Errorf(codes.NotFound, "no Docker Agent %q", host)
}

func (c *component) PlanAction(ctx context.Context, req *pb.PlanActionRequest) (*pb.PlanActionResponse, error) {
	p, _, err := c.plan(ctx, req.GetAction())
	if err != nil {
		return nil, err
	}
	return &pb.PlanActionResponse{Plan: p}, nil
}

// work is how a Plan's steps run, one func per step (nil: done by the
// job step it maps to).
type work struct {
	title string
	tgt   target
	steps []stepWork
	// probe are the containers whose Checks are probed at once when the
	// run ended without failing: those it started or recreated.
	probe []*containerpb.Container
}

// stepWork runs one Plan step. A step backed by a job reports through it.
type stepWork struct {
	run func(ctx context.Context, r *run, i int) error
	// jobStep is the Agent job's step this Plan step shows, -1 for none.
	jobStep int
}

func maintenanceStep(names []string, how string) *pb.PlanStep {
	return &pb.PlanStep{System: pb.System_SYSTEM_DDASH, Title: "Maintenance for " + strings.Join(names, ", "), Values: []*pb.PlanValue{{Label: "Until", After: how}}}
}

func names(cs []*containerpb.Container) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.GetName())
	}
	return out
}

// plan builds action's Plan and the work running it.
func (c *component) plan(ctx context.Context, a *pb.ComposeAction) (*pb.Plan, *work, error) {
	switch x := a.GetAction().(type) {
	case *pb.ComposeAction_Save:
		return c.planSave(ctx, x.Save, a.GetOverrideProtected())
	case *pb.ComposeAction_Start:
		return c.planStart(ctx, x.Start)
	case *pb.ComposeAction_Stop:
		return c.planStop(ctx, x.Stop, a.GetOverrideProtected())
	case *pb.ComposeAction_Restart:
		return c.planRestart(ctx, x.Restart, a.GetOverrideProtected())
	case *pb.ComposeAction_Update:
		return c.planUpdate(ctx, x.Update, a.GetOverrideProtected())
	case *pb.ComposeAction_Add:
		return c.planAdd(ctx, x.Add)
	}
	return nil, nil, status.Error(codes.InvalidArgument, "action is required")
}

// guard keeps p from running when names are Protected and the Operator
// didn't override.
func guard(p *pb.Plan, protected []string, override bool, what string) {
	p.Protected = protected
	if len(protected) > 0 && !override {
		p.OverrideNeeded = fmt.Sprintf("%s is Protected: ddash may %s it only if you override that below.", strings.Join(protected, ", "), what)
	}
}

func (c *component) planSave(ctx context.Context, s *pb.SaveFile, override bool) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, s.GetHost(), s.GetProject())
	if err != nil {
		return nil, nil, err
	}
	cur, err := c.agents.GetComposeFile(ctx, &dapb.GetComposeFileRequest{Host: s.GetHost(), Path: t.file()})
	if err != nil {
		return nil, nil, err
	}
	isEnv := path.Base(s.GetPath()) == ".env" && path.Dir(s.GetPath()) == path.Dir(t.file())
	// A Dockerfile its Compose services build from: written as is, and
	// a recreate rebuilds the Compose services built from it.
	var built *dapb.Dockerfile
	for _, d := range cur.GetDockerfiles() {
		if d.GetFile().GetPath() == s.GetPath() {
			built = d
		}
	}
	if s.GetPath() != t.file() && !isEnv && built == nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "%s is neither %s's Compose file, its .env nor a Dockerfile it builds from", s.GetPath(), t.project.GetName())
	}
	file := cur.GetFile()
	if built != nil {
		file = built.GetFile()
	}
	// A .env not there yet is created: from nothing, expecting nothing.
	create := false
	if isEnv {
		file = cur.GetEnv()
		if file == nil {
			create = true
			file = &dapb.ComposeFile{Path: path.Join(path.Dir(t.file()), ".env")}
		}
	}
	p := &pb.Plan{Diff: unifiedDiff(file.GetPath(), file.GetContent(), s.GetContent())}
	switch {
	case create && s.GetExpectSha256() != "":
		p.Blocked = append(p.Blocked, file.GetPath()+" was deleted on disk since you opened it: reload it, then edit again.")
	case file.GetSha256() != s.GetExpectSha256():
		p.Blocked = append(p.Blocked, file.GetPath()+" changed on disk since you opened it: reload it to see the change, then edit again.")
	}
	if file.GetContent() == s.GetContent() {
		p.Blocked = append(p.Blocked, "Nothing changed.")
	}
	if !isEnv && built == nil {
		v, err := c.agents.ValidateComposeFile(ctx, &dapb.ValidateComposeFileRequest{Host: s.GetHost(), Path: s.GetPath(), Content: s.GetContent(), Project: t.project.GetName()})
		if err != nil {
			return nil, nil, err
		}
		if !v.GetOk() {
			p.Blocked = append(p.Blocked, "docker compose config refuses it: "+v.GetError())
		}
	}
	w := &work{title: "Save " + path.Base(file.GetPath()) + " of " + t.project.GetName(), tgt: t}
	step := &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Write " + file.GetPath()}
	switch {
	case create:
		step.Title = "Create " + file.GetPath()
		step.Values = []*pb.PlanValue{{Label: "Readable by", After: "its owner only"}}
	case isEnv:
		step.Values = []*pb.PlanValue{{Label: "Previous content", After: file.GetPath() + ".bak"}}
	case built != nil:
		step.Values = []*pb.PlanValue{
			{Label: "Previous content", After: file.GetPath() + ".bak"},
			{Label: "Built for", After: strings.Join(built.GetServices(), ", ")},
		}
	default:
		step.Values = []*pb.PlanValue{
			{Label: "Previous content", After: file.GetPath() + ".bak"},
			{Label: "Validated", After: "docker compose config, on " + s.GetHost()},
		}
	}
	p.Steps = append(p.Steps, step)
	w.steps = append(w.steps, stepWork{jobStep: -1, run: func(ctx context.Context, r *run, i int) error {
		req := &dapb.WriteComposeFileRequest{Host: s.GetHost(), Path: file.GetPath(), Content: s.GetContent(), ExpectSha256: s.GetExpectSha256(), Project: t.project.GetName()}
		if built != nil {
			req.ComposeFile = t.file()
		}
		_, err := c.agents.WriteComposeFile(ctx, req)
		return err
	}})
	switch {
	case built != nil && s.GetRecreate():
		services := built.GetServices()
		guard(p, t.protected(services), override, "recreate")
		if up := t.running(services); len(up) > 0 {
			p.Steps = append(p.Steps, maintenanceStep(names(up), "its Checks are up, or 5 min"))
			w.steps = append(w.steps, stepWork{jobStep: -1, run: c.maintain(up, restartMaintenance, true)})
		}
		p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Rebuild and recreate " + strings.Join(services, ", "), Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose up -d --build " + strings.Join(services, " ")}}})
		w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
			res, err := c.agents.UpProject(ctx, &dapb.UpProjectRequest{Target: t.composeTarget(services), Build: true})
			if err != nil {
				return err
			}
			return r.follow(ctx, c, res.GetJob(), i)
		}})
	case built != nil:
		p.Notes = append(p.Notes, "Staged only: nothing is rebuilt. Its image is built again only by docker compose up --build, e.g. a save that rebuilds.")
	case s.GetRecreate():
		guard(p, t.protected(nil), override, "recreate")
		if up := t.running(nil); len(up) > 0 {
			p.Steps = append(p.Steps, maintenanceStep(names(up), "its Checks are up, or 5 min"))
			w.steps = append(w.steps, stepWork{jobStep: -1, run: c.maintain(up, restartMaintenance, true)})
		}
		p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Recreate the changed Compose services", Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose up -d"}}})
		w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
			res, err := c.agents.UpProject(ctx, &dapb.UpProjectRequest{Target: t.composeTarget(nil)})
			if err != nil {
				return err
			}
			return r.follow(ctx, c, res.GetJob(), i)
		}})
	default:
		p.Notes = append(p.Notes, "Staged only: nothing is recreated. Start or Update the Compose Project to apply it.")
	}
	return p, w, nil
}

func (c *component) planStart(ctx context.Context, s *pb.Start) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, s.GetHost(), s.GetProject())
	if err != nil {
		return nil, nil, err
	}
	p := &pb.Plan{}
	w := &work{title: "Start " + t.project.GetName(), tgt: t, probe: t.in(s.GetServices())}
	p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Start " + what(t, s.GetServices()), Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose up -d " + strings.Join(s.GetServices(), " ")}}})
	w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
		res, err := c.agents.UpProject(ctx, &dapb.UpProjectRequest{Target: t.composeTarget(s.GetServices())})
		if err != nil {
			return err
		}
		return r.follow(ctx, c, res.GetJob(), i)
	}})
	p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DDASH, Title: "End the Maintenance of its containers"})
	w.steps = append(w.steps, stepWork{jobStep: -1, run: c.endMaintenance(t, s.GetServices())})
	return p, w, nil
}

func what(t target, services []string) string {
	if len(services) == 0 {
		return t.project.GetName()
	}
	return t.project.GetName() + " (" + strings.Join(services, ", ") + ")"
}

// serviceNames are the Project's Compose services named in want (all
// when empty), for a stop that keeps the containers.
func serviceNames(t target, want []string) []string {
	if len(want) > 0 {
		return want
	}
	var out []string
	for _, s := range t.project.GetServices() {
		out = append(out, s.GetName())
	}
	return out
}

func (c *component) planStop(ctx context.Context, s *pb.Stop, override bool) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, s.GetHost(), s.GetProject())
	if err != nil {
		return nil, nil, err
	}
	p := &pb.Plan{}
	guard(p, t.protected(s.GetServices()), override, "stop")
	w := &work{title: "Stop " + t.project.GetName(), tgt: t}
	if up := t.running(s.GetServices()); len(up) == 0 {
		p.Blocked = append(p.Blocked, what(t, s.GetServices())+" isn't running.")
	} else {
		p.Steps = append(p.Steps, maintenanceStep(names(up), "ddash starts it again, or its Checks are up"))
		w.steps = append(w.steps, stepWork{jobStep: -1, run: c.maintain(up, 0, false)})
	}
	p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Stop " + what(t, s.GetServices()), Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose stop " + strings.Join(serviceNames(t, s.GetServices()), " ")}}})
	w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
		res, err := c.agents.DownProject(ctx, &dapb.DownProjectRequest{Target: t.composeTarget(serviceNames(t, s.GetServices()))})
		if err != nil {
			return err
		}
		return r.follow(ctx, c, res.GetJob(), i)
	}})
	p.Notes = append(p.Notes, "Its containers are stopped, not removed: Start brings them back.")
	return p, w, nil
}

func (c *component) planRestart(ctx context.Context, s *pb.Restart, override bool) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, s.GetHost(), s.GetProject())
	if err != nil {
		return nil, nil, err
	}
	p := &pb.Plan{}
	guard(p, t.protected(s.GetServices()), override, "restart")
	w := &work{title: "Restart " + t.project.GetName(), tgt: t, probe: t.in(s.GetServices())}
	if up := t.running(s.GetServices()); len(up) > 0 {
		p.Steps = append(p.Steps, maintenanceStep(names(up), "its Checks are up, or 5 min"))
		w.steps = append(w.steps, stepWork{jobStep: -1, run: c.maintain(up, restartMaintenance, true)})
	}
	p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Restart " + what(t, s.GetServices()), Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose restart " + strings.Join(s.GetServices(), " ")}}})
	w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
		res, err := c.agents.RestartProject(ctx, &dapb.RestartProjectRequest{Target: t.composeTarget(s.GetServices())})
		if err != nil {
			return err
		}
		return r.follow(ctx, c, res.GetJob(), i)
	}})
	return p, w, nil
}

// short is a digest's first 12 hex digits, as Docker shows image ids.
func short(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "unknown"
	}
	return d
}

func (c *component) planUpdate(ctx context.Context, u *pb.Update, override bool) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, u.GetHost(), u.GetProject())
	if err != nil {
		return nil, nil, err
	}
	p := &pb.Plan{}
	guard(p, t.protected(nil), override, "recreate")
	w := &work{title: "Update " + t.project.GetName(), tgt: t, probe: t.in(nil)}
	pull := &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Pull images", Values: nil}
	var stale []*containerpb.Container
	for _, ct := range t.containers {
		st := c.status(ct)
		v := &pb.PlanValue{Label: ct.GetService() + " (" + ct.GetImage() + ")", Before: short(st.GetCurrentDigest())}
		switch st.GetState() {
		case pb.ImageState_IMAGE_STATE_STALE:
			v.After = short(st.GetLatestDigest())
			stale = append(stale, ct)
		case pb.ImageState_IMAGE_STATE_UP_TO_DATE:
			v.After = short(st.GetCurrentDigest())
		default:
			v.After = st.GetReason()
		}
		pull.Values = append(pull.Values, v)
	}
	if len(t.containers) == 0 {
		p.Blocked = append(p.Blocked, t.project.GetName()+" has no containers to update: Start it instead.")
	}
	p.UpToDate = len(stale) == 0
	if p.UpToDate {
		p.Notes = append(p.Notes, "The last check found no newer image. The pull may find one; when it doesn't, nothing is recreated.")
	}
	if len(stale) > 0 {
		p.Steps = append(p.Steps, maintenanceStep(names(stale), "its Checks are up, or 5 min"))
		w.steps = append(w.steps, stepWork{jobStep: -1, run: c.maintain(stale, restartMaintenance, true)})
	}
	p.Steps = append(p.Steps, pull,
		&pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Compare digests"},
		&pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Recreate the Compose services whose image changed", Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose up -d --no-deps <changed>"}}})
	w.steps = append(w.steps,
		stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
			res, err := c.agents.UpdateProject(ctx, &dapb.UpdateProjectRequest{Target: t.composeTarget(nil)})
			if err != nil {
				return err
			}
			return r.follow(ctx, c, res.GetJob(), i)
		}},
		stepWork{jobStep: 1},
		stepWork{jobStep: 2},
	)
	return p, w, nil
}

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (c *component) planAdd(ctx context.Context, a *pb.AddProject) (*pb.Plan, *work, error) {
	t, err := c.resolve(ctx, a.GetHost(), "")
	if err != nil {
		return nil, nil, err
	}
	p := &pb.Plan{}
	if !projectName.MatchString(a.GetName()) {
		p.Blocked = append(p.Blocked, "A Compose Project's name is lower-case letters, digits, - and _, starting with a letter or digit.")
	}
	for _, pr := range t.host.GetProjects() {
		if pr.GetName() == a.GetName() {
			p.Blocked = append(p.Blocked, fmt.Sprintf("%s already has a Compose Project %s.", a.GetHost(), a.GetName()))
		}
	}
	dir := path.Clean(a.GetDirectory())
	under := slices.ContainsFunc(t.host.GetRoots(), func(r string) bool { return strings.HasPrefix(dir, strings.TrimRight(r, "/")+"/") })
	if !path.IsAbs(dir) || !under {
		p.Blocked = append(p.Blocked, fmt.Sprintf("The directory must be under one of %s's Compose roots: %s.", a.GetHost(), strings.Join(t.host.GetRoots(), ", ")))
	}
	file := path.Join(dir, "compose.yaml")
	if len(p.Blocked) == 0 {
		v, err := c.agents.ValidateComposeFile(ctx, &dapb.ValidateComposeFileRequest{Host: a.GetHost(), Path: file, Content: a.GetContent(), Project: a.GetName(), Env: a.GetEnv()})
		if err != nil {
			return nil, nil, err
		}
		if !v.GetOk() {
			p.Blocked = append(p.Blocked, "docker compose config refuses it: "+v.GetError())
		}
	}
	w := &work{title: "Add " + a.GetName(), tgt: t}
	create := &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Create " + file, Values: []*pb.PlanValue{{Label: "Compose Project", After: a.GetName()}}}
	if a.GetEnv() != "" {
		create.Values = append(create.Values, &pb.PlanValue{Label: ".env", After: path.Join(dir, ".env") + ", readable by its owner only"})
	}
	p.Steps = append(p.Steps, create)
	w.steps = append(w.steps, stepWork{jobStep: -1, run: func(ctx context.Context, r *run, i int) error {
		_, err := c.agents.CreateComposeProject(ctx, &dapb.CreateComposeProjectRequest{Host: a.GetHost(), Name: a.GetName(), Directory: dir, Content: a.GetContent(), Env: a.GetEnv()})
		return err
	}})
	if a.GetStart() {
		p.Steps = append(p.Steps, &pb.PlanStep{System: pb.System_SYSTEM_DOCKER_AGENT, Title: "Start " + a.GetName(), Values: []*pb.PlanValue{{Label: "Runs", After: "docker compose up -d --pull always --wait"}}})
		w.steps = append(w.steps, stepWork{jobStep: 0, run: func(ctx context.Context, r *run, i int) error {
			res, err := c.agents.UpProject(ctx, &dapb.UpProjectRequest{Target: &dapb.ComposeTarget{Host: a.GetHost(), Project: a.GetName(), WorkingDir: dir, File: file}, Pull: true, Wait: true})
			if err != nil {
				return err
			}
			return r.follow(ctx, c, res.GetJob(), i)
		}})
	} else {
		p.Notes = append(p.Notes, "It isn't started: Start it from its row once you're ready.")
	}
	return p, w, nil
}

// unifiedDiff is a minimal unified diff of a and b, 3 lines of context.
func unifiedDiff(name, a, b string) string {
	if a == b {
		return ""
	}
	x, y := lines(a), lines(b)
	// LCS table; Compose files are small.
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-', '+'
		text string
		ai   int
		bi   int
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i, j})
			i++
			j++
		case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i], i, j})
			i++
		default:
			ops = append(ops, op{'+', y[j], i, j})
			j++
		}
	}
	const ctxLines = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", name, name)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(0, k-ctxLines)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*ctxLines || run == len(ops) {
				end = min(len(ops), end+ctxLines)
				break
			}
			end = run
		}
		var aCount, bCount int
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				aCount++
			}
			if o.kind != '-' {
				bCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", ops[start].ai+1, aCount, ops[start].bi+1, bCount)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		k = end
	}
	return out.String()
}

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
