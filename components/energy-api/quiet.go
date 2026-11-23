package energyapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"github.com/itsKontra/ddash/sdk"
)

const (
	// quietAfter is how long one side goes without a reading, while the
	// other still reports, before energy-api is degraded and notifies.
	quietAfter = 15 * time.Minute
	// sendWait is how long Health waits for a Notification it sends,
	// below the host's deadline for Health; the Send runs on after it.
	sendWait = 5 * time.Second
	// sendTimeout bounds one Send, above the core's delivery deadline.
	sendTimeout = 30 * time.Second
)

// side is one device Telegraf reads, by the measurement of its power
// readings.
type side struct {
	measurement string
	// name is how a sentence names it, title how a title does.
	name, title string
}

var sides = []side{
	{measurement: "victron", name: "the Victron", title: "Victron"},
	{measurement: "kW", name: "the inverter", title: "Inverter"},
}

// quiet is what the Operator was told of each side going quiet. It is
// lost on restart: a side still quiet then is told once more.
type quiet struct {
	mu sync.Mutex
	// told is, by measurement, the last reading before the quiet the
	// Operator was told of.
	told map[string]time.Time
	// sending is closed once the Notifications being sent are done; nil
	// while none are.
	sending chan struct{}
}

// readSeen is each side's last reading of the last lookBack, by
// measurement; a side without one has none. It reads unbounded, as
// InfluxDB answers that from its storage engine.
func (c *component) readSeen(ctx context.Context, now time.Time) (map[string]time.Time, error) {
	rows, err := c.db.query(ctx, flux(c.db.bucket, now.Add(-lookBack), now, powerSeries, reduce{fn: "last"}))
	if err != nil {
		return nil, err
	}
	seen := map[string]time.Time{}
	for _, r := range rows {
		m := byField[r.field].measurement
		if r.at.After(seen[m]) {
			seen[m] = r.at
		}
	}
	return seen, nil
}

// silent is the side that has had no reading for quietAfter while the
// other still reports, and since when; ok false for none, also while
// both are quiet.
func silent(seen map[string]time.Time, now time.Time) (s side, last time.Time, ok bool) {
	reports := func(m string) bool { t := seen[m]; return !t.IsZero() && now.Sub(t) < quietAfter }
	for i, s := range sides {
		other := sides[1-i]
		if t := seen[s.measurement]; !t.IsZero() && !reports(s.measurement) && reports(other.measurement) {
			return s, t, true
		}
	}
	return side{}, time.Time{}, false
}

// quietHealth is Degraded while a side is silent.
func quietHealth(seen map[string]time.Time, now time.Time) sdk.Health {
	s, last, ok := silent(seen, now)
	if !ok {
		return sdk.Health{State: sdk.OK}
	}
	return sdk.Health{State: sdk.Degraded, Message: fmt.Sprintf("%s hasn't reported for %s", upper(s.name), howLong(now.Sub(last)))}
}

// note is one Notification due, and what the Operator was told by it: the
// side's last reading before its quiet, or zero once it reports again.
type note struct {
	measurement string
	told        time.Time
	n           *notificationpb.Notification
}

// due is the Notifications seen calls for: one when a side goes silent
// (or is silent at the first look after Start), and one when a side the
// Operator was told of reports again. While both sides are quiet nothing
// is due. Call with q.mu held.
func (c *component) due(seen map[string]time.Time, now time.Time) []note {
	var notes []note
	if s, last, ok := silent(seen, now); ok {
		if _, told := c.quiet.told[s.measurement]; !told {
			other := sides[0]
			if other == s {
				other = sides[1]
			}
			notes = append(notes, note{measurement: s.measurement, told: last, n: &notificationpb.Notification{
				Title:    s.title + " not reporting",
				Message:  fmt.Sprintf("%s hasn't reported for %s, while %s still does.", upper(s.name), howLong(now.Sub(last)), other.name),
				Priority: notificationpb.Priority_PRIORITY_DEFAULT,
				Tags:     []string{"warning"},
				Click:    c.link("/energy"),
			}})
		}
	}
	for _, s := range sides {
		before, told := c.quiet.told[s.measurement]
		last := seen[s.measurement]
		if !told || last.IsZero() || now.Sub(last) >= quietAfter {
			continue
		}
		notes = append(notes, note{measurement: s.measurement, n: &notificationpb.Notification{
			Title:    s.title + " reporting again",
			Message:  fmt.Sprintf("%s reports again after %s.", upper(s.name), howLong(last.Sub(before))),
			Priority: notificationpb.Priority_PRIORITY_DEFAULT,
			Tags:     []string{"green_circle"},
			Click:    c.link("/energy"),
		}})
	}
	return notes
}

// tell sends the Notifications seen calls for, unless some are still
// being sent, and waits for them up to sendWait. What fails to send is
// due again at the next Health.
func (c *component) tell(seen map[string]time.Time, now time.Time) {
	c.quiet.mu.Lock()
	done := c.quiet.sending
	if done == nil {
		notes := c.due(seen, now)
		if len(notes) == 0 {
			c.quiet.mu.Unlock()
			return
		}
		done = make(chan struct{})
		c.quiet.sending = done
		go c.send(notes, done)
	}
	c.quiet.mu.Unlock()
	select {
	case <-done:
	case <-time.After(sendWait):
	}
}

func (c *component) send(notes []note, done chan struct{}) {
	defer close(done)
	for _, nt := range notes {
		ctx, cancel := context.WithTimeout(c.ctx, sendTimeout)
		_, err := c.notify.Send(ctx, &notificationpb.SendRequest{Notification: nt.n, Category: notificationpb.Category_CATEGORY_CHECKS})
		cancel()
		if err != nil {
			c.log.Warn("send Notification", "title", nt.n.GetTitle(), "err", err)
			continue
		}
		c.quiet.mu.Lock()
		if nt.told.IsZero() {
			delete(c.quiet.told, nt.measurement)
		} else {
			c.quiet.told[nt.measurement] = nt.told
		}
		c.quiet.mu.Unlock()
	}
	c.quiet.mu.Lock()
	c.quiet.sending = nil
	c.quiet.mu.Unlock()
}

// link is ddash's public URL plus path; empty without a public URL.
func (c *component) link(path string) string {
	if c.publicURL == "" {
		return ""
	}
	return c.publicURL + path
}

func upper(s string) string { return string(s[0]-'a'+'A') + s[1:] }

// howLong says how long d is, to the minute: "15 min", "2 h 5 min",
// "3 d 4 h".
func howLong(d time.Duration) string {
	switch {
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
