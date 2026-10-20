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
		outcome, message := OK, ""
		if err != nil {
			st := status.Convert(err)
			if ctxErr := status.FromContextError(err); st.Code() == codes.Unknown && ctxErr.Code() != codes.Unknown {
				st = ctxErr
			}
			outcome, message = st.Code().String(), st.Message()
		}
		// Completing an entry the next host already marked interrupted
		// would rewrite history, so only a running entry is completed.
		g.db.ExecContext(context.WithoutCancel(ctx), `
			UPDATE audit_entry SET outcome = ?, message = ?, duration_ms = ?
			WHERE id = ? AND outcome IS NULL`,
			outcome, message, time.Since(start).Milliseconds(), id)
	}, nil
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
		g.db.ExecContext(ctx, `DELETE FROM audit_entry WHERE time < ?`, cutoff)
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
	rows, err := g.db.QueryContext(ctx, `
		SELECT id, time, actor_kind, username, sub, chain_id, target, method, disruptive, request,
		       COALESCE(outcome, ''), message, COALESCE(duration_ms, 0)
		FROM audit_entry ORDER BY time DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var at string
		var ms int64
		if err := rows.Scan(&e.ID, &at, &e.ActorKind, &e.Username, &e.Sub, &e.Chain, &e.Target, &e.Method,
			&e.Disruptive, &e.Request, &e.Outcome, &e.Message, &ms); err != nil {
			return nil, fmt.Errorf("audit log: %w", err)
		}
		e.Time, _ = time.Parse(timeFormat, at)
		e.Duration = time.Duration(ms) * time.Millisecond
		out = append(out, e)
	}
	return out, rows.Err()
}
