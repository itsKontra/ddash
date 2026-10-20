package smoke_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/internal/smoke"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadEnvReadsAssignmentsWithQuotesAndComments(t *testing.T) {
	path := writeFile(t, "smoke.env", `# systems
SMOKE_PFSENSE_API='{"url": "https://fw.home.example", "apiKey": "k=1"}'
export SCRUB_KEY="a key"

SCRUB_SUBNETS=198.18.1.0/24=1
`)

	env, err := smoke.ReadEnv(path)

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SMOKE_PFSENSE_API": `{"url": "https://fw.home.example", "apiKey": "k=1"}`,
		"SCRUB_KEY":         "a key",
		"SCRUB_SUBNETS":     "198.18.1.0/24=1",
	}
	if diff := cmp.Diff(want, env); diff != "" {
		t.Errorf("env (-want +got):\n%s", diff)
	}
}

func TestReadEnvRefusesALineThatIsNoAssignment(t *testing.T) {
	path := writeFile(t, "smoke.env", "SCRUB_KEY=k\nnonsense\n")

	if _, err := smoke.ReadEnv(path); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v, want line 2 refused", err)
	}
}

func TestTheScrubberComesFromSmokeEnvAndScrubMap(t *testing.T) {
	mapPath := writeFile(t, "scrub.map", "secretbox = node1\n")
	env := map[string]string{"SCRUB_KEY": "k", "SCRUB_SUBNETS": "192.0.2.0/24=4", "SCRUB_DOMAINS": "realm.test, other.test"}

	s, err := smoke.Scrubber(env, mapPath)
	if err != nil {
		t.Fatal(err)
	}

	_, got, err := s.Fixture("a.txt", []byte("secretbox.realm.test 192.0.2.9 x.other.test"))
	if err != nil || string(got) != "node1.home.example 198.18.4.9 x.home.example" {
		t.Errorf("Fixture = %q, %v", got, err)
	}
}

func TestRecordingNeedsAKeyAndAScrubMap(t *testing.T) {
	mapPath := writeFile(t, "scrub.map", "")
	if _, err := smoke.Scrubber(map[string]string{}, mapPath); err == nil || !strings.Contains(err.Error(), "SCRUB_KEY") {
		t.Errorf("without SCRUB_KEY: err = %v", err)
	}
	if _, err := smoke.Scrubber(map[string]string{"SCRUB_KEY": "k"}, filepath.Join(t.TempDir(), "scrub.map")); err == nil || !strings.Contains(err.Error(), "scrub.map") {
		t.Errorf("without scrub.map: err = %v", err)
	}
}

// smoke.env and scrub.map name the real homelab: git must never see them.
func TestSmokeEnvAndScrubMapAreGitignored(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for _, want := range []string{"smoke.env", "scrub.map", "/.smoke/"} {
		found := false
		for _, l := range lines {
			found = found || strings.TrimSpace(l) == want
		}
		if !found {
			t.Errorf(".gitignore lacks %q", want)
		}
	}
}
