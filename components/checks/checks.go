// Package checks is the checks Component: it probes Checks on fixed
// defaults, decides when each is up or down, records every beat in
// check_event and serves the Checks' states from memory. It sends a
// Notification through ntfy-api when a Check is confirmed down and when it
// recovers.
package checks

import (
	"cmp"
	"context"
	"crypto/x509"
	"embed"
	"log/slog"
	"net"
	"net/http"
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
	// CertWarning is how long before its certificate expires an HTTPS Check
	// warns. It stays up meanwhile.
	CertWarning = 14 * 24 * time.Hour
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations
var migrations embed.FS

// Definition is checks, ready for sdk.Serve.
func Definition() sdk.Definition {
	return define((&net.Dialer{}).DialContext, nil)
}

// DefinitionDialing is checks with its TCP, DNS and HTTP probes dialing
// through dial instead of the network, for tests: a time-rule test fakes
// the targets' transport inside a synctest bubble, and a host test keeps
// App Checks off the real network.
func DefinitionDialing(dial func(ctx context.Context, network, addr string) (net.Conn, error)) sdk.Definition {
	return define(dial, nil)
}

// define is checks probing TCP, DNS and HTTP through dial, trusting roots
// for HTTPS (the system's when nil).
func define(dial dialFunc, roots *x509.CertPool) sdk.Definition {
	return sdk.Define(sdk.Spec[*checkspb.ChecksConfig]{
		Manifest:   manifest,
		Service:    &checkspb.ChecksService_ServiceDesc,
		Migrations: migrations,
		New: func() sdk.Component[*checkspb.ChecksConfig] {
			return &component{dial: dial, roots: roots}
		},
	})
}

type component struct {
	checkspb.UnimplementedChecksServiceServer
	dial dialFunc
	// roots are the CAs HTTPS probes trust; the system's when nil.
	roots *x509.CertPool
	// web makes the HTTP(S) probes.
	web *http.Client

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
	// maint is the Maintenance in force, by scope key; wake tells the
	// expiry loop it changed.
	maint map[string]*maintenance
	wake  chan struct{}
	// reported is every item reported with ReportState, by item.
	reported map[string]*reported
	// flaps is each item's recent downs and Flapping, in memory only.
	flaps map[string]*flap
}

// check is one Check in memory, the source of every read.
type check struct {
	id       int64
	spec     spec
	muted    bool
	disabled bool
	standing standing

	// appID is the App an App Check follows; empty for a manual Check.
	appID string
	// appEnabled is false while an App Check's App is hidden; always true
	// for a manual Check.
	appEnabled bool
	// goneAt is when an App Check's App left apps' set; zero while there.
	goneAt time.Time
	// workload is the Workload behind an App Check's App; zero without one.
	workload workload

	// stop ends the Check's loop; nil while it isn't probed.
	stop context.CancelFunc
	// gen counts the loops started for the Check, so a beat of an earlier
	// loop (whose spec may since have changed) is dropped.
	gen int
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *checkspb.ChecksConfig) error {
	c.ctx, c.log, c.publicURL = ctx, env.Log, env.PublicURL
	c.web = webClient(ctx, c.dial, c.roots)
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
	maint, err := c.db.loadMaintenance(ctx)
	if err != nil {
		return err
	}
	reported, err := c.db.loadReported(ctx)
	if err != nil {
		return err
	}
	sdk.Every(ctx, SweepInterval, c.sweep)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notified, c.out = last, outbox{queued: map[string]time.Time{}}
	c.maint, c.wake = maint, make(chan struct{}, 1)
	c.reported, c.flaps = reported, map[string]*flap{}
	go c.expireMaintenance(ctx)
	sdk.Every(ctx, DeliveryTick, c.deliver)
	c.checks = map[int64]*check{}
	for _, ch := range loaded {
		c.checks[ch.id] = ch
		if ch.probed() {
			c.probeEvery(ch)
		}
		// The outbox is lost on restart: queue again what changed since
		// it was last notified but wasn't delivered before the stop.
		c.requeue(checkItem(ch.id))
		c.requeue(certItem(ch.id))
	}
	for item := range c.reported {
		c.requeue(item)
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

// probed reports whether ch is probed: not disabled by the Operator and,
// for an App Check, its App shown and present.
func (ch *check) probed() bool {
	return !ch.disabled && ch.appEnabled && ch.goneAt.IsZero()
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
	maint, _ := c.maintained(ch, next.lastBeat)
	c.writeErr = c.db.recordBeat(ctx, id, r, next, changed, maint)
	if c.writeErr != nil {
		c.log.Error("record beat", "check", id, "err", c.writeErr)
	}
	was := ch.standing
	ch.standing = next
	if changed {
		c.changed(checkItem(id), was.since, next.state == checkspb.State_STATE_DOWN, next.since)
	}
	c.requeue(certItem(id))
	c.endWhenUp(ctx, ch)
}

func (c *component) ListChecks(context.Context, *checkspb.ListChecksRequest) (*checkspb.ListChecksResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := make([]*check, 0, len(c.checks))
	for _, ch := range c.checks {
		if ch.goneAt.IsZero() {
			all = append(all, ch)
		}
	}
	slices.SortFunc(all, func(a, b *check) int {
		return cmp.Or(cmp.Compare(a.spec.shownName(), b.spec.shownName()), cmp.Compare(a.id, b.id))
	})
	resp := &checkspb.ListChecksResponse{}
	for _, ch := range all {
		resp.Checks = append(resp.Checks, c.proto(ch))
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
	ch := &check{spec: s, appEnabled: true, standing: standing{state: checkspb.State_STATE_PENDING, since: now()}}
	if ch.id, err = c.db.create(ctx, ch); err != nil {
		return nil, err
	}
	c.checks[ch.id] = ch
	c.probeEvery(ch)
	return &checkspb.CreateCheckResponse{Check: c.proto(ch)}, nil
}

func (c *component) UpdateCheck(ctx context.Context, req *checkspb.UpdateCheckRequest) (*checkspb.UpdateCheckResponse, error) {
	s, err := parseSpec(req.GetSpec())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.manual(req.GetId())
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
	restart := s.probes() != ch.spec.probes() && ch.probed()
	ch.spec, ch.standing = s, fresh
	if restart {
		ch.stopProbing()
		c.probeEvery(ch)
	}
	return &checkspb.UpdateCheckResponse{Check: c.proto(ch)}, nil
}

func (c *component) DeleteCheck(ctx context.Context, req *checkspb.DeleteCheckRequest) (*checkspb.DeleteCheckResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.manual(req.GetId())
	if err != nil {
		return nil, err
	}
	if err := c.db.delete(ctx, ch.id); err != nil {
		return nil, err
	}
	ch.stopProbing()
	delete(c.checks, ch.id)
	delete(c.notified, checkItem(ch.id))
	delete(c.maint, scope{checkID: ch.id}.key())
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
	return &checkspb.SetMutedResponse{Check: c.proto(ch)}, nil
}

func (c *component) SetDisabled(ctx context.Context, req *checkspb.SetDisabledRequest) (*checkspb.SetDisabledResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, err := c.get(req.GetId())
	if err != nil {
		return nil, err
	}
	if !req.GetDisabled() && !ch.appEnabled {
		return nil, status.Errorf(codes.FailedPrecondition, "%s's App is hidden: show the App to enable its Check", ch.spec.shownName())
	}
	if err := c.db.setFlag(ctx, ch.id, "disabled", req.GetDisabled()); err != nil {
		return nil, err
	}
	was := ch.probed()
	ch.disabled = req.GetDisabled()
	switch {
	case was && !ch.probed():
		ch.stopProbing()
	case !was && ch.probed():
		c.probeEvery(ch)
	}
	return &checkspb.SetDisabledResponse{Check: c.proto(ch)}, nil
}

// get returns Check id. Call with c.mu held.
func (c *component) get(id int64) (*check, error) {
	ch := c.checks[id]
	if ch == nil {
		return nil, status.Errorf(codes.NotFound, "no Check %d", id)
	}
	return ch, nil
}

// manual returns manual Check id: an App Check follows its App instead.
// Call with c.mu held.
func (c *component) manual(id int64) (*check, error) {
	ch, err := c.get(id)
	if err == nil && ch.appID != "" {
		err = status.Errorf(codes.FailedPrecondition, "%s is an App Check: it follows its App in apps", ch.spec.shownName())
	}
	return ch, err
}
