//go:build !dev

package auth_test

import (
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

func TestARegularBuildIgnoresDevAuth(t *testing.T) {
	if mw := auth.DevBypass(env(map[string]string{"DDASH_DEV_AUTH": "1"})); mw != nil {
		t.Error("a regular build has a bypass")
	}
}
