package core_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/auth/oidctest"
	"github.com/itsKontra/ddash/core/coretest"
)

// iconNodes decodes a GET /api/icons response.
func iconNodes(t *testing.T, res *httptest.ResponseRecorder) map[string][][]any {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	var got map[string][][]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// lucide 1.59.0's elements, from lucide-static's icon-nodes.json.
var (
	plusNodes   = [][]any{{"path", map[string]any{"d": "M5 12h14"}}, {"path", map[string]any{"d": "M12 5v14"}}}
	puzzleNodes = [][]any{{"path", map[string]any{"d": "M15.39 4.39a1 1 0 0 0 1.68-.474 2.5 2.5 0 1 1 3.014 3.015 1 1 0 0 0-.474 1.68l1.683 1.682a2.414 2.414 0 0 1 0 3.414L19.61 15.39a1 1 0 0 1-1.68-.474 2.5 2.5 0 1 0-3.014 3.015 1 1 0 0 1 .474 1.68l-1.683 1.682a2.414 2.414 0 0 1-3.414 0L8.61 19.61a1 1 0 0 0-1.68.474 2.5 2.5 0 1 1-3.014-3.015 1 1 0 0 0 .474-1.68l-1.683-1.682a2.414 2.414 0 0 1 0-3.414L4.39 8.61a1 1 0 0 1 1.68.474 2.5 2.5 0 1 0 3.014-3.015 1 1 0 0 1-.474-1.68l1.683-1.682a2.414 2.414 0 0 1 3.414 0z"}}}
)

func TestTheCoreServesTheNamedIconsLucideElementsImmutablyOnItsRelease(t *testing.T) {
	h := shelf(t)

	res := h.Get("/api/icons?names=plus,minus&release=v1.2.3")

	got := iconNodes(t, res)
	if diff := cmp.Diff(plusNodes, got["plus"]); diff != "" {
		t.Errorf("plus (-want +got):\n%s", diff)
	}
	if len(got) != 2 || len(got["minus"]) != 1 {
		t.Errorf("got icons %v, want plus and minus", got)
	}
	if got := res.Header().Get("Cache-Control"); got != immutable {
		t.Errorf("Cache-Control %q, want %q", got, immutable)
	}
}

func TestAnUnknownIconNameIsServedAsThePuzzle(t *testing.T) {
	h := shelf(t)

	got := iconNodes(t, h.Get("/api/icons?names=no-such-icon&release=v1.2.3"))

	if diff := cmp.Diff(puzzleNodes, got["no-such-icon"]); diff != "" {
		t.Errorf("no-such-icon (-want +got):\n%s", diff)
	}
}

func TestIconsForAnotherOrTheDevReleaseAreNotCached(t *testing.T) {
	for _, tc := range []struct{ running, asked string }{
		{"v1.2.3", "v1.2.2"},
		{"v1.2.3", ""},
		{"dev", "dev"},
	} {
		h := shelfOf(t, tc.running)

		res := h.Get("/api/icons?names=plus&release=" + tc.asked)

		iconNodes(t, res)
		if got := res.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("running %s, asked %q: Cache-Control %q, want no-cache", tc.running, tc.asked, got)
		}
	}
}

func TestTheIconsNeedNoSignIn(t *testing.T) {
	h := coretest.New(t, coretest.Options{Keycloak: oidctest.New(t), Release: "v1.2.3"})

	got := iconNodes(t, h.Get("/api/icons?names=plus&release=v1.2.3"))

	if diff := cmp.Diff(plusNodes, got["plus"]); diff != "" {
		t.Errorf("plus (-want +got):\n%s", diff)
	}
}
