package apps_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	proxyhostpb "github.com/itsKontra/ddash/proto/ddash/proxyhost"
)

// changeProxyHost is the App Action setting the App id's Proxy Host
// fields to f.
func changeProxyHost(id string, f *proxyhostpb.ProxyHostFields) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_ChangeProxyHost{ChangeProxyHost: &appspb.ChangeProxyHost{Id: id, ProxyHost: f}}}
}

// grafanaMoved is Grafana's Proxy Host fields (Proxy Host 3) with a new
// upstream, and HTTP/2 off.
var grafanaMoved = &proxyhostpb.ProxyHostFields{
	ForwardScheme: "http", ForwardHost: "198.18.2.30", ForwardPort: 3001,
	CertificateId: 3, BlockExploits: true, Websockets: true, ForceSsl: true,
}

// Editing the Reverse proxy Panel updates the Proxy Host in Nginx Proxy
// Manager, keeping its domain names and what ddash doesn't manage; the new
// upstream re-matches the Workload.
func TestChangeAProxyHost(t *testing.T) {
	l := newLab(t, labOptions{proxmox: &proxmoxFake{guests: []*proxmoxapipb.Workload{
		guest(proxmoxapipb.Kind_KIND_LXC, 120, "grafana", "198.18.1.204"),
		guest(proxmoxapipb.Kind_KIND_LXC, 130, "monitoring", "198.18.2.30"),
	}}})
	if w := l.app("proxy:grafana.home.example").GetWorkload().GetName(); w != "grafana" {
		t.Fatalf("Grafana's Workload = %q, want grafana", w)
	}
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := changeProxyHost("proxy:grafana.home.example", grafanaMoved)

	p := l.plan(action)
	if got, want := titles(p), []string{"SYSTEM_NGINX_PROXY_MANAGER: Update Proxy Host grafana.home.example"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	want := []*appspb.PlanValue{
		{Label: "Domain names", Before: "grafana.home.example", After: "grafana.home.example"},
		{Label: "Upstream", Before: "http://198.18.1.204:3000", After: "http://198.18.2.30:3001"},
		{Label: "Certificate", Before: "#3", After: "#3"},
		{Label: "Options", Before: "Block Common Exploits · Websockets · Force SSL · HTTP/2", After: "Block Common Exploits · Websockets · Force SSL"},
	}
	if d := diff(want, p.GetSteps()[0].GetValues()); d != "" {
		t.Errorf("values (-want +got):\n%s", d)
	}

	// Meanwhile, in Nginx Proxy Manager's UI: a field ddash doesn't manage.
	l.npm.EditProxyHost(3, func(row map[string]any) { row["advanced_config"] = "client_max_body_size 0;" })
	resp, err := l.tryRun(action, p)
	if err != nil || resp.GetDone() != 1 || resp.GetAppId() != "proxy:grafana.home.example" {
		t.Fatalf("RunAction = %v, %v, want its one step done", resp, err)
	}
	if d := diff([]string{"PUT /api/nginx/proxy-hosts/3"}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if w := writes(l.fw.Log(), fwLog); len(w) != 0 {
		t.Errorf("pfSense writes = %q, want none", w)
	}
	row := l.npm.ProxyHost(3)
	if row["forward_host"] != "198.18.2.30" || row["http2_support"] != false || row["advanced_config"] != "client_max_body_size 0;" {
		t.Errorf("Proxy Host 3 = %v, want the new upstream, HTTP/2 off and its advanced config kept", row)
	}
	if w := l.app("proxy:grafana.home.example").GetWorkload().GetName(); w != "monitoring" {
		t.Errorf("Grafana's Workload = %q, want monitoring, re-matched", w)
	}
}

// A managed field changed in Nginx Proxy Manager since the Plan refuses
// the save, even one the Operator didn't touch.
func TestADriftedProxyHostFieldRefusesTheSave(t *testing.T) {
	l := newLab(t, labOptions{})
	action := changeProxyHost("proxy:grafana.home.example", grafanaMoved)
	confirmed := l.plan(action)

	l.npm.EditProxyHost(3, func(row map[string]any) { row["block_exploits"] = false })
	npmLog := len(l.npm.Log())
	_, err := l.tryRun(action, confirmed)
	var fresh appspb.Plan
	if err == nil || err.code != "aborted" || !strings.HasPrefix(err.message, "changed since you looked") || !err.detail(&fresh) {
		t.Fatalf("RunAction = %v, want aborted with the new Plan", err)
	}
	if v := fresh.GetSteps()[0].GetValues()[3]; v.GetBefore() != "Websockets · Force SSL · HTTP/2" {
		t.Errorf("the new Plan's Options = %v, want Block Common Exploits off before", v)
	}
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes = %q, want none", w)
	}
}

// A Proxy Host field with Nginx Proxy Manager's toggle rules: no
// certificate turns Force SSL off, and the Plan shows it so; nothing to
// change blocks.
func TestAProxyHostChangeFollowsTheToggleRules(t *testing.T) {
	l := newLab(t, labOptions{})
	plain := &proxyhostpb.ProxyHostFields{ForwardScheme: "http", ForwardHost: "198.18.1.204", ForwardPort: 3000, BlockExploits: true, Websockets: true, ForceSsl: true, Http2: true}
	p := l.plan(changeProxyHost("proxy:grafana.home.example", plain))
	if v := p.GetSteps()[0].GetValues()[2:]; v[0].GetAfter() != "None: plain HTTP" || v[1].GetAfter() != "Block Common Exploits · Websockets" {
		t.Errorf("values = %v, want no certificate, Force SSL and HTTP/2 off", v)
	}

	same := &proxyhostpb.ProxyHostFields{ForwardScheme: "http", ForwardHost: "198.18.1.204", ForwardPort: 3000, CertificateId: 3, BlockExploits: true, Websockets: true, ForceSsl: true, Http2: true}
	if p := l.plan(changeProxyHost("proxy:grafana.home.example", same)); !blockedBy(p, "Nothing to change.") {
		t.Errorf("Plan = %v, want it blocked: nothing to change", p)
	}
}
