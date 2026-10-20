package core_test

import (
	"net/http"
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
}
