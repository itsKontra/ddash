package proxmoxapi_test

import (
	"net"
	"slices"
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
	if diff := cmp.Diff([]uint32{100, 101, 102, 122, 201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212}, ids); diff != "" {
		t.Errorf("Workloads by VMID (-want +got):\n%s", diff)
	}
	media := workloads(t, c)[212]
	want := &pb.Workload{
		Kind: pb.Kind_KIND_LXC, Vmid: 212, Node: "pve", Name: "media-stack", Status: "running",
		Tags: []string{"docker", "media"},
		Cpu:  0.00110704754522243, MaxCpu: 4,
		Mem: 1145044992, MaxMem: 8589934592,
		Disk: 6250692608, MaxDisk: 68719476736,
		NetIn: 13802129640, NetOut: 681036731,
		UptimeSeconds: 238010,
	}
	if diff := cmp.Diff(want, media, protocmp.Transform(), protocmp.IgnoreFields(&pb.Workload{}, "nics", "addresses")); diff != "" {
		t.Errorf("media-stack (-want +got):\n%s", diff)
	}
	if got := workloads(t, c)[101]; got.GetKind() != pb.Kind_KIND_VM || got.GetName() != "homeassist" || got.GetStatus() != "stopped" {
		t.Errorf("101 = %v %q %s, want the stopped VM homeassist", got.GetKind(), got.GetName(), got.GetStatus())
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
		// go-proxmox's ContainerInterface would keep only one address per
		// family of each interface. lo is left out.
		210: {
			{Name: "cert-bridge", Mac: "02:b6:ec:04:1c:6c", Ips: []string{"203.0.113.236", "2001:db8:30c:4464:a918:2889:9e93:14d8"}},
			{Name: "fw-network", Mac: "02:a0:07:75:04:2e", Ips: []string{"198.18.1.210", "2001:db8:30c:4464:96c7:be8b:5788:4506"}},
		},
		202: {
			{Name: "cert-bridge", Mac: "02:eb:b6:2f:ee:9a", Ips: []string{"203.0.113.226", "2001:db8:30c:4464:c96b:5af6:1e8a:219b"}},
			{Name: "fw-network", Mac: "02:13:15:46:bd:2e", Ips: []string{"198.18.1.202", "2001:db8:30c:4464:7dbb:5aef:304c:44b6"}},
		},
	})
}

func TestARunningVMsIPsComeFromItsGuestAgent(t *testing.T) {
	cfg, srv := proxmox(t, "baseline")
	_, c := start(t, cfg)

	addresses(t, workloads(t, c), pb.AddressSource_ADDRESS_SOURCE_LIVE, map[uint32][]*pb.Nic{
		// pfSense: FreeBSD's loopback lo0 is left out like Linux's lo; its
		// pseudo-interfaces (no MAC) are kept with their addresses.
		100: {
			{Name: "vtnet0", Mac: "02:05:5b:bb:01:86", Ips: []string{"2001:db8:30c:4464:1b7c:1928:f2fb:9983", "203.0.113.53"}},
			{Name: "vtnet1", Mac: "02:25:70:dd:a6:01", Ips: []string{"2001:db8:30c:4464:7594:20a3:7f79:221d", "198.18.1.1"}},
			{Name: "enc0", Mac: "00:00:00:00:00:00"},
			{Name: "pflog0", Mac: "00:00:00:00:00:00"},
			{Name: "pfsync0", Mac: "00:00:00:00:00:00"},
			{Name: "ovpnc1", Mac: "00:00:00:00:00:00", Ips: []string{"2001:db8:30c:4464:1b7c:1928:f2fb:9983", "203.0.113.169"}},
			{Name: "tun_wg1", Mac: "00:00:00:00:00:00", Ips: []string{"198.18.40.1"}},
			{Name: "tun_wg2", Mac: "00:00:00:00:00:00", Ips: []string{"198.18.30.2"}},
		},
	})
	if n := srv.Hits("GET /api2/json/nodes/pve/qemu/100/config"); n != 0 {
		t.Errorf("read 100's config %d times, want none while its agent answers", n)
	}
}

func TestAStoppedWorkloadsIPsComeFromTheStaticIPOrMACInItsConfig(t *testing.T) {
	cfg, _ := proxmox(t, "baseline")
	_, c := start(t, cfg)

	addresses(t, workloads(t, c), pb.AddressSource_ADDRESS_SOURCE_CONFIG, map[uint32][]*pb.Nic{
		// An LXC's static ip (its MAC upper case in the config).
		122: {{Name: "eth0", Mac: "02:cd:4c:1c:08:fa", Ips: []string{"198.18.1.122"}}},
		// A /32 is still its one address.
		211: {{Name: "eth0", Mac: "02:27:41:c6:44:12", Ips: []string{"198.18.1.211"}}},
		// A VM without cloud-init: its MAC alone.
		101: {{Name: "net0", Mac: "02:61:c0:a3:8a:16"}},
	})
}

// The homelab has no stopped guest with an ip6, on DHCP or with cloud-init:
// the config-addresses scenario lists three such guests.
func TestAStoppedWorkloadsConfigGivesItsIP6AndCloudInitAddressesAndNoneOnDHCP(t *testing.T) {
	cfg, _ := proxmox(t, "config-addresses")
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

	// The homelab's token inherits everything from /: every Workload can
	// be powered, none is Protected.
	if n := len(got.GetWorkloads()); n != 16 {
		t.Errorf("%d Workloads, want 16", n)
	}
	for _, w := range got.GetWorkloads() {
		if !slices.Contains(w.GetPrivileges(), "VM.Audit") || !slices.Contains(w.GetPrivileges(), "VM.PowerMgmt") {
			t.Errorf("%d holds %v, want VM.Audit and VM.PowerMgmt among them", w.GetVmid(), w.GetPrivileges())
		}
	}
}

// The protected scenario gives 100 (pfSense) and 210 (nginx) their own ACL
// without VM.PowerMgmt (Protected); the rest inherit /vms.
func TestAWorkloadsOwnACLOverridesWhatItWouldInherit(t *testing.T) {
	cfg, _ := proxmox(t, "protected")
	_, c := start(t, cfg)

	got, err := c.ListPermissions(t.Context(), &pb.ListPermissionsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	control := []string{"VM.Audit", "VM.GuestAgent.Audit", "VM.PowerMgmt"}
	audit := []string{"VM.Audit", "VM.GuestAgent.Audit"}
	want := &pb.ListPermissionsResponse{}
	for _, id := range []uint32{100, 101, 102, 122, 201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212} {
		privs := control
		if id == 100 || id == 210 {
			privs = audit
		}
		want.Workloads = append(want.Workloads, &pb.WorkloadPermissions{Vmid: id, Privileges: privs})
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListPermissions (-want +got):\n%s", diff)
	}
}

// A privilege granted without propagate (0) holds on its own path only:
// VM.PowerMgmt on /vms or / without propagate reaches no guest.
func TestAPrivilegeThatDoesntPropagateHoldsOnlyOnItsOwnPath(t *testing.T) {
	cfg, _ := proxmox(t, "no-propagate")
	_, c := start(t, cfg)

	got, err := c.ListPermissions(t.Context(), &pb.ListPermissionsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	byVMID := map[uint32][]string{}
	for _, w := range got.GetWorkloads() {
		byVMID[w.GetVmid()] = w.GetPrivileges()
	}
	audit := []string{"VM.Audit", "VM.GuestAgent.Audit"}
	if d := cmp.Diff(audit, byVMID[210]); d != "" {
		t.Errorf("210 (inherits /vms) (-want +got):\n%s", d)
	}
	if d := cmp.Diff([]string{"VM.Audit", "VM.GuestAgent.Audit", "VM.PowerMgmt"}, byVMID[102]); d != "" {
		t.Errorf("102 (its own ACL) (-want +got):\n%s", d)
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

	for _, r := range []string{"GET /api2/json/cluster/resources", "GET /api2/json/nodes/pve/lxc/210/interfaces", "GET /api2/json/nodes/pve/lxc/122/config"} {
		if n := srv.Hits(r); n != 1 {
			t.Errorf("%s: %d times, want once within the TTL", r, n)
		}
	}
}

func TestAWorkloadWhoseAddressesCantBeReadIsListedWithWhy(t *testing.T) {
	// config-forbidden refuses the config of the stopped LXC 122 (devenv).
	cfg, _ := proxmox(t, "config-forbidden")
	h, c := start(t, cfg)

	got := workloads(t, c)

	if w := got[122]; len(w.GetNics()) != 0 || w.GetAddressesError() != "Proxmox refused the API token" {
		t.Errorf("122: nics %v, addresses_error %q; want none and the refusal", w.GetNics(), w.GetAddressesError())
	}
	if len(got[206].GetNics()) != 1 {
		t.Errorf("206 nics = %v, want its config's", got[206].GetNics())
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

// Every VM of the homelab is stopped or answers through its agent: the
// no-agent scenario lists only a running VM 112, whose agent fails with
// baseline's hand-written 500.
func TestARunningVMWhoseAgentDoesntAnswerFallsBackToItsConfig(t *testing.T) {
	cfg, srv := proxmox(t, "no-agent")
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
