package apps_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

// deleteAction is the App Action deleting the App id.
func deleteAction(id string) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_Delete{Delete: &appspb.DeleteApp{Id: id}}}
}

// The pfSense log entries of a Host Override deleted and an alias removed.
const (
	pfDelete      = "DELETE /api/v2/services/dns_resolver/host_override"
	pfRemoveAlias = "DELETE /api/v2/services/dns_resolver/host_override/alias"
)

// dnsOf is how pfSense registers the hostname n now: "Host Override →
// 198.18.1.60", "alias of nginx.home.example → 127.0.0.1", each entry.
func (l *lab) dnsOf(n string) []string {
	var out []string
	for _, o := range l.fw.HostOverrides() {
		if o.Host+"."+o.Domain == n {
			out = append(out, "Host Override → "+strings.Join(o.IPs, ", "))
		}
		for _, a := range o.Aliases {
			if a.Host+"."+a.Domain == n {
				out = append(out, "alias of "+o.Host+"."+o.Domain+" → "+strings.Join(o.IPs, ", "))
			}
		}
	}
	return out
}

// Deleting a proxied App deletes its Proxy Host, removes its alias from
// the Reverse Proxy's Host Override in one batch applied once, then its
// record and Check.
func TestDeleteAProxiedApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Group: "Ops"}}, nil)
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())

	p := l.plan(deleteAction("proxy:grafana.home.example"))
	want := []string{
		"SYSTEM_NGINX_PROXY_MANAGER: Delete Proxy Host grafana.home.example",
		"SYSTEM_PFSENSE: Remove alias grafana.home.example from nginx.home.example",
		"SYSTEM_DDASH: Delete the App Grafana and its Check",
	}
	if got := titles(p); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if d := diff([]*appspb.PlanValue{
		{Label: "Host Override", Before: "nginx.home.example → 127.0.0.1", After: "nginx.home.example → 127.0.0.1"},
		{Label: "Alias", Before: "grafana.home.example"},
	}, p.GetSteps()[1].GetValues()); d != "" {
		t.Errorf("pfSense step's values (-want +got):\n%s", d)
	}

	resp := l.run(deleteAction("proxy:grafana.home.example"))
	if resp.GetDone() != 3 || resp.GetReread() != "" || resp.GetAppId() != "proxy:grafana.home.example" {
		t.Errorf("RunAction = %v, want 3 steps done", resp)
	}
	if d := diff([]string{"DELETE /api/nginx/proxy-hosts/3"}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if d := diff([]string{pfRemoveAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	if l.npm.ProxyHost(3) != nil || len(l.dnsOf("grafana.home.example")) != 0 {
		t.Error("the Proxy Host or the alias is left")
	}
	l.refresh()
	if l.has("proxy:grafana.home.example") || appsNamed(l.list().GetApps(), "grafana.home.example") != nil {
		t.Error("Grafana is still listed")
	}
	if _, ok := l.appChecks()["proxy:grafana.home.example"]; ok {
		t.Error("Grafana's Check is left")
	}
}

// Deleting a Not-proxied App deletes its own Host Override, its aliases
// with it.
func TestDeleteANotProxiedApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.addOverride("printer", "198.18.1.60", "print")
	l.refresh()
	printer := l.byURL("https://printer.home.example")
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: printer.GetId(), Settings: &appspb.AppSettings{Name: "Printer"}}, nil)
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())

	p := l.plan(deleteAction(printer.GetId()))
	if got, want := titles(p), []string{"SYSTEM_PFSENSE: Delete Host Override printer.home.example", "SYSTEM_DDASH: Delete the App Printer and its Check"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if d := diff([]*appspb.PlanValue{
		{Label: "Host Override", Before: "printer.home.example → 198.18.1.60"},
		{Label: "Aliases", Before: "print.home.example"},
	}, p.GetSteps()[0].GetValues()); d != "" {
		t.Errorf("pfSense step's values (-want +got):\n%s", d)
	}

	l.run(deleteAction(printer.GetId()))
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes = %q, want none", w)
	}
	if d := diff([]string{pfDelete, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	if len(l.dnsOf("printer.home.example"))+len(l.dnsOf("print.home.example")) != 0 {
		t.Error("the Host Override or its alias is left")
	}
	l.refresh()
	if l.has(printer.GetId()) {
		t.Error("Printer is still listed")
	}
}

// A Link-only App's Delete changes ddash alone.
func TestDeleteALinkOnlyApp(t *testing.T) {
	l := newLab(t, labOptions{})
	id := l.addLinkOnly("pfSense", "https://198.18.0.1")
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())

	if got, want := titles(l.plan(deleteAction(id))), []string{"SYSTEM_DDASH: Delete the App pfSense and its Check"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	l.run(deleteAction(id))
	if l.has(id) {
		t.Error("pfSense is still listed")
	}
	if _, ok := l.appChecks()[id]; ok {
		t.Error("its Check is left")
	}
	if w := append(writes(l.npm.Log(), npmLog), writes(l.fw.Log(), fwLog)...); len(w) != 0 {
		t.Errorf("writes = %q, want none", w)
	}
}

// Deleting an App whose final sync can't re-read the sources doesn't bring
// it back from the last good read.
func TestADeletedAppStaysGoneWhenTheFinalSyncFails(t *testing.T) {
	l := newLab(t, labOptions{resync: 200 * time.Millisecond})
	l.stallPfSense.Store(true)

	resp := l.run(deleteAction("proxy:grafana.home.example"))
	if !strings.HasPrefix(resp.GetReread(), "couldn't re-read pfsense-api") {
		t.Errorf("RunAction = %v, want that it couldn't re-read pfSense", resp)
	}
	l.stallPfSense.Store(false)
	if ids := appsNamed(l.list().GetApps(), "grafana.home.example"); ids != nil {
		t.Errorf("Apps for grafana.home.example = %q, want none", ids)
	}
}
