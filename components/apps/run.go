package apps

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// App Actions: their Plans built from the last reads (PlanAction), and run
// (RunAction) in their turn with the syncs and every other Action, which
// is the one-slot queue that keeps two Actions from both finding a name
// free.

func (c *component) PlanAction(ctx context.Context, req *appspb.PlanActionRequest) (*appspb.PlanActionResponse, error) {
	if err := c.waitSynced(ctx); err != nil {
		return nil, err
	}
	p, err := c.planning(ctx)
	if err != nil {
		return nil, err
	}
	return &appspb.PlanActionResponse{Plan: p.build(req.GetAction()).plan}, nil
}

func (c *component) RunAction(req *appspb.RunActionRequest, stream grpc.ServerStreamingServer[appspb.RunActionResponse]) error {
	ctx := stream.Context()
	if err := c.waitSynced(ctx); err != nil {
		return err
	}
	c.turn.Lock()
	defer c.turn.Unlock()

	// The fresh read: the Plan is built again from what the systems hold
	// now. A source that can't be read blocks the Actions writing to it.
	c.syncLocked(ctx, true)
	p, err := c.planning(ctx)
	if err != nil {
		return err
	}
	pl := p.build(req.GetAction())
	if !proto.Equal(pl.plan, req.GetPlan()) {
		st, derr := status.New(codes.Aborted, "changed since you looked: confirm the new Plan").WithDetails(pl.plan)
		if derr != nil {
			return derr
		}
		return st.Err()
	}
	if blocked := pl.blocked(); len(blocked) > 0 {
		return status.Error(codes.FailedPrecondition, strings.Join(blocked, " "))
	}

	progress := newProgress(stream, len(pl.plan.GetSteps()))
	done := 0
	var written *npmpb.ProxyHost
	if pl.npm != nil {
		progress.mark(done, done+1, appspb.StepState_STEP_STATE_RUNNING)
		if written, err = pl.npm(ctx, c.npm); err != nil {
			return c.stopped(ctx, pl, progress, done, err)
		}
		if pl.deletes != 0 {
			c.deletedProxyHost(pl.deletes)
		} else {
			c.wroteProxyHost(written)
		}
		progress.mark(done, done+1, appspb.StepState_STEP_STATE_DONE)
		done++
	}
	if len(pl.dns) > 0 {
		progress.mark(done, done+len(pl.dns), appspb.StepState_STEP_STATE_RUNNING)
		if _, err := c.pfsense.ChangeHostOverrides(ctx, &pfpb.ChangeHostOverridesRequest{Steps: pl.dns}); err != nil {
			r := batchResult(err)
			n := int(r.GetSucceeded())
			c.wroteDNS(pl.dns[:n])
			progress.mark(done, done+n, appspb.StepState_STEP_STATE_DONE)
			if r.GetApplyFailed() && n == len(pl.dns) {
				return c.stoppedApplying(ctx, pl, progress, done+n, err)
			}
			return c.stopped(ctx, pl, progress, done+n, err)
		}
		c.wroteDNS(pl.dns)
		progress.mark(done, done+len(pl.dns), appspb.StepState_STEP_STATE_DONE)
		done += len(pl.dns)
	}
	id := pl.id
	if pl.record != nil {
		progress.mark(done, done+1, appspb.StepState_STEP_STATE_RUNNING)
		recs, err := c.store.records(ctx)
		if err != nil {
			return c.stopped(ctx, pl, progress, done, err)
		}
		var ch changes
		ch, id = pl.record(recs, written)
		if err := c.store.save(ctx, ch.put, ch.drop); err != nil {
			return c.stopped(ctx, pl, progress, done, err)
		}
		progress.mark(done, done+1, appspb.StepState_STEP_STATE_DONE)
		done++
	}
	return stream.Send(&appspb.RunActionResponse{AppId: id, Done: int32(done), Reread: c.resyncLocked(ctx), Steps: progress.steps})
}

// progress is a running App Action's Plan steps' states, which it streams
// whole each time they change.
type progress struct {
	stream grpc.ServerStreamingServer[appspb.RunActionResponse]
	steps  []appspb.StepState
}

// newProgress is the progress of an App Action of n steps, all pending.
func newProgress(stream grpc.ServerStreamingServer[appspb.RunActionResponse], n int) *progress {
	steps := make([]appspb.StepState, n)
	for i := range steps {
		steps[i] = appspb.StepState_STEP_STATE_PENDING
	}
	return &progress{stream: stream, steps: steps}
}

// mark sets the steps from (counting from 0) up to to to s, and streams
// them when that changed any.
func (p *progress) mark(from, to int, s appspb.StepState) {
	changed := false
	for i := from; i < to && i < len(p.steps); i++ {
		changed = changed || p.steps[i] != s
		p.steps[i] = s
	}
	if changed {
		p.send()
	}
}

// fail marks step at (counting from 0) failed and those after it pending,
// and streams them.
func (p *progress) fail(at int) {
	p.steps[at] = appspb.StepState_STEP_STATE_FAILED
	for i := at + 1; i < len(p.steps); i++ {
		p.steps[i] = appspb.StepState_STEP_STATE_PENDING
	}
	p.send()
}

// send streams the steps' states. A message that can't be sent is lost:
// the Action runs to its end all the same (ADR-0004).
func (p *progress) send() {
	_ = p.stream.Send(&appspb.RunActionResponse{Steps: slices.Clone(p.steps)})
}

// deletedProxyHost drops a Proxy Host an App Action deleted from the last
// good read, as wroteProxyHost puts one in.
func (c *component) deletedProxyHost(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hosts == nil {
		return
	}
	hosts := maps.Clone(c.hosts)
	delete(hosts, id)
	c.hosts = hosts
}

// wroteProxyHost puts a Proxy Host an App Action wrote into the last good
// read, so the Action's result shows even when the sync ending it can't
// read Nginx Proxy Manager again, and a later sync with a stale read
// doesn't unlink it.
func (c *component) wroteProxyHost(h *npmpb.ProxyHost) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == nil || c.hosts == nil {
		return
	}
	hosts := maps.Clone(c.hosts)
	hosts[h.GetId()] = h
	c.hosts = hosts
}

// wroteDNS applies the Host Override steps an App Action wrote to the
// last good read, as wroteProxyHost does.
func (c *component) wroteDNS(steps []*pfpb.HostOverrideStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.overrides != nil {
		c.overrides = applySteps(c.overrides, steps)
	}
}

// applySteps is overrides as pfSense holds them after steps, written in
// order.
func applySteps(overrides []*pfpb.HostOverride, steps []*pfpb.HostOverrideStep) []*pfpb.HostOverride {
	out := make([]*pfpb.HostOverride, len(overrides))
	for i, o := range overrides {
		out[i] = proto.CloneOf(o)
	}
	named := func(n *pfpb.HostName) int {
		return slices.IndexFunc(out, func(o *pfpb.HostOverride) bool { return nameOf(o).same(nameOf(n)) })
	}
	for _, s := range steps {
		switch ch := s.GetChange().(type) {
		case *pfpb.HostOverrideStep_Create:
			out = append(out, proto.CloneOf(ch.Create))
		case *pfpb.HostOverrideStep_Delete:
			if i := named(ch.Delete); i >= 0 {
				out = slices.Delete(out, i, i+1)
			}
		case *pfpb.HostOverrideStep_Readdress:
			if i := named(ch.Readdress.GetHostOverride()); i >= 0 {
				out[i].Ips = slices.Clone(ch.Readdress.GetIps())
			}
		case *pfpb.HostOverrideStep_AddAlias:
			if i := named(ch.AddAlias.GetParent()); i >= 0 {
				out[i].Aliases = append(out[i].Aliases, proto.CloneOf(ch.AddAlias.GetAlias()))
			}
		case *pfpb.HostOverrideStep_RemoveAlias:
			if i := named(ch.RemoveAlias.GetParent()); i >= 0 {
				a := ch.RemoveAlias.GetAlias()
				out[i].Aliases = slices.DeleteFunc(out[i].Aliases, func(x *pfpb.HostOverrideAlias) bool {
					return nameOf(x).same(nameOf(a))
				})
			}
		}
	}
	return out
}

// batchResult is what a failed ChangeHostOverrides batch wrote, from its
// error's details: nothing when they don't say.
func batchResult(err error) *pfpb.ChangeHostOverridesResponse {
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfpb.ChangeHostOverridesResponse); ok {
			return r
		}
	}
	return &pfpb.ChangeHostOverridesResponse{}
}

// stopped is the error of an App Action that stopped at its step done
// (counting from 0) on err: it names the step and what was done before,
// and carries a RunActionResponse. The Action still ends with a sync.
func (c *component) stopped(ctx context.Context, pl *planned, progress *progress, done int, err error) error {
	steps := pl.plan.GetSteps()
	if done >= len(steps) {
		return c.stoppedAt(ctx, pl, done, fmt.Sprintf("Stopped after step %d of %d", done, len(steps)), err)
	}
	progress.fail(done)
	return c.stoppedAt(ctx, pl, done, fmt.Sprintf("Stopped at step %d of %d, %s", done+1, len(steps), steps[done].GetTitle()), err)
}

// stoppedApplying is the error of an App Action whose pfSense steps, up to
// its step done, were all written but pfSense failed to apply them.
func (c *component) stoppedApplying(ctx context.Context, pl *planned, progress *progress, done int, err error) error {
	progress.fail(done - 1)
	at := fmt.Sprintf("Stopped after step %d of %d, applying the Host Override changes in pfSense", done, len(pl.plan.GetSteps()))
	return c.stoppedAt(ctx, pl, done, at, err)
}

// stoppedAt is stopped's error, saying where the Action stopped with at.
func (c *component) stoppedAt(ctx context.Context, pl *planned, done int, at string, err error) error {
	reread := c.resyncLocked(ctx)
	steps := pl.plan.GetSteps()
	msg := fmt.Sprintf("%s: %s.", at, why(err))
	if done > 0 {
		var titles []string
		for _, s := range steps[:done] {
			titles = append(titles, s.GetTitle())
		}
		msg += " Done: " + strings.Join(titles, "; ") + "."
	}
	msg += " Nothing was rolled back."
	st, derr := status.New(status.Code(err), msg).WithDetails(&appspb.RunActionResponse{Done: int32(done), Reread: reread})
	if derr != nil {
		return status.Error(status.Code(err), msg)
	}
	return st.Err()
}

// why is a failed step's error as the Operator reads it. A name Nginx
// Proxy Manager says is in use passed "the name is free", which checks
// every Proxy Host: a Redirection or 404 Host holds it.
func why(err error) string {
	st := status.Convert(err)
	msg := strings.TrimSuffix(st.Message(), ".")
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetReason() == npmpb.ErrorReason_ERROR_REASON_DOMAIN_IN_USE.String() {
			msg += " (a Redirection or 404 Host)"
		}
	}
	return msg
}

// resyncLocked ends an App Action with a sync reading the sources afresh,
// bounded to c.resync. It returns why it couldn't re-read a source; ""
// when it could.
func (c *component) resyncLocked(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, c.resync)
	defer cancel()
	if failed := c.syncLocked(ctx, true); len(failed) > 0 {
		return "couldn't re-read " + strings.Join(failed, "; ")
	}
	return ""
}
