package apps_test

import (
	"testing"
	"testing/synctest"

	"google.golang.org/grpc/codes"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

func (s started) setOverlay(t *testing.T, o *appspb.Overlay) {
	t.Helper()
	if _, err := s.c.SetOverlay(t.Context(), &appspb.SetOverlayRequest{Overlay: o}); err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}
}

func (s started) app(t *testing.T, id string) *appspb.App {
	t.Helper()
	for _, a := range s.apps(t) {
		if a.GetId() == id {
			return a
		}
	}
	return nil
}

var (
	jellyfin = proxy([]string{"jellyfin.home.example", "media.home.example"}, "http://198.18.2.10:8096", "443 ssl")
	grafana  = proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl")
)

func TestTheOverlaySetsAnAppsDisplayDetails(t *testing.T) {
	s := start(t, newNginx(jellyfin, grafana))

	o := &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media", Icon: "jellyfin", Group: "Media", Hidden: true}
	s.setOverlay(t, o)

	want := &appspb.App{
		Id: "proxy:jellyfin.home.example", ServerName: "jellyfin.home.example", Url: "https://jellyfin.home.example",
		Aliases: []string{"media.home.example"},
		Name:    "Media", Icon: "jellyfin", Group: "Media", Hidden: true, Overlay: o,
	}
	if d := diff(want, s.app(t, "proxy:jellyfin.home.example")); d != "" {
		t.Errorf("App (-want +got):\n%s", d)
	}
}

func TestAnOverlayWithoutANameKeepsTheDefaultName(t *testing.T) {
	s := start(t, newNginx(grafana))

	s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Group: "Monitoring"})

	if got := s.app(t, "proxy:grafana.home.example"); got.GetName() != "Grafana" || got.GetGroup() != "Monitoring" {
		t.Errorf("App = %v, want Grafana in Monitoring", got)
	}
}

func TestAnEmptyOverlayIsRemoved(t *testing.T) {
	s := start(t, newNginx(grafana))
	s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Group: "Monitoring"})

	s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example"})

	if got := s.app(t, "proxy:grafana.home.example"); got.GetOverlay() != nil || got.GetGroup() != "" {
		t.Errorf("App = %v, want it back on its defaults without an overlay", got)
	}
}

func TestAnOverlayNeedsAnAppOrAnExistingOverlay(t *testing.T) {
	s := start(t, newNginx(grafana))

	for _, o := range []*appspb.Overlay{
		{Name: "No key"},
		{ServerName: "nothing.home.example", Name: "Nothing"},
	} {
		_, err := s.c.SetOverlay(t.Context(), &appspb.SetOverlayRequest{Overlay: o})
		if codeOf(err) != codes.InvalidArgument && codeOf(err) != codes.NotFound {
			t.Errorf("SetOverlay(%v) = %v, want InvalidArgument or NotFound", o, err)
		}
	}
	if got := s.list(t).GetUnmatched(); len(got) != 0 {
		t.Errorf("unmatched = %v, want none", got)
	}
}

func TestAnAppVanishingKeepsItsOverlayForItsReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin, grafana)
		s := start(t, n)
		o := &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media", Group: "Media"}
		s.setOverlay(t, o)

		n.set(grafana)
		afterPoll()
		if got := s.app(t, "proxy:jellyfin.home.example"); got != nil {
			t.Errorf("the vanished Proxy Host's App is still listed: %v", got)
		}
		if d := diff([]*appspb.Overlay{o}, s.list(t).GetUnmatched()); d != "" {
			t.Errorf("unmatched while it's gone (-want +got):\n%s", d)
		}

		n.set(jellyfin, grafana)
		afterPoll()
		if got := s.app(t, "proxy:jellyfin.home.example"); got.GetName() != "Media" || got.GetGroup() != "Media" {
			t.Errorf("returned App = %v, want its overlay re-applied", got)
		}
		if got := s.list(t).GetUnmatched(); len(got) != 0 {
			t.Errorf("unmatched after the return = %v, want none", got)
		}
	})
}

func TestARenameOrphansTheOverlayToReattachOrDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin, grafana)
		s := start(t, n)
		s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media", Icon: "jellyfin"})
		s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Group: "Monitoring"})

		// Both Proxy Hosts are renamed.
		n.set(
			proxy([]string{"tv.home.example", "jellyfin.home.example"}, "http://198.18.2.10:8096", "443 ssl"),
			proxy([]string{"metrics.home.example"}, "http://grafana", "443 ssl"),
		)
		afterPoll()

		if got := s.app(t, "proxy:tv.home.example"); got.GetName() != "Tv" || got.GetOverlay() != nil {
			t.Errorf("renamed App = %v, want it on its defaults", got)
		}
		var keys []string
		for _, o := range s.list(t).GetUnmatched() {
			keys = append(keys, o.GetServerName())
		}
		if d := diff([]string{"grafana.home.example", "jellyfin.home.example"}, keys); d != "" {
			t.Fatalf("unmatched (-want +got):\n%s", d)
		}

		if _, err := s.c.AttachOverlay(t.Context(), &appspb.AttachOverlayRequest{ServerName: "jellyfin.home.example", To: "tv.home.example"}); err != nil {
			t.Fatalf("AttachOverlay: %v", err)
		}
		want := &appspb.Overlay{ServerName: "tv.home.example", Name: "Media", Icon: "jellyfin"}
		if got := s.app(t, "proxy:tv.home.example"); got.GetName() != "Media" || diff(want, got.GetOverlay()) != "" {
			t.Errorf("re-attached App = %v, want the overlay %v", got, want)
		}

		if _, err := s.c.DeleteOverlay(t.Context(), &appspb.DeleteOverlayRequest{ServerName: "grafana.home.example"}); err != nil {
			t.Fatalf("DeleteOverlay: %v", err)
		}
		if got := s.list(t).GetUnmatched(); len(got) != 0 {
			t.Errorf("unmatched after re-attaching and deleting = %v, want none", got)
		}
	})
}

func TestAnOverlayIsReattachedOnlyToAnAppWithoutOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin, grafana)
		s := start(t, n)
		s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media"})
		s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Group: "Monitoring"})
		n.set(grafana)
		afterPoll()

		for _, tc := range []struct {
			req  *appspb.AttachOverlayRequest
			want codes.Code
		}{
			{&appspb.AttachOverlayRequest{ServerName: "jellyfin.home.example", To: "grafana.home.example"}, codes.FailedPrecondition},
			{&appspb.AttachOverlayRequest{ServerName: "jellyfin.home.example", To: "nothing.home.example"}, codes.NotFound},
			{&appspb.AttachOverlayRequest{ServerName: "nothing.home.example", To: "grafana.home.example"}, codes.NotFound},
		} {
			if _, err := s.c.AttachOverlay(t.Context(), tc.req); codeOf(err) != tc.want {
				t.Errorf("AttachOverlay(%v) = %v, want %v", tc.req, err, tc.want)
			}
		}
		if got := s.app(t, "proxy:grafana.home.example"); got.GetGroup() != "Monitoring" || got.GetName() != "Grafana" {
			t.Errorf("grafana = %v, want its own overlay untouched", got)
		}
	})
}

// A Proxy Host is identified by its first server_name only: the overlay
// follows it whatever else changes in the block.
func TestTheOverlayFollowsTheFirstServerName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		s := start(t, n)
		s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media"})

		moved := proxy([]string{"jellyfin.home.example"}, "http://198.18.2.11:8096", "80")
		n.set(moved, &nginxconfigpb.Server{ServerNames: []string{"other.home.example"}})
		afterPoll()

		if got := s.app(t, "proxy:jellyfin.home.example"); got.GetName() != "Media" || got.GetUrl() != "http://jellyfin.home.example" {
			t.Errorf("App = %v, want Media on http", got)
		}
	})
}
