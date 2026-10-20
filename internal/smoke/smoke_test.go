package smoke_test

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/internal/ipguard"
	"github.com/itsKontra/ddash/internal/scrub"
	"github.com/itsKontra/ddash/internal/smoke"
)

var repoRoot = filepath.Join("..", "..")

// lab serves the fixtures under root as the "real" homelab; nothing in
// these tests reaches a real system.
func lab(t *testing.T, root string, scenarios ...string) *fakelab.Lab {
	t.Helper()
	l, err := fakelab.Start(root, "127.0.0.1", scenarios...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

// envFor is the smoke.env pointing every system Component at l.
func envFor(t *testing.T, l *fakelab.Lab) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, c := range smoke.Components() {
		cfg, ok := l.Config()[c]
		if !ok {
			t.Fatalf("fakelab has no %s", c)
		}
		b, err := protojson.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		env[smoke.EnvVar(c)] = string(b)
	}
	return env
}

func run(t *testing.T, env map[string]string, rec *smoke.Record) (string, error) {
	t.Helper()
	var log bytes.Buffer
	err := smoke.Run(t.Context(), smoke.Options{Env: env, DataDir: t.TempDir(), Record: rec, Log: &log})
	return log.String(), err
}

func TestSmokePassesAgainstTheBaselineLab(t *testing.T) {
	log, err := run(t, envFor(t, lab(t, repoRoot)), nil)

	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
	for _, want := range []string{
		"ok docker-api ListContainers",
		"ok pfsense-api ListLeases",
		"ok pfsense-api ListGatewayGroups",
		"ok pfsense-api ListFirewallRules",
		"ok adguard-api ListQueries",
		"ok proxmox-api ListWorkloads",
		"ok nginx-config GetSnapshot",
		"ok nginx-config health",
	} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestEverySystemTheLabFakesWithGettersIsSmokeTested(t *testing.T) {
	l := lab(t, repoRoot)
	for c := range l.Addrs {
		if c == "ntfy-api" { // Actions only: nothing to read
			continue
		}
		if !slices.Contains(smoke.Components(), c) {
			t.Errorf("%s is faked by fakelab but not smoke-tested", c)
		}
	}
}

func TestASystemMissingFromSmokeEnvFails(t *testing.T) {
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_PROXMOX_API")

	_, err := run(t, env, nil)

	if err == nil || !strings.Contains(err.Error(), "SMOKE_PROXMOX_API") {
		t.Errorf("err = %v, want the missing variable named", err)
	}
}

func TestAnInvariantThatDoesntHoldFails(t *testing.T) {
	log, err := run(t, envFor(t, lab(t, repoRoot, "not-running")), nil)

	if err == nil || !strings.Contains(err.Error(), "adguard-api GetStatus: AdGuard Home isn't running") {
		t.Errorf("err = %v, want GetStatus's invariant to fail\n%s", err, log)
	}
}

func TestAnUnreachableSystemFailsAndTheOthersStillRun(t *testing.T) {
	env := envFor(t, lab(t, repoRoot))
	env["SMOKE_PFSENSE_API"] = `{"url": "http://127.0.0.1:1", "apiKey": "k"}`

	log, err := run(t, env, nil)

	if err == nil || !strings.Contains(err.Error(), "pfsense-api ListGateways") {
		t.Errorf("err = %v, want pfsense-api's getters failing", err)
	}
	if !strings.Contains(log, "ok adguard-api ListQueries\n") {
		t.Errorf("the other systems didn't run:\n%s", log)
	}
}

func TestRecordWritesScrubbedFixturesAnotherSmokeRunPassesAgainst(t *testing.T) {
	root := t.TempDir()
	s, err := scrub.New(scrub.Config{
		Key:      []byte("test key"),
		Subnets:  []scrub.Subnet{{Prefix: mustPrefix(t, "198.18.1.0/24"), N: 7}},
		Literals: []scrub.Literal{{Real: "galaxy-s24", Replacement: "phone-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	log, err := run(t, envFor(t, lab(t, repoRoot)), &smoke.Record{Root: root, Scenario: "baseline", Scrubber: s})
	if err != nil {
		t.Fatalf("record failed: %v\n%s", err, log)
	}

	for _, file := range []string{
		"components/pfsense-api/testdata/baseline/api/v2/status/dhcp_server/leases.json",
		"components/adguard-api/testdata/baseline/control/querylog.json",
		"components/proxmox-api/testdata/baseline/api2/json/cluster/resources.json",
		"components/docker-api/testdata/baseline/v1.44/containers/json.json",
		"components/nginx-config/testdata/baseline/nginx_-T.stdout",
	} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Errorf("not recorded: %v", err)
		}
	}
	if found, err := ipguard.Check(root); err != nil || len(found) != 0 {
		t.Errorf("the IP guard found %v (%v)", found, err)
	}
	leases, _ := os.ReadFile(filepath.Join(root, "components/pfsense-api/testdata/baseline/api/v2/status/dhcp_server/leases.json"))
	if strings.Contains(string(leases), "198.18.1.") || !strings.Contains(string(leases), "198.18.7.") {
		t.Errorf("leases kept the real subnet:\n%s", leases)
	}
	querylog, _ := os.ReadFile(filepath.Join(root, "components/adguard-api/testdata/baseline/control/querylog.json"))
	var ql struct{ Data []json.RawMessage }
	if err := json.Unmarshal(querylog, &ql); err != nil || len(ql.Data) == 0 || len(ql.Data) > scrub.QueryLogEntries {
		t.Errorf("query log has %d entries (%v)", len(ql.Data), err)
	}
	if strings.Contains(string(querylog), "galaxy-s24") || !strings.Contains(string(querylog), "phone-1") {
		t.Errorf("a scrub.map literal survived in the query log")
	}

	// The recording is a lab of its own: smoke passes against it too.
	if log, err := run(t, envFor(t, lab(t, root)), nil); err != nil {
		t.Errorf("smoke against the recording failed: %v\n%s", err, log)
	}
}

func TestRecordRefusesAFileThatStillHoldsALiteral(t *testing.T) {
	root := t.TempDir()
	// The replacement holds its literal, so every file holding it is refused.
	s, err := scrub.New(scrub.Config{Key: []byte("k"), Literals: []scrub.Literal{{Real: "galaxy-s24", Replacement: "galaxy-s24 phone"}}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = run(t, envFor(t, lab(t, repoRoot)), &smoke.Record{Root: root, Scenario: "baseline", Scrubber: s})

	if err == nil || !strings.Contains(err.Error(), "control/querylog.json") || !strings.Contains(err.Error(), "galaxy-s24") {
		t.Errorf("err = %v, want the query log refused", err)
	}
	if _, err := os.Stat(filepath.Join(root, "components/adguard-api/testdata/baseline/control/querylog.json")); err == nil {
		t.Errorf("the refused query log was written")
	}
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
