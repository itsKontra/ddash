package release_test

import (
	"os"
	"path/filepath"
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
