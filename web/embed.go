// Package web embeds the browser shell and the vendor ESM into the core
// binary. `make build` builds them with Vite (package @ddash/shell, in this
// directory) into dist/shell:
//
//	index.html     the shell, its import map on core.ReleasePlaceholder
//	assets/*       the shell's own content-hashed JS and CSS
//	vendor/*.js    the import map's ESM singletons (react.js, ddash-plugin.js, ...)
//
// dist/.keep keeps the embed pattern matching before the first build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Shell returns the built shell, or nil when this binary was built
// without it.
func Shell() fs.FS {
	shell, err := fs.Sub(dist, "dist/shell")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(shell, "index.html"); err != nil {
		return nil
	}
	return shell
}
