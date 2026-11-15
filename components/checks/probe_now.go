package checks

import (
	"context"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// ProbeAgain is when ProbeNow probes a Check that is still down again,
// counted from its probe at once: what was just started may take a
// moment to answer.
var ProbeAgain = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

// ProbeNow restarts the loop of every probed Check the scope covers with a
// beat at once.
func (c *component) ProbeNow(_ context.Context, req *checkspb.ProbeNowRequest) (*checkspb.ProbeNowResponse, error) {
	s, err := parseScope(req.GetScope())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.checks {
		if ch.probed() && s.covers(ch) {
			ch.stopProbing()
			c.loop(ch, time.Time{}, ProbeAgain)
		}
	}
	return &checkspb.ProbeNowResponse{}, nil
}

// down reports whether Check id is down and its loop is still gen.
func (c *component) down(id int64, gen int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.checks[id]
	return ch != nil && ch.gen == gen && ch.standing.state == checkspb.State_STATE_DOWN
}
