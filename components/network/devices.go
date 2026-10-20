package network

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
)

// checkTimeout bounds Start's check of the config against the firewall.
const checkTimeout = 10 * time.Second

// startDevices takes the Device join's config. It refuses a reserved range
// that is no range, and, when the firewall answers, a bypass alias it
// doesn't have and a reserved range overlapping a DHCP pool. A firewall
// that can't be read skips those checks: network must start at boot while
// the firewall is still coming up.
func (c *component) startDevices(ctx context.Context, env *sdk.Env, cfg *networkpb.NetworkConfig) error {
	var errs sdk.FieldErrors
	lo, hi, ok := parseRange(cfg.GetReservedRange())
	if !ok {
		errs.Add("reserved_range", "must be two addresses, low to high, like 198.18.1.20-198.18.1.49")
		return errs.Err()
	}
	c.bypassAlias = cfg.GetBypassAlias()
	c.reservedRange, c.reservedLo, c.reservedHi = cfg.GetReservedRange(), lo, hi
	c.devices = sdk.Cached(sdk.TTLSlow, c.readDevices)

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if aliases, err := c.pfsense.ListAliases(ctx, &pfsenseapipb.ListAliasesRequest{}); err != nil {
		env.Log.Warn("can't check the bypass alias", "err", status.Convert(err).Message())
	} else if msg := checkAlias(aliases.GetAliases(), c.bypassAlias); msg != "" {
		errs.Add("bypass_alias", "%s", msg)
	}
	if servers, err := c.pfsense.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{}); err != nil {
		env.Log.Warn("can't check the reserved range", "err", status.Convert(err).Message())
	} else if msg := checkRange(servers.GetServers(), lo, hi); msg != "" {
		errs.Add("reserved_range", "%s", msg)
	}
	return errs.Err()
}

// checkAlias says what is wrong with name as the bypass alias, or "".
func checkAlias(aliases []*pfsenseapipb.Alias, name string) string {
	i := slices.IndexFunc(aliases, func(a *pfsenseapipb.Alias) bool { return a.GetName() == name })
	switch {
	case i < 0:
		return fmt.Sprintf("the firewall has no alias %q", name)
	case aliases[i].GetType() == "port":
		return fmt.Sprintf("%s is a port alias; pick an alias of addresses", name)
	}
	return ""
}

// checkRange says which enabled DHCP pool lo-hi overlaps, or "".
func checkRange(servers []*pfsenseapipb.DhcpServer, lo, hi netip.Addr) string {
	for _, s := range servers {
		if !s.GetEnabled() {
			continue
		}
		for _, p := range s.GetPools() {
			from, to, ok := parseRange(p.GetFrom() + "-" + p.GetTo())
			if ok && lo.Compare(to) <= 0 && from.Compare(hi) <= 0 {
				return fmt.Sprintf("overlaps %s's DHCP pool %s-%s; pick addresses outside it", s.GetInterface(), p.GetFrom(), p.GetTo())
			}
		}
	}
	return ""
}

// parseRange reads "low-high": two addresses of one family, low first.
func parseRange(s string) (lo, hi netip.Addr, ok bool) {
	a, b, found := strings.Cut(s, "-")
	if !found {
		return lo, hi, false
	}
	lo, errLo := netip.ParseAddr(strings.TrimSpace(a))
	hi, errHi := netip.ParseAddr(strings.TrimSpace(b))
	if errLo != nil || errHi != nil || lo.Is4() != hi.Is4() || lo.Compare(hi) > 0 {
		return lo, hi, false
	}
	return lo, hi, true
}

func (c *component) ListDevices(ctx context.Context, _ *networkpb.ListDevicesRequest) (*networkpb.ListDevicesResponse, error) {
	return c.devices.Get(ctx)
}

// readDevices joins the firewall's leases, static mappings and aliases
// into Devices.
func (c *component) readDevices(ctx context.Context) (*networkpb.ListDevicesResponse, error) {
	leases, err := c.pfsense.ListLeases(ctx, &pfsenseapipb.ListLeasesRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the DHCP leases: %s", status.Convert(err).Message())
	}
	servers, err := c.pfsense.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the static mappings: %s", status.Convert(err).Message())
	}
	aliases, err := c.pfsense.ListAliases(ctx, &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the aliases: %s", status.Convert(err).Message())
	}
	if msg := checkAlias(aliases.GetAliases(), c.bypassAlias); msg != "" {
		return nil, status.Errorf(codes.FailedPrecondition, "bypass alias: %s; pick another in network's config", msg)
	}
	out := &networkpb.ListDevicesResponse{
		Devices:     join(leases.GetLeases(), servers.GetServers(), aliases.GetAliases(), c.bypassAlias),
		BypassAlias: c.bypassAlias,
	}
	// The Devices list without AdGuard: only their ad-blocking is unknown.
	if clients, err := c.adguard.ListClients(ctx, &adguardapipb.ListClientsRequest{}); err != nil {
		out.AdguardError = "can't read AdGuard's clients: " + status.Convert(err).Message()
	} else {
		joinAdGuard(out.GetDevices(), clients.GetClients())
	}
	return out, nil
}

// join makes a Device of every MAC the firewall's DHCP knows: a static
// mapping (Adopted) or a lease (New without a mapping). Hardware that set
// its own address has neither, so it is no Device, and alias members only
// ever show as a Device's memberships.
func join(leases []*pfsenseapipb.Lease, servers []*pfsenseapipb.DhcpServer, aliases []*pfsenseapipb.Alias, bypass string) []*networkpb.Device {
	byMAC := map[string]*networkpb.Device{}
	device := func(mac string) *networkpb.Device {
		mac = strings.ToLower(mac)
		d := byMAC[mac]
		if d == nil {
			d = &networkpb.Device{Mac: mac}
			byMAC[mac] = d
		}
		return d
	}
	for _, s := range servers {
		for _, m := range s.GetStaticMappings() {
			if m.GetMac() == "" {
				continue
			}
			d := device(m.GetMac())
			d.Adopted = true
			d.Reservation = &networkpb.Reservation{Interface: m.GetInterface(), Ip: m.GetIp(), Hostname: m.GetHostname(), Description: m.GetDescription()}
		}
	}
	rank := map[*networkpb.Device]int{}
	for _, l := range leases {
		if l.GetMac() == "" {
			continue
		}
		d := device(l.GetMac())
		d.Online = d.GetOnline() || online(l)
		if r, held := rank[d]; !held || leaseRank(l) < r {
			rank[d] = leaseRank(l)
			d.Lease = &networkpb.DeviceLease{
				Ip: l.GetIp(), Hostname: l.GetHostname(), Interface: l.GetInterface(), Starts: l.GetStarts(), Ends: l.GetEnds(),
				Status: l.GetActiveStatus(), OnlineStatus: l.GetOnlineStatus(),
			}
		}
	}

	byName := map[string]*pfsenseapipb.Alias{}
	for _, a := range aliases {
		byName[a.GetName()] = a
	}
	out := make([]*networkpb.Device, 0, len(byMAC))
	for _, d := range byMAC {
		d.Ip = d.GetLease().GetIp()
		if d.GetReservation() != nil {
			d.Ip = d.GetReservation().GetIp()
		}
		d.Name = cmp.Or(d.GetReservation().GetDescription(), d.GetReservation().GetHostname(), d.GetLease().GetHostname())
		if ip, err := netip.ParseAddr(d.GetIp()); err == nil {
			for _, a := range aliases {
				if !contains(byName, a.GetName(), ip, map[string]bool{}) {
					continue
				}
				if a.GetName() == bypass {
					d.Direct = true
				} else {
					d.Aliases = append(d.Aliases, &networkpb.DeviceAlias{Name: a.GetName(), Description: a.GetDescription()})
				}
			}
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b *networkpb.Device) int {
		ia, errA := netip.ParseAddr(a.GetIp())
		ib, errB := netip.ParseAddr(b.GetIp())
		switch {
		case errA == nil && errB == nil:
			if c := ia.Compare(ib); c != 0 {
				return c
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		}
		return strings.Compare(a.GetMac(), b.GetMac())
	})
	return out
}

// leaseRank orders a MAC's leases for its facts: a dynamic lease it holds
// now, then any other dynamic one, then the static entry.
func leaseRank(l *pfsenseapipb.Lease) int {
	switch l.GetActiveStatus() {
	case "active":
		return 0
	case "static":
		return 2
	}
	return 1
}

// online reads pfSense's "active/online" or "idle/offline".
func online(l *pfsenseapipb.Lease) bool {
	s := l.GetOnlineStatus()
	if _, after, ok := strings.Cut(s, "/"); ok {
		s = after
	}
	return s == "online"
}

// contains reports whether the alias named name holds ip: as an address,
// a network, a range "low-high", or through an alias it nests. A port
// alias holds no address; seen guards against nesting loops.
func contains(byName map[string]*pfsenseapipb.Alias, name string, ip netip.Addr, seen map[string]bool) bool {
	a := byName[name]
	if a == nil || a.GetType() == "port" || seen[name] {
		return false
	}
	seen[name] = true
	for _, m := range a.GetAddresses() {
		if holds(byName, m, ip, seen) {
			return true
		}
	}
	return false
}

// holds reports whether the alias member m (an address, a network, a
// range or an alias's name) holds ip.
func holds(byName map[string]*pfsenseapipb.Alias, m string, ip netip.Addr, seen map[string]bool) bool {
	m = strings.TrimSpace(m)
	if addr, err := netip.ParseAddr(m); err == nil {
		return addr == ip
	}
	if p, err := netip.ParsePrefix(m); err == nil {
		return p.Contains(ip)
	}
	if lo, hi, ok := parseRange(m); ok {
		return lo.Compare(ip) <= 0 && ip.Compare(hi) <= 0
	}
	return contains(byName, m, ip, seen)
}
