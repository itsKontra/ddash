package checks

import (
	"cmp"
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
	// GroupWindow is how long a Notification is held: everything due by
	// the time the window closes goes out as one.
	GroupWindow = 60 * time.Second
)

// Flapping: the FlapDowns-th change into down within FlapWindow makes an
// item Flapping. It sends one flapping Notification, then nothing until
// it has stayed in one state for FlapHold, then that state.
const (
	FlapDowns  = 3
	FlapWindow = time.Hour
	FlapHold   = 30 * time.Minute
)

// notified is the last state an item was notified in.
type notified struct {
	state  mark
	since  time.Time
	reason string
}

// mark is what an item was last notified as (notified.state's text).
type mark string

const (
	markUp   mark = "up"
	markDown mark = "down"
	// markFlapping: a flapping Notification was sent, and nothing since.
	markFlapping mark = "flapping"
	// markExpiring: a "cert:<id>" item's certificate-expiry Notification
	// was sent, for the certificate expiring at since.
	markExpiring mark = "expiring"
)

// outbox is the in-memory delivery queue. It is lost on restart.
type outbox struct {
	// queued maps an item to when it was first queued since its last
	// delivery.
	queued map[string]time.Time
	// window is when the open grouping window closes; zero while none is
	// open.
	window time.Time
	// next is when the next attempt is due; backoff the wait after the
	// next failure.
	next    time.Time
	backoff time.Duration
}

func checkItem(id int64) string { return "check:" + strconv.FormatInt(id, 10) }

// queue marks item as changed, to be notified in its state when the
// grouping window closes; the first item queued opens it. Call with c.mu
// held.
func (c *component) queue(item string) {
	if _, ok := c.out.queued[item]; !ok {
		c.out.queued[item] = now()
	}
	if c.out.window.IsZero() {
		c.out.window = time.Now().Add(GroupWindow)
	}
}

// say is what a Notification says of its item, in the order a group's
// title counts them.
type say int

const (
	sayDown say = iota
	sayFlapping
	sayUp
	sayExpiring
	says
)

var sayLabels = [says]string{sayDown: "down", sayFlapping: "flapping", sayUp: "up again", sayExpiring: "certificate expiring"}

// pending is one item's Notification ready to go, and what it notifies.
type pending struct {
	item string
	kind say
	// n is the Notification when the item goes out on its own; line its
	// line in a group's body, e.g. "NAS down: connection refused".
	n    *ntfyapipb.Notification
	line string
	as   notified
}

// group is the one Notification for todo: an item's own when it is
// alone, else a title counting by kind ("3 down, 1 up again"), a body
// listing each item, the highest priority, the most severe kind's tags
// and a Click to the Checks Page.
func (c *component) group(todo []pending) *ntfyapipb.Notification {
	if len(todo) == 1 {
		return todo[0].n
	}
	todo = slices.Clone(todo)
	slices.SortStableFunc(todo, func(a, b pending) int { return cmp.Compare(a.kind, b.kind) })
	var counts [says]int
	var lines []string
	g := &ntfyapipb.Notification{Tags: todo[0].n.GetTags(), Click: c.link("/checks")}
	for _, p := range todo {
		counts[p.kind]++
		lines = append(lines, p.line)
		g.Priority = max(g.Priority, p.n.GetPriority())
	}
	var parts []string
	for k, n := range counts {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sayLabels[k]))
		}
	}
	g.Title, g.Message = strings.Join(parts, ", "), strings.Join(lines, "\n")
	return g
}

// deliver is one turn of the delivery loop: once the grouping window has
// closed and no retry is pending, it collapses the queue to each item's
// current state and sends what differs from what was last notified as one
// Notification, backing off when that fails.
func (c *component) deliver(ctx context.Context) {
	c.mu.Lock()
	if len(c.out.queued) == 0 || time.Now().Before(c.out.next) || time.Now().Before(c.out.window) {
		c.mu.Unlock()
		return
	}
	todo := c.collapse()
	switch {
	case len(todo) == 0:
		c.out.window = time.Time{}
		c.mu.Unlock()
		return
	case c.out.window.IsZero():
		// Due only now (a hold ended): it waits for a window too.
		c.out.window = time.Now().Add(GroupWindow)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	_, err := c.ntfy.Send(ctx, &ntfyapipb.SendRequest{Notification: c.group(todo)})
	if ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.out.backoff = min(max(2*c.out.backoff, RetryAfter), RetryMax)
		c.out.next = time.Now().Add(c.out.backoff)
		// The window stays closed: a retry goes out when due.
		c.out.window = time.Now()
		c.log.Warn("send Notification", "items", len(todo), "err", err, "retry_in", c.out.backoff)
		return
	}
	c.out.backoff, c.out.window = 0, time.Time{}
	for _, p := range todo {
		c.notified[p.item] = p.as
		if err := c.db.setNotified(ctx, p.item, p.as); err != nil {
			c.log.Error("record Notification", "item", p.item, "err", err)
		}
		// It may have changed again while being sent: then it starts over.
		delete(c.out.queued, p.item)
		c.requeue(p.item)
	}
}

// collapse drops every queued item that is past GiveUpAfter or has
// nothing to say any more, keeps the held ones, and returns a
// Notification for each other one, in its current state. Call with c.mu
// held.
func (c *component) collapse() []pending {
	var todo []pending
	for item, at := range c.out.queued {
		p, w := c.wanted(item)
		switch {
		case w == hold:
			// Held isn't failing to deliver: the hour starts when it's due.
			c.out.queued[item] = now()
		case w == nothing || time.Since(at) > GiveUpAfter:
			delete(c.out.queued, item)
		default:
			todo = append(todo, p)
		}
	}
	slices.SortFunc(todo, func(a, b pending) int { return strings.Compare(a.item, b.item) })
	return todo
}

// want is what an item calls for.
type want int

const (
	nothing want = iota
	// hold: a Flapping item, already notified as such, waits until it has
	// stayed in one state for FlapHold.
	hold
	send
)

// wanted is the Notification item calls for now, if any. A Flapping item
// sends one flapping Notification, then is held. Otherwise it is down or
// up while last notified otherwise (an item never notified counts as
// notified up, one notified Flapping as neither). A silenced item (muted,
// in Maintenance, gone) calls for nothing. Call with c.mu held.
func (c *component) wanted(item string) (pending, want) {
	if strings.HasPrefix(item, "cert:") {
		return c.certWanted(item)
	}
	sub, ok := c.subject(item)
	if !ok {
		return pending{}, nothing
	}
	last, ok := c.notified[item]
	if !ok {
		last = notified{state: markUp}
	}
	at := now()
	p := pending{item: item, n: &ntfyapipb.Notification{Click: c.link(sub.path)}}
	if c.flapping(item, sub.since, at) {
		if last.state == markFlapping {
			return pending{}, hold
		}
		p.kind, p.as = sayFlapping, notified{state: markFlapping, since: at}
		p.n.Title, p.n.Priority, p.n.Tags = sub.name+" flapping", 4, []string{"warning"}
		p.n.Message = fmt.Sprintf("Down %d times within %s. Next Notification once it has been up or down for %s.",
			FlapDowns, howLong(FlapWindow), howLong(FlapHold))
		p.line = joinLine(p.n.Title, fmt.Sprintf("down %d times within %s", FlapDowns, howLong(FlapWindow)))
		return p, send
	}

	var mark mark
	switch sub.state {
	case checkspb.State_STATE_DOWN:
		mark = markDown
	case checkspb.State_STATE_UP:
		mark = markUp
	default:
		return pending{}, nothing
	}
	if mark == last.state {
		return pending{}, nothing
	}
	p.as = notified{state: mark, since: sub.since, reason: sub.reason}
	if mark == markDown {
		p.kind = sayDown
		p.n.Title, p.n.Message, p.n.Priority, p.n.Tags = sub.name+" down", sub.reason, 4, []string{"red_circle"}
		p.line = joinLine(p.n.Title, sub.reason)
		return p, send
	}
	p.kind = sayUp
	p.n.Title, p.n.Priority, p.n.Tags = sub.name+" up again", 3, []string{"green_circle"}
	var detail string
	if last.state == markFlapping {
		detail = "up for " + howLong(at.Sub(sub.since)) + " after Flapping"
	} else {
		detail = "down for " + howLong(sub.since.Sub(last.since))
		if last.reason != "" {
			detail += " (" + last.reason + ")"
		}
	}
	p.n.Message, p.line = strings.ToUpper(detail[:1])+detail[1:], joinLine(p.n.Title, detail)
	return p, send
}

// joinLine is a group body's line: the title, then the detail if any.
func joinLine(title, detail string) string {
	if detail == "" {
		return title
	}
	return title + ": " + detail
}

// subject is a Notification item as it stands now.
type subject struct {
	name   string
	state  checkspb.State
	since  time.Time
	reason string
	// path is where its Notification's Click leads; empty for nowhere.
	path string
}

// subject resolves item to what it notifies of, unless it is silenced:
// gone, in Maintenance, or a muted Check. Call with c.mu held.
func (c *component) subject(item string) (subject, bool) {
	if c.inMaintenance(item, now()) {
		return subject{}, false
	}
	prefix, _, _ := strings.Cut(item, ":")
	switch prefix {
	case "check":
		ch := c.itemCheck(item)
		if ch == nil || ch.muted {
			return subject{}, false
		}
		st := ch.standing
		return subject{name: ch.spec.shownName(), state: st.state, since: st.since, reason: st.reason, path: fmt.Sprintf("/checks/%d", ch.id)}, true
	case "report":
		r := c.reported[item]
		if r == nil {
			return subject{}, false
		}
		return subject{name: r.name, state: r.state, since: r.since, reason: r.reason, path: r.path}, true
	}
	return subject{}, false
}

// requeue queues item if it has something to say now: what changed since
// it was last notified but wasn't delivered before a restart, or what
// Maintenance kept quiet. Call with c.mu held.
func (c *component) requeue(item string) {
	if _, w := c.wanted(item); w != nothing {
		c.queue(item)
	}
}

// changed records that item changed state at at, leaving the state it was
// in since was (zero when it had none), and queues it. A change into down
// outside Maintenance counts toward Flapping. Call with c.mu held.
func (c *component) changed(item string, was time.Time, down bool, at time.Time) {
	f := c.flaps[item]
	if f == nil {
		f = &flap{}
		c.flaps[item] = f
	}
	if !f.since.IsZero() && !was.IsZero() && at.Sub(was) >= FlapHold {
		f.since = time.Time{} // it had settled: Flapping is over
	}
	if down && !c.inMaintenance(item, at) {
		f.downs = slices.DeleteFunc(f.downs, func(d time.Time) bool { return at.Sub(d) >= FlapWindow })
		f.downs = append(f.downs, at)
		if f.since.IsZero() && len(f.downs) >= FlapDowns {
			f.since = at
		}
	}
	c.queue(item)
}

// itemCheck is the Check a "check:<id>" or "cert:<id>" item is about;
// nil for another item or a deleted Check. Call with c.mu held.
func (c *component) itemCheck(item string) *check {
	prefix, id, _ := strings.Cut(item, ":")
	if prefix != "check" && prefix != "cert" {
		return nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	return c.checks[n]
}

// inMaintenance reports whether item is in Maintenance at at: a Check's
// items when a Maintenance covers it, anything else only by Maintenance
// on everything. Call with c.mu held.
func (c *component) inMaintenance(item string, at time.Time) bool {
	if ch := c.itemCheck(item); ch != nil {
		in, _ := c.maintained(ch, at)
		return in
	}
	m := c.maint[scope{everything: true}.key()]
	return m != nil && m.on(at)
}

func certItem(id int64) string { return "cert:" + strconv.FormatInt(id, 10) }

// certWanted is the certificate-expiry Notification a "cert:<id>" item
// calls for: one per certificate, once an up HTTPS Check's certificate
// expires within CertWarning, unless the Check is silenced. Call with
// c.mu held.
func (c *component) certWanted(item string) (pending, want) {
	ch := c.itemCheck(item)
	if ch == nil || ch.muted || c.inMaintenance(item, now()) {
		return pending{}, nothing
	}
	exp, left := ch.standing.certExpires, time.Until(ch.standing.certExpires)
	if exp.IsZero() || ch.standing.state != checkspb.State_STATE_UP || left >= CertWarning {
		return pending{}, nothing
	}
	if last, ok := c.notified[item]; ok && last.state == markExpiring && last.since.Equal(exp) {
		return pending{}, nothing
	}
	title, detail := ch.spec.shownName()+" certificate expiring", "expires in "+days(left)
	p := pending{item: item, kind: sayExpiring, as: notified{state: markExpiring, since: exp}, line: joinLine(title, detail)}
	p.n = &ntfyapipb.Notification{
		Title:    title,
		Message:  "E" + detail[1:],
		Priority: 3,
		Tags:     []string{"warning"},
		Click:    c.link(fmt.Sprintf("/checks/%d", ch.id)),
	}
	return p, send
}

// days says how many days d is, counting a part day as one: "1 day",
// "10 days".
func days(d time.Duration) string {
	if n := int((d + 24*time.Hour - 1) / (24 * time.Hour)); n != 1 {
		return fmt.Sprintf("%d days", n)
	}
	return "1 day"
}

// flap is an item's recent downs and whether it is Flapping.
type flap struct {
	// downs are its changes into down within the last FlapWindow (pruned at
	// each down).
	downs []time.Time
	// since is when it became Flapping; zero while it isn't.
	since time.Time
}

// flapping reports whether item, in its state since since, is Flapping at
// at: it went down for the FlapDowns-th time within FlapWindow and hasn't
// stayed in one state for FlapHold since. Call with c.mu held.
func (c *component) flapping(item string, since, at time.Time) bool {
	f := c.flaps[item]
	return f != nil && !f.since.IsZero() && at.Sub(since) < FlapHold
}

// link is ddash's public URL plus path, e.g. "/checks/7"; empty without
// a public URL.
func (c *component) link(path string) string {
	if c.publicURL == "" {
		return ""
	}
	return strings.TrimSuffix(c.publicURL, "/") + path
}

// howLong says how long d is, to the minute once it is one: "45 s",
// "12 min", "2 h 5 min", "3 d 4 h".
func howLong(d time.Duration) string {
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
