package core_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/internal/release"
)

// repoRoot is the repo, seen from this package.
var repoRoot = filepath.Join("..")

// The subprocess test: the Release built from the repo, run by the real
// go-plugin launcher against fakelab, as `make dev` runs it.
func TestTheBuiltReleaseRunsAsSubprocessesAgainstFakelab(t *testing.T) {
	dist := t.TempDir()
	if err := release.Build(t.Context(), release.Options{Root: repoRoot, Out: dist, Release: "dev"}); err != nil {
		t.Fatal(err)
	}
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)

	h := coretest.New(t, coretest.Options{ReleaseDir: dist})
	for component, cfg := range lab.Config() {
		h.SeedConfig(component, cfg)
	}
	h.Install("docker")
	h.WaitState("docker-api", runtime.OK)

	res := h.Call("docker", "docker-api", "ListContainers", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"jellyfin"`) {
		t.Errorf("ListContainers: status %d: %s; want baseline's jellyfin", res.Code, res.Body)
	}

	// nginx-config reads `nginx -T` from fakelab's SSH server.
	h.Install("reverse-proxy")
	h.WaitState("nginx-config", runtime.OK)
	res = h.Call("reverse-proxy", "nginx-config", "GetSnapshot", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"jellyfin.home.example"`) {
		t.Errorf("GetSnapshot: status %d: %s; want baseline's jellyfin server block", res.Code, res.Body)
	}

	// apps reads nginx-config's snapshot through the host (GRPCBroker).
	h.Install("apps")
	h.WaitState("apps", runtime.OK)
	res = h.Call("apps", "apps", "ListApps", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"url":"https://jellyfin.home.example"`) {
		t.Errorf("ListApps: status %d: %s; want baseline's jellyfin App", res.Code, res.Body)
	}
}

// sampleRelease lays out a Release dir of Release "dev" whose Plugin hello
// requires greeter, a sample binary built for Release rel with ldflags.
func sampleRelease(t *testing.T, rel string, ldflags ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := release.Binary(t.Context(), repoRoot, "./core/internal/sample/cmd/sample", filepath.Join(dir, "greeter"), rel, ldflags...); err != nil {
		t.Fatal(err)
	}
	catalogue := `{"release":"dev","plugins":[{"name":"hello","requires":["greeter"]}],"components":[{"name":"greeter"}]}`
	if err := os.WriteFile(filepath.Join(dir, release.CatalogueFile), []byte(catalogue), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := release.Sums(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestASubprocessStartsAndAnswers(t *testing.T) {
	h := coretest.New(t, coretest.Options{ReleaseDir: sampleRelease(t, "dev")})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	if res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("Greet: status %d: %s", res.Code, res.Body)
	}
}

func TestABinaryThatDoesntMatchItsChecksumIsRefused(t *testing.T) {
	dir := sampleRelease(t, "dev")
	f, err := os.OpenFile(filepath.Join(dir, "greeter"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("tampered"))
	f.Close()
	h := coretest.New(t, coretest.Options{ReleaseDir: dir})
	h.SeedConfig("greeter", configured)

	h.Install("hello")

	h.WaitState("greeter", runtime.Down)
	if got := h.Status("greeter"); !strings.Contains(got.Message, "checksum") {
		t.Errorf("greeter is %v, want down for its checksum", got)
	}
}

func TestABinaryWithoutAChecksumIsRefused(t *testing.T) {
	dir := sampleRelease(t, "dev")
	if err := os.WriteFile(filepath.Join(dir, release.SumsFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := coretest.New(t, coretest.Options{ReleaseDir: dir})

	h.Install("hello")

	h.WaitState("greeter", runtime.UpdatePending) // never launched
	if got := h.Status("greeter"); !strings.Contains(got.Message, "no checksum") {
		t.Errorf("greeter is %v, want update pending for its missing checksum", got)
	}
}

func TestABinaryThatSaysItIsAnotherComponentIsRefused(t *testing.T) {
	h := coretest.New(t, coretest.Options{ReleaseDir: sampleRelease(t, "dev", "-X", "main.name=imposter")})
	h.SeedConfig("greeter", configured)

	h.Install("hello")

	h.WaitState("greeter", runtime.Down)
	if got := h.Status("greeter"); !strings.Contains(got.Message, "it says it is imposter") {
		t.Errorf("greeter is %v, want down naming the imposter", got)
	}
}

func TestABinaryOfAnotherReleaseIsRefused(t *testing.T) {
	h := coretest.New(t, coretest.Options{ReleaseDir: sampleRelease(t, "v0.0.1")})
	h.SeedConfig("greeter", configured)

	h.Install("hello")

	h.WaitState("greeter", runtime.Down)
	if got := h.Status("greeter"); !strings.Contains(got.Message, "v0.0.1") || !strings.Contains(got.Message, "dev") {
		t.Errorf("greeter is %v, want down naming both Releases", got)
	}
}

func TestASubprocessThatExitsIsCrashedThenRestarted(t *testing.T) {
	h := coretest.New(t, coretest.Options{ReleaseDir: sampleRelease(t, "dev")})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	h.Call("hello", "greeter", "Act", `{}`) // the sample exits

	h.WaitState("greeter", runtime.Crashed)
	h.WaitState("greeter", runtime.OK)
	if res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("Greet after the restart: status %d: %s", res.Code, res.Body)
	}
}
