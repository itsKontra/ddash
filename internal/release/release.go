// Package release lays out a Release from the repo: catalogue.json merged
// from every plugin.yaml and component.yaml, every Component's binary, every
// Plugin's bundle, and SHA256SUMS over them. `make dev` and the subprocess
// test build one into a local dir that the core reads as DDASH_RELEASE_DIR.
//
// The layout is flat, like the assets of a GitHub release:
//
//	catalogue.json
//	SHA256SUMS      sha256sum format: "<hex>  <file>"
//	<component>     one static binary per Component, named after it
//	<plugin>.plugin.js, <plugin>.plugin.css
//	                each Plugin's bundle, built by Vite into plugins/<plugin>/dist/
package release

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/itsKontra/ddash/core/runtime"
)

// CatalogueFile and SumsFile are the Release dir's two index files.
const (
	CatalogueFile = "catalogue.json"
	SumsFile      = runtime.SumsFile
)

// Options say what to build and where.
type Options struct {
	// Root is the repo.
	Root string
	// Out is the Release dir to fill.
	Out string
	// Release is stamped into catalogue.json and every binary.
	Release string
	// LinkBundles symlinks each Plugin's bundle instead of copying it, so
	// `vite build --watch` rebuilds show through (`make dev`).
	LinkBundles bool
}

// Build fills o.Out with the Release: catalogue.json, every Component's
// binary (static, CGO off, for this machine's OS and architecture), every
// Plugin's bundle (which `make build` must have built) and SHA256SUMS.
func Build(ctx context.Context, o Options) error {
	cat, err := Catalogue(o.Root, o.Release)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.Out, 0o750); err != nil {
		return err
	}
	for _, c := range cat.Components {
		pkg := "./" + path.Join("components", c.Name, "cmd")
		if err := Binary(ctx, o.Root, pkg, filepath.Join(o.Out, c.Name), o.Release); err != nil {
			return err
		}
	}
	for _, p := range cat.Plugins {
		if err := bundle(o, p.Name); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.Out, CatalogueFile), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return Sums(o.Out)
}

// bundle puts plugin's bundle, as Vite built it into plugins/<name>/dist/,
// into the Release dir as <name>.plugin.js and <name>.plugin.css.
func bundle(o Options, plugin string) error {
	for _, ext := range []string{"js", "css"} {
		src, err := filepath.Abs(filepath.Join(o.Root, "plugins", plugin, "dist", "index."+ext))
		if err != nil {
			return err
		}
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("plugin %s has no built bundle (run `make build`): %w", plugin, err)
		}
		dst := filepath.Join(o.Out, plugin+".plugin."+ext)
		if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if o.LinkBundles {
			err = os.Symlink(src, dst)
		} else {
			err = copyFile(src, dst)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// Catalogue merges every plugins/*/plugin.yaml and components/*/component.yaml
// under the repo root into release's catalogue, Plugins and Components each
// sorted by name. It fails on a manifest not named after its directory and
// on a requires that doesn't resolve.
func Catalogue(root, release string) (runtime.Catalogue, error) {
	cat := runtime.Catalogue{Release: release}
	if err := manifests(root, "plugins", "plugin.yaml", func(p runtime.PluginEntry) {
		cat.Plugins = append(cat.Plugins, p)
	}); err != nil {
		return cat, err
	}
	if err := manifests(root, "components", "component.yaml", func(c runtime.ComponentEntry) {
		cat.Components = append(cat.Components, c)
	}); err != nil {
		return cat, err
	}
	if err := cat.Check(); err != nil {
		return cat, fmt.Errorf("catalogue: %w", err)
	}
	return cat, nil
}

// entry is a manifest's entry in the catalogue.
type entry interface {
	runtime.PluginEntry | runtime.ComponentEntry
}

// manifests reads root/dir/*/file, in directory order, into add.
func manifests[E entry](root, dir, file string, add func(e E)) error {
	paths, err := filepath.Glob(filepath.Join(root, dir, "*", file))
	if err != nil {
		return err
	}
	for _, p := range paths { // Glob sorts
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var e E
		if err := yaml.Unmarshal(b, &e); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		name := filepath.Base(filepath.Dir(p))
		if got := entryName(e); got != name {
			return fmt.Errorf("%s/%s/%s: name is %q, want its directory's name %q", dir, name, file, got, name)
		}
		add(e)
	}
	return nil
}

func entryName(e any) string {
	switch e := e.(type) {
	case runtime.PluginEntry:
		return e.Name
	case runtime.ComponentEntry:
		return e.Name
	}
	return ""
}

// Binary builds the Go package pkg (relative to the repo root) into out,
// stamped with release. ldflags are added to the linker flags, e.g.
// "-X main.name=imposter".
func Binary(ctx context.Context, root, pkg, out, release string, ldflags ...string) error {
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	flags := append([]string{"-X", "github.com/itsKontra/ddash/sdk.Release=" + release}, ldflags...)
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", strings.Join(flags, " "), "-o", out, pkg)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s: %w\n%s", pkg, err, b)
	}
	return nil
}

// Sums writes dir's SHA256SUMS over every other file in it except
// catalogue.json, in sha256sum's format.
func Sums(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var sums strings.Builder
	for _, e := range entries { // ReadDir sorts
		if e.IsDir() || e.Name() == SumsFile || e.Name() == CatalogueFile {
			continue
		}
		sum, err := fileSum(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sum, e.Name())
	}
	return os.WriteFile(filepath.Join(dir, SumsFile), []byte(sums.String()), 0o644)
}

func fileSum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
