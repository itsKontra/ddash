package network_test

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
)

// tunnelKey is the Remote Access tunnel's public key.
const tunnelKey = "L/711l8eD25p0ET+JXNg85+elMdhg/98adYt60nxxbk="

func (f *firewall) ListWireguardPeers(context.Context, *pfsenseapipb.ListWireguardPeersRequest, ...grpc.CallOption) (*pfsenseapipb.ListWireguardPeersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListWireguardPeersResponse{Peers: f.peers}, nil
}

func (f *firewall) CreateWireguardPeer(_ context.Context, req *pfsenseapipb.CreateWireguardPeerRequest, _ ...grpc.CallOption) (*pfsenseapipb.CreateWireguardPeerResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.createErr != nil {
		return nil, f.createErr
	}
	peer := &pfsenseapipb.WireguardPeerConfig{Tunnel: req.GetTunnel(), Enabled: true, PublicKey: req.GetPublicKey(), Description: req.GetDescription(), AllowedIps: req.GetAllowedIps()}
	f.peers = append(f.peers, peer)
	out := &pfsenseapipb.CreateWireguardPeerResponse{Peer: peer}
	if f.applyFails {
		out.ApplyFailed = true
		st, _ := status.New(codes.Unavailable, "pfSense holds the peer, but applying it failed: pfSense answered 500").WithDetails(out)
		return nil, st.Err()
	}
	return out, nil
}

// configuredPeers is the homelab's peers as configured: the two live ones,
// a disabled one at .4, and the VPN Tunnel's provider.
func configuredPeers() []*pfsenseapipb.WireguardPeerConfig {
	return []*pfsenseapipb.WireguardPeerConfig{
		{Tunnel: "tun_wg1", Enabled: true, PublicKey: "phone-key", Description: "Phone", AllowedIps: []string{"198.18.40.2/32"}},
		{Tunnel: "tun_wg1", Enabled: true, PublicKey: "laptop-key", Description: "Laptop", AllowedIps: []string{"198.18.40.3/32"}},
		{Tunnel: "tun_wg1", PublicKey: "tablet-key", Description: "Old tablet", AllowedIps: []string{"198.18.40.4/32"}},
		{Tunnel: "tun_wg2", Enabled: true, PublicKey: "provider-key", Description: "VPN provider", AllowedIps: []string{"0.0.0.0/0"}},
	}
}

func remoteAccessFirewall() *firewall {
	return &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: homelab, tunnels: wireguard(), peers: configuredPeers()}
}

// withRemoteAccess is the homelab's config with an endpoint and AdGuard as
// the peers' DNS server; the routes are left to their default.
func withRemoteAccess() *networkpb.NetworkConfig {
	cfg := homeConfig()
	cfg.RemoteAccessEndpoint = "vpn.example.org"
	cfg.RemoteAccessDns = []string{"198.18.1.53"}
	return cfg
}

func addPeer(t *testing.T, c networkpb.NetworkServiceClient, name string) *networkpb.AddRemoteAccessPeerResponse {
	t.Helper()
	resp, err := c.AddRemoteAccessPeer(t.Context(), &networkpb.AddRemoteAccessPeerRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

var privateKeyLine = regexp.MustCompile(`(?m)^PrivateKey = (\S+)$`)

// privateKey is the private key a config names, and the public key that
// goes with it.
func privateKey(t *testing.T, config string) (private, public string) {
	t.Helper()
	m := privateKeyLine.FindStringSubmatch(config)
	if m == nil {
		t.Fatalf("no PrivateKey in\n%s", config)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("PrivateKey %q isn't base64: %v", m[1], err)
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		t.Fatalf("PrivateKey %q isn't a Curve25519 key: %v", m[1], err)
	}
	return m[1], base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

func TestAddingADeviceCreatesItsPeerAtTheLowestFreeAddressAndReturnsItsConfig(t *testing.T) {
	fw := remoteAccessFirewall()
	_, c := startConfigured(t, fw, withRemoteAccess())

	got := addPeer(t, c, "  Anna's phone ")

	private, public := privateKey(t, got.GetConfig())
	// .1 is the firewall's, .2 and .3 the live peers', .4 the disabled one's.
	wantWrites := []proto.Message{&pfsenseapipb.CreateWireguardPeerRequest{Tunnel: "tun_wg1", PublicKey: public, Description: "Anna's phone", AllowedIps: []string{"198.18.40.5/32"}}}
	if diff := cmp.Diff(wantWrites, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	wantConfig := `[Interface]
PrivateKey = ` + private + `
Address = 198.18.40.5/32
DNS = 198.18.1.53

[Peer]
PublicKey = ` + tunnelKey + `
Endpoint = vpn.example.org:51820
AllowedIPs = 198.18.1.0/24, 198.18.40.0/24
PersistentKeepalive = 25
`
	if diff := cmp.Diff(wantConfig, got.GetConfig()); diff != "" {
		t.Errorf("config (-want +got):\n%s", diff)
	}
	wantPeer := &networkpb.RemoteAccessPeer{Name: "Anna's phone", PublicKey: public, Addresses: []string{"198.18.40.5/32"}}
	if diff := cmp.Diff(wantPeer, got.GetPeer(), protocmp.Transform()); diff != "" {
		t.Errorf("peer (-want +got):\n%s", diff)
	}
	if got.GetWarning() != "" {
		t.Errorf("warning = %q, want none", got.GetWarning())
	}
}

// Only the public key leaves network, and the config is a secret field,
// which the Audit Log redacts on any message it records.
func TestTheDevicesPrivateKeyIsInItsConfigAlone(t *testing.T) {
	fw := remoteAccessFirewall()
	_, c := startConfigured(t, fw, withRemoteAccess())

	got := addPeer(t, c, "Anna's phone")

	private, _ := privateKey(t, got.GetConfig())
	for _, w := range fw.written() {
		if b, _ := protojson.Marshal(w); strings.Contains(string(b), private) {
			t.Errorf("the private key reached pfsense-api: %s", b)
		}
	}
	fd := got.ProtoReflect().Descriptor().Fields().ByName("config")
	if opts, _ := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field); !opts.GetSecret() {
		t.Error("AddRemoteAccessPeerResponse.config isn't marked secret")
	}
	if strings.Contains(got.GetPeer().String(), private) || strings.Contains(got.GetWarning(), private) {
		t.Error("the private key is outside the config")
	}
}

func TestEveryDeviceGetsItsOwnKeyPairAndAddress(t *testing.T) {
	fw := remoteAccessFirewall()
	_, c := startConfigured(t, fw, withRemoteAccess())

	first := addPeer(t, c, "Anna's phone")
	second := addPeer(t, c, "Anna's laptop")

	a, _ := privateKey(t, first.GetConfig())
	b, _ := privateKey(t, second.GetConfig())
	if a == b {
		t.Error("both devices got the same private key")
	}
	if got := second.GetPeer().GetAddresses(); !cmp.Equal(got, []string{"198.18.40.6/32"}) {
		t.Errorf("second device at %v, want 198.18.40.6/32", got)
	}
}

func TestTheConfigTakesTheEndpointsPortAndTheConfiguredRoutes(t *testing.T) {
	fw := remoteAccessFirewall()
	cfg := withRemoteAccess()
	cfg.RemoteAccessEndpoint = "vpn.example.org:443"
	cfg.RemoteAccessDns = nil
	cfg.RemoteAccessRoutes = []string{"0.0.0.0/0"}
	_, c := startConfigured(t, fw, cfg)

	got := addPeer(t, c, "Anna's phone").GetConfig()

	for _, want := range []string{"\nEndpoint = vpn.example.org:443\n", "\nAllowedIPs = 0.0.0.0/0\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("config misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "DNS") {
		t.Errorf("config names a DNS server, want the device's own:\n%s", got)
	}
}

func TestAddingADeviceIsRefusedWithoutARemoteAccessEndpoint(t *testing.T) {
	fw := remoteAccessFirewall()
	_, c := start(t, fw)

	_, err := c.AddRemoteAccessPeer(t.Context(), &networkpb.AddRemoteAccessPeerRequest{Name: "Anna's phone"})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "Remote Access endpoint") {
		t.Errorf("error = %v, want FailedPrecondition naming the Remote Access endpoint", err)
	}
	if w := fw.written(); len(w) != 0 {
		t.Errorf("wrote %v, want nothing", w)
	}
	if got := remoteAccess(t, fw).GetAddUnavailable(); !strings.Contains(got, "Remote Access endpoint") {
		t.Errorf("add_unavailable = %q, want it naming the Remote Access endpoint", got)
	}
}

func TestAddingADeviceIsRefusedWhenItCantBeAdded(t *testing.T) {
	small := func() []*pfsenseapipb.Interface {
		ifaces := homelab()
		ifaces[2].Ipv4Prefix = 30 // .1 the firewall's, .2 the phone's
		return ifaces
	}
	for name, tc := range map[string]struct {
		fw   *firewall
		req  string
		code codes.Code
		says string
	}{
		"without a name":                 {remoteAccessFirewall(), " ", codes.InvalidArgument, "name"},
		"with the subnet full":           {&firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: small, tunnels: wireguard(), peers: configuredPeers()}, "Tablet", codes.FailedPrecondition, "198.18.40.0/30 is full"},
		"without a Remote Access tunnel": {&firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: homelab, tunnels: wireguard()[:1]}, "Tablet", codes.FailedPrecondition, "no Remote Access tunnel"},
	} {
		t.Run(name, func(t *testing.T) {
			_, c := startConfigured(t, tc.fw, withRemoteAccess())

			_, err := c.AddRemoteAccessPeer(t.Context(), &networkpb.AddRemoteAccessPeerRequest{Name: tc.req})

			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want %v naming %q", err, tc.code, tc.says)
			}
			if w := tc.fw.written(); len(w) != 0 {
				t.Errorf("wrote %v, want nothing", w)
			}
		})
	}
}

// pfSense holds the peer though its apply failed, so the config is still
// the device's only chance at its private key.
func TestADeviceWhoseApplyFailedStillGetsItsConfigWithAWarning(t *testing.T) {
	fw := remoteAccessFirewall()
	fw.applyFails = true
	_, c := startConfigured(t, fw, withRemoteAccess())

	got := addPeer(t, c, "Anna's phone")

	privateKey(t, got.GetConfig())
	if !strings.Contains(got.GetWarning(), "apply") {
		t.Errorf("warning = %q, want it naming the failed apply", got.GetWarning())
	}
}

func TestACreateThatFailsReturnsNoConfig(t *testing.T) {
	fw := remoteAccessFirewall()
	fw.createErr = status.Error(codes.AlreadyExists, "198.18.40.5/32 overlaps 198.18.40.5/32, which the peer Tablet on tun_wg1 holds")
	_, c := startConfigured(t, fw, withRemoteAccess())

	got, err := c.AddRemoteAccessPeer(t.Context(), &networkpb.AddRemoteAccessPeerRequest{Name: "Anna's phone"})

	if err == nil || got != nil {
		t.Errorf("AddRemoteAccessPeer = %v, %v, want an error and no config", got, err)
	}
}

func TestAnEndpointThatIsNoHostIsAConfigError(t *testing.T) {
	for _, endpoint := range []string{"https://vpn.example.org/", "vpn.example.org:http", "vpn.example.org:0"} {
		cfg := homeConfig()
		cfg.RemoteAccessEndpoint = endpoint

		errs := sdk.FieldErrorsOf(startWith(t, remoteAccessFirewall(), cfg))

		if len(errs) != 1 || errs[0].Path != "remote_access_endpoint" {
			t.Errorf("%q: Start errors = %v, want one on remote_access_endpoint", endpoint, errs)
		}
	}
}
