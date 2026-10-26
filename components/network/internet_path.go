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

// Tunnelled, Direct and No Internet: membership of the Device's own
// address in the bypass and block aliases, moved through pfsense-api with
// one apply. network writes only that member of only those aliases; any
// other member is no Device's, and stays as it is.

func (c *component) SetInternetPath(ctx context.Context, req *networkpb.SetInternetPathRequest) (*networkpb.SetInternetPathResponse, error) {
	path := req.GetPath()
	switch path {
	case networkpb.InternetPath_INTERNET_PATH_TUNNELLED, networkpb.InternetPath_INTERNET_PATH_DIRECT:
	case networkpb.InternetPath_INTERNET_PATH_NO_INTERNET:
		if c.blockAlias == "" {
			return nil, status.Error(codes.FailedPrecondition, "No Internet needs the No Internet alias: pick your block rule's alias in network's config")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "pick Tunnelled, Direct or No Internet")
	}
	all, err := c.readDevices(ctx)
	if err != nil {
		return nil, err
	}
	mac := strings.ToLower(req.GetMac())
	d := find(all.GetDevices(), mac)
	if d == nil {
		return nil, status.Errorf(codes.NotFound, "no Device has the MAC %s", mac)
	}
	if !d.GetAdopted() {
		// A New Device's address is temporary: Adopt it first, so the
		// member written is the address it keeps.
		if d, err = c.adopt(ctx, mac, nameOrMAC(d), ""); err != nil {
			return nil, err
		}
	}
	ip, err := netip.ParseAddr(d.GetIp())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%s has no address", nameOrMAC(d))
	}

	aliases, err := c.pfsense.ListAliases(ctx, &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "can't read the aliases: %s", status.Convert(err).Message())
	}
	if err := c.checkAliases(aliases.GetAliases()); err != nil {
		return nil, err
	}
	paths := c.paths(byName(aliases.GetAliases()))
	to := map[networkpb.InternetPath]string{
		networkpb.InternetPath_INTERNET_PATH_DIRECT:      c.bypassAlias,
		networkpb.InternetPath_INTERNET_PATH_NO_INTERNET: c.blockAlias,
	}[path]
	move := &pfsenseapipb.MoveAliasAddressRequest{Address: ip.String(), Detail: nameOrMAC(d)}
	for _, name := range []string{c.bypassAlias, c.blockAlias} {
		if name == "" {
			continue
		}
		own, other := membership(paths.aliases, name, ip)
		if name == to {
			if own == "" && other == "" {
				move.To = name
			}
			continue
		}
		if own != "" {
			move.From = append(move.From, name)
		}
		// Held through another member, it stays in the alias. That only
		// keeps it from the path asked for when that alias wins: the block
		// alias always, the bypass alias over Tunnelled.
		if other != "" && (name == c.blockAlias || path == networkpb.InternetPath_INTERNET_PATH_TUNNELLED) {
			stays := "Direct"
			if name == c.blockAlias {
				stays = "No Internet"
			}
			return nil, status.Errorf(codes.FailedPrecondition,
				"%s stays %s: %s holds %s through %s, which is no Device's address and which ddash leaves alone; change it in pfSense",
				nameOrMAC(d), stays, name, ip, other)
		}
	}
	if len(move.GetFrom()) > 0 || move.GetTo() != "" {
		if _, err := c.pfsense.MoveAliasAddress(ctx, move); err != nil {
			return nil, err
		}
	}
	d, err = c.reread(ctx, mac)
	if err != nil {
		return nil, err
	}
	return &networkpb.SetInternetPathResponse{Device: d}, nil
}

// membership finds ip in the alias named name: own is the member that is
// ip itself, other the first member that isn't but holds it all the same
// (a network, a range or a nested alias).
func membership(aliases map[string]*pfsenseapipb.Alias, name string, ip netip.Addr) (own, other string) {
	seen := map[string]bool{name: true}
	for _, m := range aliases[name].GetAddresses() {
		if a, err := netip.ParseAddr(strings.TrimSpace(m)); err == nil && a == ip {
			own = m
		} else if other == "" && holds(aliases, m, ip, seen) {
			other = m
		}
	}
	return own, other
}
