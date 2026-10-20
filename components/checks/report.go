package checks

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// reported is an item that isn't a Check, in the state its caller last
// reported.
type reported struct {
	name   string
	state  checkspb.State
	since  time.Time
	reason string
	path   string
}

// reportItem is the Notification item of the caller's item.
func reportItem(item string) string { return "report:" + item }

func (c *component) ReportState(ctx context.Context, req *checkspb.ReportStateRequest) (*checkspb.ReportStateResponse, error) {
	var problem string
	switch {
	case strings.TrimSpace(req.GetItem()) == "":
		problem = "item is required"
	case strings.TrimSpace(req.GetName()) == "":
		problem = "name is required"
	case req.GetState() != checkspb.State_STATE_UP && req.GetState() != checkspb.State_STATE_DOWN:
		problem = "state must be up or down"
	case req.GetPath() != "" && !strings.HasPrefix(req.GetPath(), "/"):
		problem = `path must be a path of ddash's UI, e.g. "/reverse-proxy"`
	}
	if problem != "" {
		return nil, status.Error(codes.InvalidArgument, problem)
	}
	item := reportItem(strings.TrimSpace(req.GetItem()))
	c.mu.Lock()
	defer c.mu.Unlock()
	r := &reported{name: strings.TrimSpace(req.GetName()), state: req.GetState(), since: now(), reason: req.GetReason(), path: req.GetPath()}
	last := c.reported[item]
	if last != nil && last.state == r.state {
		r.since = last.since
	}
	if err := c.db.saveReported(ctx, item, r); err != nil {
		return nil, err
	}
	c.reported[item] = r
	if last == nil {
		c.changed(item, time.Time{}, r.state == checkspb.State_STATE_DOWN, r.since)
	} else if last.state != r.state {
		c.changed(item, last.since, r.state == checkspb.State_STATE_DOWN, r.since)
	}
	return &checkspb.ReportStateResponse{}, nil
}

// loadReported reads every reported item.
func (s *store) loadReported(ctx context.Context) (map[string]*reported, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item, name, state, since, reason, path FROM reported`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*reported{}
	for rows.Next() {
		var (
			item, state string
			since       int64
			r           reported
		)
		if err := rows.Scan(&item, &r.name, &state, &since, &r.reason, &r.path); err != nil {
			return nil, err
		}
		r.state, r.since = key(states, state), time.UnixMilli(since)
		out[item] = &r
	}
	return out, rows.Err()
}

func (s *store) saveReported(ctx context.Context, item string, r *reported) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO reported (item, name, state, since, reason, path) VALUES (?, ?, ?, ?, ?, ?)`,
		item, r.name, states[r.state], ms(r.since), r.reason, r.path)
	return err
}
