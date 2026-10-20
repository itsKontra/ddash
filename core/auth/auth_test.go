package auth_test

import (
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestWithoutDevAuthThereIsNoBypass(t *testing.T) {
	if mw := auth.DevBypass(env(nil)); mw != nil {
		t.Error("a bypass without DDASH_DEV_AUTH")
	}
}
