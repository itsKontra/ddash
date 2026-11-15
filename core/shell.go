package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/itsKontra/ddash/core/icons"
)

// Plugin bundles are served at bundlePrefix<name>/<release>/<file> from
// the files in the Release's <name>.ddapp, whose entry points are index.js
// and index.css.
const (
	bundlePrefix = "/plugins/"
	bundleScript = "index.js"
	bundleStyle  = "index.css"
)

// Cache-Control values: Release-versioned paths never change, except in
// the dev Release, which `make dev` rebuilds in place.
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNone      = "no-cache"
	devRelease     = "dev"
)

// bundle serves GET /plugins/{plugin}/{release}/{file}: an installed
// Plugin's bundle of the running Release.
func (h *Host) bundle(w http.ResponseWriter, r *http.Request) {
	plugin, rel, file := r.PathValue("plugin"), r.PathValue("release"), r.PathValue("file")
	if _, installed := h.runtime.PluginRequires(plugin); !installed || rel != h.runtime.Release() {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, h.release, path.Join(plugin, file), versioned(rel))
}

// bundleStyleURL is the URL of the plugin's stylesheet under base, its
// bundle's URL prefix, or empty when the stylesheet holds no rule: Vite
// writes one of only a marker comment for a Plugin without CSS, and the
// shell links no empty stylesheet.
func (h *Host) bundleStyleURL(plugin, base string) string {
	b, err := fs.ReadFile(h.release, path.Join(plugin, bundleStyle))
	if err != nil || cssEmpty(b) {
		return ""
	}
	return base + bundleStyle
}

// cssEmpty reports whether css holds only whitespace and comments.
func cssEmpty(css []byte) bool {
	for {
		css = bytes.TrimLeft(css, " \t\n\r\f")
		if len(css) == 0 {
			return true
		}
		rest, ok := bytes.CutPrefix(css, []byte("/*"))
		if !ok {
			return false
		}
		_, css, ok = bytes.Cut(rest, []byte("*/"))
		if !ok {
			return true // an unclosed comment runs to the end
		}
	}
}

// ReleasePlaceholder stands for the running Release in the shell's
// index.html, so its static import map points at /vendor/<release>/.
const ReleasePlaceholder = "__DDASH_RELEASE__"

// vendor serves GET /vendor/{release}/{file}: the import map's ESM
// singletons, embedded in the core with the shell.
func (h *Host) vendor(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("release")
	if h.shell == nil || rel != h.runtime.Release() {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, h.shell, path.Join("vendor", r.PathValue("file")), versioned(rel))
}

// assets serves GET /assets/{file}: the shell's own content-hashed files.
func (h *Host) assets(w http.ResponseWriter, r *http.Request) {
	if h.shell == nil {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, h.shell, path.Join("assets", r.PathValue("file")), cacheImmutable)
}

// index serves the shell's index.html for every other GET, so the shell
// routes /<plugin-name>/* itself.
func (h *Host) index(w http.ResponseWriter, r *http.Request) {
	if h.shell == nil {
		http.Error(w, "this core was built without the shell: run `make build`", http.StatusNotFound)
		return
	}
	b, err := fs.ReadFile(h.shell, "index.html")
	if err != nil {
		http.Error(w, "the shell has no index.html", http.StatusInternalServerError)
		return
	}
	b = bytes.ReplaceAll(b, []byte(ReleasePlaceholder), []byte(h.runtime.Release()))
	w.Header().Set("Content-Security-Policy", shellPolicy(importMapHash(b), h.signInOrigin))
	serveContent(w, r, "index.html", b, cacheNone)
}

// shellPolicy is the shell page's Content-Security-Policy: the shell and
// Plugins load scripts, stylesheets and data from ddash itself only, and
// no inline script runs but the import map, allowed by its hash. React
// sets style props through the CSSOM, which style-src doesn't govern, so
// no inline style is allowed either. Images may come from any http(s)
// site, for each App's own favicon at its URL. A form may post to ddash
// and, when it signs in with Keycloak, to signInOrigin: Sign out is a
// form whose answer redirects to Keycloak's end-session endpoint, and a
// browser holds a form's redirects to form-action too.
func shellPolicy(importMap, signInOrigin string) string {
	script := "script-src 'self'"
	if importMap != "" {
		script += " " + importMap
	}
	form := "form-action 'self'"
	if signInOrigin != "" {
		form += " " + signInOrigin
	}
	return strings.Join([]string{
		"default-src 'none'",
		script,
		"style-src 'self'",
		"img-src 'self' http: https:",
		"connect-src 'self'",
		form,
		"base-uri 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

// importMapTag opens the shell's inline import map in index.html.
const importMapTag = `<script type="importmap">`

// importMapHash is the CSP hash source of the import map in html, or empty
// when it has none.
func importMapHash(html []byte) string {
	_, rest, ok := bytes.Cut(html, []byte(importMapTag))
	if !ok {
		return ""
	}
	importMap, _, ok := bytes.Cut(rest, []byte("</script>"))
	if !ok {
		return ""
	}
	sum := sha256.Sum256(importMap)
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// origin is rawURL's scheme://host, or empty when it has none.
func origin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// iconsPath serves lucide icons: GET iconsPath?names=a,b&release=<release>.
const iconsPath = "/api/icons"

// icons serves GET /api/icons: each named icon's lucide elements, by name,
// the puzzle's for an unknown name. The URL carries the Release, so the
// running Release's icons are immutable.
func (h *Host) icons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cache := cacheNone
	if rel := q.Get("release"); rel == h.runtime.Release() {
		cache = versioned(rel)
	}
	writeJSON(w, icons.Nodes(strings.Split(q.Get("names"), ",")), cache)
}

// iconSearchPath searches the lucide icons: GET iconSearchPath?q=.
const iconSearchPath = iconsPath + "/search"

// iconSearch serves GET /api/icons/search: the names of the icons q
// matches by name or tag, best first (icons.Search).
func (h *Host) iconSearch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, icons.Search(r.URL.Query().Get("q")), cacheNone)
}

// appIconSearchPath searches the dashboard-icons names an App's icon may
// take: GET appIconSearchPath?q=.
const appIconSearchPath = iconsPath + "/apps/search"

// appIconSearch serves GET /api/icons/apps/search: the dashboard-icons names
// q matches by name or alias, best first (icons.AppSearch).
func (h *Host) appIconSearch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, icons.AppSearch(r.URL.Query().Get("q")), cacheNone)
}

// appIconPath serves the dashboard-icons logos: GET appIconPath<name>?v=<commit>.
const appIconPath = iconsPath + "/apps/logo/"

// appIcon serves GET /api/icons/apps/logo/{name}: the dashboard-icons logo
// of that name, downloaded once and kept (icons.AppIcons), so the browser
// asks no CDN. The URL carries the pinned commit, so a logo is immutable.
// Behind sign-in, since asking can make the core download.
func (h *Host) appIcon(w http.ResponseWriter, r *http.Request) {
	cache := cacheNone
	if r.URL.Query().Get("v") == icons.DashboardIconsCommit {
		cache = cacheImmutable
	}
	icon, err := h.appIcons.Get(r.Context(), r.PathValue("name"))
	switch {
	case errors.Is(err, icons.ErrNoAppIcon):
		// As lasting as a logo: the pinned commit will never have it.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", cache)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("no such App icon\n"))
		return
	case err != nil:
		h.log.Warn("App icon", "name", r.PathValue("name"), "err", err)
		http.Error(w, "the App icon can't be downloaded right now", http.StatusBadGateway)
		return
	}
	b, err := os.ReadFile(icon.Path)
	if err != nil {
		http.Error(w, "the App icon can't be read", http.StatusInternalServerError)
		return
	}
	// A logo is someone else's file served from ddash's origin: opened as
	// a page, an SVG may run nothing and load nothing.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", icon.ContentType)
	w.Header().Set("Cache-Control", cache)
	w.Write(b)
}

// writeJSON writes v as a JSON response with the given Cache-Control.
func writeJSON(w http.ResponseWriter, v any, cacheControl string) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheControl)
	w.Write(b)
}

// versioned is the Cache-Control of a path versioned by rel.
func versioned(rel string) string {
	if rel == devRelease {
		return cacheNone
	}
	return cacheImmutable
}

// serveFile serves fsys's file name with cacheControl, its Content-Type
// from its extension.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name, cacheControl string) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveContent(w, r, name, b, cacheControl)
}

// contentTypes pins the shell's own types, which a system's mime.types
// could otherwise change.
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
}

func serveContent(w http.ResponseWriter, r *http.Request, name string, b []byte, cacheControl string) {
	ct, ok := contentTypes[path.Ext(name)]
	if !ok {
		ct = mime.TypeByExtension(path.Ext(name))
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, name, time.Time{}, io.ReadSeeker(bytes.NewReader(b)))
}
