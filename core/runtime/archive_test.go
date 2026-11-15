package runtime_test

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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

func TestADirReadsAPluginsAppFromTheArchiveItOpenedOnce(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root opens a file whatever its mode")
	}
	dir := releaseDir(t, "v1.0.0", helloV1)
	d := runtime.Dir{Path: dir}
	if _, err := fs.ReadFile(d, "hello/index.js"); err != nil {
		t.Fatal(err)
	}
	// Opening the .ddapp again would fail now.
	app := filepath.Join(dir, runtime.AppFile("hello"))
	if err := os.Chmod(app, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(app, 0o600) }) //nolint:errcheck // only for TempDir's cleanup

	for _, name := range []string{"hello/index.js", "hello/index.css"} {
		if b, err := fs.ReadFile(d, name); err != nil {
			t.Errorf("%s = %q, %v; want it read from the open .ddapp", name, b, err)
		}
	}
}

func TestADirReadsAPluginsAppRepackedInPlaceAnew(t *testing.T) {
	dir := releaseDir(t, "dev", helloV1)
	d := runtime.Dir{Path: dir}
	app := filepath.Join(dir, runtime.AppFile("hello"))
	if _, err := fs.ReadFile(d, "hello/index.js"); err != nil {
		t.Fatal(err)
	}

	for _, repack := range []struct {
		how  string
		pack func(fsys fs.FS) error
	}{
		{"replaced", func(fsys fs.FS) error { return runtime.PackFile(app, fsys) }},
		{"overwritten", func(fsys fs.FS) error {
			f, err := os.Create(app) // the same file, truncated
			if err != nil {
				return err
			}
			defer f.Close()
			return runtime.Pack(f, fsys)
		}},
	} {
		want := "export default " + repack.how
		if err := repack.pack(fstest.MapFS{"index.js": {Data: []byte(want)}}); err != nil {
			t.Fatal(err)
		}

		if b, err := fs.ReadFile(d, "hello/index.js"); err != nil || string(b) != want {
			t.Errorf("%s: index.js = %q, %v; want %q", repack.how, b, err, want)
		}
	}
	if err := os.Remove(app); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(d, "hello/index.js"); err == nil {
		t.Error("removed: index.js read, want an error")
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
