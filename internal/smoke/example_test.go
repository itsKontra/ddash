package smoke

import (
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
)

// smoke.env.example names every system Component, with a config its
// Component reads.
func TestTheSmokeEnvExampleConfiguresEverySystem(t *testing.T) {
	env, err := ReadEnv(filepath.Join("..", "..", "smoke.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range systems {
		text, ok := env[EnvVar(s.def.Name())]
		if !ok {
			t.Errorf("smoke.env.example lacks %s", EnvVar(s.def.Name()))
			continue
		}
		if err := protojson.Unmarshal([]byte(text), s.config()); err != nil {
			t.Errorf("%s: %v", EnvVar(s.def.Name()), err)
		}
	}
	for _, v := range []string{"SCRUB_KEY", "SCRUB_SUBNETS", "SCRUB_DOMAINS", UnusedAddressVar, HostOverrideVar} {
		if env[v] == "" {
			t.Errorf("smoke.env.example lacks %s", v)
		}
	}
}
