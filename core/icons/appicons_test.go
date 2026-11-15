package icons_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/itsKontra/ddash/core/icons"
)

// upstream is a fake dashboard-icons CDN holding files by path, recording
// what was asked.
type upstream struct {
	*httptest.Server
	mu    sync.Mutex
	asked []string
}

func newUpstream(t *testing.T, files map[string]string) *upstream {
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.asked = append(u.asked, r.URL.Path)
		u.mu.Unlock()
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(u.Close)
	return u
}

func TestAnAppIconIsDownloadedOnceAndKept(t *testing.T) {
	up := newUpstream(t, map[string]string{"/svg/jellyfin.svg": "<svg/>"})
	a := &icons.AppIcons{Dir: t.TempDir(), URL: up.URL}

	for range 2 {
		got, err := a.Get(context.Background(), "jellyfin")
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(got.Path); string(b) != "<svg/>" || got.ContentType != "image/svg+xml" {
			t.Errorf("got %q as %s", b, got.ContentType)
		}
	}
	if want := []string{"/svg/jellyfin.svg"}; !slices.Equal(up.asked, want) {
		t.Errorf("asked upstream %v, want %v", up.asked, want)
	}

	// Kept on disk: it still draws when upstream is gone.
	up.Close()
	again := &icons.AppIcons{Dir: a.Dir, URL: up.URL}
	if _, err := again.Get(context.Background(), "jellyfin"); err != nil {
		t.Errorf("without upstream: %v", err)
	}
}

func TestAnAppIconWithoutAnSVGIsItsPNG(t *testing.T) {
	up := newUpstream(t, map[string]string{"/png/jellyfin.png": "png"})
	a := &icons.AppIcons{Dir: t.TempDir(), URL: up.URL}

	got, err := a.Get(context.Background(), "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != "image/png" {
		t.Errorf("Content-Type %s, want image/png", got.ContentType)
	}
}

func TestANameDashboardIconsLacksNeverReachesUpstreamOrTheDisk(t *testing.T) {
	up := newUpstream(t, nil)
	a := &icons.AppIcons{Dir: t.TempDir(), URL: up.URL}

	for _, name := range []string{"no-such-app-icon", "../../etc/passwd", ""} {
		if _, err := a.Get(context.Background(), name); !errors.Is(err, icons.ErrNoAppIcon) {
			t.Errorf("%q: %v, want ErrNoAppIcon", name, err)
		}
	}
	if len(up.asked) != 0 {
		t.Errorf("asked upstream %v", up.asked)
	}
}

func TestAnUpstreamThatFailsIsAnErrorAndRetried(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write([]byte("<svg/>"))
	}))
	defer srv.Close()
	a := &icons.AppIcons{Dir: t.TempDir(), URL: srv.URL}

	if _, err := a.Get(context.Background(), "jellyfin"); err == nil || errors.Is(err, icons.ErrNoAppIcon) {
		t.Fatalf("upstream down: %v, want an error that isn't ErrNoAppIcon", err)
	}
	fail = false
	if _, err := a.Get(context.Background(), "jellyfin"); err != nil {
		t.Errorf("once upstream is back: %v", err)
	}
}
