package apps_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

const (
	noWorkload = appspb.WarningKind_WARNING_KIND_NO_WORKLOAD
	noTLS      = appspb.WarningKind_WARNING_KIND_NO_TLS
	offline    = appspb.WarningKind_WARNING_KIND_PROXY_HOST_OFFLINE
	urlNotOn   = appspb.WarningKind_WARNING_KIND_URL_NOT_ON_PROXY_HOST
	orphaned   = appspb.WarningKind_WARNING_KIND_ORPHANED
)

func warning(kind appspb.WarningKind, ignored bool, detail string, fixes ...*appspb.Fix) *appspb.Warning {
	return &appspb.Warning{Kind: kind, Ignored: ignored, Detail: detail, Fixes: fixes}
}

// fixed is a fix of kind, acting on what its Fix fields say.
func fixed(kind appspb.FixKind, on ...func(*appspb.Fix)) *appspb.Fix {
	f := &appspb.Fix{Kind: kind}
	for _, o := range on {
		o(f)
	}
	return f
}

func onName(n string) func(*appspb.Fix) { return func(f *appspb.Fix) { f.Hostname = n } }

func onProxyHost(id int64) func(*appspb.Fix) { return func(f *appspb.Fix) { f.ProxyHostId = id } }

func onApp(id string) func(*appspb.Fix) { return func(f *appspb.Fix) { f.AppId = id } }

// The fixes of the kinds about the Proxy Host and the Workload.
var (
	setOverride = fixed(appspb.FixKind_FIX_KIND_SET_WORKLOAD_OVERRIDE)
	pickCert    = fixed(appspb.FixKind_FIX_KIND_PICK_CERTIFICATE)
	deleteApp   = fixed(appspb.FixKind_FIX_KIND_DELETE_APP)
)

// Every sync derives each App's Warnings from its Proxy Host and its
// Workload, in kind order, each with its fixes. A disabled Proxy Host
// warns of nothing: its App says so.
func TestWarningsAreDerivedFromTheProxyHost(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	const emerg = `nginx: [emerg] unknown directive "proxy_pas" in /data/nginx/proxy_host/3.conf:11`
	l.npm.EditProxyHost(3, func(row map[string]any) {
		noCert(row)
		row["meta"] = map[string]any{"nginx_online": false, "nginx_err": emerg}
		row["domain_names"] = []string{"graphs.home.example"}
	})
	l.unregister("grafana.home.example")
	l.register("graphs.home.example")
	l.npm.EditProxyHost(4, func(row map[string]any) { row["enabled"] = false })
	l.refresh()

	got := map[string][]*appspb.Warning{}
	for _, a := range l.list().GetApps() {
		if len(a.GetWarnings()) > 0 {
			got[a.GetId()] = a.GetWarnings()
		}
	}
	// No Workload where the fixtures list none at the upstream; the
	// Operator's home.example is in no Host Override.
	want := map[string][]*appspb.Warning{
		"proxy:backup.home.example":  {warning(noWorkload, false, "", setOverride)},
		"proxy:fritz.home.example":   {warning(noWorkload, false, "", setOverride)},
		"proxy:ha.home.example":      {warning(noWorkload, false, "", setOverride)},
		"proxy:nodered.home.example": {warning(noWorkload, false, "", setOverride)},
		"proxy:pv.home.example":      {warning(noWorkload, false, "", setOverride)},
		"proxy:pve.home.example":     {warning(noWorkload, false, "", setOverride)},
		"proxy:wlan0.home.example":   {warning(noWorkload, false, "", setOverride)},
		"proxy:wlan1.home.example":   {warning(noWorkload, false, "", setOverride)},
		"proxy:grafana.home.example": {
			warning(noTLS, false, "", pickCert),
			warning(offline, false, emerg, fixed(appspb.FixKind_FIX_KIND_SAVE_AGAIN, onProxyHost(3)), deleteApp),
			warning(urlNotOn, false, "", fixed(appspb.FixKind_FIX_KIND_USE_AS_URL, onName("graphs.home.example"))),
		},
		"proxy:home.example": {notRegistered("home.example")},
	}
	if d := diff(want, got); d != "" {
		t.Errorf("Warnings by App (-want +got):\n%s", d)
	}
	if home := l.app("proxy:home.home.example").GetProxyHost(); home.GetEnabled() {
		t.Errorf("home's Proxy Host = %v, want it disabled", home)
	}
}

// An Ignore lasts per App and kind until it is un-ignored: the Warning is
// still listed, flagged Ignored.
func TestAWarningIsIgnoredUntilUnignored(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	l.npm.EditProxyHost(11, noCert) // backup
	l.refresh()

	l.call("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:backup.home.example", Kind: noTLS}, nil)
	want := []*appspb.Warning{warning(noWorkload, false, "", setOverride), warning(noTLS, true, "", pickCert)}
	if d := diff(want, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings once No TLS is Ignored (-want +got):\n%s", d)
	}
	l.refresh()
	if d := diff(want, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings after a sync (-want +got):\n%s", d)
	}

	l.call("UnignoreWarning", &appspb.UnignoreWarningRequest{Id: "proxy:backup.home.example", Kind: noTLS}, nil)
	want = []*appspb.Warning{warning(noWorkload, false, "", setOverride), warning(noTLS, false, "", pickCert)}
	if d := diff(want, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings once un-ignored (-want +got):\n%s", d)
	}

	if err := l.try("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:backup.home.example"}, nil); err == nil || err.code != "invalid_argument" {
		t.Errorf("Ignoring no kind: %v, want invalid_argument", err)
	}
	if err := l.try("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:nowhere", Kind: noTLS}, nil); err == nil || err.code != "not_found" {
		t.Errorf("Ignoring on no App: %v, want not_found", err)
	}
}

// An Ignore touches the App: its record is kept when its Proxy Host goes,
// orphaned with the Ignore.
func TestAnIgnoreTouchesTheApp(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	l.call("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:backup.home.example", Kind: noWorkload}, nil)
	l.call("UnignoreWarning", &appspb.UnignoreWarningRequest{Id: "proxy:backup.home.example", Kind: noWorkload}, nil)

	l.npm.DeleteProxyHost(11)
	l.unregister("backup.home.example")
	l.refresh()

	orphanFixes := []*appspb.Fix{fixed(appspb.FixKind_FIX_KIND_REATTACH), fixed(appspb.FixKind_FIX_KIND_DELETE_ORPHAN)}
	if d := diff([]*appspb.Warning{warning(orphaned, false, "", orphanFixes...)}, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("backup's Warnings (-want +got):\n%s", d)
	}
}
