package apps

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

func (c *component) RunAction(ctx context.Context, req *appspb.RunActionRequest) (*appspb.RunActionResponse, error) {
	if err := c.waitSynced(ctx); err != nil {
		return nil, err
	}
	c.turn.Lock()
	defer c.turn.Unlock()

	// The fresh read: the Plan is built again from what the systems hold
	// now. A source that can't be read blocks the Actions writing to it.
	c.syncLocked(ctx, true)
	p, err := c.planning(ctx)
	if err != nil {
		return nil, err
	}
	pl := p.build(req.GetAction())
	if !proto.Equal(pl.plan, req.GetPlan()) {
		st, derr := status.New(codes.Aborted, "changed since you looked: confirm the new Plan").WithDetails(pl.plan)
		if derr != nil {
			return nil, derr
		}
		return nil, st.Err()
	}
	if blocked := pl.blocked(); len(blocked) > 0 {
		return nil, status.Error(codes.FailedPrecondition, strings.Join(blocked, " "))
	}

	done := 0
	var written *npmpb.ProxyHost
	if pl.npm != nil {
		if written, err = pl.npm(ctx, c.npm); err != nil {
			return nil, c.stopped(ctx, pl, done, err)
		}
		c.wroteProxyHost(written)
		done++
	}
	if len(pl.dns) > 0 {
		if _, err := c.pfsense.ChangeHostOverrides(ctx, &pfpb.ChangeHostOverridesRequest{Steps: pl.dns}); err != nil {
			n := succeeded(err)
			c.wroteDNS(pl.dns[:n])
			return nil, c.stopped(ctx, pl, done+n, err)
		}
		c.wroteDNS(pl.dns)
		done += len(pl.dns)
	}
	recs, err := c.store.records(ctx)
	if err != nil {
		return nil, c.stopped(ctx, pl, done, err)
	}
	ch, id := pl.record(recs, written)
	if err := c.store.save(ctx, ch.put, ch.drop); err != nil {
		return nil, c.stopped(ctx, pl, done, err)
	}
	done++
	return &appspb.RunActionResponse{AppId: id, Done: int32(done), Reread: c.resyncLocked(ctx)}, nil
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
		return slices.IndexFunc(out, func(o *pfpb.HostOverride) bool { return nameOf(o).same(hostName{n.GetHost(), n.GetDomain()}) })
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
					return hostName{x.GetHost(), x.GetDomain()}.same(hostName{a.GetHost(), a.GetDomain()})
				})
			}
		}
	}
	return out
}

// succeeded is how many steps of a failed ChangeHostOverrides batch were
// written, from its error's details.
func succeeded(err error) int {
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfpb.ChangeHostOverridesResponse); ok {
			return int(r.GetSucceeded())
		}
	}
	return 0
}

// stopped is the error of an App Action that stopped at its step done
// (counting from 0) on err: it names the step and what was done before,
// and carries a RunActionResponse. The Action still ends with a sync.
func (c *component) stopped(ctx context.Context, pl *planned, done int, err error) error {
	reread := c.resyncLocked(ctx)
	steps := pl.plan.GetSteps()
	msg := fmt.Sprintf("Stopped at step %d of %d, %s: %s.", done+1, len(steps), steps[done].GetTitle(), why(err))
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
