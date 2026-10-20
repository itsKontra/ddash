package network

import (
	"cmp"
	"context"
	"net/netip"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// Adopt and rename: both write the Device's static mapping through
// pfsense-api, which queues and applies the write, then drop the cached
// Device join so the next read shows the change.

func (c *component) NextReservedAddress(ctx context.Context, _ *networkpb.NextReservedAddressRequest) (*networkpb.NextReservedAddressResponse, error) {
	holders, err := c.holders(ctx)
	if err != nil {
		return nil, err
	}
	out := &networkpb.NextReservedAddressResponse{ReservedRange: c.reservedRange}
	if ip, ok := c.lowestFree(holders); ok {
		out.Ip = ip.String()
	}
	return out, nil
}

func (c *component) AdoptDevice(ctx context.Context, req *networkpb.AdoptDeviceRequest) (*networkpb.AdoptDeviceResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "give the Device a name")
	}
	holders, err := c.holders(ctx)
	if err != nil {
		return nil, err
	}
	ip, err := c.adoptAddress(req.GetIp(), holders)
	if err != nil {
		return nil, err
	}
	all, err := c.readDevices(ctx)
	if err != nil {
		return nil, err
	}
	mac := strings.ToLower(req.GetMac())
	d := find(all.GetDevices(), mac)
	switch {
	case d == nil:
		return nil, status.Errorf(codes.NotFound, "no Device has the MAC %s", mac)
	case d.GetAdopted():
		return nil, status.Errorf(codes.FailedPrecondition, "%s is already Adopted, at %s", nameOrMAC(d), d.GetIp())
	}
	if holder, held := holders[ip]; held && holder != mac {
		return nil, status.Errorf(codes.AlreadyExists, "%s is already held by %s", ip, nameOrMAC(cmp.Or(find(all.GetDevices(), holder), &networkpb.Device{Mac: holder})))
	}
	iface, err := c.dhcpInterface(ctx, ip)
	if err != nil {
		return nil, err
	}
	mapping := &pfsenseapipb.StaticMapping{Interface: iface, Mac: mac, Ip: ip.String(), Description: name}
	if h := d.GetLease().GetHostname(); validHostname.MatchString(h) {
		mapping.Hostname = h
	}
	if _, err := c.pfsense.CreateStaticMapping(ctx, &pfsenseapipb.CreateStaticMappingRequest{Mapping: mapping}); err != nil {
		return nil, err
	}
	d, err = c.reread(ctx, mac)
	if err != nil {
		return nil, err
	}
	return &networkpb.AdoptDeviceResponse{Device: d}, nil
}

func (c *component) RenameDevice(ctx context.Context, req *networkpb.RenameDeviceRequest) (*networkpb.RenameDeviceResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "give the Device a name")
	}
	all, err := c.readDevices(ctx)
	if err != nil {
		return nil, err
	}
	mac := strings.ToLower(req.GetMac())
	d := find(all.GetDevices(), mac)
	switch {
	case d == nil:
		return nil, status.Errorf(codes.NotFound, "no Device has the MAC %s", mac)
	case !d.GetAdopted():
		return nil, status.Errorf(codes.FailedPrecondition, "%s is a New Device with no reservation to rename; Adopt it", nameOrMAC(d))
	}
	if _, err := c.pfsense.RenameStaticMapping(ctx, &pfsenseapipb.RenameStaticMappingRequest{Interface: d.GetReservation().GetInterface(), Mac: mac, Description: name}); err != nil {
		return nil, err
	}
	d, err = c.reread(ctx, mac)
	if err != nil {
		return nil, err
	}
	return &networkpb.RenameDeviceResponse{Device: d}, nil
}

// reread drops the cached join and returns mac's Device as it is now.
func (c *component) reread(ctx context.Context, mac string) (*networkpb.Device, error) {
	c.devices.Invalidate()
	all, err := c.devices.Get(ctx)
	if err != nil {
		return nil, err
	}
	d := find(all.GetDevices(), mac)
	if d == nil {
		return nil, status.Errorf(codes.Unavailable, "the firewall doesn't list %s after the write", mac)
	}
	return d, nil
}

// holders maps each address a static mapping (on any DHCP server) or a
// current lease holds to the MAC holding it.
func (c *component) holders(ctx context.Context) (map[netip.Addr]string, error) {
	servers, err := c.pfsense.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the static mappings: %s", status.Convert(err).Message())
	}
	leases, err := c.pfsense.ListLeases(ctx, &pfsenseapipb.ListLeasesRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the DHCP leases: %s", status.Convert(err).Message())
	}
	out := map[netip.Addr]string{}
	for _, s := range servers.GetServers() {
		for _, m := range s.GetStaticMappings() {
			if ip, err := netip.ParseAddr(m.GetIp()); err == nil {
				out[ip] = strings.ToLower(m.GetMac())
			}
		}
	}
	for _, l := range leases.GetLeases() {
		if l.GetActiveStatus() == "expired" {
			continue
		}
		if ip, err := netip.ParseAddr(l.GetIp()); err == nil {
			if _, mapped := out[ip]; !mapped {
				out[ip] = strings.ToLower(l.GetMac())
			}
		}
	}
	return out, nil
}

// lowestFree is the lowest address of the reserved range nobody holds.
func (c *component) lowestFree(holders map[netip.Addr]string) (netip.Addr, bool) {
	for ip := c.reservedLo; ip.IsValid() && ip.Compare(c.reservedHi) <= 0; ip = ip.Next() {
		if _, held := holders[ip]; !held {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// adoptAddress is the address Adopt reserves: the one asked for, which
// must lie in the reserved range, else the lowest free one.
func (c *component) adoptAddress(asked string, holders map[netip.Addr]string) (netip.Addr, error) {
	if asked == "" {
		ip, ok := c.lowestFree(holders)
		if !ok {
			return ip, status.Errorf(codes.FailedPrecondition, "the reserved range %s is full; widen it in network's config", c.reservedRange)
		}
		return ip, nil
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(asked))
	if err != nil {
		return ip, status.Errorf(codes.InvalidArgument, "%q is no address", asked)
	}
	if ip.Compare(c.reservedLo) < 0 || ip.Compare(c.reservedHi) > 0 {
		return ip, status.Errorf(codes.InvalidArgument, "%s is outside the reserved range %s", ip, c.reservedRange)
	}
	return ip, nil
}

// dhcpInterface is the interface whose subnet holds ip and whose DHCP
// server is enabled: where its static mapping goes.
func (c *component) dhcpInterface(ctx context.Context, ip netip.Addr) (string, error) {
	ifaces, err := c.pfsense.ListInterfaces(ctx, &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "can't read the interfaces: %s", status.Convert(err).Message())
	}
	servers, err := c.pfsense.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "can't read the DHCP servers: %s", status.Convert(err).Message())
	}
	enabled := map[string]bool{}
	for _, s := range servers.GetServers() {
		enabled[s.GetInterface()] = s.GetEnabled()
	}
	for _, i := range ifaces.GetInterfaces() {
		addr, err := netip.ParseAddr(i.GetIpv4Address())
		if err != nil || !enabled[i.GetName()] {
			continue
		}
		if p, err := addr.Prefix(int(i.GetIpv4Prefix())); err == nil && p.Contains(ip) {
			return i.GetName(), nil
		}
	}
	return "", status.Errorf(codes.FailedPrecondition, "no interface with an enabled DHCP server has %s in its subnet", ip)
}

func find(devices []*networkpb.Device, mac string) *networkpb.Device {
	for _, d := range devices {
		if d.GetMac() == mac {
			return d
		}
	}
	return nil
}

func nameOrMAC(d *networkpb.Device) string { return cmp.Or(d.GetName(), d.GetMac()) }

// validHostname is a hostname pfSense accepts on a static mapping; one a
// Device sent that isn't is left out.
var validHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
