package runtime_test

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakegithub"
	"github.com/itsKontra/ddash/internal/release"
)

func TestADirUnpacksAComponentsBinaryFromItsCt(t *testing.T) {
	unpack := t.TempDir()
	d := runtime.Dir{Path: releaseDir(t, "dev", helloV1), Unpack: unpack}

	bin, err := d.Component(t.Context(), "greeter")
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(unpack, "greeter"); bin.Path != want {
		t.Errorf("binary at %s, want %s", bin.Path, want)
	}
	info, err := os.Stat(bin.Path)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary %v, %v; want it executable", info, err)
	}
	if sum := sha256.Sum256([]byte("greeter binary v1")); string(bin.Checksum) != string(sum[:]) {
		t.Errorf("checksum %x, want the binary's %x", bin.Checksum, sum)
	}
}

func TestADirUnpacksABinaryAgainWhenTheUnpackedOneChanged(t *testing.T) {
	d := runtime.Dir{Path: releaseDir(t, "dev", helloV1), Unpack: t.TempDir()}
	bin, err := d.Component(t.Context(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin.Path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	again, err := d.Component(t.Context(), "greeter")
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(again.Path); string(b) != "greeter binary v1" {
		t.Errorf("binary holds %q, want the .ct's", b)
	}
}

func TestADirRefusesACtThatDoesntMatchItsChecksum(t *testing.T) {
	dir := releaseDir(t, "dev", helloV1)
	if err := os.WriteFile(filepath.Join(dir, "greeter.ct"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runtime.Dir{Path: dir, Unpack: t.TempDir()}.Component(t.Context(), "greeter")

	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want the checksum named", err)
	}
}

func TestADirServesAPluginsFilesFromItsApp(t *testing.T) {
	d := runtime.Dir{Path: releaseDir(t, "dev", helloV1)}

	for name, want := range map[string]string{"hello/index.js": "export default {}", "hello/index.css": ".hello {}"} {
		if b, err := fs.ReadFile(d, name); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	for _, name := range []string{"hello/other.js", "hello.ddapp", "nope/index.js", "hello/../greeter.ct"} {
		if _, err := fs.ReadFile(d, name); err == nil {
			t.Errorf("%s: read, want an error", name)
		}
	}
}

func TestGitHubDownloadsAReleaseFileAsItIs(t *testing.T) {
	dir := releaseDir(t, "v1.0.0", helloV1)
	agent := runtime.HostAgentFile("amd64")
	if err := os.WriteFile(filepath.Join(dir, agent), []byte("host agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := release.Sums(dir); err != nil {
		t.Fatal(err)
	}
	gh := fakegithub.New(repo)
	gh.Publish("v1.0.0", dir)

	bin, err := github(gh, t.TempDir(), "v1.0.0").File(t.Context(), agent)
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(bin.Path); string(b) != "host agent" {
		t.Errorf("%s holds %q, want the asset", agent, b)
	}
	if sum := sha256.Sum256([]byte("host agent")); string(bin.Checksum) != string(sum[:]) {
		t.Errorf("checksum %x, want %x", bin.Checksum, sum)
	}
}
