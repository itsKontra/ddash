// Package auth decides who is calling: it puts the actor of every request
// that reaches /api into the request's context. Sign-in with Keycloak
// replaces Refuse later; until then only dev builds get in.
package auth

import (
	"context"
	"net/http"
	"strings"
)

// Middleware guards the paths behind sign-in. It lets a request through
// only with its actor in the context, see WithActor.
type Middleware func(http.Handler) http.Handler

// The kinds of actor.
const (
	// Operator is the signed-in Operator.
	Operator = "operator"
	// Component is a Component doing work it started itself.
	Component = "component"
	// Core is the core doing work it started itself.
	Core = "core"
)

// Actor is who an Action runs for.
type Actor struct {
	// Kind is Operator, Component or Core.
	Kind string
	// Name is the Operator's preferred_username or the Component's name;
	// empty for Core.
	Name string
	// Sub is the Operator's OIDC subject; empty for other kinds.
	Sub string
}

// ParseActor reads an actor written as String writes it, e.g.
// "operator:alice", "component:checks" or "core". It has no Sub.
func ParseActor(s string) Actor {
	kind, name, _ := strings.Cut(s, ":")
	return Actor{Kind: kind, Name: name}
}

// String writes a as "<kind>:<name>", or just the kind when it has no name.
func (a Actor) String() string {
	if a.Name == "" {
		return a.Kind
	}
	return a.Kind + ":" + a.Name
}

type actorKey struct{}

// WithActor returns ctx carrying actor.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor WithActor put into ctx.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// As lets every request through as actor, e.g. "operator:alice". Tests use
// it in place of a session.
func As(actor string) Middleware {
	a := ParseActor(actor)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), a)))
		})
	}
}

// Refuse lets nothing through: every request is unauthenticated.
func Refuse(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthenticated","message":"sign in first"}`))
	})
}

// FromEnv returns the Middleware this build uses: the dev bypass when the
// binary was built with the dev tag and DDASH_DEV_AUTH=1, and Refuse
// otherwise.
func FromEnv(getenv func(string) string) Middleware {
	if devBypass != nil && getenv("DDASH_DEV_AUTH") == "1" {
		return devBypass
	}
	return Refuse
}

// devBypass is set only in dev builds, see dev.go.
var devBypass Middleware
