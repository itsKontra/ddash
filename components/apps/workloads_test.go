package apps_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// proxmoxFake fakes proxmox-api: the guests the test sets, or an error.
type proxmoxFake struct {
	proxmoxapipb.ProxmoxApiServiceClient

	mu     sync.Mutex
	guests []*proxmoxapipb.Workload
	err    error
}

func (f *proxmoxFake) ListWorkloads(context.Context, *proxmoxapipb.ListWorkloadsRequest, ...grpc.CallOption) (*proxmoxapipb.ListWorkloadsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &proxmoxapipb.ListWorkloadsResponse{Workloads: f.guests}, nil
}

func (f *proxmoxFake) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// dockerFake fakes docker-api: the hosts the test sets, or an error.
type dockerFake struct {
	dockerapipb.DockerApiServiceClient

	mu    sync.Mutex
	hosts []*dockerapipb.HostContainers
	err   error
}

func (f *dockerFake) ListContainers(context.Context, *dockerapipb.ListContainersRequest, ...grpc.CallOption) (*dockerapipb.ListContainersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &dockerapipb.ListContainersResponse{Hosts: f.hosts}, nil
}

func (f *dockerFake) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// guest is a running Proxmox guest with one NIC holding ips.
func guest(kind proxmoxapipb.Kind, vmid uint32, name string, ips ...string) *proxmoxapipb.Workload {
	return &proxmoxapipb.Workload{
		Kind: kind, Vmid: vmid, Node: "pve1", Name: name, Status: "running",
		Nics: []*proxmoxapipb.Nic{{Name: "eth0", Ips: ips}},
	}
}

// container is a running container publishing ports ("8096" on every
// address, "198.18.2.10:8096" on one).
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

// lab is the Workload sources behind a home network: the Docker host
// "media" is itself LXC 110 at 198.18.2.10.
type lab struct {
	proxmox *proxmoxFake
	docker  *dockerFake
}

func newLab() lab {
	return lab{
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
	}
}

func (l lab) options() []sdktest.Option {
	return []sdktest.Option{
		sdktest.WithClient[proxmoxapipb.ProxmoxApiServiceClient](l.proxmox),
		sdktest.WithClient[dockerapipb.DockerApiServiceClient](l.docker),
	}
}

func startInLab(t *testing.T, n *nginx, l lab) started {
	t.Helper()
	return start(t, n, l.options()...)
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
func (s started) workloads(t *testing.T) map[string]*appspb.Workload {
	t.Helper()
	out := map[string]*appspb.Workload{}
	for _, a := range s.apps(t) {
		out[a.GetId()] = a.GetWorkload()
	}
	return out
}

func TestAnAppIsLinkedToTheContainerPublishingItsUpstreamPortOnADockerHost(t *testing.T) {
	// media is both a Docker host and LXC 110: the container wins.
	s := startInLab(t, newNginx(jellyfin), newLab())

	if d := diff(docker("jellyfin"), s.workloads(t)["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
}

func TestAnUpstreamPortNoContainerPublishesIsTheGuestAtThatAddress(t *testing.T) {
	n := newNginx(
		proxy([]string{"portainer.home.example"}, "http://198.18.2.10:9000", "443 ssl"),
		proxy([]string{"paperless.home.example"}, "http://198.18.2.30:8000", "443 ssl"),
		proxy([]string{"home-assistant.home.example"}, "http://198.18.1.41:8123", "443 ssl"),
	)
	s := startInLab(t, n, newLab())

	want := map[string]*appspb.Workload{
		"proxy:portainer.home.example":      lxc("110", "media"),
		"proxy:paperless.home.example":      lxc("120", "paperless"),
		"proxy:home-assistant.home.example": {Ref: ref(workloadpb.Kind_KIND_VM, "pve1", "200"), Name: "homeassistant", State: "running"},
	}
	if d := diff(want, s.workloads(t)); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
}

func TestAPortBoundToOneAddressIsMatchedOnThatAddressOnly(t *testing.T) {
	n := newNginx(
		proxy([]string{"sonarr.home.example"}, "http://198.18.2.11:8989", "443 ssl"),
		proxy([]string{"wrong.home.example"}, "http://198.18.2.10:8989", "443 ssl"),
	)
	s := startInLab(t, n, newLab())

	want := map[string]*appspb.Workload{
		"proxy:sonarr.home.example": docker("sonarr"),
		"proxy:wrong.home.example":  lxc("110", "media"),
	}
	if d := diff(want, s.workloads(t)); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
}

func TestAProxyPassNamingAnUpstreamBlockUsesItsFirstServer(t *testing.T) {
	n := newNginx(grafana)
	n.snap.Upstreams = []*nginxconfigpb.Upstream{{Name: "grafana", Servers: []string{"198.18.2.30:3000", "198.18.2.31:3000"}}}
	s := startInLab(t, n, newLab())

	if d := diff(lxc("120", "paperless"), s.workloads(t)["proxy:grafana.home.example"]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
}

func TestTheDefaultPortIsTheProxyPassSchemes(t *testing.T) {
	l := newLab()
	l.docker.hosts[0].Containers = append(l.docker.hosts[0].Containers, container("caddy", "80", "443"))
	n := newNginx(
		proxy([]string{"web.home.example"}, "http://198.18.2.10", "443 ssl"),
		proxy([]string{"tls.home.example"}, "https://198.18.2.10/app", "443 ssl"),
	)
	s := startInLab(t, n, l)

	want := map[string]*appspb.Workload{
		"proxy:web.home.example": docker("caddy"),
		"proxy:tls.home.example": docker("caddy"),
	}
	if d := diff(want, s.workloads(t)); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
}

func TestAnUpstreamMatchingNothingHasNoWorkload(t *testing.T) {
	n := newNginx(
		proxy([]string{"printer.home.example"}, "http://198.18.1.40", "8080"),
		proxy([]string{"dynamic.home.example"}, "http://$backend", "443 ssl"),
		proxy([]string{"socket.home.example"}, "http://unix:/run/app.sock", "443 ssl"),
	)
	s := startInLab(t, n, newLab())

	for id, w := range s.workloads(t) {
		if w != nil {
			t.Errorf("%s: Workload %v, want none", id, w)
		}
	}
}

func TestAManualAppIsMatchedOnItsURLHost(t *testing.T) {
	s := startInLab(t, newNginx(), newLab())
	created, err := s.c.CreateApp(t.Context(), &appspb.CreateAppRequest{App: &appspb.ManualApp{Name: "Jellyfin direct", Url: "http://198.18.2.10:8096"}})
	if err != nil {
		t.Fatal(err)
	}

	if d := diff(docker("jellyfin"), s.workloads(t)[created.GetApp().GetId()]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
}

func TestAWorkloadOverrideInTheOverlayWins(t *testing.T) {
	s := startInLab(t, newNginx(jellyfin, grafana), newLab())

	s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Workload: ref(workloadpb.Kind_KIND_LXC, "pve1", "120")})
	s.setOverlay(t, &appspb.Overlay{ServerName: "grafana.home.example", Workload: ref(workloadpb.Kind_KIND_EXTERNAL, "198.18.1.50", "")})

	want := map[string]*appspb.Workload{
		"proxy:jellyfin.home.example": {Ref: ref(workloadpb.Kind_KIND_LXC, "pve1", "120"), Name: "paperless", State: "running", Override: true},
		"proxy:grafana.home.example":  {Ref: ref(workloadpb.Kind_KIND_EXTERNAL, "198.18.1.50", ""), Name: "198.18.1.50", Override: true},
	}
	if d := diff(want, s.workloads(t)); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
}

func TestAnOverrideOnlyIsAnOverlay(t *testing.T) {
	s := startInLab(t, newNginx(jellyfin), newLab())
	o := &appspb.Overlay{ServerName: "jellyfin.home.example", Workload: ref(workloadpb.Kind_KIND_LXC, "pve1", "110")}

	s.setOverlay(t, o)

	if d := diff(o, s.app(t, "proxy:jellyfin.home.example").GetOverlay()); d != "" {
		t.Errorf("overlay (-want +got):\n%s", d)
	}
}

func TestAnOverrideOfAWorkloadNoSourceListsKeepsItsReference(t *testing.T) {
	s := startInLab(t, newNginx(jellyfin), newLab())

	s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Workload: ref(workloadpb.Kind_KIND_DOCKER, "media", "gone")})

	want := &appspb.Workload{Ref: ref(workloadpb.Kind_KIND_DOCKER, "media", "gone"), Override: true}
	if d := diff(want, s.workloads(t)["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
}

func TestABadWorkloadOverrideIsRefused(t *testing.T) {
	s := startInLab(t, newNginx(jellyfin), newLab())

	for name, r := range map[string]*workloadpb.WorkloadRef{
		"LXC without a VMID":       ref(workloadpb.Kind_KIND_LXC, "pve1", ""),
		"VM with a name for VMID":  ref(workloadpb.Kind_KIND_VM, "pve1", "homeassistant"),
		"container without a host": ref(workloadpb.Kind_KIND_DOCKER, "", "jellyfin"),
		"external without a host":  ref(workloadpb.Kind_KIND_EXTERNAL, "", ""),
		"unknown kind":             ref(workloadpb.Kind(99), "x", "y"),
	} {
		_, err := s.c.SetOverlay(t.Context(), &appspb.SetOverlayRequest{Overlay: &appspb.Overlay{ServerName: "jellyfin.home.example", Workload: r}})
		if codeOf(err) != codes.InvalidArgument {
			t.Errorf("%s: %v, want InvalidArgument", name, err)
		}
	}
}

func TestAnUnconfiguredWorkloadSourceDegradesAppsAndDropsItsLinks(t *testing.T) {
	l := newLab()
	l.proxmox.fail(status.Error(codes.FailedPrecondition, "not configured"))
	n := newNginx(jellyfin, proxy([]string{"paperless.home.example"}, "http://198.18.2.30:8000", "443 ssl"))
	s := startInLab(t, n, l)

	want := map[string]*appspb.Workload{
		"proxy:jellyfin.home.example":  docker("jellyfin"),
		"proxy:paperless.home.example": nil,
	}
	if d := diff(want, s.workloads(t)); d != "" {
		t.Errorf("Workloads (-want +got):\n%s", d)
	}
	if got := s.h.Health(); got.State != sdk.Degraded || got.Message != "proxmox-api not configured" {
		t.Errorf("health = %v, want degraded naming proxmox-api", got)
	}
}

func TestAnUnavailableWorkloadSourceDegradesAppsAndDropsItsLinks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) { anUnavailableWorkloadSource(t) })
}

func anUnavailableWorkloadSource(t *testing.T) {
	l := newLab()
	l.docker.fail(status.Error(codes.Unavailable, "docker-api is down"))
	s := startInLab(t, newNginx(jellyfin), l)

	if d := diff(lxc("110", "media"), s.workloads(t)["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload without docker-api (-want +got):\n%s", d)
	}
	if got := s.h.Health(); got.State != sdk.Degraded || got.Message != "docker-api unavailable: docker-api is down" {
		t.Errorf("health = %v, want degraded naming docker-api", got)
	}

	l.docker.fail(nil)
	afterPoll()
	if d := diff(docker("jellyfin"), s.workloads(t)["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload once docker-api answers (-want +got):\n%s", d)
	}
	if got := s.h.Health(); got.State != sdk.OK {
		t.Errorf("health once docker-api answers = %v, want ok", got)
	}
}

func TestADockerHostThatCantBeReadDegradesApps(t *testing.T) {
	l := newLab()
	l.docker.hosts = append(l.docker.hosts, &dockerapipb.HostContainers{Host: "build", Address: "198.18.2.40", Error: "connection refused"})
	s := startInLab(t, newNginx(jellyfin), l)

	if d := diff(docker("jellyfin"), s.workloads(t)["proxy:jellyfin.home.example"]); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
	if got := s.h.Health(); got.State != sdk.Degraded || got.Message != "docker-api can't read build: connection refused" {
		t.Errorf("health = %v, want degraded naming the host", got)
	}
}

func TestAppChecksCarryTheirAppsWorkload(t *testing.T) {
	s := startInLab(t, newNginx(jellyfin, printer), newLab())
	s.apps(t)

	want := []*checkspb.AppCheck{
		{AppId: "proxy:jellyfin.home.example", Name: "Jellyfin", Url: "https://jellyfin.home.example", Enabled: true, Workload: ref(workloadpb.Kind_KIND_DOCKER, "media", "jellyfin")},
		appCheck("proxy:printer.home.example", "Printer", "http://printer.home.example:8080", true),
	}
	if d := diff(want, s.checks.last()); d != "" {
		t.Errorf("App Checks (-want +got):\n%s", d)
	}

	s.setOverlay(t, &appspb.Overlay{ServerName: "printer.home.example", Workload: ref(workloadpb.Kind_KIND_EXTERNAL, "198.18.1.40", "")})
	if got := s.checks.last()[1].GetWorkload(); got.GetKind() != workloadpb.Kind_KIND_EXTERNAL {
		t.Errorf("printer's Check Workload after the override: %v, want the external host", got)
	}
}

func TestListWorkloadsOffersEveryContainerAndGuest(t *testing.T) {
	l := newLab()
	l.proxmox.guests = append(l.proxmox.guests, &proxmoxapipb.Workload{Kind: proxmoxapipb.Kind_KIND_VM, Vmid: 9000, Node: "pve1", Name: "debian-template", Template: true, Status: "stopped"})
	s := startInLab(t, newNginx(), l)

	resp, err := s.c.ListWorkloads(t.Context(), &appspb.ListWorkloadsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := &appspb.ListWorkloadsResponse{Workloads: []*appspb.Workload{
		docker("jellyfin"),
		docker("sonarr"),
		lxc("110", "media"),
		lxc("120", "paperless"),
		{Ref: ref(workloadpb.Kind_KIND_VM, "pve1", "200"), Name: "homeassistant", State: "running"},
	}}
	if d := diff(want, resp); d != "" {
		t.Errorf("ListWorkloads (-want +got):\n%s", d)
	}
}
