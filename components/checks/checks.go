// Package checks is the checks Component: it probes Checks on fixed
// defaults, decides when each is up or down, records every beat in
// check_event and serves the Checks' states from memory. It sends a
// Notification through ntfy-api when a Check is confirmed down and when it
// recovers.
package checks

import (
	"cmp"
	"context"
	"embed"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk"
)

// The fixed defaults every Check runs on. There are no per-Check overrides.
const (
	// Interval is the time between a Check's beats (plus sdk.Every's jitter).
	Interval = 60 * time.Second
	// Timeout bounds one probe.
	Timeout = 10 * time.Second
	// DownAfter is how many failed beats in a row make a Check down. One
	// success makes it up again.
	DownAfter = 3
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations
var migrations embed.FS

// Definition is checks, ready for sdk.Serve.
func Definition() sdk.Definition {
	return define((&net.Dialer{}).DialContext)
}

// define is checks probing TCP and DNS through dial.
func define(dial dialFunc) sdk.Definition {
	return sdk.Define(sdk.Spec[*checkspb.ChecksConfig]{
		Manifest:   manifest,
		Service:    &checkspb.ChecksService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*checkspb.ChecksConfig] { return &component{dial: dial} },
	})
}

type component struct {
	checkspb.UnimplementedChecksServiceServer
	dial dialFunc

	// ctx is the Component's lifetime; each Check's loop runs under it.
	ctx       context.Context
	db        *store
	log       *slog.Logger
	ntfy      ntfyapipb.NtfyApiServiceClient
	publicURL string

	mu     sync.Mutex
	checks map[int64]*check
	// writeErr is why the last write to the DB failed; nil once one works.
	writeErr error
	// notified is the state each item was last notified in.
	notified map[string]notified
	out      outbox
}

// check is one Check in memory, the source of every read.
type check struct {
	id       int64
	spec     spec
	muted    bool
	disabled bool
	standing standing

	// stop ends the Check's loop; nil while it isn't probed.
	stop context.CancelFunc
	// gen counts the loops started for the Check, so a beat of an earlier
	// loop (whose spec may since have changed) is dropped.
	gen int
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *checkspb.ChecksConfig) error {
	c.ctx, c.log, c.publicURL = ctx, env.Log, env.PublicURL
	c.db = &store{env.DB}
	c.ntfy = sdk.Require(env, ntfyapipb.NewNtfyApiServiceClient)
	loaded, err := c.db.load(ctx)
	if err != nil {
		return err
	}
	last, err := c.db.loadNotified(ctx)
	if err != nil {
		return err
	}
	sdk.Every(ctx, SweepInterval, c.sweep)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notified, c.out = last, outbox{queued: map[string]time.Time{}}
	sdk.Every(ctx, DeliveryTick, c.deliver)
	c.checks = map[int64]*check{}
	for _, ch := range loaded {
		c.checks[ch.id] = ch
		if !ch.disabled {
			c.probeEvery(ch)
		}
		// The outbox is lost on restart: queue again what changed since
		// it was last notified but wasn't delivered before the stop.
		if _, ok := c.wanted(checkItem(ch.id)); ok {
			c.queue(checkItem(ch.id))
		}
	}
	return nil
}

// Health is degraded while beats can't be recorded.
func (c *component) Health(context.Context) sdk.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return sdk.Health{State: sdk.Degraded, Message: "can't record beats: " + c.writeErr.Error()}
	}
	return sdk.Health{State: sdk.OK}
}

// probeEvery starts ch's loop: its first beat is due Interval after its
// last one (at once if that has passed or it was never probed), so a
// restart leaves a gap rather than catching up. Call with c.mu held.
func (c *component) probeEvery(ch *check) {
	ctx, stop := context.WithCancel(c.ctx)
	ch.stop = stop
	ch.gen++
	id, gen := ch.id, ch.gen
	var due time.Time
	if !ch.standing.lastBeat.IsZero() {
		due = ch.standing.lastBeat.Add(Interval)
	}
	sdk.Every(ctx, Interval, func(ctx context.Context) {
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		due = time.Time{}
		c.beat(ctx, id, gen)
	})
}

// stopProbing ends ch's loop. Call with c.mu held.
func (ch *check) stopProbing() {
	if ch.stop != nil {
		ch.stop()
		ch.stop = nil
	}
	ch.gen++
}

// beat probes Check id once and records the result, unless the loop gen
// was replaced meanwhile or the Component is stopping.
func (c *component) beat(ctx context.Context, id int64, gen int) {
	c.mu.Lock()
	ch := c.checks[id]
	if ch == nil || ch.gen != gen {
		c.mu.Unlock()
		return
	}
	s := ch.spec
	c.mu.Unlock()

	r := c.probe(ctx, s)
	if ctx.Err() != nil {
		return // stopped mid-probe: that's no failure of the target
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if ch := c.checks[id]; ch == nil || ch.gen != gen {
		return
	}
	next := ch.standing.after(r, now())
	changed := next.state != ch.standing.state
	// Maintenance (#49) will record its beats as maint.
	c.writeErr = c.db.recordBeat(ctx, id, r, next, changed, false)
	if c.writeErr != nil {
		c.log.Error("record beat", "check", id, "err", c.writeErr)
	}
	ch.standing = next
	if changed {
		c.queue(checkItem(id))
	}
}

func (c *component) ListChecks(context.Context, *checkspb.ListChecksRequest) (*checkspb.ListChecksResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := make([]*check, 0, len(c.checks))
	for _, ch := range c.checks {
		all = append(all, ch)
	}
	slices.SortFunc(all, func(a, b *check) int {
		return cmp.Or(cmp.Compare(a.spec.shownName(), b.spec.shownName()), cmp.Compare(a.id, b.id))
	})
	resp := &checkspb.ListChecksResponse{}
	for _, ch := range all {
		resp.Checks = append(resp.Checks, ch.proto())
	}
	return resp, nil
}

func (c *component) CreateCheck(ctx context.Context, req *checkspb.CreateCheckRequest) (*checkspb.CreateCheckResponse, error) {
	s, err := parseSpec(req.GetSpec())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := &check{spec: s, standing: standing{state: checkspb.State_STATE_PENDING, since: now()}}
	if ch.id, err = c.db.create(ctx, ch); err != nil {
		return nil, err
	}
	c.checks[ch.id] = ch
	c.probeEvery(ch)
	return &checkspb.CreateCheckResponse{Check: ch.proto()}, nil
}

func (c *component) UpdateCheck(ctx context.Context, req *checkspb.UpdateCheckRequest) (*checkspb.UpdateCheckResponse, error) {
	s, err := parseSpec(req.GetSpec())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.get(req.GetId())
	if err != nil {
		return nil, err
	}
	fresh := ch.standing
	if s.probes() != ch.spec.probes() {
		fresh = standing{state: checkspb.State_STATE_PENDING, since: now()}
	}
	if err := c.db.update(ctx, ch.id, s, fresh); err != nil {
		return nil, err
	}
	restart := s.probes() != ch.spec.probes() && !ch.disabled
	ch.spec, ch.standing = s, fresh
	if restart {
		ch.stopProbing()
		c.probeEvery(ch)
	}
	return &checkspb.UpdateCheckResponse{Check: ch.proto()}, nil
}

func (c *component) DeleteCheck(ctx context.Context, req *checkspb.DeleteCheckRequest) (*checkspb.DeleteCheckResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.get(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := c.db.delete(ctx, ch.id); err != nil {
		return nil, err
	}
	ch.stopProbing()
	delete(c.checks, ch.id)
	delete(c.notified, checkItem(ch.id))
	return &checkspb.DeleteCheckResponse{}, nil
}

func (c *component) SetMuted(ctx context.Context, req *checkspb.SetMutedRequest) (*checkspb.SetMutedResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.get(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := c.db.setFlag(ctx, ch.id, "muted", req.GetMuted()); err != nil {
		return nil, err
	}
	ch.muted = req.GetMuted()
	return &checkspb.SetMutedResponse{Check: ch.proto()}, nil
}

func (c *component) SetDisabled(ctx context.Context, req *checkspb.SetDisabledRequest) (*checkspb.SetDisabledResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.get(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := c.db.setFlag(ctx, ch.id, "disabled", req.GetDisabled()); err != nil {
		return nil, err
	}
	switch {
	case req.GetDisabled() && !ch.disabled:
		ch.stopProbing()
	case !req.GetDisabled() && ch.disabled:
		c.probeEvery(ch)
	}
	ch.disabled = req.GetDisabled()
	return &checkspb.SetDisabledResponse{Check: ch.proto()}, nil
}

// get returns Check id. Call with c.mu held.
func (c *component) get(id int64) (*check, error) {
	ch := c.checks[id]
	if ch == nil {
		return nil, status.Errorf(codes.NotFound, "no Check %d", id)
	}
	return ch, nil
}
