package apps_test

import (
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/core/runtime"
)

// While Nginx Proxy Manager can't be read, Apps keep its last good read:
// nothing is removed, the Warnings stay those of that read, and the read
// says since when it fails. Once it answers again, the next sync catches up.
func TestAFailedReadKeepsTheLastGoodRead(t *testing.T) {
	l := newLab(t, labOptions{})
	before := l.list()

	l.npm.DeleteProxyHost(3)
	l.failNPM.Store(true)
	err := l.try("Refresh", &appspb.RefreshRequest{}, nil)
	if err == nil || err.code != "unavailable" || !strings.Contains(err.message, "nginx-proxy-manager-api") {
		t.Errorf("Refresh = %v, want unavailable naming nginx-proxy-manager-api", err)
	}

	after := l.list()
	if d := diff(before.GetApps(), after.GetApps()); d != "" {
		t.Errorf("Apps changed while Nginx Proxy Manager can't be read (-before +after):\n%s", d)
	}
	read := after.GetProxyHosts()
	if read.GetError() == "" || read.GetFailingSince() == nil || !read.GetReadAt().AsTime().Equal(before.GetProxyHosts().GetReadAt().AsTime()) {
		t.Errorf("Proxy Hosts read %v, want the error, since when, and the last good read's time", read)
	}

	l.failNPM.Store(false)
	l.unregister("grafana.home.example")
	l.refresh()
	if l.has("proxy:grafana.home.example") {
		t.Error("Grafana is still listed once Nginx Proxy Manager answers again")
	}
	if read := l.list().GetProxyHosts(); read.GetError() != "" || read.GetFailingSince() != nil {
		t.Errorf("Proxy Hosts read %v, want no error", read)
	}
}

// Until nginx-proxy-manager-api is configured, the read says so, for the
// "Set up Nginx Proxy Manager" Notice.
func TestTheReadSaysWhenNginxProxyManagerIsNotConfigured(t *testing.T) {
	l := newLab(t, labOptions{npmUnconfigured: true})

	read := l.list().GetProxyHosts()
	if !read.GetNotConfigured() || read.GetReadAt() != nil || read.GetError() == "" {
		t.Errorf("Proxy Hosts read %v, want not configured, with its error and no good read", read)
	}
}

// With Proxy Hosts set to View for ddash's user, the read says the Proxy
// Hosts are read-only, for the Notice blocking the writes; reads go on.
func TestTheReadSaysWhenProxyHostsAreReadOnly(t *testing.T) {
	npm, failNPM := newNPM(t)
	npm.EditUser(func(u *npmfake.User) { u.ProxyHosts = "view" })
	l := newLab(t, labOptions{npm: npm, failNPM: failNPM})

	resp := l.list()
	if read := resp.GetProxyHosts(); !read.GetReadOnly() || read.GetError() != "" {
		t.Errorf("Proxy Hosts read %v, want read-only without an error", read)
	}
	if !l.has("proxy:grafana.home.example") {
		t.Error("Grafana isn't listed while the Proxy Hosts are read-only")
	}
	if l := newLab(t, labOptions{}); l.list().GetProxyHosts().GetReadOnly() {
		t.Error("Proxy Hosts read read-only with Manage")
	}
}

// Before the first good read since apps started, the Apps are their
// records as they were, without a Warning needing Nginx Proxy Manager,
// and apps is degraded, naming it.
func TestBeforeTheFirstGoodReadNoWarningNeedingItIsDerived(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Name: "Graphs"}}, nil)
	l.npm.EditProxyHost(5, noCert)
	l.npm.DeleteProxyHost(3)
	l.failNPM.Store(true)

	// Restarted, nginx-proxy-manager-api has no cached read either.
	for _, c := range []string{"nginx-proxy-manager-api", "apps"} {
		if err := l.h.Restart(c); err != nil {
			t.Fatal(err)
		}
	}
	l.h.WaitState("apps", runtime.Degraded)
	if msg := l.h.Status("apps").Message; !strings.Contains(msg, "nginx-proxy-manager-api") {
		t.Errorf("health %q, want it to name nginx-proxy-manager-api", msg)
	}

	list := l.list()
	if len(list.GetApps()) != 28 {
		t.Errorf("%d Apps, want all 28 records", len(list.GetApps()))
	}
	for _, a := range list.GetApps() {
		if len(a.GetWarnings()) > 0 || a.GetOrphaned() || (a.GetLinks().GetProxyHostId() != 0) != (a.GetReach() == appspb.Reach_REACH_PROXIED) {
			t.Errorf("%s = %v, want proxied when it was, without Warnings", a.GetId(), a)
		}
	}
	if read := list.GetProxyHosts(); read.GetReadAt() != nil || read.GetError() == "" {
		t.Errorf("Proxy Hosts read %v, want no good read and the error", read)
	}
}
