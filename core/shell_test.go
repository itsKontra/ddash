package core_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/auth/oidctest"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakegithub"
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
	h := coretest.New(t, shelfOptions(release))
	h.Install("docker")
	return h
}

// shelfOptions are shelf's Options, of Release release.
func shelfOptions(release string) coretest.Options {
	return coretest.Options{
		Release:    release,
		Shell:      builtShell,
		Components: []sdk.Definition{sample.Define("docker-api", nil, sample.Hooks{}), sample.Define("checks", nil, sample.Hooks{})},
		Plugins: []coretest.Plugin{
			{Name: "docker", Title: "Docker", Description: "Containers.", Icon: "container", Author: "itsKontra", Requires: []string{"docker-api"}},
			{Name: "checks", Title: "Checks", Requires: []string{"checks"}},
		},
		ReleaseFiles: map[string]string{
			"docker/index.js":  "export default {}",
			"docker/index.css": ".docker{}",
			"docker/logo.svg":  "<svg/>",
			"checks/index.js":  "export default {}",
		},
	}
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

// Vite writes a stylesheet of only a marker comment for a Plugin without
// CSS; the shell would link it for nothing.
func TestAPluginWhoseStylesheetHoldsNoRuleIsListedWithoutOne(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Release:    "v1.2.3",
		Shell:      builtShell,
		Components: []sdk.Definition{sample.Define("docker-api", nil, sample.Hooks{}), sample.Define("checks", nil, sample.Hooks{}), sample.Define("calendar-api", nil, sample.Hooks{})},
		Plugins: []coretest.Plugin{
			{Name: "docker", Requires: []string{"docker-api"}},
			{Name: "checks", Requires: []string{"checks"}},
			{Name: "calendar", Requires: []string{"calendar-api"}},
		},
		ReleaseFiles: map[string]string{
			"docker/index.js":   "export default {}",
			"docker/index.css":  "/* a comment */\n@keyframes docker-spin{}",
			"checks/index.js":   "export default {}",
			"checks/index.css":  "\n/*$vite$:1*/",
			"calendar/index.js": "export default {}", // no stylesheet at all
		},
	})
	for _, p := range []string{"docker", "checks", "calendar"} {
		h.Install(p)
	}

	res := h.CoreCall("ListInstalledPlugins", `{}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var got struct {
		Plugins []struct{ Name, Style string }
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	styles := map[string]string{}
	for _, p := range got.Plugins {
		styles[p.Name] = p.Style
	}
	want := map[string]string{"docker": "/plugins/docker/v1.2.3/index.css", "checks": "", "calendar": ""}
	if diff := cmp.Diff(want, styles); diff != "" {
		t.Errorf("styles (-want +got):\n%s", diff)
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
		{"/plugins/docker/v1.2.3/logo.svg", "image/svg+xml", "<svg/>"}, // any file in its .ddapp
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
		"/plugins/docker.ddapp",
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

// The Catalogue's shell route /plugins shares its path with the bundles'
// /plugins/: a direct load or a reload of it still gets the shell, in a
// Release from a dir (DDASH_RELEASE_DIR) and one downloaded from GitHub,
// and what is under /plugins/ is still only bundles.
func TestTheCataloguesRouteIsServedTheShellThoughBundlesShareItsPath(t *testing.T) {
	gh := fakegithub.New(repo)
	gh.Publish("v1.2.3", coretest.ReleaseDir(t, shelfOptions("v1.2.3")))
	data := t.TempDir()
	downloaded := shelfOptions("v1.2.3")
	downloaded.DataDir = data
	downloaded.Source = runtime.GitHub(runtime.GitHubOptions{Repo: repo, Release: "v1.2.3", DataDir: data, Client: gh.Client()})

	for mode, opts := range map[string]coretest.Options{"release dir": shelfOptions("v1.2.3"), "downloaded": downloaded} {
		t.Run(mode, func(t *testing.T) {
			h := coretest.New(t, opts)
			h.Install("docker")

			for _, path := range []string{"/plugins", "/plugins/"} {
				res := h.Get(path)

				want := `<script type="importmap">{"imports":{"react":"/vendor/v1.2.3/react.js"}}</script>`
				if res.Code != http.StatusOK || res.Body.String() != want {
					t.Errorf("GET %s: %d %q (Location %q), want 200 %q", path, res.Code, res.Body, res.Header().Get("Location"), want)
				}
			}
			if res := h.Get("/plugins/docker/v1.2.3/index.js"); res.Code != http.StatusOK || res.Header().Get("Cache-Control") != immutable {
				t.Errorf("GET the bundle: %d with Cache-Control %q, want 200 %q", res.Code, res.Header().Get("Cache-Control"), immutable)
			}
			for _, path := range []string{"/plugins/docker/v1.2.3/nope.js", "/plugins/docker", "/plugins/docker/"} {
				if res := h.Get(path); res.Code != http.StatusNotFound {
					t.Errorf("GET %s: %d, want 404", path, res.Code)
				}
			}
		})
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

func TestAStaticFileAskedForAgainWithItsETagIsAnswered304(t *testing.T) {
	for _, rel := range []string{"v1.2.3", "dev"} {
		h := shelfOf(t, rel)

		for _, path := range []string{"/", "/plugins/docker/" + rel + "/index.js", "/vendor/" + rel + "/react.js", "/assets/index-4f2a.js"} {
			first := h.Get(path)
			etag := first.Header().Get("ETag")
			if first.Code != http.StatusOK || etag == "" {
				t.Fatalf("GET %s: %d with ETag %q, want 200 with one", path, first.Code, etag)
			}

			again := httptest.NewRequest(http.MethodGet, path, nil)
			again.Header.Set("If-None-Match", etag)
			res := h.Do(again)

			if res.Code != http.StatusNotModified || res.Body.Len() != 0 {
				t.Errorf("GET %s with its ETag: %d %q, want 304 and no body", path, res.Code, res.Body)
			}
			if got := res.Header().Get("ETag"); got != etag {
				t.Errorf("GET %s with its ETag: ETag %q, want %q", path, got, etag)
			}
		}
	}
}

func TestAStaticFileAskedForWithAnotherFilesETagIsSentWhole(t *testing.T) {
	h := shelf(t)
	js, css := h.Get("/plugins/docker/v1.2.3/index.js"), h.Get("/plugins/docker/v1.2.3/index.css")
	if a, b := js.Header().Get("ETag"), css.Header().Get("ETag"); a == b {
		t.Fatalf("index.js and index.css share the ETag %q, want one each", a)
	}

	r := httptest.NewRequest(http.MethodGet, "/plugins/docker/v1.2.3/index.js", nil)
	r.Header.Set("If-None-Match", css.Header().Get("ETag"))
	res := h.Do(r)

	if res.Code != http.StatusOK || res.Body.String() != "export default {}" {
		t.Errorf("GET with another file's ETag: %d %q, want 200 with the file", res.Code, res.Body)
	}
}

// ddash's UI runs Actions, so no other site may frame it, and no response
// is to be sniffed into another type.
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	h := shelf(t)

	for _, path := range []string{"/", "/healthz", "/api/icons?names=plus", "/assets/nope.js", "/api/core/nope"} {
		res := h.Get(path)
		for name, want := range map[string]string{
			"X-Content-Type-Options":    "nosniff",
			"X-Frame-Options":           "DENY",
			"Referrer-Policy":           "same-origin",
			"Strict-Transport-Security": "max-age=31536000",
		} {
			if got := res.Header().Get(name); got != want {
				t.Errorf("GET %s: %s = %q, want %q", path, name, got, want)
			}
		}
		if got := policy(res)["frame-ancestors"]; got != "'none'" {
			t.Errorf("GET %s: frame-ancestors %q, want 'none'", path, got)
		}
	}
}

// policy is res's Content-Security-Policy, by directive.
func policy(res *httptest.ResponseRecorder) map[string]string {
	directives := map[string]string{}
	for d := range strings.SplitSeq(res.Header().Get("Content-Security-Policy"), ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(d), " ")
		directives[name] = value
	}
	return directives
}

// hashSource is the CSP source that allows the inline script script.
func hashSource(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// An injected script would run with the Operator's session: the shell's
// page loads only ddash's own files, and runs no inline script but its
// import map.
func TestTheShellsPolicyAllowsItsOwnFilesAndOfInlineScriptsOnlyTheImportMap(t *testing.T) {
	importMap := `{"imports":{"react":"/vendor/v1.2.3/react.js"}}`
	injected := `fetch("/api/ddash.core.CoreService/Uninstall")`
	h := coretest.New(t, coretest.Options{
		Release: "v1.2.3",
		Shell: fstest.MapFS{"index.html": {Data: []byte(`<script type="importmap">{"imports":{"react":"/vendor/__DDASH_RELEASE__/react.js"}}</script>` +
			`<script>` + injected + `</script>`)}},
	})

	for _, path := range []string{"/", "/docker/hosts/media"} {
		got := policy(h.Get(path))

		want := map[string]string{
			"default-src":     "'none'",
			"script-src":      "'self' " + hashSource(importMap),
			"style-src":       "'self'",
			"img-src":         "'self' http: https:", // each App's favicon
			"connect-src":     "'self'",
			"form-action":     "'self'",
			"base-uri":        "'none'",
			"frame-ancestors": "'none'",
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET %s: Content-Security-Policy (-want +got):\n%s", path, diff)
		}
		if strings.Contains(got["script-src"], hashSource(injected)) {
			t.Errorf("GET %s: script-src %q allows the injected script", path, got["script-src"])
		}
	}
}

// Sign out posts a form that redirects to Keycloak's end-session endpoint,
// and the browser holds that redirect to form-action.
func TestTheShellsPolicyLetsSignOutGoOnToKeycloak(t *testing.T) {
	h := coretest.New(t, coretest.Options{Shell: builtShell, Keycloak: oidctest.New(t)})
	h.SignIn()

	res := h.Get("/")

	if res.Code != http.StatusOK {
		t.Fatalf("GET /: %d", res.Code)
	}
	if got, want := policy(res)["form-action"], "'self' https://keycloak.home.example"; got != want {
		t.Errorf("form-action %q, want %q", got, want)
	}
}
