package network

import (
	"context"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// GetRemoteAccess reads the Remote Access tunnel's peers and works out
// what each can reach from the rules on its interface. pfsense-api caches
// what it reads, so this reads on every call.
func (c *component) GetRemoteAccess(ctx context.Context, _ *networkpb.GetRemoteAccessRequest) (*networkpb.GetRemoteAccessResponse, error) {
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
		return &networkpb.GetRemoteAccessResponse{Error: "pfSense has no Remote Access tunnel (a WireGuard tunnel on an interface without a gateway)"}, nil
	}
	rules, err := c.pfsense.ListFirewallRules(ctx, &pfsenseapipb.ListFirewallRulesRequest{Interface: iface.GetName()})
	if err != nil {
		return nil, unavailable("can't read the firewall rules", err)
	}
	nets := interfaceNets(ifaces.GetInterfaces())
	out := &networkpb.GetRemoteAccessResponse{Tunnel: tunnel.GetName(), Interface: iface.GetDescription()}
	for _, p := range tunnel.GetPeers() {
		out.Peers = append(out.Peers, &networkpb.RemoteAccessPeer{
			Name:            p.GetDescription(),
			PublicKey:       p.GetPublicKey(),
			Addresses:       p.GetAllowedIps(),
			Endpoint:        p.GetEndpoint(),
			LatestHandshake: p.GetLatestHandshake(),
			RxBytes:         p.GetRxBytes(),
			TxBytes:         p.GetTxBytes(),
			Reaches:         reaches(p, rules.GetRules(), nets),
		})
	}
	return out, nil
}

func unavailable(what string, err error) error {
	return status.Errorf(codes.Unavailable, "%s: %s", what, status.Convert(err).Message())
}

// remoteAccessTunnel is the first WireGuard tunnel assigned to an
// interface without a gateway. The VPN Tunnel's WireGuard interface has
// one (pfSense routes through it); Remote Access peers dial in instead.
func remoteAccessTunnel(tunnels []*pfsenseapipb.WireguardTunnel, ifaces []*pfsenseapipb.Interface) (*pfsenseapipb.WireguardTunnel, *pfsenseapipb.Interface) {
	for _, t := range tunnels {
		for _, i := range ifaces {
			if i.GetHwInterface() == t.GetName() && i.GetGateway() == "" {
				return t, i
			}
		}
	}
	return nil, nil
}

// ifaceNet is an interface's address and subnet, by interface id.
type ifaceNet struct {
	description string
	addr        netip.Addr
	subnet      netip.Prefix
}

func interfaceNets(ifaces []*pfsenseapipb.Interface) map[string]ifaceNet {
	out := map[string]ifaceNet{}
	for _, i := range ifaces {
		n := ifaceNet{description: i.GetDescription()}
		if addr, err := netip.ParseAddr(i.GetIpv4Address()); err == nil {
			n.addr = addr
			if p, err := addr.Prefix(int(i.GetIpv4Prefix())); err == nil {
				n.subnet = p
			}
		}
		out[i.GetName()] = n
	}
	return out
}

// reaches walks the interface's rules in order, as pfSense evaluates them,
// and lists what each pass rule whose source covers the peer lets it
// reach. A block or reject rule for any traffic to anywhere ends the walk;
// narrower block rules are left out. A source that is an alias isn't
// resolved, so it never covers a peer.
func reaches(peer *pfsenseapipb.WireguardPeer, rules []*pfsenseapipb.FirewallRule, nets map[string]ifaceNet) []*networkpb.Reach {
	var prefixes []netip.Prefix
	for _, a := range peer.GetAllowedIps() {
		if p, err := netip.ParsePrefix(a); err == nil && p.Addr().Is4() {
			prefixes = append(prefixes, p)
		}
	}
	var out []*networkpb.Reach
	for _, r := range rules {
		if r.GetDisabled() || r.GetIpProtocol() == "inet6" || !covers(r.GetSource(), prefixes, nets) {
			continue
		}
		if r.GetType() != "pass" {
			if r.GetDestination() == "any" && r.GetProtocol() == "" && r.GetDestinationPort() == "" {
				break
			}
			continue
		}
		dest, label := describe(r.GetDestination(), nets)
		protocol := r.GetProtocol()
		if protocol == "" {
			protocol = "any"
		}
		out = append(out, &networkpb.Reach{Destination: dest, Label: label, Protocol: protocol, Ports: r.GetDestinationPort(), Rule: r.GetDescription()})
	}
	return out
}

// covers reports whether a rule's source matches every address of the
// peer. Only a source ddash can resolve covers it.
func covers(source string, peer []netip.Prefix, nets map[string]ifaceNet) bool {
	inverted := strings.HasPrefix(source, "!")
	source = strings.TrimPrefix(source, "!")
	if source == "any" {
		return !inverted
	}
	var within netip.Prefix
	if n, ok := nets[source]; ok {
		within = n.subnet
	} else if p, err := netip.ParsePrefix(source); err == nil {
		within = p.Masked()
	} else if a, err := netip.ParseAddr(source); err == nil {
		within = netip.PrefixFrom(a, a.BitLen())
	} else {
		return false // an alias, an interface address or (self)
	}
	if !within.IsValid() || len(peer) == 0 {
		return false
	}
	in := true
	for _, p := range peer {
		in = in && within.Bits() <= p.Bits() && within.Contains(p.Addr())
	}
	return in != inverted
}

// describe resolves a rule's destination to addresses where it names an
// interface, and says what it is.
func describe(dest string, nets map[string]ifaceNet) (string, string) {
	if rest, ok := strings.CutPrefix(dest, "!"); ok {
		d, label := describe(rest, nets)
		if label != "" {
			label = "not " + label
		}
		return "!" + d, label
	}
	switch dest {
	case "any":
		return dest, "anywhere"
	case "(self)":
		return dest, "the firewall"
	}
	if name, ok := strings.CutSuffix(dest, ":ip"); ok {
		if n, ok := nets[name]; ok && n.addr.IsValid() {
			return n.addr.String(), n.description + " address"
		}
	}
	if n, ok := nets[dest]; ok && n.subnet.IsValid() {
		return n.subnet.String(), n.description + " subnet"
	}
	if _, err := netip.ParsePrefix(dest); err == nil {
		return dest, ""
	}
	if _, err := netip.ParseAddr(dest); err == nil {
		return dest, ""
	}
	return dest, "alias"
}
