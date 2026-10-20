package sdk_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/itsKontra/ddash/sdk"
)

// The host's go-plugin client parses these hclog JSON lines from the
// Component's stderr and logs them under the Component's name.
func TestLogsAreHclogJSONNamedAfterTheComponent(t *testing.T) {
	var out bytes.Buffer
	log := sdk.NewLogger("docker-api", &out).With("host", "media")

	log.Warn("host unreachable", "err", errors.New("connection refused"), "attempt", 3)

	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not one JSON line: %q", out.String())
	}
	want := map[string]any{
		"@level":   "warn",
		"@message": "host unreachable",
		"@module":  "docker-api",
		"host":     "media",
		"err":      "connection refused",
		"attempt":  float64(3),
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %#v, want %#v", k, line[k], v)
		}
	}
}

func TestDebugLogsAreDropped(t *testing.T) {
	var out bytes.Buffer
	log := sdk.NewLogger("docker-api", &out)

	log.Debug("noise")

	if out.Len() != 0 {
		t.Errorf("debug line written: %q", out.String())
	}
	if !log.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("info is disabled")
	}
}
