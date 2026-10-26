package apps_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/runtime"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// byURL is the App opening at url; it fails the test without exactly one.
func (l *lab) byURL(url string) *appspb.App {
	l.t.Helper()
	var found []*appspb.App
	for _, a := range l.list().GetApps() {
		if a.GetUrl() == url {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		l.t.Fatalf("%d Apps open at %s, want one", len(found), url)
	}
	return found[0]
}

// appsNamed are the ids of the Apps with the hostname n as their URL's or as an
// alias.
func appsNamed(apps []*appspb.App, n string) []string {
	var ids []string
	for _, a := range apps {
		if a.GetUrl() == "https://"+n || a.GetUrl() == "http://"+n || slices.Contains(a.GetAliases(), n) {
			ids = append(ids, a.GetId())
		}
	}
	return ids
}

// In the Operator's layout every alias of the Reverse Proxy's Host
// Override names a Proxy Host's App, which stays proxied, and each of the
// five other Host Overrides is a Not-proxied App of its own, linked to
// it. The Reverse Proxy's own name is an App only as a name a Proxy Host
// serves.
func TestTheOperatorsHostOverridesAreApps(t *testing.T) {
	l := newLab(t, labOptions{})
	list := l.list()

	proxied, notProxied := 0, map[string]*appspb.App{}
	for _, a := range list.GetApps() {
		switch a.GetReach() {
		case appspb.Reach_REACH_PROXIED:
			proxied++
			if a.GetLinks().GetProxyHostId() == 0 || a.GetLinks().GetHostOverride() != nil {
				t.Errorf("proxied %s links %v, want its Proxy Host only", a.GetId(), a.GetLinks())
			}
		case appspb.Reach_REACH_NOT_PROXIED:
			notProxied[a.GetUrl()] = a
		default:
			t.Errorf("%s is %v", a.GetId(), a.GetReach())
		}
	}
	if proxied != 23 {
		t.Errorf("%d proxied Apps, want one per Proxy Host (23)", proxied)
	}
	want := map[string]string{
		"https://cloud.home.example": "198.18.1.209", "https://docker.home.example": "198.18.1.201", "https://nas.home.example": "198.18.2.10",
		"https://pfsense.home.example": "198.18.1.1", "https://proxmox.home.example": "198.18.1.200",
	}
	for url, ip := range want {
		a := notProxied[url]
		if a == nil {
			t.Errorf("no Not-proxied App at %s", url)
			continue
		}
		host := a.GetLinks().GetHostOverride()
		if "https://"+host.GetHost()+"."+host.GetDomain() != url || a.GetLinks().GetProxyHostId() != 0 {
			t.Errorf("%s links %v, want its Host Override", url, a.GetLinks())
		}
		if len(a.GetDns()) != 1 || !slices.Equal(a.GetDns()[0].GetIps(), []string{ip}) {
			t.Errorf("%s's DNS = %v, want its Host Override at %s", url, a.GetDns(), ip)
		}
	}
	if len(notProxied) != len(want) {
		t.Errorf("%d Not-proxied Apps, want %d", len(notProxied), len(want))
	}
	if ids := appsNamed(list.GetApps(), "nginx.home.example"); !slices.Equal(ids, []string{"proxy:home.example"}) {
		t.Errorf("Apps named nginx.home.example: %v, want only the Proxy Host serving it", ids)
	}
	if rp := list.GetReverseProxy(); rp.GetAddress() != rpAddress || !slices.Equal(rp.GetHostOverrides(), []string{"nginx.home.example"}) || rp.GetNotice() != "" {
		t.Errorf("Reverse Proxy = %v, want nginx.home.example at %s, no notice", rp, rpAddress)
	}

	l.npm.DeleteProxyHost(1)
	l.refresh()
	if ids := appsNamed(l.list().GetApps(), "nginx.home.example"); len(ids) != 0 {
		t.Errorf("Apps named nginx.home.example once no Proxy Host serves it: %v, want none", ids)
	}
}

// An alias at the Reverse Proxy's address is a proxied App's hostname: a
// new one no Proxy Host serves is a proxied App of its own.
func TestANewAliasOfTheReverseProxyIsAProxiedApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			if all[i].Host == "nginx" {
				all[i].Aliases = append(all[i].Aliases, pfsensefake.HostOverrideAlias{Host: "wiki", Domain: "home.example"})
			}
		}
		return all
	})
	l.refresh()

	wiki := l.byURL("https://wiki.home.example")
	if wiki.GetReach() != appspb.Reach_REACH_PROXIED || wiki.GetLinks().GetProxyHostId() != 0 || wiki.GetLinks().GetHostOverride() != nil {
		t.Errorf("wiki = %v, want proxied without links", wiki)
	}
}

// With no Host Override at the Reverse Proxy's address, or two, ListApps
// carries the Notice naming them, and the Apps are still read.
func TestNoneOrTwoHostOverridesAtTheReverseProxyRaiseTheNotice(t *testing.T) {
	l := newLab(t, labOptions{})

	l.addOverride("old", rpAddress)
	l.refresh()
	list := l.list()
	want := &appspb.ReverseProxy{
		Address:       rpAddress,
		HostOverrides: []string{"nginx.home.example", "old.home.example"},
		Notice:        "2 Host Overrides point at the Reverse Proxy (127.0.0.1): nginx.home.example, old.home.example. Keep one to add proxied Apps.",
	}
	if d := diff(want, list.GetReverseProxy()); d != "" {
		t.Errorf("Reverse Proxy (-want +got):\n%s", d)
	}
	if len(appsNamed(list.GetApps(), "old.home.example")) != 0 || l.byURL("https://grafana.home.example").GetReach() != appspb.Reach_REACH_PROXIED {
		t.Error("with two, want the Reverse Proxy's own names no Apps, and the proxied Apps read")
	}

	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			if all[i].IPs[0] == rpAddress {
				all[i].IPs = []string{"198.18.1.210"}
			}
		}
		return all
	})
	l.refresh()
	list = l.list()
	want = &appspb.ReverseProxy{
		Address: rpAddress,
		Notice:  "No Host Override points at the Reverse Proxy (127.0.0.1): create one to add proxied Apps.",
	}
	if d := diff(want, list.GetReverseProxy()); d != "" {
		t.Errorf("Reverse Proxy (-want +got):\n%s", d)
	}
	if len(list.GetApps()) < 28 || l.app("proxy:grafana.home.example").GetProxyHost() == nil {
		t.Errorf("with none, %d Apps, want them all read", len(list.GetApps()))
	}
}

// A Link-only App absorbs a new Host Override for its hostname: it is Not
// proxied from then on, under its id.
func TestALinkOnlyAppAbsorbsAHostOverrideForItsHostname(t *testing.T) {
	l := newLab(t, labOptions{})
	id := l.addLinkOnly("Printer", "https://printer.home.example")
	before := len(l.list().GetApps())

	l.addOverride("printer", "198.18.1.60", "scan")
	l.refresh()

	got := l.app(id)
	if got.GetReach() != appspb.Reach_REACH_NOT_PROXIED || got.GetLinks().GetHostOverride().GetHost() != "printer" || got.GetName() != "Printer" {
		t.Errorf("Printer = %v, want Not proxied by its Host Override, its name kept", got)
	}
	if d := diff([]string{"scan.home.example"}, got.GetAliases()); d != "" {
		t.Errorf("aliases (-want +got):\n%s", d)
	}
	if n := len(l.list().GetApps()); n != before {
		t.Errorf("%d Apps, want %d: no other for the new Host Override", n, before)
	}
}

// A Not-proxied App's Workload is matched at its Host Override's address
// and its URL's port (443 for https): the container publishing it on a
// Docker host there, else the guest with the address, else none, which
// isn't warned of.
func TestANotProxiedAppsWorkloadIsMatchedAtItsAddress(t *testing.T) {
	docker := &dockerFake{hosts: []*dockerapipb.HostContainers{{
		Host: "docker", Address: "198.18.1.201",
		Containers: []*dockerapipb.Container{container("portainer", "443"), container("homepage", "3000")},
	}}}
	proxmox := &proxmoxFake{guests: []*proxmoxapipb.Workload{guest(proxmoxapipb.Kind_KIND_LXC, 201, "docker-host", "198.18.1.201")}}
	l := newLab(t, labOptions{docker: docker, proxmox: proxmox})
	app := func() *appspb.App { return l.byURL("https://docker.home.example") }

	if d := diff(&appspb.Workload{Ref: ref(workloadpb.Kind_KIND_DOCKER, "docker", "portainer"), Name: "portainer", State: "running"}, app().GetWorkload()); d != "" {
		t.Errorf("Workload with a container on 443 (-want +got):\n%s", d)
	}

	docker.mu.Lock()
	docker.hosts[0].Containers = docker.hosts[0].Containers[1:]
	docker.mu.Unlock()
	l.refresh()
	if d := diff(lxc("201", "docker-host"), app().GetWorkload()); d != "" {
		t.Errorf("Workload with none on 443 (-want +got):\n%s", d)
	}

	proxmox.mu.Lock()
	proxmox.guests = nil
	proxmox.mu.Unlock()
	l.refresh()
	if got := app(); got.GetWorkload() != nil || len(got.GetWarnings()) != 0 {
		t.Errorf("docker = %v, want no Workload and no Warning", got)
	}
}

// A touched Not-proxied App whose Host Override goes (renamed in pfSense's
// UI, which breaks its link) is orphaned App settings, reattached to the
// new name's App with its id and settings.
func TestANotProxiedOrphanIsReattachedToAHostOverride(t *testing.T) {
	l := newLab(t, labOptions{})
	nas := l.byURL("https://nas.home.example").GetId()
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: nas, Settings: &appspb.AppSettings{Name: "Storage"}}, nil)

	l.unregister("nas.home.example")
	l.addOverride("files", "198.18.2.10")
	l.refresh()
	if got := l.app(nas); !got.GetOrphaned() || got.GetLinks().GetHostOverride() != nil {
		t.Errorf("nas = %v, want orphaned App settings", got)
	}

	files := l.byURL("https://files.home.example").GetId()
	l.call("Reattach", &appspb.ReattachRequest{Id: nas, To: files}, nil)
	got := l.app(nas)
	if got.GetName() != "Storage" || got.GetUrl() != "https://files.home.example" || got.GetReach() != appspb.Reach_REACH_NOT_PROXIED || got.GetLinks().GetHostOverride().GetHost() != "files" {
		t.Errorf("reattached = %v, want Storage at files, linked to its Host Override", got)
	}
	if l.has(files) {
		t.Error("the App reattached to is still listed")
	}
}

// Refresh reads both sources afresh: a change in pfSense shows at once,
// although pfsense-api caches its reads.
func TestRefreshReadsPfSenseAfresh(t *testing.T) {
	l := newLab(t, labOptions{})
	l.addOverride("printer", "198.18.1.60")
	l.refresh()
	if got := l.byURL("https://printer.home.example"); got.GetReach() != appspb.Reach_REACH_NOT_PROXIED {
		t.Errorf("printer = %v, want Not proxied", got)
	}
}

// While pfSense can't be read, Apps keep its last good read: nothing is
// removed, the read says since when it fails, and apps is degraded naming
// pfsense-api. Once it answers again, the next sync catches up.
func TestAFailedPfSenseReadKeepsTheLastGoodRead(t *testing.T) {
	l := newLab(t, labOptions{})
	before := l.list()

	l.unregister("nas.home.example")
	l.failPfSense.Store(true)
	err := l.try("Refresh", &appspb.RefreshRequest{}, nil)
	if err == nil || err.code != "unavailable" || !strings.Contains(err.message, "pfsense-api") {
		t.Errorf("Refresh = %v, want unavailable naming pfsense-api", err)
	}
	after := l.list()
	if d := diff(before.GetApps(), after.GetApps()); d != "" {
		t.Errorf("Apps changed while pfSense can't be read (-before +after):\n%s", d)
	}
	read := after.GetHostOverrides()
	if read.GetError() == "" || read.GetFailingSince() == nil || !read.GetReadAt().AsTime().Equal(before.GetHostOverrides().GetReadAt().AsTime()) {
		t.Errorf("Host Overrides read %v, want the error, since when, and the last good read's time", read)
	}

	// Restarted (pfsense-api with no cached read either), apps reads its
	// health afresh.
	for _, c := range []string{"pfsense-api", "apps"} {
		if err := l.h.Restart(c); err != nil {
			t.Fatal(err)
		}
	}
	l.h.WaitState("apps", runtime.Degraded)
	if msg := l.h.Status("apps").Message; !strings.Contains(msg, "pfsense-api") {
		t.Errorf("health %q, want it to name pfsense-api", msg)
	}
	if n := len(l.list().GetApps()); n != len(before.GetApps()) {
		t.Errorf("%d Apps before pfSense was first read since the restart, want all %d records", n, len(before.GetApps()))
	}

	l.failPfSense.Store(false)
	l.refresh()
	if len(appsNamed(l.list().GetApps(), "nas.home.example")) != 0 {
		t.Error("nas is still listed once pfSense answers again")
	}
	if read := l.list().GetHostOverrides(); read.GetError() != "" || read.GetFailingSince() != nil {
		t.Errorf("Host Overrides read %v, want no error", read)
	}
}
