package web_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/coretest"
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

// The core allows the shell's import map by its hash: a change to how the
// build writes index.html must not leave an inline script or style the
// policy refuses.
func TestTheBuiltShellsInlineScriptsAreAllAllowedByItsPolicy(t *testing.T) {
	h := coretest.New(t, coretest.Options{Release: "v1.2.3", Shell: web.Shell()})

	res := h.Get("/")

	var scriptSrc string
	for d := range strings.SplitSeq(res.Header().Get("Content-Security-Policy"), ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(d), "script-src "); ok {
			scriptSrc = v
		}
	}
	page := res.Body.String()
	inline := 0
	for _, m := range regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`).FindAllStringSubmatch(page, -1) {
		if strings.Contains(m[1], "src=") {
			continue
		}
		inline++
		sum := sha256.Sum256([]byte(m[2]))
		if hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !strings.Contains(scriptSrc, hash) {
			t.Errorf("script-src %q refuses the inline script <script%s>%.60s", scriptSrc, m[1], m[2])
		}
	}
	if inline != 1 {
		t.Errorf("%d inline scripts in the shell, want 1, the import map", inline)
	}
	if strings.Contains(page, "<style") || strings.Contains(page, " style=") {
		t.Errorf("the shell has an inline style, which style-src refuses:\n%s", page)
	}
}
