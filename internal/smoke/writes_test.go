package smoke_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/internal/smoke"
)

// unused is a LAN address the lab's fixtures give no lease, static
// mapping or AdGuard client.
const unused = "198.18.1.60"

// writesLab is the baseline lab plus the smoke targets: pfSense's empty
// DDASH_SMOKE alias, and Proxmox's VM 100 and LXC 210 Protected (the
// protected scenario; the baseline already has the stopped ddash-smoke
// LXC 211).
func writesLab(t *testing.T) (*fakelab.Lab, map[string]string) {
	t.Helper()
	l := lab(t, repoRoot, "smoke-writes", "protected")
	env := envFor(t, l)
	env[smoke.UnusedAddressVar] = unused
	return l, env
}

func runWrites(t *testing.T, env map[string]string) (string, error) {
	t.Helper()
	var log bytes.Buffer
	err := smoke.RunWrites(t.Context(), smoke.Options{Env: env, DataDir: t.TempDir(), Log: &log})
	return log.String(), err
}

// labState is what the round trips may touch, to compare before and after.
type labState struct {
	Aliases                  []pfsensefake.Alias
	Mappings                 []pfsensefake.Mapping
	DHCPPending, FWPending   bool
	Clients                  []map[string]any
	Smoke, Protected, OtherV string
}

func stateOf(l *fakelab.Lab) labState {
	fw := l.Handler("pfsense-api").(*pfsensefake.Firewall)
	ag := l.Handler("adguard-api").(*adguardfake.AdGuard)
	px := l.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	return labState{
		Aliases: fw.Aliases(), Mappings: fw.StaticMappings("lan"),
		DHCPPending: fw.Pending(), FWPending: fw.FirewallPending(),
		Clients: ag.Clients(),
		Smoke:   px.Status(211), Protected: px.Status(100), OtherV: px.Status(210),
	}
}

func logs(l *fakelab.Lab) (fw, ag, px []string) {
	return l.Handler("pfsense-api").(*pfsensefake.Firewall).Log(),
		l.Handler("adguard-api").(*adguardfake.AdGuard).Log(),
		l.Handler("proxmox-api").(*proxmoxfake.Proxmox).Log()
}

func count(entries []string, want string) int {
	n := 0
	for _, e := range entries {
		if e == want {
			n++
		}
	}
	return n
}

// powerRequests are the Proxmox power requests sent, in order.
func powerRequests(px []string) []string {
	var out []string
	for _, e := range px {
		if strings.HasPrefix(e, "POST ") && strings.Contains(e, "/status/") {
			out = append(out, strings.TrimPrefix(e, "POST /api2/json/nodes/pve/"))
		}
	}
	return out
}

func TestWritesRoundTripOnTheSmokeTargetsAndLeaveThemAsTheyWere(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	before := stateOf(l)

	log, err := runWrites(t, env)

	if err != nil {
		t.Fatalf("smoke-writes failed: %v\n%s", err, log)
	}
	for _, want := range []string{
		"ok pfsense-api alias DDASH_SMOKE round trip",
		"ok pfsense-api static mapping round trip",
		"ok adguard-api SetClientFiltering round trip",
		"ok proxmox-api ddash-smoke start and shutdown",
		"ok proxmox-api Protected refusal (VM 100 pfSense)",
	} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if diff := cmp.Diff(before, stateOf(l)); diff != "" {
		t.Errorf("the lab after smoke-writes (-before +after):\n%s", diff)
	}

	fw, ag, px := logs(l)
	// The placeholder went in and out, each write applied.
	if n := count(fw, "PATCH /api/v2/firewall/alias"); n != 2 {
		t.Errorf("%d alias writes, want 2 (add, remove)\n%v", n, fw)
	}
	if n := count(fw, pfsensefake.AppliedFirewall); n != 2 {
		t.Errorf("the firewall applied %d times, want 2", n)
	}
	for _, want := range []string{
		"POST /api/v2/services/dhcp_server/static_mapping",
		"PATCH /api/v2/services/dhcp_server/static_mapping",
		"DELETE /api/v2/services/dhcp_server/static_mapping",
	} {
		if count(fw, want) != 1 {
			t.Errorf("static mapping requests lack one %q:\n%v", want, fw)
		}
	}
	if n := count(fw, pfsensefake.Applied); n != 3 {
		t.Errorf("DHCP applied %d times, want 3 (create, rename, delete)", n)
	}
	for _, want := range []string{"POST /control/clients/add", "POST /control/clients/delete"} {
		if count(ag, want) != 1 {
			t.Errorf("AdGuard requests lack one %q:\n%v", want, ag)
		}
	}
	if n := count(ag, "POST /control/clients/update"); n != 2 {
		t.Errorf("%d client updates, want 2 (off, then on)", n)
	}
	if diff := cmp.Diff([]string{
		"lxc/211/status/start",
		"lxc/211/status/shutdown timeout=60",
		"qemu/100/status/start",
	}, powerRequests(px)); diff != "" {
		t.Errorf("power requests (-want +got):\n%s", diff)
	}
}

// failOnce answers the first request matching match with 500 and passes
// everything else to h.
func failOnce(h http.Handler, match func(r *http.Request) bool) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed := false
		if match(r) {
			once.Do(func() { failed = true })
		}
		if failed {
			http.Error(w, `{"code": 500, "status": "server error", "response_id": "SMOKE_TEST", "message": "injected failure", "data": []}`, http.StatusInternalServerError)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// pointAt serves h in front of a system and points the component's config
// in env at it.
func pointAt(t *testing.T, env map[string]string, component string, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(env[smoke.EnvVar(component)]), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["url"] = srv.URL
	b, _ := json.Marshal(cfg)
	env[smoke.EnvVar(component)] = string(b)
}

func TestWritesCleanUpWhenAStepFails(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	before := stateOf(l)
	// Each round trip fails after its first write.
	pointAt(t, env, "pfsense-api", failOnce(failOnce(l.Handler("pfsense-api"), func(r *http.Request) bool {
		// The rename.
		return r.Method == http.MethodPatch && r.URL.Path == "/api/v2/services/dhcp_server/static_mapping"
	}), func(r *http.Request) bool {
		// The firewall check after the placeholder went in.
		return r.Method == http.MethodGet && r.URL.Path == "/api/v2/firewall/apply"
	}))
	var updates atomic.Int32
	pointAt(t, env, "adguard-api", failOnce(l.Handler("adguard-api"), func(r *http.Request) bool {
		// The toggle: the second update.
		return r.URL.Path == "/control/clients/update" && updates.Add(1) == 2
	}))
	px := l.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	pointAt(t, env, "proxmox-api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The graceful shutdown's task fails; a forced one would not.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/211/status/shutdown") {
			b, _ := io.ReadAll(r.Body)
			var params map[string]any
			_ = json.Unmarshal(b, &params)
			if params["forceStop"] == nil {
				px.FailNextTask("shutdown timed out")
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		px.ServeHTTP(w, r)
	}))

	log, err := runWrites(t, env)

	for _, want := range []string{
		"pfsense-api alias DDASH_SMOKE round trip",
		"pfsense-api static mapping round trip",
		"adguard-api SetClientFiltering round trip",
		"proxmox-api ddash-smoke start and shutdown",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q failing", err, want)
		}
	}
	if !strings.Contains(log, "ok proxmox-api Protected refusal (VM 100 pfSense)\n") {
		t.Errorf("the Protected check didn't run after the failures:\n%s", log)
	}
	if diff := cmp.Diff(before, stateOf(l)); diff != "" {
		t.Errorf("cleanup left the lab changed (-before +after):\n%s\n%s", diff, log)
	}
	if got := powerRequests(px.Log()); !slices.Contains(got, "lxc/211/status/shutdown forceStop=1 timeout=30") {
		t.Errorf("cleanup didn't force ddash-smoke down: %v", got)
	}
}

func TestWritesRemoveWhatAnInterruptedRunLeftFirst(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	before := stateOf(l)
	fw := l.Handler("pfsense-api").(*pfsensefake.Firewall)
	leftover := httptest.NewRequest(http.MethodPost, "/api/v2/services/dhcp_server/static_mapping",
		strings.NewReader(`{"parent_id": "lan", "mac": "`+smoke.SmokeMAC+`", "ipaddr": "`+unused+`", "descr": "ddash smoke-writes", "apply": true}`))
	fw.ServeHTTP(httptest.NewRecorder(), leftover)

	log, err := runWrites(t, env)

	if err != nil {
		t.Fatalf("smoke-writes failed: %v\n%s", err, log)
	}
	if want := "cleanup before pfsense-api static mapping round trip: deleted the static mapping of " + smoke.SmokeMAC + " on lan (left by an earlier run)\n"; !strings.Contains(log, want) {
		t.Errorf("log lacks %q:\n%s", want, log)
	}
	if diff := cmp.Diff(before, stateOf(l)); diff != "" {
		t.Errorf("the lab after smoke-writes (-before +after):\n%s", diff)
	}
}

func TestWritesRefuseAnAddressInUseAndWriteNothing(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	// operator-pc's: a static mapping, a lease and an AdGuard client.
	env[smoke.UnusedAddressVar] = "198.18.1.12"
	before := stateOf(l)

	_, err := runWrites(t, env)

	for _, want := range []string{
		"pfsense-api static mapping round trip: 198.18.1.12 is in use",
		"adguard-api SetClientFiltering round trip: 198.18.1.12 is in use",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	fw, ag, _ := logs(l)
	if count(fw, "POST /api/v2/services/dhcp_server/static_mapping") != 0 || count(ag, "POST /control/clients/add") != 0 {
		t.Errorf("wrote for an address in use:\n%v\n%v", fw, ag)
	}
	if diff := cmp.Diff(before, stateOf(l)); diff != "" {
		t.Errorf("the lab changed (-before +after):\n%s", diff)
	}
}

func TestWritesNeedTheUnusedAddress(t *testing.T) {
	t.Parallel()
	_, env := writesLab(t)
	delete(env, smoke.UnusedAddressVar)

	_, err := runWrites(t, env)

	if err == nil || !strings.Contains(err.Error(), smoke.UnusedAddressVar) {
		t.Errorf("err = %v, want %s named", err, smoke.UnusedAddressVar)
	}
}

func TestWritesLeaveARunningDdashSmokeAlone(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	px := l.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	start := httptest.NewRequest(http.MethodPost, "/api2/json/nodes/pve/lxc/211/status/start", nil)
	px.ServeHTTP(httptest.NewRecorder(), start)
	px.FinishTasks()

	_, err := runWrites(t, env)

	if err == nil || !strings.Contains(err.Error(), "proxmox-api ddash-smoke start and shutdown: LXC 211 ddash-smoke is running") {
		t.Errorf("err = %v, want ddash-smoke refused while running", err)
	}
	if got := powerRequests(px.Log()); !slices.Equal(got, []string{"lxc/211/status/start", "qemu/100/status/start"}) {
		t.Errorf("power requests = %v, want only the setup's and the Protected check's", got)
	}
	if px.Status(211) != "running" {
		t.Errorf("ddash-smoke is %s, want left running", px.Status(211))
	}
}

func TestWritesFailWhenTheTokenIsNotRefusedOnAProtectedWorkload(t *testing.T) {
	t.Parallel()
	l, env := writesLab(t)
	// Proxmox let the start through to the Workload, which is running already.
	pointAt(t, env, "proxmox-api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/100/status/start") {
			http.Error(w, "VM 100 already running", http.StatusInternalServerError)
			return
		}
		l.Handler("proxmox-api").ServeHTTP(w, r)
	}))

	_, err := runWrites(t, env)

	if err == nil || !strings.Contains(err.Error(), "proxmox-api Protected refusal: the token wasn't refused on VM 100 pfSense") {
		t.Errorf("err = %v, want the missing refusal", err)
	}
}
