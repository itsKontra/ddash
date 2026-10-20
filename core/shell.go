package core

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"
)

// Plugin bundles are served at bundlePrefix<name>/<release>/ from the
// Release's <name>.plugin.js and <name>.plugin.css.
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
	var name string
	switch file {
	case bundleScript:
		name = plugin + ".plugin.js"
	case bundleStyle:
		name = plugin + ".plugin.css"
	}
	if _, installed := h.runtime.PluginRequires(plugin); !installed || name == "" || rel != h.runtime.Release() {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, h.release, name, versioned(rel))
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
	serveContent(w, r, "index.html", b, cacheNone)
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
