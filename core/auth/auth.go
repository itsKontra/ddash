// Package auth decides who is calling: it puts the actor of every request
// that reaches /api into the request's context. Sign-in with Keycloak
// replaces Refuse later; until then only dev builds get in.
package auth

import (
	"context"
	"net/http"
)

// Middleware guards the paths behind sign-in. It lets a request through
// only with its actor in the context, see WithActor.
type Middleware func(http.Handler) http.Handler

type actorKey struct{}

// WithActor returns ctx carrying actor, e.g. "operator:alice".
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor WithActor put into ctx.
func ActorFrom(ctx context.Context) (string, bool) {
	a, ok := ctx.Value(actorKey{}).(string)
	return a, ok
}

// As lets every request through as actor. Tests use it in place of a
// session.
func As(actor string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
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
