package checks

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
)

// The Notification pipeline's fixed timings.
const (
	// DeliveryTick is how often the delivery loop looks at the queue (plus
	// sdk.Every's jitter).
	DeliveryTick = 5 * time.Second
	// RetryAfter is the wait after a first failed attempt; each further
	// failure doubles it, up to RetryMax.
	RetryAfter = 30 * time.Second
	RetryMax   = 10 * time.Minute
	// GiveUpAfter drops a queued item that hasn't been delivered since it
	// was queued this long ago.
	GiveUpAfter = time.Hour
)

// notified is the last state an item was notified in.
type notified struct {
	state  checkspb.State
	since  time.Time
	reason string
}

// outbox is the in-memory delivery queue. It is lost on restart.
type outbox struct {
	// queued maps an item to when it was first queued since its last
	// delivery.
	queued map[string]time.Time
	// next is when the next attempt is due; backoff the wait after the
	// next failure.
	next    time.Time
	backoff time.Duration
}

func checkItem(id int64) string { return "check:" + strconv.FormatInt(id, 10) }

// queue marks item as changed, to be notified in its state at the next
// attempt. Call with c.mu held.
func (c *component) queue(item string) {
	if _, ok := c.out.queued[item]; !ok {
		c.out.queued[item] = now()
	}
}

// pending is one Notification ready to go, and what it notifies.
type pending struct {
	item string
	n    *ntfyapipb.Notification
	as   notified
}

// deliver is one turn of the delivery loop: when an attempt is due, it
// collapses the queue to each item's current state and sends what
// differs from what was last notified, stopping at the first failure and
// backing off.
func (c *component) deliver(ctx context.Context) {
	c.mu.Lock()
	if len(c.out.queued) == 0 || time.Now().Before(c.out.next) {
		c.mu.Unlock()
		return
	}
	todo := c.collapse()
	c.mu.Unlock()

	for _, p := range todo {
		_, err := c.ntfy.Send(ctx, &ntfyapipb.SendRequest{Notification: p.n})
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		if err != nil {
			c.out.backoff = min(max(2*c.out.backoff, RetryAfter), RetryMax)
			c.out.next = time.Now().Add(c.out.backoff)
			c.mu.Unlock()
			c.log.Warn("send Notification", "item", p.item, "err", err, "retry_in", c.out.backoff)
			return
		}
		c.out.backoff = 0
		c.notified[p.item] = p.as
		if err := c.db.setNotified(ctx, p.item, p.as); err != nil {
			c.log.Error("record Notification", "item", p.item, "err", err)
		}
		// It may have changed again while being sent: then it starts over.
		if _, again := c.wanted(p.item); again {
			c.out.queued[p.item] = now()
		} else {
			delete(c.out.queued, p.item)
		}
		c.mu.Unlock()
	}
}

// collapse drops every queued item that is past GiveUpAfter or has
// nothing to say any more, and returns a Notification for each other one,
// in its current state. Call with c.mu held.
func (c *component) collapse() []pending {
	var todo []pending
	for item, at := range c.out.queued {
		p, ok := c.wanted(item)
		if !ok || time.Since(at) > GiveUpAfter {
			delete(c.out.queued, item)
			continue
		}
		todo = append(todo, p)
	}
	slices.SortFunc(todo, func(a, b pending) int { return strings.Compare(a.item, b.item) })
	return todo
}

// wanted is the Notification item calls for now, if any: a Check that is
// down or up while it was last notified in the other state, unless it is
// muted. An item never notified counts as notified up. Call with c.mu
// held.
func (c *component) wanted(item string) (pending, bool) {
	id, err := strconv.ParseInt(strings.TrimPrefix(item, "check:"), 10, 64)
	ch := c.checks[id]
	if err != nil || ch == nil || ch.muted {
		return pending{}, false
	}
	st := ch.standing
	if st.state != checkspb.State_STATE_DOWN && st.state != checkspb.State_STATE_UP {
		return pending{}, false
	}
	last, ok := c.notified[item]
	if !ok {
		last = notified{state: checkspb.State_STATE_UP}
	}
	if st.state == last.state {
		return pending{}, false
	}
	name := ch.spec.shownName()
	n := &ntfyapipb.Notification{Click: c.link(fmt.Sprintf("/checks/%d", id))}
	if st.state == checkspb.State_STATE_DOWN {
		n.Title, n.Message, n.Priority, n.Tags = name+" down", st.reason, 4, []string{"red_circle"}
	} else {
		n.Title, n.Priority, n.Tags = name+" up again", 3, []string{"green_circle"}
		n.Message = "Down for " + downtime(st.since.Sub(last.since))
		if last.reason != "" {
			n.Message += " (" + last.reason + ")"
		}
	}
	return pending{item: item, n: n, as: notified{state: st.state, since: st.since, reason: st.reason}}, true
}

// link is ddash's public URL plus path, e.g. "/checks/7"; empty without
// a public URL.
func (c *component) link(path string) string {
	if c.publicURL == "" {
		return ""
	}
	return strings.TrimSuffix(c.publicURL, "/") + path
}

// downtime says how long d is, to the minute once it is one: "45 s",
// "12 min", "2 h 5 min", "3 d 4 h".
func downtime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	days, h := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
	if h == 0 {
		return fmt.Sprintf("%d d", days)
	}
	return fmt.Sprintf("%d d %d h", days, h)
}
