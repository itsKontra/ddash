package network

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
)

// Adding a Remote Access peer. network generates the device's key pair,
// sends pfsense-api the public key only, and puts the private key in the
// config it returns, nowhere else: not in a field of the component, a log
// line, an error or the peer it returns. pfSense keeps no private key
// either, so the config can't be shown again.

// keepalive is the device's PersistentKeepalive in seconds: a phone behind
// NAT keeps its mapping open, so the firewall can reach it.
const keepalive = 25

// remoteAccessConfig is what network's config says about new peers.
type remoteAccessConfig struct {
	// host and port of the endpoint; host is "" when unset, port "" for
	// the tunnel's listen port.
	host, port string
	dns        []string
	// routes are the AllowedIPs of the device's config; nil for the
	// firewall's internal subnets.
	routes []string
}

// errNoEndpoint is why a peer can't be added before the endpoint is set.
const errNoEndpoint = "Add a device needs network's Remote Access endpoint, the firewall's public hostname: set it in network's config"

// hostname is a DNS name, as an endpoint may give one.
var hostname = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.?$`)

// startRemoteAccess takes the Remote Access part of network's config.
func (c *component) startRemoteAccess(cfg *networkpb.NetworkConfig) error {
	var errs sdk.FieldErrors
	if e := strings.TrimSpace(cfg.GetRemoteAccessEndpoint()); e != "" {
		host, port, err := net.SplitHostPort(e)
		if err != nil {
			host, port = strings.Trim(e, "[]"), ""
		}
		if n, err := strconv.ParseUint(port, 10, 16); port != "" && (err != nil || n == 0) {
			errs.Add("remote_access_endpoint", "%q is no port; give a hostname with an optional :port, like vpn.example.org:51820", port)
		} else if _, err := netip.ParseAddr(host); err != nil && !hostname.MatchString(host) {
			errs.Add("remote_access_endpoint", "must be a hostname or address with an optional :port, like vpn.example.org:51820")
		}
		c.remote.host, c.remote.port = host, port
	}
	for _, d := range cfg.GetRemoteAccessDns() {
		if _, err := netip.ParseAddr(strings.TrimSpace(d)); err != nil {
			errs.Add("remote_access_dns", "%q is no address", d)
		}
		c.remote.dns = append(c.remote.dns, strings.TrimSpace(d))
	}
	for _, r := range cfg.GetRemoteAccessRoutes() {
		p, err := netip.ParsePrefix(strings.TrimSpace(r))
		if err != nil {
			errs.Add("remote_access_routes", "%q is no address with a prefix length, like 198.18.1.0/24", r)
			continue
		}
		c.remote.routes = append(c.remote.routes, p.Masked().String())
	}
	return errs.Err()
}

func (c *component) AddRemoteAccessPeer(ctx context.Context, req *networkpb.AddRemoteAccessPeerRequest) (*networkpb.AddRemoteAccessPeerResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "give the device a name")
	}
	if c.remote.host == "" {
		return nil, status.Error(codes.FailedPrecondition, errNoEndpoint)
	}
	tunnels, err := c.pfsense.ListWireguardTunnels(ctx, &pfsenseapipb.ListWireguardTunnelsRequest{})
	if err != nil {
		return nil, unavailable("can't read the WireGuard tunnels", err)
	}
	ifaces, err := c.pfsense.ListInterfaces(ctx, &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		return nil, unavailable("can't read the interfaces", err)
	}
	tunnel, iface := remoteAccessTunnel(tunnels.GetTunnels(), ifaces.GetInterfaces())
	if tunnel == nil {
		return nil, status.Error(codes.FailedPrecondition, "pfSense has no Remote Access tunnel (a WireGuard tunnel on an interface without a gateway)")
	}
	own, err := netip.ParseAddr(iface.GetIpv4Address())
	if err != nil || !own.Is4() {
		return nil, status.Errorf(codes.FailedPrecondition, "the Remote Access interface %s has no IPv4 address", iface.GetDescription())
	}
	subnet, err := own.Prefix(int(iface.GetIpv4Prefix()))
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "the Remote Access interface %s has no IPv4 subnet", iface.GetDescription())
	}
	configured, err := c.pfsense.ListWireguardPeers(ctx, &pfsenseapipb.ListWireguardPeersRequest{Fresh: true})
	if err != nil {
		return nil, unavailable("can't read the WireGuard peers", err)
	}
	addr, ok := freePeerAddress(subnet, own, tunnel, configured.GetPeers())
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "the Remote Access subnet %s is full", subnet)
	}

	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "can't generate a key pair: %v", err)
	}
	public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	address := netip.PrefixFrom(addr, addr.BitLen()).String()
	out := &networkpb.AddRemoteAccessPeerResponse{
		Peer: &networkpb.RemoteAccessPeer{Name: name, PublicKey: public, Addresses: []string{address}},
	}
	_, err = c.pfsense.CreateWireguardPeer(ctx, &pfsenseapipb.CreateWireguardPeerRequest{
		Tunnel: tunnel.GetName(), PublicKey: public, Description: name, AllowedIps: []string{address},
	})
	if err != nil {
		if !applyFailed(err) {
			return nil, err
		}
		out.Warning = fmt.Sprintf("pfSense holds the peer, but applying it failed (%s): the device connects once WireGuard's changes are applied in pfSense (VPN > WireGuard).", status.Convert(err).Message())
	}
	out.Config = clientConfig(key, address, c.remote, tunnel, c.routes(ifaces.GetInterfaces()))
	return out, nil
}

// applyFailed reports whether err is a create whose write pfSense holds
// and only the apply failed.
func applyFailed(err error) bool {
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfsenseapipb.CreateWireguardPeerResponse); ok && r.GetApplyFailed() {
			return true
		}
	}
	return false
}

// freePeerAddress is the lowest address of subnet that isn't its network
// or broadcast address, the firewall's own, or within the allowed IPs of
// any peer of the tunnel, configured (enabled or not) or live.
func freePeerAddress(subnet netip.Prefix, own netip.Addr, tunnel *pfsenseapipb.WireguardTunnel, configured []*pfsenseapipb.WireguardPeerConfig) (netip.Addr, bool) {
	var held []netip.Prefix
	hold := func(allowed []string) {
		for _, a := range allowed {
			if p, err := netip.ParsePrefix(a); err == nil {
				held = append(held, p.Masked())
			}
		}
	}
	for _, p := range configured {
		if p.GetTunnel() == tunnel.GetName() {
			hold(p.GetAllowedIps())
		}
	}
	for _, p := range tunnel.GetPeers() {
		hold(p.GetAllowedIps())
	}
	subnet = subnet.Masked()
	last := lastAddr(subnet)
	for a := subnet.Addr().Next(); a.IsValid() && a.Less(last); a = a.Next() {
		if a == own || containsAny(held, a) {
			continue
		}
		return a, true
	}
	return netip.Addr{}, false
}

func containsAny(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// routes are a new device's AllowedIPs: the configured routes, else the
// subnets of the firewall's interfaces without a gateway (LAN, the Remote
// Access subnet, ...), lowest first.
func (c *component) routes(ifaces []*pfsenseapipb.Interface) []string {
	if len(c.remote.routes) > 0 {
		return c.remote.routes
	}
	var subnets []netip.Prefix
	for _, i := range ifaces {
		if i.GetGateway() != "" {
			continue
		}
		a, err := netip.ParseAddr(i.GetIpv4Address())
		if err != nil {
			continue
		}
		if p, err := a.Prefix(int(i.GetIpv4Prefix())); err == nil && p.Bits() > 0 && !slices.Contains(subnets, p) {
			subnets = append(subnets, p)
		}
	}
	slices.SortFunc(subnets, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	out := make([]string, 0, len(subnets))
	for _, p := range subnets {
		out = append(out, p.String())
	}
	return out
}

// clientConfig is the device's wg-quick config.
func clientConfig(key *ecdh.PrivateKey, address string, remote remoteAccessConfig, tunnel *pfsenseapipb.WireguardTunnel, routes []string) string {
	port := remote.port
	if port == "" {
		port = strconv.Itoa(int(tunnel.GetListenPort()))
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", base64.StdEncoding.EncodeToString(key.Bytes()))
	fmt.Fprintf(&b, "Address = %s\n", address)
	if len(remote.dns) > 0 {
		fmt.Fprintf(&b, "DNS = %s\n", strings.Join(remote.dns, ", "))
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", tunnel.GetPublicKey())
	fmt.Fprintf(&b, "Endpoint = %s\n", net.JoinHostPort(remote.host, port))
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(routes, ", "))
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", keepalive)
	return b.String()
}
