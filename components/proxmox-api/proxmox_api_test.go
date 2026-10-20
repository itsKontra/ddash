package proxmoxapi_test

import (
	"net"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// proxmox fakes the Proxmox API serving a scenario, and returns its config.
func proxmox(t *testing.T, scenario string) (*pb.ProxmoxApiConfig, *fixture.Fake) {
	srv := fixture.Server(t, "testdata/"+scenario)
	return &pb.ProxmoxApiConfig{Url: srv.URL, TokenId: "ddash@pve!dashboard", TokenSecret: "4c1f9a3e-0000-4000-8000-000000000000"}, srv
}

func start(t *testing.T, cfg *pb.ProxmoxApiConfig) (*sdktest.Harness, pb.ProxmoxApiServiceClient) {
	h := sdktest.New(t, proxmoxapi.Definition())
	h.MustStart(cfg)
	return h, pb.NewProxmoxApiServiceClient(h.Conn)
}

func workloads(t *testing.T, c pb.ProxmoxApiServiceClient) map[uint32]*pb.Workload {
	t.Helper()
	resp, err := c.ListWorkloads(t.Context(), &pb.ListWorkloadsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uint32]*pb.Workload{}
	for _, w := range resp.GetWorkloads() {
		byID[w.GetVmid()] = w
	}
	return byID
}

func TestListsWorkloadsWithStatusAndResources(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	_, c := start(t, cfg)

	resp, err := c.ListWorkloads(t.Context(), &pb.ListWorkloadsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var ids []uint32
	for _, w := range resp.GetWorkloads() {
		ids = append(ids, w.GetVmid())
	}
	if diff := cmp.Diff([]uint32{101, 102, 103, 104, 110, 111, 112}, ids); diff != "" {
		t.Errorf("Workloads by VMID (-want +got):\n%s", diff)
	}
	nginx := workloads(t, c)[101]
	want := &pb.Workload{
		Kind: pb.Kind_KIND_LXC, Vmid: 101, Node: "pve", Name: "nginx", Status: "running",
		Tags: []string{"infra", "web"},
		Cpu:  0.0125, MaxCpu: 2,
		Mem: 268435456, MaxMem: 1073741824,
		Disk: 2147483648, MaxDisk: 8589934592,
		NetIn: 1048576, NetOut: 2097152,
		UptimeSeconds: 86400,
	}
	if diff := cmp.Diff(want, nginx, protocmp.Transform(), protocmp.IgnoreFields(&pb.Workload{}, "nics", "addresses")); diff != "" {
		t.Errorf("nginx (-want +got):\n%s", diff)
	}
	if got := workloads(t, c)[110]; got.GetKind() != pb.Kind_KIND_VM || got.GetName() != "home-assistant" {
		t.Errorf("110 = %v %q, want the VM home-assistant", got.GetKind(), got.GetName())
	}
}

// addresses checks the nics and their source of the Workloads in want.
func addresses(t *testing.T, got map[uint32]*pb.Workload, src pb.AddressSource, want map[uint32][]*pb.Nic) {
	t.Helper()
	for id, nics := range want {
		if diff := cmp.Diff(nics, got[id].GetNics(), protocmp.Transform()); diff != "" {
			t.Errorf("%d nics (-want +got):\n%s", id, diff)
		}
		if got := got[id].GetAddresses(); got != src {
			t.Errorf("%d addresses from %v, want %v", id, got, src)
		}
	}
}

func TestARunningLXCsIPsComeFromEveryAddressOfItsInterfaces(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	_, c := start(t, cfg)

	addresses(t, workloads(t, c), pb.AddressSource_ADDRESS_SOURCE_LIVE, map[uint32][]*pb.Nic{
		// go-proxmox's ContainerInterface would keep only 198.18.1.10.
		101: {{Name: "eth0", Mac: "02:5e:1a:00:01:01", Ips: []string{"198.18.1.10", "198.18.1.11", "2001:db8:1::10"}}},
		102: {
			{Name: "eth0", Mac: "02:5e:1a:00:01:02", Ips: []string{"198.18.2.10"}},
			{Name: "docker0", Mac: "02:5e:1a:00:02:02", Ips: []string{"198.18.200.1"}},
		},
	})
}

func TestARunningVMsIPsComeFromItsGuestAgent(t *testing.T) {
	cfg, srv := proxmox(t, "baseline")
	_, c := start(t, cfg)

	addresses(t, workloads(t, c), pb.AddressSource_ADDRESS_SOURCE_LIVE, map[uint32][]*pb.Nic{
		110: {{Name: "enp0s18", Mac: "02:5e:1a:00:01:10", Ips: []string{"198.18.1.20", "2001:db8:1::20"}}},
	})
	if n := srv.Hits("GET /api2/json/nodes/pve/qemu/110/config"); n != 0 {
		t.Errorf("read 110's config %d times, want none while its agent answers", n)
	}
}

func TestAStoppedWorkloadsIPsComeFromTheStaticIPOrMACInItsConfig(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	_, c := start(t, cfg)

	addresses(t, workloads(t, c), pb.AddressSource_ADDRESS_SOURCE_CONFIG, map[uint32][]*pb.Nic{
		// An LXC's static ip and ip6.
		103: {{Name: "eth0", Mac: "02:5e:1a:00:01:03", Ips: []string{"198.18.1.103", "2001:db8:1::103"}}},
		// An LXC on DHCP: its MAC alone.
		104: {{Name: "eth0", Mac: "02:5e:1a:00:01:04"}},
		// A VM's cloud-init ipconfig0 belongs to its net0.
		111: {{Name: "net0", Mac: "02:5e:1a:00:01:11", Ips: []string{"198.18.1.111"}}},
	})
}

func TestListsDdashsEffectivePrivilegesOnEachWorkload(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	_, c := start(t, cfg)

	got, err := c.ListPermissions(t.Context(), &pb.ListPermissionsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	control := []string{"VM.Audit", "VM.GuestAgent.Audit", "VM.PowerMgmt"}
	audit := []string{"VM.Audit", "VM.GuestAgent.Audit"}
	// 101 and 110 have their own ACL (Protected); the rest inherit /vms.
	want := &pb.ListPermissionsResponse{Workloads: []*pb.WorkloadPermissions{
		{Vmid: 101, Privileges: audit},
		{Vmid: 102, Privileges: control},
		{Vmid: 103, Privileges: control},
		{Vmid: 104, Privileges: control},
		{Vmid: 110, Privileges: audit},
		{Vmid: 111, Privileges: control},
		{Vmid: 112, Privileges: control},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListPermissions (-want +got):\n%s", diff)
	}
}

func TestHealthIsOKWhileTheAPIAnswersWithTheToken(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	h, _ := start(t, cfg)

	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestHealthIsDownWhenTheTokenIsRefused(t *testing.T) {
	cfg, _ := proxmox(t, "unauthorized")
	h, c := start(t, cfg)

	if got := h.Health(); got.State != sdk.Down || got.Message != "Proxmox refused the API token" {
		t.Errorf("health = %v, want down: Proxmox refused the API token", got)
	}
	if _, err := c.ListWorkloads(t.Context(), &pb.ListWorkloadsRequest{}); status.Code(err) != codes.Unavailable {
		t.Errorf("ListWorkloads error = %v, want Unavailable", err)
	}
}

func TestHealthIsDownWhileTheAPIIsUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	l.Close()
	h, _ := start(t, &pb.ProxmoxApiConfig{Url: url, TokenId: "ddash@pve!dashboard", TokenSecret: "x"})

	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "connection refused") {
		t.Errorf("health = %v, want down: connection refused", got)
	}
}

func TestReadsAreCachedBetweenRefreshes(t *testing.T) {
	cfg, srv := proxmox(t, "baseline")
	h, c := start(t, cfg)

	workloads(t, c)
	workloads(t, c)
	h.Health()
	if _, err := c.ListPermissions(t.Context(), &pb.ListPermissionsRequest{}); err != nil {
		t.Fatal(err)
	}

	for _, r := range []string{"GET /api2/json/cluster/resources", "GET /api2/json/nodes/pve/lxc/101/interfaces", "GET /api2/json/nodes/pve/lxc/103/config"} {
		if n := srv.Hits(r); n != 1 {
			t.Errorf("%s: %d times, want once within the TTL", r, n)
		}
	}
}

func TestAWorkloadWhoseAddressesCantBeReadIsListedWithWhy(t *testing.T) {
	cfg, _ := proxmox(t, "config-refused")
	h, c := start(t, cfg)

	got := workloads(t, c)

	if w := got[103]; len(w.GetNics()) != 0 || w.GetAddressesError() != "Proxmox refused the API token" {
		t.Errorf("103: nics %v, addresses_error %q; want none and the refusal", w.GetNics(), w.GetAddressesError())
	}
	if len(got[104].GetNics()) != 1 {
		t.Errorf("104 nics = %v, want its config's", got[104].GetNics())
	}
	if health := h.Health(); health.State != sdk.OK {
		t.Errorf("health = %v, want ok: the API answers", health)
	}
}

func TestStartReportsConfigErrorsByPath(t *testing.T) {
	for _, tc := range []struct {
		cfg  *pb.ProxmoxApiConfig
		want []string
	}{
		{&pb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006", TokenId: "ddash@pve!dashboard", TokenSecret: "s"}, nil},
		{&pb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006/api2/json/", TokenId: "ddash@pve!dashboard", TokenSecret: "s"}, nil},
		{&pb.ProxmoxApiConfig{Url: "ftp://198.18.1.2", TokenId: "ddash@pve", TokenSecret: "s"}, []string{"url", "token_id"}},
		{&pb.ProxmoxApiConfig{Url: "https://", TokenId: "dashboard", TokenSecret: "s"}, []string{"url", "token_id"}},
		{&pb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006", TokenId: "ddash@pve!", TokenSecret: "s"}, []string{"token_id"}},
	} {
		h := sdktest.New(t, proxmoxapi.Definition())

		var paths []string
		for _, fe := range sdk.FieldErrorsOf(h.Start(tc.cfg)) {
			paths = append(paths, fe.Path)
		}
		if diff := cmp.Diff(tc.want, paths); diff != "" {
			t.Errorf("%v: errors at (-want +got):\n%s", tc.cfg, diff)
		}
	}
}

func TestARunningVMWhoseAgentDoesntAnswerFallsBackToItsConfig(t *testing.T) {
	cfg, srv := proxmox(t, "baseline")
	_, c := start(t, cfg)

	got := workloads(t, c)

	addresses(t, got, pb.AddressSource_ADDRESS_SOURCE_CONFIG, map[uint32][]*pb.Nic{
		112: {{Name: "net0", Mac: "02:5e:1a:00:01:12"}, {Name: "net1", Mac: "02:5e:1a:00:02:12"}},
	})
	if n := srv.Hits("GET /api2/json/nodes/pve/qemu/112/agent/network-get-interfaces"); n != 1 {
		t.Errorf("asked 112's agent %d times, want once", n)
	}
	if e := got[112].GetAddressesError(); e != "" {
		t.Errorf("addresses_error = %q, want none: the config answered", e)
	}
}
