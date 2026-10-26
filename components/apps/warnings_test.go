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

func warning(kind appspb.WarningKind, ignored bool, detail string) *appspb.Warning {
	return &appspb.Warning{Kind: kind, Ignored: ignored, Detail: detail}
}

// Every sync derives each App's Warnings from its Proxy Host and its
// Workload, in kind order. A disabled Proxy Host warns of nothing: its App
// says so.
func TestWarningsAreDerivedFromTheProxyHost(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	const emerg = `nginx: [emerg] unknown directive "proxy_pas" in /data/nginx/proxy_host/3.conf:11`
	l.npm.EditProxyHost(3, func(row map[string]any) {
		noCert(row)
		row["meta"] = map[string]any{"nginx_online": false, "nginx_err": emerg}
		row["domain_names"] = []string{"graphs.home.example"}
	})
	l.npm.EditProxyHost(4, func(row map[string]any) { row["enabled"] = false })
	l.refresh()

	got := map[string][]*appspb.Warning{}
	for _, a := range l.list().GetApps() {
		if len(a.GetWarnings()) > 0 {
			got[a.GetId()] = a.GetWarnings()
		}
	}
	// No Workload where the fixtures list none at the upstream.
	want := map[string][]*appspb.Warning{
		"proxy:backup.home.example":  {warning(noWorkload, false, "")},
		"proxy:fritz.home.example":   {warning(noWorkload, false, "")},
		"proxy:ha.home.example":      {warning(noWorkload, false, "")},
		"proxy:nodered.home.example": {warning(noWorkload, false, "")},
		"proxy:pv.home.example":      {warning(noWorkload, false, "")},
		"proxy:pve.home.example":     {warning(noWorkload, false, "")},
		"proxy:wlan0.home.example":   {warning(noWorkload, false, "")},
		"proxy:wlan1.home.example":   {warning(noWorkload, false, "")},
		"proxy:grafana.home.example": {warning(noTLS, false, ""), warning(offline, false, emerg), warning(urlNotOn, false, "")},
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
	want := []*appspb.Warning{warning(noWorkload, false, ""), warning(noTLS, true, "")}
	if d := diff(want, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings once No TLS is Ignored (-want +got):\n%s", d)
	}
	l.refresh()
	if d := diff(want, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings after a sync (-want +got):\n%s", d)
	}

	l.call("UnignoreWarning", &appspb.UnignoreWarningRequest{Id: "proxy:backup.home.example", Kind: noTLS}, nil)
	want = []*appspb.Warning{warning(noWorkload, false, ""), warning(noTLS, false, "")}
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
	l.refresh()

	if d := diff([]*appspb.Warning{warning(orphaned, false, "")}, l.app("proxy:backup.home.example").GetWarnings()); d != "" {
		t.Errorf("backup's Warnings (-want +got):\n%s", d)
	}
}
