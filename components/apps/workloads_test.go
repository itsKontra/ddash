package apps_test

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/runtime"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// guest is a running Proxmox guest with one NIC holding ips.
func guest(kind proxmoxapipb.Kind, vmid uint32, name string, ips ...string) *proxmoxapipb.Workload {
	return &proxmoxapipb.Workload{
		Kind: kind, Vmid: vmid, Node: "pve1", Name: name, Status: "running",
		Nics: []*proxmoxapipb.Nic{{Name: "eth0", Ips: ips}},
	}
}

// container is a running container publishing ports ("8096" on every
// address, "198.18.2.11:8989" on one).
func container(name string, ports ...string) *dockerapipb.Container {
	c := &dockerapipb.Container{Id: name + "-id", Name: name, State: "running"}
	for _, p := range ports {
		ip, port := "0.0.0.0", p
		if i := strings.LastIndex(p, ":"); i >= 0 {
			ip, port = strings.Trim(p[:i], "[]"), p[i+1:]
		}
		n := uint32(0)
		for _, r := range port {
			n = n*10 + uint32(r-'0')
		}
		c.Ports = append(c.Ports, &dockerapipb.Port{Ip: ip, PublicPort: n, PrivatePort: n, Protocol: "tcp"})
	}
	return c
}

// workloadLab is a home network's Workloads: the Docker host "media" is
// itself LXC 110 at 198.18.2.10; Nginx Proxy Manager has no Proxy Host
// to begin with.
func workloadLab(t *testing.T) *lab {
	return newLab(t, labOptions{
		noProxyHosts: true,
		proxmox: &proxmoxFake{guests: []*proxmoxapipb.Workload{
			guest(proxmoxapipb.Kind_KIND_LXC, 110, "media", "198.18.2.10"),
			guest(proxmoxapipb.Kind_KIND_LXC, 120, "paperless", "198.18.2.30"),
			guest(proxmoxapipb.Kind_KIND_VM, 200, "homeassistant", "198.18.1.41"),
		}},
		docker: &dockerFake{hosts: []*dockerapipb.HostContainers{{
			Host: "media", Address: "198.18.2.10",
			Containers: []*dockerapipb.Container{
				container("jellyfin", "8096", "[::]:8096"),
				container("sonarr", "198.18.2.11:8989"),
			},
		}}},
	})
}

func ref(kind workloadpb.Kind, host, id string) *workloadpb.WorkloadRef {
	return &workloadpb.WorkloadRef{Kind: kind, Host: host, Id: id}
}

func lxc(vmid, name string) *appspb.Workload {
	return &appspb.Workload{Ref: ref(workloadpb.Kind_KIND_LXC, "pve1", vmid), Name: name, State: "running"}
}

func docker(name string) *appspb.Workload {
	return &appspb.Workload{Ref: ref(workloadpb.Kind_KIND_DOCKER, "media", name), Name: name, State: "running"}
}

// workloads is each App's Workload by App id; nil for none.
func (l *lab) workloads() map[string]*appspb.Workload {
	l.t.Helper()
	out := map[string]*appspb.Workload{}
	for _, a := range l.list().GetApps() {
		out[a.GetId()] = a.GetWorkload()
	}
	return out
}

// A proxied App's Workload is matched at its Proxy Host's upstream: the
// container publishing the upstream's port on a Docker host at that
// address (a Docker host is often a guest itself: the container wins),
// else the guest with that address; a port bound to one address matches
// there only. An upstream matching nothing has no Workload, and warns.
func TestAProxiedAppsWorkloadIsMatchedAtItsUpstream(t *testing.T) {
	l := workloadLab(t)
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8096", "jellyfin.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8282", "download.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8989", "series.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.2.11:8989", "sonarr.home.example"))
	l.npm.AddProxyHost(proxyHost("https://198.18.1.41:8123", "ha.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.2.99:80", "gone.home.example"))
	l.refresh()

	want := map[string]*appspb.Workload{
		"proxy:jellyfin.home.example": docker("jellyfin"),
		"proxy:download.home.example": lxc("110", "media"),
		"proxy:series.home.example":   lxc("110", "media"),
		"proxy:sonarr.home.example":   docker("sonarr"),
		"proxy:ha.home.example":       {Ref: ref(workloadpb.Kind_KIND_VM, "pve1", "200"), Name: "homeassistant", State: "running"},
		"proxy:gone.home.example":     nil,
	}
	if d := diff(want, l.workloads()); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
	if d := diff([]appspb.WarningKind{noWorkload}, kinds(l.app("proxy:gone.home.example"))); d != "" {
		t.Errorf("gone's Warnings (-want +got):\n%s", d)
	}

	// A new upstream re-matches.
	l.npm.EditProxyHost(int(l.app("proxy:gone.home.example").GetLinks().GetProxyHostId()), proxyHost("http://198.18.2.30:8000", "gone.home.example"))
	l.refresh()
	if d := diff(lxc("120", "paperless"), l.workloads()["proxy:gone.home.example"]); d != "" {
		t.Errorf("Workload at the new upstream (-want +got):\n%s", d)
	}
}

// A Link-only App is matched on its URL's host and port, and never warns
// of having no Workload.
func TestALinkOnlyAppIsMatchedOnItsURL(t *testing.T) {
	l := workloadLab(t)
	var a, b appspb.CreateAppResponse
	l.call("CreateApp", &appspb.CreateAppRequest{App: &appspb.LinkOnlyApp{Name: "Jellyfin", Url: "http://198.18.2.10:8096"}}, &a)
	l.call("CreateApp", &appspb.CreateAppRequest{App: &appspb.LinkOnlyApp{Name: "Printer", Url: "https://198.18.1.60"}}, &b)

	want := map[string]*appspb.Workload{a.GetApp().GetId(): docker("jellyfin"), b.GetApp().GetId(): nil}
	if d := diff(want, l.workloads()); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
	if w := l.app(b.GetApp().GetId()).GetWarnings(); len(w) != 0 {
		t.Errorf("Printer's Warnings = %v, want none", w)
	}
}

// The override wins over the match, and is kept even while no source
// lists it.
func TestAWorkloadOverrideWins(t *testing.T) {
	l := workloadLab(t)
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8096", "jellyfin.home.example"))
	l.refresh()

	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:jellyfin.home.example", Settings: &appspb.AppSettings{Workload: ref(workloadpb.Kind_KIND_LXC, "", "120")}}, nil)
	want := lxc("120", "paperless")
	want.Override = true
	if d := diff(want, l.workloads()["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}

	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:jellyfin.home.example", Settings: &appspb.AppSettings{Workload: ref(workloadpb.Kind_KIND_LXC, "", "999")}}, nil)
	want = &appspb.Workload{Ref: ref(workloadpb.Kind_KIND_LXC, "", "999"), Override: true}
	if d := diff(want, l.workloads()["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("an override no source lists (-want +got):\n%s", d)
	}
}

// App Checks name their App's Workload.
func TestAppChecksCarryTheirAppsWorkload(t *testing.T) {
	l := workloadLab(t)
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8096", "jellyfin.home.example"))
	l.refresh()

	got := l.appChecks()["proxy:jellyfin.home.example"].GetWorkload()
	if d := diff(ref(workloadpb.Kind_KIND_DOCKER, "media", "jellyfin"), got); d != "" {
		t.Errorf("the Check's Workload (-want +got):\n%s", d)
	}
}

// While a Workload source can't be read, apps is degraded naming it, its
// Workloads are no App's, and no App warns of having none.
func TestAnUnavailableWorkloadSourceDegradesApps(t *testing.T) {
	l := workloadLab(t)
	l.npm.AddProxyHost(proxyHost("http://198.18.1.41:8123", "ha.home.example"))
	l.refresh()

	l.proxmox.fail(status.Error(codes.Unavailable, "proxmox is down"))
	if err := l.h.Restart("apps"); err != nil {
		t.Fatal(err)
	}
	l.h.WaitState("apps", runtime.Degraded)
	if msg := l.h.Status("apps").Message; msg != "proxmox-api unavailable: proxmox is down" {
		t.Errorf("health %q, want proxmox-api named", msg)
	}
	ha := l.app("proxy:ha.home.example")
	if ha.GetWorkload() != nil || len(ha.GetWarnings()) != 0 {
		t.Errorf("ha = %v, want no Workload and no Warning", ha)
	}
}

func TestListWorkloadsOffersEveryContainerAndGuest(t *testing.T) {
	l := workloadLab(t)
	var resp appspb.ListWorkloadsResponse
	l.call("ListWorkloads", &appspb.ListWorkloadsRequest{}, &resp)

	want := []*appspb.Workload{
		docker("jellyfin"), docker("sonarr"),
		lxc("110", "media"), lxc("120", "paperless"),
		{Ref: ref(workloadpb.Kind_KIND_VM, "pve1", "200"), Name: "homeassistant", State: "running"},
	}
	if d := diff(want, resp.GetWorkloads()); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
}
