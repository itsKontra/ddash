//go:build !dev

package auth_test

import (
	"net/http"
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

func TestARegularBuildIgnoresDevAuth(t *testing.T) {
	res, actor := call(auth.FromEnv(env(map[string]string{"DDASH_DEV_AUTH": "1"})))

	if res.Code != http.StatusUnauthorized || actor != "" {
		t.Errorf("status %d, actor %q; want 401 and no actor", res.Code, actor)
	}
}
