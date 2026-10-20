package runtime_test

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakegithub"
	"github.com/itsKontra/ddash/internal/release"
)

const repo = "itsKontra/ddash"

// releaseDir lays out a Release dir of Release rel whose Plugin hello
// requires greeter, with files (binaries and bundles) by name.
func releaseDir(t *testing.T, rel string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalogue := `{"release":"` + rel + `","plugins":[{"name":"hello","requires":["greeter"]}],"components":[{"name":"greeter"}]}`
	if err := os.WriteFile(filepath.Join(dir, release.CatalogueFile), []byte(catalogue), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := release.Sums(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// helloV1 is Release v1.0.0 of hello and greeter.
var helloV1 = map[string]string{
	"greeter":          "greeter binary v1",
	"hello.plugin.js":  "export default {}",
	"hello.plugin.css": ".hello {}",
}

func github(gh *fakegithub.Fake, data, rel string) runtime.ReleaseSource {
	return runtime.GitHub(runtime.GitHubOptions{Repo: repo, Release: rel, DataDir: data, Client: gh.Client()})
}

func TestGitHubDownloadsAComponentIntoTheDataDirVerified(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	data := t.TempDir()
	src := github(gh, data, "v1.0.0")

	cat, err := src.Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bin, err := src.Component(t.Context(), "greeter")
	if err != nil {
		t.Fatal(err)
	}

	if cat.Release != "v1.0.0" || len(cat.Plugins) != 1 {
		t.Errorf("catalogue = %+v, want v1.0.0's", cat)
	}
	if want := filepath.Join(data, "components", "greeter", "v1.0.0", "greeter"); bin.Path != want {
		t.Errorf("binary at %s, want %s", bin.Path, want)
	}
	if b, _ := os.ReadFile(bin.Path); string(b) != "greeter binary v1" {
		t.Errorf("binary holds %q, want v1's", b)
	}
	if sum := sha256.Sum256([]byte("greeter binary v1")); string(bin.Checksum) != string(sum[:]) {
		t.Errorf("checksum %x, want %x", bin.Checksum, sum)
	}
}

func TestGitHubRefusesADownloadThatDoesntMatchItsChecksum(t *testing.T) {
	gh := fakegithub.New(repo)
	dir := releaseDir(t, "v1.0.0", helloV1)
	if err := os.WriteFile(filepath.Join(dir, "greeter"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	gh.Publish("v1.0.0", dir)
	data := t.TempDir()

	_, err := github(gh, data, "v1.0.0").Component(t.Context(), "greeter")

	if !errors.Is(err, runtime.ErrDownload) || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want a failed download for its checksum", err)
	}
	if _, err := os.Stat(filepath.Join(data, "components", "greeter", "v1.0.0", "greeter")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the tampered binary was kept: %v", err)
	}
}

func TestGitHubSendsTheTokenToGitHubButNotToTheAssetStorage(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	src := runtime.GitHub(runtime.GitHubOptions{Repo: repo, Release: "v1.0.0", DataDir: t.TempDir(), Client: gh.Client(), Token: "ghp_secret"})

	if _, err := src.Component(t.Context(), "greeter"); err != nil {
		t.Fatal(err)
	}

	for _, r := range gh.Requests() {
		api := strings.HasPrefix(r.Path, "/repos/")
		if api && r.Authorization != "Bearer ghp_secret" || !api && r.Authorization != "" {
			t.Errorf("%s %s sent Authorization %q", r.Method, r.Path, r.Authorization)
		}
	}
}

func TestGitHubKeepsWhatItDownloadedSoItNeedsNoNetworkAfterwards(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	data := t.TempDir()
	first := github(gh, data, "v1.0.0")
	if _, err := first.Component(t.Context(), "greeter"); err != nil {
		t.Fatal(err)
	}
	if err := first.Plugin(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	gh.SetOffline(true)

	again := github(gh, data, "v1.0.0") // as after a restart
	cat, err := again.Catalogue(t.Context())
	if err != nil || cat.Release != "v1.0.0" {
		t.Errorf("Catalogue offline = %v, %v; want v1.0.0's kept catalogue.json", cat.Release, err)
	}
	if _, err := again.Component(t.Context(), "greeter"); err != nil {
		t.Errorf("Component offline: %v", err)
	}
	if err := again.Plugin(t.Context(), "hello"); err != nil {
		t.Errorf("Plugin offline: %v", err)
	}
	if b, err := fs.ReadFile(again, "hello.plugin.js"); string(b) != "export default {}" {
		t.Errorf("hello.plugin.js = %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "catalogue.json")); err != nil || !strings.Contains(string(b), `"v1.0.0"`) {
		t.Errorf("/data/catalogue.json = %q, %v", b, err)
	}
}

func TestGitHubFetchesTheCatalogueOfANewRelease(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	gh.Publish("v1.1.0", releaseDir(t, "v1.1.0", map[string]string{"greeter": "greeter binary v1.1"}))
	data := t.TempDir()
	if _, err := github(gh, data, "v1.0.0").Catalogue(t.Context()); err != nil {
		t.Fatal(err)
	}

	src := github(gh, data, "v1.1.0")
	cat, err := src.Catalogue(t.Context())
	if err != nil || cat.Release != "v1.1.0" {
		t.Fatalf("Catalogue = %v, %v; want v1.1.0's", cat.Release, err)
	}
	bin, err := src.Component(t.Context(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin.Path); string(b) != "greeter binary v1.1" {
		t.Errorf("binary holds %q, want v1.1's", b)
	}
}

func TestGitHubWhileUnreachableFailsAsADownload(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	gh.SetOffline(true)

	_, err := github(gh, t.TempDir(), "v1.0.0").Catalogue(t.Context())

	if !errors.Is(err, runtime.ErrDownload) {
		t.Errorf("err = %v, want ErrDownload", err)
	}
}

func TestGitHubNamesTheLatestRelease(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", releaseDir(t, "v1.0.0", helloV1))
	gh.Publish("v1.1.0", releaseDir(t, "v1.1.0", helloV1))

	latest, err := runtime.GitHub(runtime.GitHubOptions{Repo: repo, Release: "v1.0.0", DataDir: t.TempDir(), Client: gh.Client()}).Latest(t.Context())

	if err != nil || latest != "v1.1.0" {
		t.Errorf("Latest = %q, %v; want v1.1.0", latest, err)
	}
}
