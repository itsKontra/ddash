package apps_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// An App the Operator never touched disappears with its sources (its Proxy
// Host and its name in pfSense), its Check included.
func TestAnUntouchedAppDisappearsWithItsProxyHost(t *testing.T) {
	l := newLab(t, labOptions{})
	if l.appChecks()["proxy:grafana.home.example"] == nil {
		t.Fatal("Grafana has no Check to begin with")
	}

	l.npm.DeleteProxyHost(3)
	l.refresh()
	if !l.has("proxy:grafana.home.example") {
		t.Fatal("Grafana went while pfSense still has its name")
	}
	l.unregister("grafana.home.example")
	l.refresh()

	if l.has("proxy:grafana.home.example") {
		t.Error("Grafana is still listed")
	}
	if c := l.appChecks()["proxy:grafana.home.example"]; c != nil {
		t.Errorf("its Check %v is still listed", c)
	}
}

// A touched App stays as orphaned App settings when its sources go,
// without a Check, until it is reattached to an App or deleted.
func TestATouchedAppBecomesOrphanedAppSettings(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Name: "Graphs", Group: "Monitoring"}}, nil)
	l.call("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:influxdb.home.example", Kind: appspb.WarningKind_WARNING_KIND_NO_TLS}, nil)

	l.npm.DeleteProxyHost(3) // grafana
	l.npm.DeleteProxyHost(5) // influxdb
	l.unregister("grafana.home.example", "influxdb.home.example")
	l.refresh()

	for _, id := range []string{"proxy:grafana.home.example", "proxy:influxdb.home.example"} {
		got := l.app(id)
		if !got.GetOrphaned() || got.GetReach() != appspb.Reach_REACH_UNSPECIFIED || got.GetLinks().GetProxyHostId() != 0 {
			t.Errorf("%s = %v, want orphaned App settings", id, got)
		}
		if d := diff([]appspb.WarningKind{appspb.WarningKind_WARNING_KIND_ORPHANED}, kinds(got)); d != "" {
			t.Errorf("%s's Warnings (-want +got):\n%s", id, d)
		}
		if c := l.appChecks()[id]; c != nil {
			t.Errorf("%s's Check %v is still listed", id, c)
		}
	}
	if got := l.app("proxy:grafana.home.example"); got.GetName() != "Graphs" || got.GetUrl() != "https://grafana.home.example" {
		t.Errorf("Grafana = %v, want what was kept", got)
	}

	// Reattached to the Proxy Host's new App, Grafana's record takes its
	// sources and URL and keeps its id, settings and Check.
	newHost := l.npm.AddProxyHost(proxyHost("http://198.18.1.204:3000", "graphs.home.example"))
	l.register("graphs.home.example")
	l.refresh()
	if !l.has("proxy:graphs.home.example") {
		t.Fatal("no App for the new Proxy Host")
	}
	l.call("Reattach", &appspb.ReattachRequest{Id: "proxy:grafana.home.example", To: "proxy:graphs.home.example"}, nil)
	got := l.app("proxy:grafana.home.example")
	if got.GetOrphaned() || got.GetLinks().GetProxyHostId() != int64(newHost) || got.GetUrl() != "https://graphs.home.example" || got.GetGroup() != "Monitoring" {
		t.Errorf("Grafana reattached = %v, want linked to Proxy Host %d at its URL, its settings kept", got, newHost)
	}
	if l.has("proxy:graphs.home.example") {
		t.Error("the App reattached to is still listed")
	}
	checks := l.appChecks()
	if checks["proxy:grafana.home.example"].GetSpec().GetTarget() != "https://graphs.home.example" || checks["proxy:graphs.home.example"] != nil {
		t.Errorf("App Checks %v, want Grafana's probing its new URL and none for the App reattached to", checks)
	}

	// Deleted, influxdb's settings are gone for good.
	l.call("DeleteOrphan", &appspb.DeleteOrphanRequest{Id: "proxy:influxdb.home.example"}, nil)
	if l.has("proxy:influxdb.home.example") {
		t.Error("influxdb's orphaned App settings are still listed after their delete")
	}
}

// Only orphaned App settings are reattached or deleted as such, and only
// onto an App without settings of its own.
func TestReattachAndDeleteOrphanTakeOrphanedAppSettings(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Name: "Graphs"}}, nil)
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:ha.home.example", Settings: &appspb.AppSettings{Hidden: true}}, nil)
	l.npm.DeleteProxyHost(3)
	l.unregister("grafana.home.example")
	l.refresh()

	for _, tc := range []struct {
		method string
		req    proto.Message
		code   string
	}{
		{"DeleteOrphan", &appspb.DeleteOrphanRequest{Id: "proxy:ha.home.example"}, "failed_precondition"},
		{"Reattach", &appspb.ReattachRequest{Id: "proxy:ha.home.example", To: "proxy:md.home.example"}, "failed_precondition"},
		{"Reattach", &appspb.ReattachRequest{Id: "proxy:grafana.home.example", To: "proxy:ha.home.example"}, "failed_precondition"},
		{"Reattach", &appspb.ReattachRequest{Id: "proxy:grafana.home.example", To: "proxy:nowhere"}, "not_found"},
		{"DeleteOrphan", &appspb.DeleteOrphanRequest{Id: "proxy:nowhere"}, "not_found"},
	} {
		err := l.try(tc.method, tc.req, nil)
		if err == nil || err.code != tc.code {
			t.Errorf("%s %v: %v, want %s", tc.method, tc.req, err, tc.code)
		}
	}
}

// A Link-only App never disappears on its own.
func TestALinkOnlyAppStaysWithoutSources(t *testing.T) {
	l := newLab(t, labOptions{})
	var created appspb.CreateAppResponse
	l.call("CreateApp", &appspb.CreateAppRequest{App: &appspb.LinkOnlyApp{Name: "pfSense", Url: "https://198.18.0.1"}}, &created)
	l.refresh()

	got := l.app(created.GetApp().GetId())
	if got.GetReach() != appspb.Reach_REACH_LINK_ONLY || got.GetOrphaned() || len(got.GetWarnings()) != 0 {
		t.Errorf("pfSense = %v, want Link only without Warnings", got)
	}
	if c := l.appChecks()[got.GetId()]; c.GetSpec().GetTarget() != "https://198.18.0.1" {
		t.Errorf("its Check = %v, want one probing its URL", c)
	}
}
