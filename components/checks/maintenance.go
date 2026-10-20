package checks

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// MaxMaintenance is the longest a Maintenance with a duration lasts.
const MaxMaintenance = 24 * time.Hour

// scope is what a Maintenance covers: one Check, a Workload's App Checks
// or everything.
type scope struct {
	everything bool
	checkID    int64
	workload   workload
}

// key identifies the scope: one Maintenance per key. LXC and VM are keyed
// by VMID alone, as the node changes on migration.
func (s scope) key() string {
	switch {
	case s.everything:
		return "everything"
	case s.checkID != 0:
		return "check:" + strconv.FormatInt(s.checkID, 10)
	}
	w := s.workload
	switch w.kind {
	case workloadpb.Kind_KIND_LXC, workloadpb.Kind_KIND_VM:
		return "workload:" + kinds[w.kind] + ":" + w.id
	case workloadpb.Kind_KIND_EXTERNAL:
		return "workload:external:" + w.host
	}
	return "workload:" + kinds[w.kind] + ":" + w.host + "/" + w.id
}

// rank orders scopes: everything, then Workloads, then single Checks.
func (s scope) rank() int {
	switch {
	case s.everything:
		return 0
	case s.checkID != 0:
		return 2
	}
	return 1
}

// covers reports whether ch is in the scope: a Workload covers the App
// Checks whose App runs on it.
func (s scope) covers(ch *check) bool {
	switch {
	case s.everything:
		return true
	case s.checkID != 0:
		return ch.id == s.checkID
	case ch.appID == "":
		return false
	}
	return scope{workload: ch.workload}.key() == s.key()
}

func (s scope) proto() *checkspb.MaintenanceScope {
	switch {
	case s.everything:
		return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Everything{Everything: true}}
	case s.checkID != 0:
		return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_CheckId{CheckId: s.checkID}}
	}
	return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: s.workload.proto()}}
}

// parseScope checks a scope sent by a caller: a Workload needs what
// identifies it.
func parseScope(p *checkspb.MaintenanceScope) (scope, error) {
	switch v := p.GetScope().(type) {
	case *checkspb.MaintenanceScope_Everything:
		if v.Everything {
			return scope{everything: true}, nil
		}
	case *checkspb.MaintenanceScope_CheckId:
		if v.CheckId != 0 {
			return scope{checkID: v.CheckId}, nil
		}
	case *checkspb.MaintenanceScope_Workload:
		w := workloadOf(v.Workload)
		var problem string
		switch w.kind {
		case workloadpb.Kind_KIND_LXC, workloadpb.Kind_KIND_VM:
			if w.id == "" {
				problem = "an LXC or VM Workload needs its VMID as id"
			}
		case workloadpb.Kind_KIND_DOCKER:
			if w.host == "" || w.id == "" {
				problem = "a Docker Workload needs its host and container name as id"
			}
		case workloadpb.Kind_KIND_EXTERNAL:
			if w.host == "" {
				problem = "an external Workload needs its address as host"
			}
		default:
			problem = "the Workload needs a kind"
		}
		if problem != "" {
			return scope{}, status.Error(codes.InvalidArgument, problem)
		}
		return scope{workload: w}, nil
	}
	return scope{}, status.Error(codes.InvalidArgument, "scope must be a Check, a Workload or everything")
}

// maintenance is one Maintenance in force.
type maintenance struct {
	scope   scope
	started time.Time
	// ends is zero when it lasts until ended.
	ends    time.Time
	untilUp bool
}

// on reports whether m is in force at at.
func (m *maintenance) on(at time.Time) bool { return m.ends.IsZero() || at.Before(m.ends) }

func (m *maintenance) proto() *checkspb.Maintenance {
	p := &checkspb.Maintenance{Scope: m.scope.proto(), Started: timestamppb.New(m.started), UntilUp: m.untilUp}
	if !m.ends.IsZero() {
		p.Ends = timestamppb.New(m.ends)
	}
	return p
}

// maintained reports whether ch is in Maintenance at at, and when the last
// Maintenance covering it ends (zero when one lasts until ended). Call with
// c.mu held.
func (c *component) maintained(ch *check, at time.Time) (in bool, ends time.Time) {
	forever := false
	for _, m := range c.maint {
		if !m.on(at) || !m.scope.covers(ch) {
			continue
		}
		in = true
		if m.ends.IsZero() {
			forever = true
		} else if m.ends.After(ends) {
			ends = m.ends
		}
	}
	if forever {
		ends = time.Time{}
	}
	return in, ends
}

// proto is ch as callers see it, with its Maintenance. Call with c.mu held.
func (c *component) proto(ch *check) *checkspb.Check {
	p := ch.proto()
	in, ends := c.maintained(ch, now())
	p.Maintenance = in
	if in && !ends.IsZero() {
		p.MaintenanceEnds = timestamppb.New(ends)
	}
	return p
}

func (c *component) StartMaintenance(ctx context.Context, req *checkspb.StartMaintenanceRequest) (*checkspb.StartMaintenanceResponse, error) {
	s, err := parseScope(req.GetScope())
	if err != nil {
		return nil, err
	}
	var d time.Duration
	if req.Duration != nil {
		if d = req.GetDuration().AsDuration(); d <= 0 || d > MaxMaintenance {
			return nil, status.Error(codes.InvalidArgument, "duration must be more than 0 and at most 24 h; leave it unset to last until ended")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.checkID != 0 {
		if _, err := c.get(s.checkID); err != nil {
			return nil, err
		}
	}
	m := &maintenance{scope: s, started: now(), untilUp: req.GetUntilUp()}
	if d > 0 {
		m.ends = m.started.Add(d).Truncate(time.Millisecond)
	}
	if err := c.db.saveMaintenance(ctx, m); err != nil {
		return nil, err
	}
	c.maint[s.key()] = m
	c.rewake()
	return &checkspb.StartMaintenanceResponse{Maintenance: m.proto()}, nil
}

func (c *component) EndMaintenance(ctx context.Context, req *checkspb.EndMaintenanceRequest) (*checkspb.EndMaintenanceResponse, error) {
	s, err := parseScope(req.GetScope())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.endMaintenance(ctx, s.key()); err != nil {
		return nil, err
	}
	return &checkspb.EndMaintenanceResponse{}, nil
}

func (c *component) ListMaintenance(context.Context, *checkspb.ListMaintenanceRequest) (*checkspb.ListMaintenanceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := now()
	var on []*maintenance
	for _, m := range c.maint {
		if m.on(at) {
			on = append(on, m)
		}
	}
	slices.SortFunc(on, func(a, b *maintenance) int {
		return cmp.Or(cmp.Compare(a.scope.rank(), b.scope.rank()), cmp.Compare(a.scope.key(), b.scope.key()))
	})
	resp := &checkspb.ListMaintenanceResponse{}
	for _, m := range on {
		resp.Maintenance = append(resp.Maintenance, m.proto())
	}
	return resp, nil
}

// endMaintenance ends the Maintenance keyed key, if any. Call with c.mu
// held.
func (c *component) endMaintenance(ctx context.Context, key string) error {
	if _, ok := c.maint[key]; !ok {
		return nil
	}
	if err := c.db.deleteMaintenance(ctx, key); err != nil {
		return err
	}
	delete(c.maint, key)
	c.rewake()
	return nil
}

// endWhenUp ends each until-up Maintenance covering ch once every probed
// Check it covers is up with a beat after it started. Call with c.mu held,
// after ch's beat.
func (c *component) endWhenUp(ctx context.Context, ch *check) {
	for key, m := range c.maint {
		if !m.untilUp || !m.scope.covers(ch) || !c.allUpSince(m) {
			continue
		}
		if err := c.endMaintenance(ctx, key); err != nil {
			c.log.Error("end Maintenance", "scope", key, "err", err)
		}
	}
}

// allUpSince reports whether every probed Check m covers is up with a beat
// after m started. Call with c.mu held.
func (c *component) allUpSince(m *maintenance) bool {
	for _, ch := range c.checks {
		if !ch.probed() || !m.scope.covers(ch) {
			continue
		}
		if ch.standing.state != checkspb.State_STATE_UP || !ch.standing.lastBeat.After(m.started) {
			return false
		}
	}
	return true
}

// rewake tells the expiry loop that the Maintenance changed. Call with
// c.mu held.
func (c *component) rewake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// expireMaintenance ends each Maintenance when its time is up, until ctx
// ends.
func (c *component) expireMaintenance(ctx context.Context) {
	for {
		c.mu.Lock()
		var next time.Time
		for _, m := range c.maint {
			if !m.ends.IsZero() && (next.IsZero() || m.ends.Before(next)) {
				next = m.ends
			}
		}
		c.mu.Unlock()
		var due <-chan time.Time
		var timer *time.Timer
		if !next.IsZero() {
			timer = time.NewTimer(time.Until(next))
			due = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-c.wake:
		case <-due:
		}
		if timer != nil {
			timer.Stop()
		}
		c.mu.Lock()
		at := time.Now()
		for key, m := range c.maint {
			if !m.on(at) {
				if err := c.endMaintenance(ctx, key); err != nil && ctx.Err() == nil {
					c.log.Error("end Maintenance", "scope", key, "err", err)
				}
			}
		}
		c.mu.Unlock()
	}
}

// loadMaintenance reads every Maintenance in force, expired ones too.
func (s *store) loadMaintenance(ctx context.Context) (map[string]*maintenance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, check_id, workload_kind, workload_host, workload_id, started, ends, until_up
		FROM maintenance`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*maintenance{}
	for rows.Next() {
		var (
			m            maintenance
			kind, wlKind string
			started      int64
			ends         sql.NullInt64
		)
		if err := rows.Scan(&kind, &m.scope.checkID, &wlKind, &m.scope.workload.host, &m.scope.workload.id,
			&started, &ends, &m.untilUp); err != nil {
			return nil, err
		}
		m.scope.everything = kind == "everything"
		m.scope.workload.kind = key(kinds, wlKind)
		m.started, m.ends = time.UnixMilli(started), fromMs(ends)
		out[m.scope.key()] = &m
	}
	return out, rows.Err()
}

// saveMaintenance stores m, replacing its scope's Maintenance.
func (s *store) saveMaintenance(ctx context.Context, m *maintenance) error {
	kind := "workload"
	switch {
	case m.scope.everything:
		kind = "everything"
	case m.scope.checkID != 0:
		kind = "check"
	}
	w := m.scope.workload
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO maintenance
		  (scope, kind, check_id, workload_kind, workload_host, workload_id, started, ends, until_up)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.scope.key(), kind, m.scope.checkID, kinds[w.kind], w.host, w.id, ms(m.started), orNull(m.ends), m.untilUp)
	return err
}

func (s *store) deleteMaintenance(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance WHERE scope = ?`, key)
	return err
}
