package core_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

// iconSearch decodes a GET /api/icons/search response.
func iconSearch(t *testing.T, h *coretest.Harness, q string) []string {
	t.Helper()
	return searchAt(t, h, "/api/icons/search", q)
}

// appIconSearch decodes a GET /api/icons/apps/search response.
func appIconSearch(t *testing.T, h *coretest.Harness, q string) []string {
	t.Helper()
	return searchAt(t, h, "/api/icons/apps/search", q)
}

// searchAt decodes a GET path?q= response.
func searchAt(t *testing.T, h *coretest.Harness, path, q string) []string {
	t.Helper()
	res := h.Get(path + "?q=" + q)
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var got []string
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestIconSearchRanksTheExactNameThenNamePrefixesThenOtherNamesThenTags(t *testing.T) {
	got := iconSearch(t, shelf(t), "House")

	// lucide 1.59.0's names holding "house", ranked.
	want := []string{
		"house",
		"house-cog", "house-heart", "house-plug", "house-plus", "house-wifi", "houses",
		"birdhouse", "lighthouse", "map-pin-house", "shield-house", "warehouse",
	}
	if len(got) < len(want) {
		t.Fatalf("got %v, want at least %v", got, want)
	}
	if diff := cmp.Diff(want, got[:len(want)]); diff != "" {
		t.Errorf("names first (-want +got):\n%s", diff)
	}
	// Icons with a tag word starting with house come after (not tower-control, tagged lighthouse).
	tagged := got[len(want):]
	for _, n := range []string{"blinds", "lamp", "mop"} {
		if !slices.Contains(tagged, n) {
			t.Errorf("tagged %q missing from %v", n, tagged)
		}
	}
}

func TestIconSearchFindsIconsByTagAlone(t *testing.T) {
	got := iconSearch(t, shelf(t), "home")

	for _, n := range []string{"house", "school", "dock"} {
		if !slices.Contains(got, n) {
			t.Errorf("%q missing from %v", n, got)
		}
	}
}

func TestIconSearchWithNoMatchOrNoQueryIsEmpty(t *testing.T) {
	h := shelf(t)

	for _, q := range []string{"zzqx", "", "%20"} {
		if got := iconSearch(t, h, q); got == nil || len(got) != 0 {
			t.Errorf("q %q: got %#v, want []", q, got)
		}
	}
}

func TestIconSearchNeedsNoSignIn(t *testing.T) {
	h := coretest.New(t, coretest.Options{Keycloak: oidctest.New(t), Release: "v1.2.3"})

	if got := iconSearch(t, h, "database"); len(got) == 0 || got[0] != "database" {
		t.Errorf("got %v, want database first", got)
	}
}

func TestAppIconSearchFindsDashboardIconsNamesNotLucides(t *testing.T) {
	h := shelf(t)

	if got := appIconSearch(t, h, "Jellyfin"); len(got) == 0 || got[0] != "jellyfin" {
		t.Errorf("got %v, want jellyfin first", got)
	}
	if got := appIconSearch(t, h, "house"); slices.Contains(got, "house") {
		t.Errorf("got lucide's house in %v", got)
	}
}

func TestAppIconSearchMatchesSpacesToDashes(t *testing.T) {
	if got := appIconSearch(t, shelf(t), "home%20assistant"); len(got) == 0 || got[0] != "home-assistant" {
		t.Errorf("got %v, want home-assistant first", got)
	}
}

func TestAppIconSearchFindsIconsByAliasAlone(t *testing.T) {
	// proxmox's alias is "Virtualization Platform".
	if got := appIconSearch(t, shelf(t), "virtualization"); !slices.Contains(got, "proxmox") {
		t.Errorf("proxmox missing from %v", got)
	}
}

func TestAppIconSearchWithNoMatchOrNoQueryIsEmpty(t *testing.T) {
	h := shelf(t)

	for _, q := range []string{"zzqx", "", "%20"} {
		if got := appIconSearch(t, h, q); got == nil || len(got) != 0 {
			t.Errorf("q %q: got %#v, want []", q, got)
		}
	}
}

func TestAppIconSearchNeedsNoSignIn(t *testing.T) {
	h := coretest.New(t, coretest.Options{Keycloak: oidctest.New(t), Release: "v1.2.3"})

	if got := appIconSearch(t, h, "jellyfin"); len(got) == 0 || got[0] != "jellyfin" {
		t.Errorf("got %v, want jellyfin first", got)
	}
}
