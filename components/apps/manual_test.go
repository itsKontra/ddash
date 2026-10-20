package apps_test

import (
	"regexp"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

func (s started) create(t *testing.T, m *appspb.ManualApp) *appspb.App {
	t.Helper()
	resp, err := s.c.CreateApp(t.Context(), &appspb.CreateAppRequest{App: m})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return resp.GetApp()
}

func TestAManualAppGetsAGeneratedIdAndIsListed(t *testing.T) {
	s := start(t, newNginx(grafana))

	created := s.create(t, &appspb.ManualApp{Name: "pfSense", Url: "https://198.18.0.1", Icon: "pfsense", Group: "Network"})

	if !regexp.MustCompile(`^manual:[0-9a-f]{16}$`).MatchString(created.GetId()) {
		t.Errorf("id = %q, want manual:<16 hex>", created.GetId())
	}
	want := []*appspb.App{
		{Id: "proxy:grafana.home.example", ServerName: "grafana.home.example", Name: "Grafana", Url: "https://grafana.home.example"},
		{Id: created.GetId(), Name: "pfSense", Url: "https://198.18.0.1", Icon: "pfsense", Group: "Network", Manual: true},
	}
	if d := diff(want, s.apps(t)); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}
	if other := s.create(t, &appspb.ManualApp{Name: "pfSense", Url: "https://198.18.0.1"}); other.GetId() == created.GetId() {
		t.Error("two manual Apps got the same id")
	}
}

func TestAManualAppIsEditedAndDeleted(t *testing.T) {
	s := start(t, newNginx())
	created := s.create(t, &appspb.ManualApp{Name: "Proxmox", Url: "https://198.18.0.2:8006"})

	resp, err := s.c.UpdateApp(t.Context(), &appspb.UpdateAppRequest{Id: created.GetId(), App: &appspb.ManualApp{
		Name: "Proxmox VE", Url: "https://pve.home.example:8006", Icon: "proxmox", Group: "Compute",
	}})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	want := &appspb.App{Id: created.GetId(), Name: "Proxmox VE", Url: "https://pve.home.example:8006", Icon: "proxmox", Group: "Compute", Manual: true}
	if d := diff(want, resp.GetApp()); d != "" {
		t.Errorf("UpdateApp answer (-want +got):\n%s", d)
	}
	if d := diff([]*appspb.App{want}, s.apps(t)); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}

	if _, err := s.c.DeleteApp(t.Context(), &appspb.DeleteAppRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if got := s.apps(t); len(got) != 0 {
		t.Errorf("Apps after delete = %v, want none", got)
	}
	if _, err := s.c.DeleteApp(t.Context(), &appspb.DeleteAppRequest{Id: created.GetId()}); codeOf(err) != codes.NotFound {
		t.Errorf("deleting it again = %v, want NotFound", err)
	}
	if _, err := s.c.UpdateApp(t.Context(), &appspb.UpdateAppRequest{Id: created.GetId(), App: &appspb.ManualApp{Name: "x", Url: "https://x.home.example"}}); codeOf(err) != codes.NotFound {
		t.Errorf("updating a deleted App = %v, want NotFound", err)
	}
}

func TestADiscoveredAppIsNoManualApp(t *testing.T) {
	s := start(t, newNginx(grafana))

	if _, err := s.c.DeleteApp(t.Context(), &appspb.DeleteAppRequest{Id: "proxy:grafana.home.example"}); codeOf(err) != codes.NotFound {
		t.Errorf("DeleteApp(a discovered App) = %v, want NotFound", err)
	}
}

func TestAManualAppNeedsANameAndAnHttpURL(t *testing.T) {
	s := start(t, newNginx())

	for _, m := range []*appspb.ManualApp{
		{Url: "https://198.18.0.1"},
		{Name: "pfSense"},
		{Name: "pfSense", Url: "198.18.0.1"},
		{Name: "pfSense", Url: "ftp://198.18.0.1"},
		{Name: "pfSense", Url: "https://"},
		{Name: "pfSense", Url: "https://198.18.0.1", Icon: "Not An Icon!"},
	} {
		if _, err := s.c.CreateApp(t.Context(), &appspb.CreateAppRequest{App: m}); codeOf(err) != codes.InvalidArgument {
			t.Errorf("CreateApp(%v) = %v, want InvalidArgument", m, err)
		}
	}
	if got := s.apps(t); len(got) != 0 {
		t.Errorf("Apps = %v, want none", got)
	}
}

func TestOverlaysAndManualAppsSurviveARestart(t *testing.T) {
	n := newNginx(grafana)
	first := start(t, n)
	first.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Group: "Monitoring"})
	created := first.create(t, &appspb.ManualApp{Name: "AdGuard", Url: "http://198.18.0.3:3000"})
	first.h.Stop()

	s := start(t, n, sdktest.WithDB(first.h.DBPath))
	if got := s.app(t, "proxy:grafana.home.example"); got.GetGroup() != "Monitoring" {
		t.Errorf("grafana = %v, want its group kept", got)
	}
	if got := s.app(t, created.GetId()); got.GetName() != "AdGuard" {
		t.Errorf("manual App = %v, want it kept", got)
	}
}
