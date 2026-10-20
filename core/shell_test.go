package core_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/sdk"
)

// builtShell is a shell as the web build lays it out.
var builtShell = fstest.MapFS{
	"index.html":           {Data: []byte(`<script type="importmap">{"imports":{"react":"/vendor/__DDASH_RELEASE__/react.js"}}</script>`)},
	"assets/index-4f2a.js": {Data: []byte("shell()")},
	"vendor/react.js":      {Data: []byte("react()")},
}

// shelf is a Release v1.2.3 with two Plugins, each with its bundle, of which
// only docker is installed.
func shelf(t *testing.T) *coretest.Harness {
	return shelfOf(t, "v1.2.3")
}

func shelfOf(t *testing.T, release string) *coretest.Harness {
	h := coretest.New(t, coretest.Options{
		Release:    release,
		Shell:      builtShell,
		Components: []sdk.Definition{sample.Define("docker-api", nil, sample.Hooks{}), sample.Define("checks", nil, sample.Hooks{})},
		Plugins: []coretest.Plugin{
			{Name: "docker", Title: "Docker", Description: "Containers.", Icon: "container", Author: "itsKontra", Requires: []string{"docker-api"}},
			{Name: "checks", Title: "Checks", Requires: []string{"checks"}},
		},
		ReleaseFiles: map[string]string{
			"docker.plugin.js":  "export default {}",
			"docker.plugin.css": ".docker{}",
			"checks.plugin.js":  "export default {}",
		},
	})
	h.Install("docker")
	return h
}

func TestTheCoreListsInstalledPluginsWithTheirManifestsAndBundleURLs(t *testing.T) {
	h := shelf(t)

	res := h.CoreCall("ListInstalledPlugins", `{}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"plugins": []any{map[string]any{
		"name":        "docker",
		"title":       "Docker",
		"description": "Containers.",
		"icon":        "container",
		"author":      "itsKontra",
		"requires":    []any{"docker-api"},
		"script":      "/plugins/docker/v1.2.3/index.js",
		"style":       "/plugins/docker/v1.2.3/index.css",
	}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListInstalledPlugins (-want +got):\n%s", diff)
	}
}

// immutable is how Release-versioned paths are cached.
const immutable = "public, max-age=31536000, immutable"

func TestTheCoreServesAnInstalledPluginsBundleImmutably(t *testing.T) {
	h := shelf(t)

	for _, tc := range []struct {
		path, contentType, body string
	}{
		{"/plugins/docker/v1.2.3/index.js", "text/javascript; charset=utf-8", "export default {}"},
		{"/plugins/docker/v1.2.3/index.css", "text/css; charset=utf-8", ".docker{}"},
	} {
		res := h.Get(tc.path)

		if res.Code != http.StatusOK || res.Body.String() != tc.body {
			t.Fatalf("GET %s: %d %q, want 200 %q", tc.path, res.Code, res.Body, tc.body)
		}
		if got := res.Header().Get("Content-Type"); got != tc.contentType {
			t.Errorf("GET %s: Content-Type %q, want %q", tc.path, got, tc.contentType)
		}
		if got := res.Header().Get("Cache-Control"); got != immutable {
			t.Errorf("GET %s: Cache-Control %q, want %q", tc.path, got, immutable)
		}
	}
}

func TestTheCoreServesNoBundleOfAPluginNotInstalledOrOfAnotherRelease(t *testing.T) {
	h := shelf(t)

	for _, path := range []string{
		"/plugins/checks/v1.2.3/index.js", // not installed
		"/plugins/docker/v1.2.2/index.js", // another Release
		"/plugins/docker/v1.2.3/other.js",
		"/plugins/docker/v1.2.3/",
		"/plugins/nope/v1.2.3/index.js",
		"/plugins/docker.plugin.js",
	} {
		if res := h.Get(path); res.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, res.Code)
		}
	}
}

func TestTheShellIsServedUncachedWithItsImportMapOnTheRunningRelease(t *testing.T) {
	h := shelf(t)

	for _, path := range []string{"/", "/docker", "/docker/hosts/media"} {
		res := h.Get(path)

		want := `<script type="importmap">{"imports":{"react":"/vendor/v1.2.3/react.js"}}</script>`
		if res.Code != http.StatusOK || res.Body.String() != want {
			t.Errorf("GET %s: %d %q, want 200 %q", path, res.Code, res.Body, want)
		}
		if got := res.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("GET %s: Content-Type %q", path, got)
		}
		if got := res.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", path, got)
		}
	}
}

func TestTheShellsAssetsAndTheVendorESMAreImmutable(t *testing.T) {
	h := shelf(t)

	for path, body := range map[string]string{
		"/assets/index-4f2a.js":   "shell()",
		"/vendor/v1.2.3/react.js": "react()",
	} {
		res := h.Get(path)

		if res.Code != http.StatusOK || res.Body.String() != body {
			t.Errorf("GET %s: %d %q, want 200 %q", path, res.Code, res.Body, body)
		}
		if got := res.Header().Get("Cache-Control"); got != immutable {
			t.Errorf("GET %s: Cache-Control %q, want %q", path, got, immutable)
		}
	}
	for _, path := range []string{"/vendor/v1.2.2/react.js", "/vendor/v1.2.3/nope.js", "/assets/nope.js"} {
		if res := h.Get(path); res.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, res.Code)
		}
	}
}

func TestTheDevReleaseIsNeverCached(t *testing.T) {
	h := shelfOf(t, "dev")

	for _, path := range []string{"/vendor/dev/react.js", "/plugins/docker/dev/index.js"} {
		res := h.Get(path)

		if res.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, res.Code)
		}
		if got := res.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", path, got)
		}
	}
}
