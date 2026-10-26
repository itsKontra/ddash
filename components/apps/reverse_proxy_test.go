package apps_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

func addReverseProxy(id string, f *appspb.ProxyHostFields) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_AddReverseProxy{AddReverseProxy: &appspb.AddReverseProxy{Id: id, ProxyHost: f}}}
}

func removeReverseProxy(id, address string) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_RemoveReverseProxy{RemoveReverseProxy: &appspb.RemoveReverseProxy{Id: id, Address: address}}}
}

// nasProxy is a Proxy Host's fields for the NAS's web UI.
var nasProxy = &appspb.ProxyHostFields{ForwardScheme: "https", ForwardHost: "198.18.2.10", ForwardPort: 5001, CertificateId: 3, BlockExploits: true, ForceSsl: true}

// Add reverse proxy puts a Not-proxied App behind the Reverse Proxy: a
// Proxy Host for its hostname, and its own Host Override replaced by an
// alias of the Reverse Proxy's Host Override. Remove reverse proxy does
// the reverse. The App keeps its id throughout.
func TestAddAndRemoveTheReverseProxy(t *testing.T) {
	l := newLab(t, labOptions{})
	nas := l.byURL("https://nas.home.example").GetId()
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())

	add := addReverseProxy(nas, nasProxy)
	want := []string{
		"SYSTEM_NGINX_PROXY_MANAGER: Create Proxy Host nas.home.example",
		"SYSTEM_PFSENSE: Delete Host Override nas.home.example",
		"SYSTEM_PFSENSE: Add alias nas.home.example to nginx.home.example",
		"SYSTEM_DDASH: Put Nas behind the Reverse Proxy",
	}
	if got := titles(l.plan(add)); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if resp := l.run(add); resp.GetDone() != 4 || resp.GetAppId() != nas {
		t.Errorf("RunAction = %v, want 4 steps done on %s", resp, nas)
	}
	if d := diff([]string{"POST /api/nginx/proxy-hosts"}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if d := diff([]string{pfDelete, pfAddAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	l.refresh()
	a := l.app(nas)
	if a.GetReach() != appspb.Reach_REACH_PROXIED || a.GetLinks().GetProxyHostId() == 0 || a.GetLinks().GetHostOverride() != nil {
		t.Errorf("NAS = %v, want it proxied, linked to its Proxy Host alone", a)
	}
	if d := diff([]string{"alias of nginx.home.example → 127.0.0.1"}, l.dnsOf("nas.home.example")); d != "" {
		t.Errorf("nas.home.example's DNS (-want +got):\n%s", d)
	}
	host := a.GetLinks().GetProxyHostId()

	npmLog, fwLog = len(l.npm.Log()), len(l.fw.Log())
	remove := removeReverseProxy(nas, "198.18.2.10")
	want = []string{
		"SYSTEM_NGINX_PROXY_MANAGER: Delete Proxy Host nas.home.example",
		"SYSTEM_PFSENSE: Remove alias nas.home.example from nginx.home.example",
		"SYSTEM_PFSENSE: Create Host Override nas.home.example",
		"SYSTEM_DDASH: Take Nas from behind the Reverse Proxy",
	}
	if got := titles(l.plan(remove)); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	l.run(remove)
	if d := diff([]string{"DELETE /api/nginx/proxy-hosts/" + strconv.FormatInt(host, 10)}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if d := diff([]string{pfRemoveAlias, pfCreate, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	l.refresh()
	a = l.app(nas)
	if a.GetReach() != appspb.Reach_REACH_NOT_PROXIED || a.GetLinks().GetProxyHostId() != 0 || a.GetLinks().GetHostOverride().GetHost() != "nas" {
		t.Errorf("NAS = %v, want it Not proxied, linked to its Host Override alone", a)
	}
	if d := diff([]string{"Host Override → 198.18.2.10"}, l.dnsOf("nas.home.example")); d != "" {
		t.Errorf("nas.home.example's DNS (-want +got):\n%s", d)
	}
}

// Add reverse proxy on an alias without a Proxy Host creates the Proxy
// Host alone, which clears No Proxy Host.
func TestAddReverseProxyFixesNoProxyHost(t *testing.T) {
	l := newLab(t, labOptions{})
	l.register("wiki.home.example")
	l.refresh()
	wikiID := l.byURL("https://wiki.home.example").GetId()
	fwLog := len(l.fw.Log())

	action := addReverseProxy(wikiID, wiki)
	if got, want := titles(l.plan(action)), []string{"SYSTEM_NGINX_PROXY_MANAGER: Create Proxy Host wiki.home.example", "SYSTEM_DDASH: Put Wiki behind the Reverse Proxy"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	l.run(action)
	if w := writes(l.fw.Log(), fwLog); len(w) != 0 {
		t.Errorf("pfSense writes = %q, want none", w)
	}
	l.refresh()
	if a := l.app(wikiID); a.GetLinks().GetProxyHostId() == 0 || slices.Contains(kinds(a), noProxyHost) {
		t.Errorf("Wiki = %v, want it linked to its Proxy Host, without No Proxy Host", a)
	}
}
