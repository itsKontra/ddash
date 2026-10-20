//go:build dev

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

func TestADevBuildWithDevAuthSignsEveryRequestInAsOperatorDev(t *testing.T) {
	mw := auth.DevBypass(env(map[string]string{"DDASH_DEV_AUTH": "1"}))
	if mw == nil {
		t.Fatal("no bypass")
	}
	var actor string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		a, _ := auth.ActorFrom(r.Context())
		actor = a.String()
	}))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/c/docker-api/ListContainers", nil))

	if res.Code != http.StatusOK || actor != "operator:dev" {
		t.Errorf("status %d, actor %q; want 200 as operator:dev", res.Code, actor)
	}
}
