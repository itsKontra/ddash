//go:build dev

package auth_test

import (
	"net/http"
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

func TestADevBuildWithDevAuthSignsEveryRequestInAsOperatorDev(t *testing.T) {
	res, actor := call(auth.FromEnv(env(map[string]string{"DDASH_DEV_AUTH": "1"})))

	if res.Code != http.StatusOK || actor != "operator:dev" {
		t.Errorf("status %d, actor %q; want 200 as operator:dev", res.Code, actor)
	}
}
