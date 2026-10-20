package web_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/web"
)

// The shell must be built first (`make build`); `make test` does that.
func TestTheShellAndTheVendorESMAreEmbedded(t *testing.T) {
	shell := web.Shell()
	if shell == nil {
		t.Fatal("no shell embedded: run `make build` before go test")
	}
	index, err := fs.ReadFile(shell, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), `"react":"/vendor/`+core.ReleasePlaceholder+`/react.js"`) {
		t.Errorf("index.html has no import map on the Release placeholder:\n%s", index)
	}
	for _, f := range []string{"react.js", "react-jsx-runtime.js", "react-dom.js", "react-dom-client.js", "react-router.js", "ddash-ui.js", "ddash-plugin.js"} {
		if _, err := fs.Stat(shell, "vendor/"+f); err != nil {
			t.Errorf("vendor/%s: %v", f, err)
		}
	}
}
