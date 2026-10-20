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
