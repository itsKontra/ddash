// Package appstate is the gate every Action passes and the Audit Log it
// writes: one entry per hop, inserted when the Action is admitted and
// completed when it ends. Getters are never audited.
package appstate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/core/auth"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
)

// The outcomes of an entry besides a failed Action's gRPC code, e.g.
// "PermissionDenied".
const (
	// Running: the Action hasn't ended yet.
	Running = ""
	OK      = "ok"
	// Interrupted: the host stopped before the Action ended.
	Interrupted = "interrupted"
)

// timeFormat is fixed-width, so times sort as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Gate admits Actions and keeps the Audit Log in the core DB.
type Gate struct {
	db *sql.DB
}

// Open returns the Gate over the core DB db. Entries left running by the
// last host become Interrupted.
func Open(ctx context.Context, db *sql.DB) (*Gate, error) {
	_, err := db.ExecContext(ctx, `UPDATE audit_entry SET outcome = ? WHERE outcome IS NULL`, Interrupted)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	return &Gate{db: db}, nil
}

// Action is one hop of an Action, as the gate sees it.
type Action struct {
	// Chain links the hops of one Action.
	Chain string
	// Target is the Component the call goes to, or "core".
	Target string
	// Method is the method called. A getter is never audited.
	Method protoreflect.MethodDescriptor
	// Request is the call's request.
	Request proto.Message
}

// Admit records a, run for actor, in the Audit Log before it runs. done
// completes the entry with the Action's result. If the entry can't be
// written the Action is refused: Unavailable "audit log unavailable". A
// getter is admitted without an entry.
func (g *Gate) Admit(ctx context.Context, actor auth.Actor, a Action) (done func(error), err error) {
	if !IsAction(a.Method) {
		return func(error) {}, nil
	}
	start := time.Now()
	res, err := g.db.ExecContext(ctx, `
		INSERT INTO audit_entry (time, actor_kind, username, sub, chain_id, target, method, disruptive, request)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		start.UTC().Format(timeFormat), actor.Kind, actor.Name, actor.Sub, a.Chain, a.Target,
		string(a.Method.Name()), disruptive(a.Method), record(a.Request))
	if err != nil {
		return nil, status.Error(codes.Unavailable, "audit log unavailable")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, status.Error(codes.Unavailable, "audit log unavailable")
	}
	return func(err error) {
		outcome, message := result(err)
		// Completing an entry the next host already marked interrupted
		// would rewrite history, so only a running entry is completed.
		_, _ = g.db.ExecContext(context.WithoutCancel(ctx), `
			UPDATE audit_entry SET outcome = ?, message = ?, duration_ms = ?
			WHERE id = ? AND outcome IS NULL`,
			outcome, message, time.Since(start).Milliseconds(), id)
	}, nil
}

// result is the outcome and message an entry records for err.
func result(err error) (outcome, message string) {
	if err == nil {
		return OK, ""
	}
	st := status.Convert(err)
	if ctxErr := status.FromContextError(err); st.Code() == codes.Unknown && ctxErr.Code() != codes.Unknown {
		st = ctxErr
	}
	return st.Code().String(), st.Message()
}

// Event is something audited that isn't a call: a sign-in, a refused
// sign-in or a logout.
type Event struct {
	Chain  string
	Target string
	// Method names the event, e.g. "SignIn".
	Method string
	// Err is why it failed, or nil.
	Err error
}

// Record writes e, done by actor, to the Audit Log as a completed entry.
// It fails Unavailable "audit log unavailable" when it can't.
func (g *Gate) Record(ctx context.Context, actor auth.Actor, e Event) error {
	outcome, message := result(e.Err)
	_, err := g.db.ExecContext(ctx, `
		INSERT INTO audit_entry (time, actor_kind, username, sub, chain_id, target, method, disruptive, request, outcome, message, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, '{}', ?, ?, 0)`,
		time.Now().UTC().Format(timeFormat), actor.Kind, actor.Name, actor.Sub, e.Chain, e.Target, e.Method, outcome, message)
	if err != nil {
		return status.Error(codes.Unavailable, "audit log unavailable")
	}
	return nil
}

// IsAction reports whether md is an Action rather than a getter.
func IsAction(md protoreflect.MethodDescriptor) bool {
	return proto.HasExtension(md.Options(), ddashpb.E_Action)
}

func disruptive(md protoreflect.MethodDescriptor) bool {
	a, _ := proto.GetExtension(md.Options(), ddashpb.E_Action).(*ddashpb.Action)
	return a.GetDisruptive()
}

// MaxRequest is how much of a request the Audit Log keeps. A longer one is
// cut there and ends in TruncatedMarker.
const MaxRequest = 4 << 10

// TruncatedMarker ends a request cut at MaxRequest.
const TruncatedMarker = "…(truncated)"

// Redacted replaces the value of every secret string field.
const Redacted = "REDACTED"

// record writes req as the Audit Log keeps it: compact protojson, secrets
// redacted, cut at MaxRequest.
func record(req proto.Message) string {
	if req == nil {
		return "{}"
	}
	req = proto.Clone(req)
	redact(req.ProtoReflect())
	b, err := protojson.Marshal(req)
	if err != nil {
		return fmt.Sprintf("(unrecordable: %v)", err)
	}
	// protojson varies its whitespace on purpose; the log shouldn't.
	var compact bytes.Buffer
	if json.Compact(&compact, b) == nil {
		b = compact.Bytes()
	}
	if len(b) <= MaxRequest {
		return string(b)
	}
	cut := MaxRequest
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut]) + TruncatedMarker
}

// redact blanks every field of m, and of the messages it holds, that has
// the secret option: a string becomes Redacted, anything else is cleared.
func redact(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if opts, ok := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field); ok && opts.GetSecret() {
			if fd.Kind() == protoreflect.StringKind && !fd.IsList() && !fd.IsMap() {
				m.Set(fd, protoreflect.ValueOfString(Redacted))
			} else {
				m.Clear(fd)
			}
			return true
		}
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					redact(mv.Message())
					return true
				})
			}
		case fd.Message() == nil:
		case fd.IsList():
			for i := range v.List().Len() {
				redact(v.List().Get(i).Message())
			}
		default:
			redact(v.Message())
		}
		return true
	})
}

// Retention is how long the Audit Log keeps an entry.
const Retention = 365 * 24 * time.Hour

// SweepDaily deletes the entries older than Retention at once and then
// every day, until ctx ends. It is one of the core's housekeeping loops.
func (g *Gate) SweepDaily(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		cutoff := time.Now().Add(-Retention).UTC().Format(timeFormat)
		_, _ = g.db.ExecContext(ctx, `DELETE FROM audit_entry WHERE time < ?`, cutoff) // retried tomorrow
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Entry is one hop in the Audit Log.
type Entry struct {
	ID   int64
	Time time.Time
	// ActorKind is auth.Operator, auth.Component or auth.Core.
	ActorKind string
	// Username is the Operator's preferred_username or the Component's name.
	Username string
	// Sub is the Operator's OIDC subject.
	Sub        string
	Chain      string
	Target     string
	Method     string
	Disruptive bool
	// Request is the request as protojson, secrets redacted, truncated.
	Request string
	// Outcome is OK, Interrupted, Running, or a failed Action's gRPC code.
	Outcome string
	// Message is a failed Action's error message.
	Message  string
	Duration time.Duration
}

// Entries returns the Audit Log, newest first.
func (g *Gate) Entries(ctx context.Context) ([]Entry, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM audit_entry ORDER BY time DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Chain is the hops of one Action, linked by their chain id.
type Chain struct {
	ID string
	// Entries are in the order they were admitted: the first is the root,
	// the Action the actor started.
	Entries []Entry
}

// Query picks a page of chains. Its filters left empty match any chain.
type Query struct {
	// ActorKind keeps the chains whose root ran for this kind of actor.
	ActorKind string
	// Target keeps the chains with a hop to this Component, or to "core".
	Target string
	// Outcome keeps the chains whose hops all ended OK, or that have a hop
	// that failed (Failed), was Interrupted, or is still running
	// (StillRunning).
	Outcome string
	// Search keeps the chains whose root has every whitespace-separated
	// word of it, ignoring case, in its target, method or username.
	Search string
	// Order is Newest or Oldest root first; empty is Newest.
	Order string
	// PageSize is how many chains a page holds: DefaultPageSize when 0, at
	// most MaxPageSize.
	PageSize int
	// PageToken is the previous Page's Next, from a Query with the same
	// filters, Search and Order; empty for the first page.
	PageToken string
}

// The orders Query.Order takes.
const (
	Newest = "newest"
	Oldest = "oldest"
)

// Page sizes for Query.PageSize.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// The outcomes a Query filters by besides OK and Interrupted.
const (
	// Failed matches any gRPC code.
	Failed = "failed"
	// StillRunning matches an Action that hasn't ended yet.
	StillRunning = "running"
)

// outcomeFilters are the conditions on a root r that Query.Outcome picks.
var outcomeFilters = map[string]string{
	OK:           `NOT EXISTS (SELECT 1 FROM audit_entry h WHERE h.chain_id = r.chain_id AND (h.outcome IS NULL OR h.outcome != 'ok'))`,
	Failed:       `EXISTS (SELECT 1 FROM audit_entry h WHERE h.chain_id = r.chain_id AND h.outcome NOT IN ('ok', 'interrupted'))`,
	Interrupted:  `EXISTS (SELECT 1 FROM audit_entry h WHERE h.chain_id = r.chain_id AND h.outcome = 'interrupted')`,
	StillRunning: `EXISTS (SELECT 1 FROM audit_entry h WHERE h.chain_id = r.chain_id AND h.outcome IS NULL)`,
}

// actorKinds are the kinds Query.ActorKind takes.
var actorKinds = []string{auth.Operator, auth.Component, auth.Core}

// Page is one page of chains, in its Query's Order.
type Page struct {
	Chains []Chain
	// Next is the PageToken of the next page; empty on the last page.
	Next string
}

// Targets returns every target in the Audit Log, sorted.
func (g *Gate) Targets(ctx context.Context) ([]string, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT DISTINCT target FROM audit_entry ORDER BY target`)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("audit log: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// entryColumns are the columns scanEntry reads.
const entryColumns = `id, time, actor_kind, username, sub, chain_id, target, method, disruptive, request,
	COALESCE(outcome, ''), message, COALESCE(duration_ms, 0)`

// Chains returns the page of chains q picks. A chain's root is its first
// admitted hop, and the chain is as new as its root.
func (g *Gate) Chains(ctx context.Context, q Query) (Page, error) {
	// A root is an entry no earlier entry shares its chain with.
	where := []string{`NOT EXISTS (SELECT 1 FROM audit_entry p WHERE p.chain_id = r.chain_id AND p.id < r.id)`}
	var args []any
	if q.ActorKind != "" {
		if !slices.Contains(actorKinds, q.ActorKind) {
			return Page{}, status.Errorf(codes.InvalidArgument, "actor kind %q isn't one of %s", q.ActorKind, strings.Join(actorKinds, ", "))
		}
		where = append(where, `r.actor_kind = ?`)
		args = append(args, q.ActorKind)
	}
	if q.Outcome != "" {
		cond, ok := outcomeFilters[q.Outcome]
		if !ok {
			return Page{}, status.Errorf(codes.InvalidArgument, "outcome %q isn't one of ok, failed, interrupted, running", q.Outcome)
		}
		where = append(where, cond)
	}
	if q.Target != "" {
		where = append(where, `EXISTS (SELECT 1 FROM audit_entry h WHERE h.chain_id = r.chain_id AND h.target = ?)`)
		args = append(args, q.Target)
	}
	for _, word := range strings.Fields(q.Search) {
		// LIKE ignores the case of ASCII letters only; others match as typed.
		where = append(where, `(r.target LIKE ? ESCAPE '\' OR r.method LIKE ? ESCAPE '\' OR r.username LIKE ? ESCAPE '\')`)
		like := "%" + likeEscaper.Replace(word) + "%"
		args = append(args, like, like, like)
	}
	after, direction := "<", "DESC"
	switch q.Order {
	case "", Newest:
	case Oldest:
		after, direction = ">", "ASC"
	default:
		return Page{}, status.Errorf(codes.InvalidArgument, "order %q isn't %s or %s", q.Order, Newest, Oldest)
	}
	if q.PageToken != "" {
		// The page goes on after the root the token names.
		at, id, ok := parseToken(q.PageToken)
		if !ok {
			return Page{}, status.Errorf(codes.InvalidArgument, "page token %q isn't one this Audit Log gave", q.PageToken)
		}
		where = append(where, `(r.time `+after+` ? OR (r.time = ? AND r.id `+after+` ?))`)
		args = append(args, at, at, id)
	}
	size := q.PageSize
	if size <= 0 {
		size = DefaultPageSize
	}
	size = min(size, MaxPageSize)
	rows, err := g.db.QueryContext(ctx, `
		SELECT r.chain_id, r.time, r.id FROM audit_entry r
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY r.time `+direction+`, r.id `+direction+`
		LIMIT ?`, append(args, size+1)...)
	if err != nil {
		return Page{}, fmt.Errorf("audit log: %w", err)
	}
	var roots []root
	var page Page
	for rows.Next() {
		var chain, at string
		var id int64
		if err := rows.Scan(&chain, &at, &id); err != nil {
			rows.Close()
			return Page{}, fmt.Errorf("audit log: %w", err)
		}
		if len(roots) == size {
			// One more root than fits: there is a next page.
			page.Next = token(roots[len(roots)-1])
			break
		}
		roots = append(roots, root{chain, at, id})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("audit log: %w", err)
	}
	page.Chains, err = g.hops(ctx, roots)
	return page, err
}

// likeEscaper escapes a LIKE pattern's wildcards, for ESCAPE '\'.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// root is a chain's first hop, as a page of chains is sorted by.
type root struct {
	chain string
	time  string
	id    int64
}

// token is the PageToken of the page after r: its time and id.
func token(r root) string { return r.time + "/" + strconv.FormatInt(r.id, 10) }

func parseToken(s string) (at string, id int64, ok bool) {
	at, idText, ok := strings.Cut(s, "/")
	if !ok {
		return "", 0, false
	}
	if _, err := time.Parse(timeFormat, at); err != nil || len(at) != len(timeFormat) {
		return "", 0, false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	return at, id, err == nil
}

// hops returns the chains of roots, in that order, each with its hops in
// the order they were admitted.
func (g *Gate) hops(ctx context.Context, roots []root) ([]Chain, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	chains := make([]Chain, len(roots))
	at := make(map[string]int, len(roots))
	ids := make([]any, len(roots))
	for i, r := range roots {
		chains[i].ID = r.chain
		at[r.chain] = i
		ids[i] = r.chain
	}
	rows, err := g.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM audit_entry
		WHERE chain_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`) ORDER BY id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		i := at[e.Chain]
		chains[i].Entries = append(chains[i].Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	return chains, nil
}

func scanEntry(rows *sql.Rows) (Entry, error) {
	var e Entry
	var at string
	var ms int64
	if err := rows.Scan(&e.ID, &at, &e.ActorKind, &e.Username, &e.Sub, &e.Chain, &e.Target, &e.Method,
		&e.Disruptive, &e.Request, &e.Outcome, &e.Message, &ms); err != nil {
		return Entry{}, fmt.Errorf("audit log: %w", err)
	}
	e.Time, _ = time.Parse(timeFormat, at)
	e.Duration = time.Duration(ms) * time.Millisecond
	return e, nil
}
