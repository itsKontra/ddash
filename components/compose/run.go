package compose

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	pb "github.com/itsKontra/ddash/components/compose/composepb"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk"
)

const (
	// StreamFor is how long RunAction streams a run before it returns and
	// leaves it to ListRuns, inside the host's 60 s Action deadline.
	StreamFor = 45 * time.Second
	// RunAtMost bounds a run: a pull or a start that takes longer stops.
	RunAtMost = 30 * time.Minute
	// KeepRuns is how long an ended run stays in ListRuns.
	KeepRuns = 15 * time.Minute
	// followEvery is how often a run reads its job's progress.
	followEvery = time.Second
	// restartMaintenance is how long a restarted or recreated container
	// stays in Maintenance at most, unless its Checks are up first.
	restartMaintenance = 5 * time.Minute
	// callTimeout bounds one call a run makes.
	callTimeout = 20 * time.Second
)

// run is one Compose Project Action's run.
type run struct {
	mu       sync.Mutex
	p        *pb.Run
	changed  chan struct{} // closed and replaced on every change
	streamed bool          // RunAction returned while it ran
}

func (r *run) snapshot() *pb.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return proto.Clone(r.p).(*pb.Run)
}

// edit changes the run and wakes its watchers.
func (r *run) edit(f func(*pb.Run)) {
	r.mu.Lock()
	f(r.p)
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}

func (r *run) wait() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

func (r *run) state(i int, s pb.StepState, detail string) {
	r.edit(func(p *pb.Run) {
		p.States[i] = s
		if detail != "" {
			p.Details[i] = detail
		}
	})
}

// jobStates maps a job step's state onto a Plan step's.
var jobStates = map[jobpb.State]pb.StepState{
	jobpb.State_STATE_PENDING: pb.StepState_STEP_STATE_PENDING,
	jobpb.State_STATE_RUNNING: pb.StepState_STEP_STATE_RUNNING,
	jobpb.State_STATE_DONE:    pb.StepState_STEP_STATE_DONE,
	jobpb.State_STATE_FAILED:  pb.StepState_STEP_STATE_FAILED,
	jobpb.State_STATE_SKIPPED: pb.StepState_STEP_STATE_SKIPPED,
}

// follow reads job's progress into the run's steps from first on, until
// it ends, then collects it (in the run's Audit Log chain: the collect
// fails with the job's error when it failed).
func (r *run) follow(ctx context.Context, c *component, job *jobpb.Job, first int) error {
	host := job.GetAgent()
	show := func(j *jobpb.Job) {
		r.edit(func(p *pb.Run) {
			p.Job = j
			for k, s := range j.GetSteps() {
				if i := first + k; i < len(p.States) {
					p.States[i] = jobStates[s.GetState()]
					p.Details[i] = s.GetDetail()
				}
			}
			if v := j.GetResult()["unchanged"]; v == "true" {
				p.Outcome = "already up to date"
			} else if n := len(j.GetResult()); n > 0 && strings.HasPrefix(j.GetOp(), "compose.update") {
				p.Outcome = fmt.Sprintf("%d Compose %s updated: %s", n, plural(n, "service", "services"), strings.Join(slices.Sorted(maps.Keys(j.GetResult())), ", "))
			}
		})
	}
	show(job)
	for job.GetEnd() == nil {
		select {
		case <-ctx.Done():
			return status.Errorf(codes.DeadlineExceeded, "%s is still running on %s; ddash stopped following it", job.GetTitle(), host)
		case <-time.After(followEvery):
		}
		res, err := c.agents.GetJob(ctx, &dapb.GetJobRequest{Host: host, Id: job.GetId()})
		if status.Code(err) == codes.NotFound {
			return status.Errorf(codes.Unknown, "the Docker Agent on %s no longer knows %s: it may have restarted", host, job.GetTitle())
		}
		if err != nil {
			continue // a failed read doesn't end the job
		}
		job = res.GetJob()
		show(job)
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if _, err := c.agents.CollectJob(callCtx, &dapb.CollectJobRequest{Host: host, Id: job.GetId()}); err != nil {
		return err
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ref is a container as checks names its Workload.
func workloadRef(host, container string) *workloadpb.WorkloadRef {
	return &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: host, Id: container}
}

// maintain puts containers into Maintenance for d (0: until ended) or
// until their Checks are up. A failure doesn't stop the run: the Operator
// may get paged, but gets what they asked for.
func (c *component) maintain(cs []*dapb.Container, d time.Duration, untilUp bool) func(context.Context, *run, int) error {
	return func(ctx context.Context, r *run, i int) error {
		host := r.snapshot().GetHost()
		var failed []string
		for _, ct := range cs {
			req := &checkspb.StartMaintenanceRequest{Scope: &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: workloadRef(host, ct.GetName())}}, UntilUp: untilUp}
			if d > 0 {
				req.Duration = durationpb.New(d)
			}
			if _, err := c.checks.StartMaintenance(ctx, req); err != nil {
				c.log.Warn("can't start Maintenance", "container", ct.GetName(), "err", err)
				failed = append(failed, ct.GetName())
			}
		}
		if len(failed) > 0 {
			r.state(i, pb.StepState_STEP_STATE_DONE, "couldn't start it for "+strings.Join(failed, ", "))
		}
		return nil
	}
}

func (c *component) endMaintenance(t target, services []string) func(context.Context, *run, int) error {
	return func(ctx context.Context, r *run, i int) error {
		for _, ct := range t.containers {
			if len(services) > 0 && !slices.Contains(services, ct.GetService()) {
				continue
			}
			scope := &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: workloadRef(t.host.GetHost(), ct.GetName())}}
			if _, err := c.checks.EndMaintenance(ctx, &checkspb.EndMaintenanceRequest{Scope: scope}); err != nil {
				c.log.Warn("can't end Maintenance", "container", ct.GetName(), "err", err)
			}
		}
		return nil
	}
}

// runOn is the id of the run in flight on host's project, or "".
func (c *component) runOn(host, project string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, r := range c.runs {
		p := r.snapshot()
		if p.GetHost() == host && p.GetProject() == project && p.GetEnded() == nil {
			return id
		}
	}
	return ""
}

func (c *component) RunAction(req *pb.RunActionRequest, stream pb.ComposeService_RunActionServer) error {
	ctx := stream.Context()
	plan, w, err := c.plan(ctx, req.GetAction())
	if err != nil {
		return err
	}
	if !proto.Equal(plan, req.GetPlan()) {
		st, _ := status.New(codes.Aborted, "changed since you looked: check the new Plan, then confirm again").WithDetails(plan)
		return st.Err()
	}
	if len(plan.GetBlocked()) > 0 {
		return status.Error(codes.FailedPrecondition, strings.Join(plan.GetBlocked(), " "))
	}
	host, project := w.tgt.host.GetHost(), w.tgt.project.GetName()
	if a, ok := req.GetAction().GetAction().(*pb.ComposeAction_Add); ok {
		project = a.Add.GetName()
	}
	r, err := c.claim(host, project, w.title, plan, sdk.Actor(ctx))
	if err != nil {
		return err
	}
	// The run keeps the call's actor and chain, not its cancellation.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RunAtMost)
	stop := context.AfterFunc(c.life, cancel)
	go func() {
		defer cancel()
		defer stop()
		c.execute(runCtx, r, w)
	}()

	deadline := time.After(StreamFor)
	for {
		changed := r.wait()
		snap := r.snapshot()
		if err := stream.Send(&pb.RunActionResponse{Run: snap}); err != nil {
			// The browser left: the run goes on (ADR-0004).
			r.mu.Lock()
			r.streamed = true
			r.mu.Unlock()
			return nil
		}
		if snap.GetEnded() != nil {
			if snap.GetError() != "" {
				return status.Error(codes.Aborted, snap.GetError())
			}
			return nil
		}
		select {
		case <-changed:
		case <-deadline:
			r.mu.Lock()
			r.streamed = true
			r.mu.Unlock()
			return nil
		case <-ctx.Done():
			r.mu.Lock()
			r.streamed = true
			r.mu.Unlock()
			return nil
		}
	}
}

// claim registers a run on host's project, refusing a second one.
func (c *component) claim(host, project, title string, plan *pb.Plan, actor string) (*run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, r := range c.runs {
		p := r.snapshot()
		if p.GetEnded() != nil && time.Since(p.GetEnded().AsTime()) > KeepRuns {
			delete(c.runs, id)
			continue
		}
		if p.GetHost() == host && p.GetProject() == project && p.GetEnded() == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "%s is already running on %s", p.GetTitle(), project)
		}
	}
	c.seq++
	id := strconv.FormatInt(time.Now().UnixMilli(), 36) + "-" + strconv.Itoa(c.seq)
	p := &pb.Run{Id: id, Host: host, Project: project, Title: title, Steps: plan.GetSteps(), Started: timestamppb.Now(), Actor: actor}
	for range plan.GetSteps() {
		p.States = append(p.States, pb.StepState_STEP_STATE_PENDING)
		p.Details = append(p.Details, "")
	}
	r := &run{p: p, changed: make(chan struct{})}
	c.runs[id] = r
	return r, nil
}

// execute runs w's steps in order and stops at the first that fails.
func (c *component) execute(ctx context.Context, r *run, w *work) {
	var failed error
	for i, s := range w.steps {
		if s.run == nil {
			continue // shown by an earlier step's job
		}
		if snap := r.snapshot(); snap.GetStates()[i] != pb.StepState_STEP_STATE_PENDING {
			continue
		}
		r.state(i, pb.StepState_STEP_STATE_RUNNING, "")
		callCtx := ctx
		var cancel context.CancelFunc = func() {}
		if s.jobStep < 0 {
			callCtx, cancel = context.WithTimeout(ctx, callTimeout)
		}
		err := s.run(callCtx, r, i)
		cancel()
		if err != nil {
			failed = fmt.Errorf("%s: %s", r.snapshot().GetSteps()[i].GetTitle(), status.Convert(err).Message())
			r.edit(func(p *pb.Run) {
				// The job's own failed step, else this one.
				marked := false
				for k := range p.States {
					if p.States[k] == pb.StepState_STEP_STATE_FAILED {
						marked = true
					}
				}
				if !marked {
					p.States[i] = pb.StepState_STEP_STATE_FAILED
					p.Details[i] = status.Convert(err).Message()
				}
				for k := range p.States {
					if p.States[k] == pb.StepState_STEP_STATE_RUNNING {
						p.States[k] = pb.StepState_STEP_STATE_FAILED
					}
				}
			})
			break
		}
		r.edit(func(p *pb.Run) {
			if p.States[i] == pb.StepState_STEP_STATE_RUNNING || (s.jobStep < 0 && p.States[i] != pb.StepState_STEP_STATE_SKIPPED) {
				p.States[i] = pb.StepState_STEP_STATE_DONE
			}
		})
	}
	r.edit(func(p *pb.Run) {
		p.Ended = timestamppb.Now()
		if failed != nil {
			p.Error = failed.Error()
		} else if p.Outcome == "" {
			p.Outcome = "done"
		}
	})
	c.outcome(ctx, r)
}

// outcome notifies how a run ended when the Operator may not be watching:
// it failed, or it ran on after RunAction returned.
func (c *component) outcome(ctx context.Context, r *run) {
	r.mu.Lock()
	streamed := r.streamed
	r.mu.Unlock()
	p := r.snapshot()
	if p.GetError() == "" && !streamed {
		return
	}
	n := &notifypb.Notification{Category: notifypb.Category_CATEGORY_ACTION_OUTCOMES, Click: c.public + "/docker"}
	if p.GetError() != "" {
		n.Title, n.Message, n.Tags, n.Priority = p.GetTitle()+" failed", p.GetError(), []string{"warning"}, 4
	} else {
		n.Title, n.Message, n.Tags = p.GetTitle()+" done", p.GetOutcome(), []string{"white_check_mark"}
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if _, err := c.notify.Send(ctx, &notifypb.SendRequest{Notification: n}); err != nil {
		c.log.Warn("can't notify a run's outcome", "run", p.GetTitle(), "err", err)
	}
}

func (c *component) ListRuns(context.Context, *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	c.mu.Lock()
	rs := slices.Collect(maps.Values(c.runs))
	c.mu.Unlock()
	out := &pb.ListRunsResponse{}
	for _, r := range rs {
		p := r.snapshot()
		if p.GetEnded() != nil && time.Since(p.GetEnded().AsTime()) > KeepRuns {
			continue
		}
		out.Runs = append(out.Runs, p)
	}
	slices.SortFunc(out.Runs, func(a, b *pb.Run) int { return a.GetStarted().AsTime().Compare(b.GetStarted().AsTime()) })
	return out, nil
}
