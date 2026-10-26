package apps_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
)

// proxied, notProxied and linkOnly add url with each Reach.
func proxied(url string) *appspb.AppAction {
	return addApp(&appspb.AddApp{Url: url, Reach: appspb.Reach_REACH_PROXIED, ProxyHost: wiki})
}

func notProxied(url string) *appspb.AppAction {
	return addApp(&appspb.AddApp{Url: url, Reach: appspb.Reach_REACH_NOT_PROXIED, Address: "198.18.1.60"})
}

func linkOnly(url string) *appspb.AppAction {
	return addApp(&appspb.AddApp{Url: url, Reach: appspb.Reach_REACH_LINK_ONLY})
}

// "The name is free" refuses every hostname taken, compared without case
// or a trailing dot, each with its one message naming what took it; Add
// stays refused while it is.
func TestATakenNameRefusesAddApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:influxdb.home.example", Settings: &appspb.AppSettings{Name: "Influx", Hidden: true}}, nil)
	pfsense := l.byURL("https://pfsense.home.example").GetId()
	l.npm.EditProxyHost(7, func(row map[string]any) { row["enabled"] = false }) // portainer
	l.addLinkOnly("Printer", "https://printer.home.example")
	l.refresh()

	app := func(n, id, name string) *appspb.NameNote {
		return &appspb.NameNote{Hostname: n, Kind: appspb.NameNoteKind_NAME_NOTE_KIND_APP, Text: n + " is taken by " + name + ".", AppId: id, Name: name}
	}
	hidden := app("influxdb.home.example", "proxy:influxdb.home.example", "Influx")
	hidden.Text, hidden.Hidden = "influxdb.home.example is taken by Influx (Hidden).", true
	for _, tc := range []struct {
		why    string
		action *appspb.AppAction
		want   *appspb.NameNote
	}{
		{"an App's URL", proxied("https://grafana.home.example"), app("grafana.home.example", "proxy:grafana.home.example", "Grafana")},
		{"in other case, with a trailing dot", notProxied("https://GRAFANA.home.example."), app("grafana.home.example", "proxy:grafana.home.example", "Grafana")},
		{"a Proxy Host's further domain name", proxied("https://local.home.example"), app("local.home.example", "proxy:keycloak.home.example", "Keycloak")},
		{"a disabled Proxy Host's", proxied("https://portainer.home.example"), app("portainer.home.example", "proxy:portainer.home.example", "Portainer")},
		{"a Hidden App's", proxied("https://influxdb.home.example"), hidden},
		{"a Host Override", notProxied("https://pfsense.home.example"), app("pfsense.home.example", pfsense, "Pfsense")},
		{"a Link-only App's, for Link only too", linkOnly("https://printer.home.example"), app("printer.home.example", l.byURL("https://printer.home.example").GetId(), "Printer")},
		{"a Device's reservation", proxied("https://homeassistant.home.example"), &appspb.NameNote{
			Hostname: "homeassistant.home.example", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_DEVICE,
			Text: "homeassistant.home.example is the Device homeassistant's name. To link to it as it is, choose Link only.",
			Name: "homeassistant", DeviceMac: "02:61:c0:a3:8a:16",
		}},
		{"a Device's, for Host Override too", notProxied("https://w1.home.example"), &appspb.NameNote{
			Hostname: "w1.home.example", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_DEVICE,
			Text: "w1.home.example is the Device Access Point (OpenWrt)'s name. To link to it as it is, choose Link only.",
			Name: "Access Point (OpenWrt)", DeviceMac: "02:56:74:bd:32:5b",
		}},
		{"an address", proxied("https://198.18.1.60"), &appspb.NameNote{Hostname: "198.18.1.60", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_NOT_A_HOSTNAME, Text: "Not a hostname."}},
	} {
		p := l.plan(tc.action)
		if d := diff([]*appspb.NameNote{tc.want}, p.GetNames()); d != "" {
			t.Errorf("%s: name notes (-want +got):\n%s", tc.why, d)
			continue
		}
		if _, err := l.tryRun(tc.action, p); err == nil || err.code != "failed_precondition" || err.message != tc.want.GetText() {
			t.Errorf("%s: RunAction = %v, want failed_precondition saying %q", tc.why, err, tc.want.GetText())
		}
	}

	// The Reverse Proxy's own name, once no Proxy Host serves it.
	l.npm.DeleteProxyHost(1)
	l.refresh()
	want := &appspb.NameNote{Hostname: "nginx.home.example", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_REVERSE_PROXY, Text: "nginx.home.example is the Reverse Proxy's own name."}
	if d := diff([]*appspb.NameNote{want}, l.plan(notProxied("https://nginx.home.example")).GetNames()); d != "" {
		t.Errorf("the Reverse Proxy's own name: notes (-want +got):\n%s", d)
	}
}

// blockedBy reports whether p is blocked with why, as its only block.
func blockedBy(p *appspb.Plan, why string) bool {
	return len(p.GetBlocked()) == 1 && p.GetBlocked()[0] == why
}

// While a source's last read failed, "the name is free" can't be told
// for the Reaches writing to it: they are blocked, the others not.
func TestAnUnreadableSourceBlocksTheReachesWritingToIt(t *testing.T) {
	l := newLab(t, labOptions{})
	const npm, pfsense = "Can't check Nginx Proxy Manager right now: its last read failed.", "Can't check pfSense right now: its last read failed."

	l.failPfSense.Store(true)
	_ = l.try("Refresh", &appspb.RefreshRequest{}, nil)
	if p := l.plan(proxied("https://wiki.home.example")); !blockedBy(p, pfsense) {
		t.Errorf("Reverse proxy with pfSense unreadable: Plan = %v, want it blocked: %q", p, pfsense)
	}
	if p := l.plan(notProxied("https://wiki.home.example")); !blockedBy(p, pfsense) {
		t.Errorf("Host Override with pfSense unreadable: Plan = %v, want it blocked: %q", p, pfsense)
	}
	if p := l.plan(linkOnly("https://wiki.home.example")); len(p.GetBlocked()) != 0 {
		t.Errorf("Link only with pfSense unreadable: Plan = %v, want it free", p)
	}
	action := proxied("https://wiki.home.example")
	if _, err := l.tryRun(action, l.plan(action)); err == nil || err.code != "failed_precondition" || err.message != pfsense {
		t.Errorf("RunAction with pfSense unreadable = %v, want it refused: %q", err, pfsense)
	}

	l.failPfSense.Store(false)
	l.failNPM.Store(true)
	_ = l.try("Refresh", &appspb.RefreshRequest{}, nil)
	if p := l.plan(proxied("https://wiki.home.example")); !blockedBy(p, npm) {
		t.Errorf("Reverse proxy with Nginx Proxy Manager unreadable: Plan = %v, want it blocked: %q", p, npm)
	}
	if p := l.plan(notProxied("https://wiki.home.example")); len(p.GetBlocked()) != 0 {
		t.Errorf("Host Override with Nginx Proxy Manager unreadable: Plan = %v, want it free", p)
	}
}

// Proxied Adds are blocked by the Reverse Proxy's Host Override Notice,
// and by Proxy Hosts being read-only; Host Override and Link only aren't.
func TestTheNoticesBlockProxiedAdds(t *testing.T) {
	l := newLab(t, labOptions{})
	l.addOverride("old", rpAddress)
	l.refresh()
	notice := l.list().GetReverseProxy().GetNotice()
	if p := l.plan(proxied("https://wiki.home.example")); notice == "" || !blockedBy(p, notice) {
		t.Errorf("Plan = %v, want it blocked by the Notice %q", p, notice)
	}
	if p := l.plan(notProxied("https://wiki.home.example")); len(p.GetBlocked()) != 0 {
		t.Errorf("Host Override: Plan = %v, want it free", p)
	}

	npm, failNPM := newNPM(t)
	npm.EditUser(func(u *npmfake.User) { u.ProxyHosts = "view" })
	l = newLab(t, labOptions{npm: npm, failNPM: failNPM})
	readOnly := "ddash's user in Nginx Proxy Manager can only view Proxy Hosts: set them to Manage to add proxied Apps."
	if p := l.plan(proxied("https://wiki.home.example")); !blockedBy(p, readOnly) {
		t.Errorf("read-only: Plan = %v, want it blocked: %q", p, readOnly)
	}
	if p := l.plan(notProxied("https://wiki.home.example")); len(p.GetBlocked()) != 0 {
		t.Errorf("read-only, Host Override: Plan = %v, want it free", p)
	}
}

// A Device's name is free for Link only, which links to it as it is; so
// is a hostname outside the home domain, which only Link only may have.
func TestLinkOnlyMayHaveADevicesNameOrOneOutsideTheHomeDomain(t *testing.T) {
	l := newLab(t, labOptions{})

	if p := l.plan(linkOnly("https://homeassistant.home.example:8123")); len(p.GetNames()) != 0 || len(p.GetBlocked()) != 0 {
		t.Errorf("Link only at a Device's name: Plan = %v, want nothing taken or blocked", p)
	}
	l.run(linkOnly("https://homeassistant.home.example:8123"))

	want := "wiki.example.org isn't in home.example, the home domain: choose Link only."
	for _, action := range []*appspb.AppAction{proxied("https://wiki.example.org"), notProxied("https://wiki.example.org")} {
		if p := l.plan(action); len(p.GetBlocked()) != 1 || p.GetBlocked()[0] != want {
			t.Errorf("Plan = %v, want it blocked: %q", p, want)
		}
	}
	l.run(linkOnly("https://wiki.example.org"))
}

// Adding a hostname orphaned App settings were kept for reattaches them:
// the App keeps the orphan's id, so its settings and its Check's history,
// and the Plan ends with "Reattach".
func TestAddingAnOrphansHostnameReattachesItsSettings(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Name: "Graphs", Group: "Monitoring"}}, nil)
	check := l.appChecks()["proxy:grafana.home.example"].GetId()
	l.npm.DeleteProxyHost(3)
	l.unregister("grafana.home.example")
	l.refresh()
	if !l.app("proxy:grafana.home.example").GetOrphaned() {
		t.Fatal("Grafana isn't orphaned")
	}

	action := addApp(&appspb.AddApp{Url: "https://grafana.home.example", Icon: "grafana", Reach: appspb.Reach_REACH_PROXIED, ProxyHost: wiki})
	p := l.plan(action)
	want := []*appspb.NameNote{{
		Hostname: "grafana.home.example", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_REATTACH,
		Text: "Settings were kept for Graphs: adding it reattaches them, with its Check history.", AppId: "proxy:grafana.home.example", Name: "Graphs",
	}}
	if d := diff(want, p.GetNames()); d != "" {
		t.Errorf("name notes (-want +got):\n%s", d)
	}
	if steps := titles(p); len(steps) != 3 || steps[2] != "SYSTEM_DDASH: Reattach Graphs" {
		t.Errorf("steps = %q, want them to end with Reattach Graphs", steps)
	}

	resp := l.run(action)
	got := l.app("proxy:grafana.home.example")
	if resp.GetAppId() != got.GetId() || got.GetOrphaned() || got.GetReach() != appspb.Reach_REACH_PROXIED || got.GetName() != "Graphs" || got.GetGroup() != "Monitoring" || got.GetIcon() != "grafana" {
		t.Errorf("Grafana = %v, want it proxied again with its settings and the icon given", got)
	}
	if c := l.appChecks()["proxy:grafana.home.example"]; c.GetId() != check {
		t.Errorf("Grafana's Check = %v, want its Check %d with its history", c, check)
	}
}
