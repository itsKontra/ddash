package apps_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

func kinds(a *appspb.App) []appspb.WarningKind {
	var out []appspb.WarningKind
	for _, w := range a.GetWarnings() {
		out = append(out, w.GetKind())
	}
	return out
}

// A domain renamed in Nginx Proxy Manager's UI keeps the App: its record
// is linked to the Proxy Host's id. Its URL stays until the Operator picks
// one of the new names, and says it is no longer on the Proxy Host.
func TestARenamedProxyHostKeepsItsApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Group: "Monitoring"}}, nil)

	l.npm.EditProxyHost(3, func(row map[string]any) {
		row["domain_names"] = []string{"graphs.home.example", "metrics.home.example"}
	})
	l.refresh()

	got := l.app("proxy:grafana.home.example")
	want := seen{"proxy:grafana.home.example", "Grafana", "https://grafana.home.example", []string{"graphs.home.example", "metrics.home.example"}, ""}
	if d := diff(want, seenOf(got)); d != "" {
		t.Errorf("Grafana after the rename (-want +got):\n%s", d)
	}
	if got.GetGroup() != "Monitoring" {
		t.Errorf("group = %q, want its settings kept", got.GetGroup())
	}
	if d := diff([]appspb.WarningKind{appspb.WarningKind_WARNING_KIND_NO_WORKLOAD, appspb.WarningKind_WARNING_KIND_URL_NOT_ON_PROXY_HOST}, kinds(got)); d != "" {
		t.Errorf("Warnings (-want +got):\n%s", d)
	}
	if l.has("proxy:graphs.home.example") {
		t.Error("the renamed Proxy Host also became a new App")
	}

	// "Use one of its names as the URL".
	l.call("PromoteAlias", &appspb.PromoteAliasRequest{Id: "proxy:grafana.home.example", Alias: "graphs.home.example"}, nil)
	got = l.app("proxy:grafana.home.example")
	want = seen{"proxy:grafana.home.example", "Grafana", "https://graphs.home.example", []string{"metrics.home.example"}, ""}
	if d := diff(want, seenOf(got)); d != "" {
		t.Errorf("Grafana with a new URL (-want +got):\n%s", d)
	}
	if d := diff([]appspb.WarningKind{appspb.WarningKind_WARNING_KIND_NO_WORKLOAD}, kinds(got)); d != "" {
		t.Errorf("Warnings with a new URL (-want +got):\n%s", d)
	}
	if c := l.appChecks()["proxy:grafana.home.example"]; c.GetSpec().GetTarget() != "https://graphs.home.example" {
		t.Errorf("its Check probes %q, want the new URL", c.GetSpec().GetTarget())
	}
}

// Promoting an alias makes it the URL's hostname, keeping the scheme; the
// old hostname becomes an alias. Only an alias can be promoted.
func TestAnAliasIsPromotedToTheURL(t *testing.T) {
	l := newLab(t, labOptions{})

	l.call("PromoteAlias", &appspb.PromoteAliasRequest{Id: "proxy:keycloak.home.example", Alias: "local.home.example"}, nil)

	want := seen{"proxy:keycloak.home.example", "Keycloak", "https://local.home.example", []string{"keycloak.home.example"}, ""}
	if d := diff(want, seenOf(l.app("proxy:keycloak.home.example"))); d != "" {
		t.Errorf("Keycloak (-want +got):\n%s", d)
	}
	l.refresh()
	if d := diff(want, seenOf(l.app("proxy:keycloak.home.example"))); d != "" {
		t.Errorf("Keycloak after a sync (-want +got):\n%s", d)
	}

	err := l.try("PromoteAlias", &appspb.PromoteAliasRequest{Id: "proxy:keycloak.home.example", Alias: "grafana.home.example"}, nil)
	if err == nil || err.code != "invalid_argument" {
		t.Errorf("promoting another App's name: %v, want invalid_argument", err)
	}
}

// A Proxy Host no record links to attaches by hostname: a Proxy Host
// deleted and created again in Nginx Proxy Manager (a new id) gets back
// the App it had, settings and Check included.
func TestAProxyHostWithANewIdAttachesByHostname(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Name: "Graphs"}}, nil)

	l.npm.DeleteProxyHost(3)
	id := l.npm.AddProxyHost(proxyHost("http://198.18.1.204:3000", "grafana.home.example"))
	l.refresh()

	got := l.app("proxy:grafana.home.example")
	if got.GetName() != "Graphs" || got.GetLinks().GetProxyHostId() != int64(id) || got.GetOrphaned() {
		t.Errorf("Grafana = %v, want its settings, linked to Proxy Host %d", got, id)
	}
	if len(l.list().GetApps()) != 23 {
		t.Errorf("%d Apps, want 23", len(l.list().GetApps()))
	}
}

// A Link-only App absorbs a new Proxy Host for its URL's hostname: it is
// proxied from then on, under its id.
func TestALinkOnlyAppAbsorbsAProxyHostForItsHostname(t *testing.T) {
	l := newLab(t, labOptions{})
	var created appspb.CreateAppResponse
	l.call("CreateApp", &appspb.CreateAppRequest{App: &appspb.LinkOnlyApp{Name: "Wiki", Url: "https://wiki.home.example"}}, &created)
	id := created.GetApp().GetId()
	if created.GetApp().GetReach() != appspb.Reach_REACH_LINK_ONLY {
		t.Fatalf("created %v, want Link only", created.GetApp())
	}

	host := l.npm.AddProxyHost(proxyHost("http://198.18.2.11:3000", "wiki.home.example", "docs.home.example"))
	l.refresh()

	got := l.app(id)
	if got.GetReach() != appspb.Reach_REACH_PROXIED || got.GetLinks().GetProxyHostId() != int64(host) || got.GetName() != "Wiki" {
		t.Errorf("Wiki = %v, want proxied by Proxy Host %d, its name kept", got, host)
	}
	if d := diff([]string{"docs.home.example"}, got.GetAliases()); d != "" {
		t.Errorf("aliases (-want +got):\n%s", d)
	}
	if len(l.list().GetApps()) != 24 {
		t.Errorf("%d Apps, want 24: no other for the new Proxy Host", len(l.list().GetApps()))
	}
}
