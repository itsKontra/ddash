package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

// call serves one request through mw and returns the response and the actor
// that reached the handler behind it.
func call(mw auth.Middleware) (*httptest.ResponseRecorder, string) {
	var actor string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		actor, _ = auth.ActorFrom(r.Context())
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/c/docker-api/ListContainers", nil))
	return w, actor
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestWithoutDevAuthEveryRequestIsUnauthenticated(t *testing.T) {
	res, actor := call(auth.FromEnv(env(nil)))

	if res.Code != http.StatusUnauthorized || actor != "" {
		t.Errorf("status %d, actor %q; want 401 and no actor", res.Code, actor)
	}
}
