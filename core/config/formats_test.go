package config_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/itsKontra/ddash/core/config"
)

// TestFormatsAcceptWhatTheSharedCasesSay runs testdata/formats.json, which
// the shell's form runs too.
func TestFormatsAcceptWhatTheSharedCasesSay(t *testing.T) {
	b, err := os.ReadFile("testdata/formats.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]json.RawMessage
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	delete(cases, "_comment")
	for format, raw := range cases {
		var c struct{ OK, Bad []string }
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		for _, v := range c.OK {
			if msg := config.CheckFormat(format, v); msg != "" {
				t.Errorf("%s %q: %s, want ok", format, v, msg)
			}
		}
		for _, v := range c.Bad {
			if msg := config.CheckFormat(format, v); msg == "" {
				t.Errorf("%s %q: ok, want refused", format, v)
			}
		}
	}
}
