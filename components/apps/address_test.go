package apps_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

// changeAddress is the App Action giving the App id the URL and aliases.
func changeAddress(id, url string, aliases ...string) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_ChangeAddress{ChangeAddress: &appspb.ChangeAddress{Id: id, Url: url, Aliases: aliases}}}
}

// A proxied App's URL change updates its Proxy Host's names, registers the
// new name and removes the old one in one batch applied once, and updates
// the record: the App keeps its id, settings and Check, now on the new
// URL.
func TestChangeAProxiedAppsURL(t *testing.T) {
	l := newLab(t, labOptions{})
	const id = "proxy:grafana.home.example"
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: id, Settings: &appspb.AppSettings{Group: "Ops"}}, nil)
	check := l.appChecks()[id]
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := changeAddress(id, "https://graphs.home.example")

	p := l.plan(action)
	want := []string{
		"SYSTEM_NGINX_PROXY_MANAGER: Update Proxy Host grafana.home.example",
		"SYSTEM_PFSENSE: Add alias graphs.home.example to nginx.home.example",
		"SYSTEM_PFSENSE: Remove alias grafana.home.example from nginx.home.example",
		"SYSTEM_DDASH: Update the App Grafana",
	}
	if got := titles(p); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if v := p.GetSteps()[0].GetValues()[0]; v.GetBefore() != "grafana.home.example" || v.GetAfter() != "graphs.home.example" {
		t.Errorf("Proxy Host's domain names = %v, want grafana.home.example → graphs.home.example", v)
	}
	if d := diff([]*appspb.PlanValue{{Label: "URL", Before: "https://grafana.home.example", After: "https://graphs.home.example"}}, p.GetSteps()[3].GetValues()); d != "" {
		t.Errorf("the record step's values (-want +got):\n%s", d)
	}

	resp := l.run(action)
	if resp.GetDone() != 4 || resp.GetAppId() != id || resp.GetReread() != "" {
		t.Errorf("RunAction = %v, want 4 steps done on %s", resp, id)
	}
	if d := diff([]string{"PUT /api/nginx/proxy-hosts/3"}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if d := diff([]string{pfAddAlias, pfRemoveAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	l.refresh()
	a := l.app(id)
	if a.GetUrl() != "https://graphs.home.example" || a.GetName() != "Grafana" || a.GetGroup() != "Ops" || len(a.GetAliases()) != 0 || !slices.Equal(kinds(a), []appspb.WarningKind{noWorkload}) {
		t.Errorf("App = %v, want Grafana in Ops at https://graphs.home.example, no aliases, no Warning but No Workload", a)
	}
	if d := diff([]string{"alias of nginx.home.example → 127.0.0.1"}, l.dnsOf("graphs.home.example")); d != "" || len(l.dnsOf("grafana.home.example")) != 0 {
		t.Errorf("DNS = %q and %q, want graphs.home.example alone registered", l.dnsOf("graphs.home.example"), l.dnsOf("grafana.home.example"))
	}
	got := l.appChecks()[id]
	if got.GetId() != check.GetId() || got.GetSpec().GetTarget() != "https://graphs.home.example" {
		t.Errorf("Check = %v, want Check %d, on the new URL", got, check.GetId())
	}
}

// Aliases added and removed on a proxied App: the Proxy Host serves the
// names, the Reverse Proxy's Host Override registers them.
func TestChangeAProxiedAppsAliases(t *testing.T) {
	l := newLab(t, labOptions{})
	keycloak := l.byURL("https://keycloak.home.example").GetId()
	fwLog := len(l.fw.Log())

	resp := l.run(changeAddress(keycloak, "https://keycloak.home.example", "sso.home.example"))
	if resp.GetDone() != 4 {
		t.Errorf("RunAction = %v, want the Proxy Host, two pfSense steps and the record done", resp)
	}
	if d := diff([]string{pfAddAlias, pfRemoveAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	if names := fmt.Sprint(l.npm.ProxyHost(6)["domain_names"]); names != "[keycloak.home.example sso.home.example]" {
		t.Errorf("Proxy Host 6's names = %v", names)
	}
	l.refresh()
	if a := l.app(keycloak); !slices.Equal(a.GetAliases(), []string{"sso.home.example"}) || !slices.Equal(kinds(a), []appspb.WarningKind{noWorkload}) {
		t.Errorf("Keycloak = %v, want sso.home.example its alias, no Warning but No Workload", a)
	}
}

// A new name must be free: one an App has is refused naming it, one kept
// for orphaned App settings is refused naming them.
func TestANewNameMustBeFree(t *testing.T) {
	l := newLab(t, labOptions{})
	keycloak := l.byURL("https://keycloak.home.example").GetId()

	p := l.plan(changeAddress(keycloak, "https://keycloak.home.example", "local.home.example", "grafana.home.example"))
	if n := p.GetNames(); len(n) != 1 || n[0].GetKind() != appspb.NameNoteKind_NAME_NOTE_KIND_APP || n[0].GetAppId() != "proxy:grafana.home.example" {
		t.Errorf("notes = %v, want grafana.home.example taken by Grafana", n)
	}
	if _, err := l.tryRun(changeAddress(keycloak, "https://keycloak.home.example", "local.home.example", "grafana.home.example"), p); err == nil || err.code != "failed_precondition" {
		t.Errorf("RunAction = %v, want it refused", err)
	}

	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{Name: "Films"}}, nil)
	l.npm.DeleteProxyHost(34)
	l.unregister("video.home.example")
	l.refresh()
	p = l.plan(changeAddress(keycloak, "https://keycloak.home.example", "local.home.example", "video.home.example"))
	want := []*appspb.NameNote{{
		Hostname: "video.home.example", Kind: appspb.NameNoteKind_NAME_NOTE_KIND_ORPHAN, AppId: "proxy:video.home.example", Name: "Films",
		Text: "video.home.example is kept for the orphaned App settings Films: reattach or delete them first.",
	}}
	if d := diff(want, p.GetNames()); d != "" {
		t.Errorf("notes (-want +got):\n%s", d)
	}
}

// A Not-proxied App's URL change renames its own Host Override, its
// aliases moving with it; the App keeps its id and its link follows.
func TestChangeANotProxiedAppsURL(t *testing.T) {
	l := newLab(t, labOptions{})
	l.addOverride("printer", "198.18.1.60", "print")
	l.refresh()
	id := l.byURL("https://printer.home.example").GetId()
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := changeAddress(id, "https://laser.home.example", "print.home.example")

	want := []string{
		"SYSTEM_PFSENSE: Delete Host Override printer.home.example",
		"SYSTEM_PFSENSE: Create Host Override laser.home.example",
		"SYSTEM_PFSENSE: Add alias print.home.example to laser.home.example",
		"SYSTEM_DDASH: Update the App Printer",
	}
	if got := titles(l.plan(action)); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	l.run(action)
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes = %q, want none", w)
	}
	if d := diff([]string{pfDelete, pfCreate, pfAddAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	l.refresh()
	a := l.app(id)
	if a.GetUrl() != "https://laser.home.example" || a.GetLinks().GetHostOverride().GetHost() != "laser" || !slices.Equal(a.GetAliases(), []string{"print.home.example"}) {
		t.Errorf("App = %v, want it at laser.home.example, linked to its Host Override, print.home.example its alias", a)
	}
	if appsNamed(l.list().GetApps(), "printer.home.example") != nil {
		t.Error("an App is left for printer.home.example")
	}
}

// A Link-only App's URL changes in ddash alone.
func TestChangeALinkOnlyAppsURL(t *testing.T) {
	l := newLab(t, labOptions{})
	id := l.addLinkOnly("Router", "https://198.18.0.1")
	if got, want := titles(l.plan(changeAddress(id, "https://198.18.0.2:8443"))), []string{"SYSTEM_DDASH: Update the App Router"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	l.run(changeAddress(id, "https://198.18.0.2:8443"))
	if a := l.app(id); a.GetUrl() != "https://198.18.0.2:8443" || a.GetName() != "Router" {
		t.Errorf("App = %v, want Router at its new URL", a)
	}
}

// With two Host Overrides at the Reverse Proxy's address, a proxied App's
// new name is blocked by the Notice.
func TestTheReverseProxysNoticeBlocksANewProxiedName(t *testing.T) {
	l := newLab(t, labOptions{})
	l.addOverride("rp", rpAddress)
	l.refresh()
	p := l.plan(changeAddress("proxy:grafana.home.example", "https://graphs.home.example"))
	if !blockedBy(p, "2 Host Overrides point at the Reverse Proxy (127.0.0.1): nginx.home.example, rp.home.example. Keep one to add proxied Apps.") {
		t.Errorf("Plan = %v, want it blocked by the Notice", p)
	}
}
