package release_test

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/release"
)

// tree writes files (path → contents) under a temp repo root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheCatalogueMergesEveryPluginAndComponentManifest(t *testing.T) {
	root := tree(t, map[string]string{
		"plugins/docker/plugin.yaml": `name: docker
title: Docker
description: Containers on every Docker host.
icon: container
author: itsKontra
requires: [docker-api]
`,
		"components/docker-api/component.yaml": `name: docker-api
description: Reads containers.
requires: []
`,
		"components/apps/component.yaml": `name: apps
description: Discovers Apps.
requires: [docker-api]
`,
	})

	cat, err := release.Catalogue(root, "dev")
	if err != nil {
		t.Fatal(err)
	}

	want := runtime.Catalogue{
		Release: "dev",
		Plugins: []runtime.PluginEntry{{
			Name: "docker", Title: "Docker", Description: "Containers on every Docker host.",
			Icon: "container", Author: "itsKontra", Requires: []string{"docker-api"},
		}},
		Components: []runtime.ComponentEntry{
			{Name: "apps", Description: "Discovers Apps.", Requires: []string{"docker-api"}},
			{Name: "docker-api", Description: "Reads containers.", Requires: []string{}},
		},
	}
	if diff := cmp.Diff(want, cat); diff != "" {
		t.Errorf("catalogue (-want +got):\n%s", diff)
	}
}

func TestTheCatalogueFailsOnARequiresThatDoesntResolve(t *testing.T) {
	root := tree(t, map[string]string{
		"plugins/docker/plugin.yaml":           "name: docker\ntitle: Docker\nrequires: [docker-api]\n",
		"components/apps/component.yaml":       "name: apps\nrequires: [proxmox-api]\n",
		"components/docker-api/component.yaml": "name: docker-api\nrequires: []\n",
	})

	_, err := release.Catalogue(root, "dev")

	if err == nil || !strings.Contains(err.Error(), "apps requires proxmox-api") {
		t.Errorf("err = %v, want apps' unresolved requires named", err)
	}
}

func TestTheCatalogueFailsWhenAManifestIsntNamedAfterItsDirectory(t *testing.T) {
	root := tree(t, map[string]string{
		"plugins/docker/plugin.yaml": "name: containers\ntitle: Docker\nrequires: []\n",
	})

	_, err := release.Catalogue(root, "dev")

	if err == nil || !strings.Contains(err.Error(), "plugins/docker") {
		t.Errorf("err = %v, want the misnamed manifest named", err)
	}
}

func TestTheCatalogueFailsOnAPluginNameClashingWithACorePath(t *testing.T) {
	for _, name := range []string{"api", "auth", "vendor", "plugins", "settings", "healthz"} {
		t.Run(name, func(t *testing.T) {
			root := tree(t, map[string]string{
				"plugins/" + name + "/plugin.yaml": "name: " + name + "\ntitle: Clash\nrequires: []\n",
			})

			_, err := release.Catalogue(root, "dev")

			if want := "plugin name " + name + " clashes with the core's /" + name; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
}

// The repo's own manifests: CI fails here when a Plugin or Component in the
// tree breaks a catalogue rule.
func TestTheReposCatalogueChecks(t *testing.T) {
	cat, err := release.Catalogue("../..", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Plugins) == 0 {
		t.Error("no Plugins found in the repo")
	}
}

// The reverse-proxy Page joins nginx-config's Proxy Hosts with apps' Apps
// and their Checks, so the Plugin requires all three.
func TestReverseProxyRequiresNginxConfigAppsAndChecks(t *testing.T) {
	cat, err := release.Catalogue("../..", "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cat.Plugins {
		if p.Name != "reverse-proxy" {
			continue
		}
		got := slices.Sorted(slices.Values(p.Requires))
		if want := []string{"apps", "checks", "nginx-config"}; !slices.Equal(got, want) {
			t.Errorf("reverse-proxy requires %v, want %v", p.Requires, want)
		}
		return
	}
	t.Error("no reverse-proxy Plugin in the repo")
}

// widgetsOnly is a repo with one Plugin requiring nothing, so building its
// Release builds no binaries.
func widgetsOnly(t *testing.T, bundle map[string]string) string {
	files := map[string]string{"plugins/docker/plugin.yaml": "name: docker\ntitle: Docker\nrequires: []\n"}
	for name, body := range bundle {
		files["plugins/docker/dist/"+name] = body
	}
	return tree(t, files)
}

func TestTheReleaseCarriesEachPluginsBundleWithItsChecksum(t *testing.T) {
	root := widgetsOnly(t, map[string]string{"index.js": "export default {}", "index.css": ".d{}"})
	out := t.TempDir()

	if err := release.Build(t.Context(), release.Options{Root: root, Out: out, Release: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}

	for file, want := range map[string]string{"docker.plugin.js": "export default {}", "docker.plugin.css": ".d{}"} {
		if b, err := os.ReadFile(filepath.Join(out, file)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v; want %q", file, b, err, want)
		}
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  docker.plugin.css\n", "  docker.plugin.js\n"} {
		if !strings.Contains(string(sums), want) {
			t.Errorf("SHA256SUMS lacks %q:\n%s", want, sums)
		}
	}
}

func TestTheReleaseFailsOnAPluginWhoseBundleIsntBuilt(t *testing.T) {
	root := widgetsOnly(t, nil)

	err := release.Build(t.Context(), release.Options{Root: root, Out: t.TempDir(), Release: "dev"})

	if err == nil || !strings.Contains(err.Error(), "make build") {
		t.Errorf("err = %v, want one saying to run make build", err)
	}
}

func TestALinkedReleaseShowsThePluginsRebuiltBundle(t *testing.T) {
	root := widgetsOnly(t, map[string]string{"index.js": "v1", "index.css": ""})
	out := t.TempDir()
	if err := release.Build(t.Context(), release.Options{Root: root, Out: out, Release: "dev", LinkBundles: true}); err != nil {
		t.Fatal(err)
	}

	// vite build --watch replaces the file.
	if err := os.WriteFile(filepath.Join(root, "plugins/docker/dist/index.js"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}

	if b, err := os.ReadFile(filepath.Join(out, "docker.plugin.js")); err != nil || string(b) != "v2" {
		t.Errorf("docker.plugin.js: %q, %v; want the rebuilt v2", b, err)
	}
}

// oneComponent is a module with one Component, probe, that needs nothing
// but the standard library.
func oneComponent(t *testing.T) string {
	return tree(t, map[string]string{
		"go.mod":                          "module example.com/probe\n\ngo 1.26\n",
		"components/probe/component.yaml": "name: probe\nrequires: []\n",
		"components/probe/cmd/main.go":    "package main\n\nfunc main() {}\n",
	})
}

// executable opens the ELF binary at path, failing the test on anything else.
func executable(t *testing.T, path string) *elf.File {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// assertStatic fails unless f asks for no dynamic loader, so it runs on
// distroless/static.
func assertStatic(t *testing.T, f *elf.File) {
	t.Helper()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
			t.Errorf("binary has %v: dynamically linked", p.Type)
		}
	}
}

func TestAReleaseForAPlatformHoldsStaticBinariesForIt(t *testing.T) {
	root := oneComponent(t)

	for platform, machine := range map[string]elf.Machine{"linux/amd64": elf.EM_X86_64, "linux/arm64": elf.EM_AARCH64} {
		out := t.TempDir()
		if err := release.Build(t.Context(), release.Options{Root: root, Out: out, Release: "v1.0.0", Platform: platform}); err != nil {
			t.Fatal(err)
		}

		f := executable(t, filepath.Join(out, "probe"))
		if f.Machine != machine {
			t.Errorf("%s: machine %v, want %v", platform, f.Machine, machine)
		}
		assertStatic(t, f)
	}
}

func TestTheCoreBinaryIsStaticAndStampedWithTheRelease(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the image's core binary is a Linux one")
	}
	bin := filepath.Join(t.TempDir(), "ddash")

	if err := release.Core(t.Context(), "../..", bin, "v9.8.7", "linux/"+goruntime.GOARCH); err != nil {
		t.Fatal(err)
	}

	assertStatic(t, executable(t, bin))
	out, err := exec.CommandContext(t.Context(), bin, "version").Output()
	if err != nil || string(out) != "v9.8.7\n" {
		t.Errorf("ddash version: %q, %v; want v9.8.7", out, err)
	}
}

func TestTheCoreBinaryNeedsTheBuiltShell(t *testing.T) {
	root := tree(t, map[string]string{"web/dist/.keep": ""})

	err := release.Core(t.Context(), root, filepath.Join(t.TempDir(), "ddash"), "v1.0.0", "linux/amd64")

	if err == nil || !strings.Contains(err.Error(), "make build") {
		t.Errorf("err = %v, want one saying to run make build", err)
	}
}

func TestAReleaseIsDevOrACanonicalSemverTag(t *testing.T) {
	root := widgetsOnly(t, map[string]string{"index.js": "", "index.css": ""})

	for _, rel := range []string{"dev", "v1.2.3", "v1.2.3-rc.1"} {
		if err := release.Build(t.Context(), release.Options{Root: root, Out: t.TempDir(), Release: rel}); err != nil {
			t.Errorf("Release %q: %v", rel, err)
		}
	}
	// The core compares Releases as semver, and the image is tagged with
	// the Release, which can't carry a "+".
	for _, rel := range []string{"", "v1.2", "1.2.3", "v1.2.3+build.4", "latest"} {
		err := release.Build(t.Context(), release.Options{Root: root, Out: t.TempDir(), Release: rel})
		if err == nil || !strings.Contains(err.Error(), "vMAJOR.MINOR.PATCH") {
			t.Errorf("Release %q: err = %v, want it refused", rel, err)
		}
	}
}
