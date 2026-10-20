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

// Direct and Tunnelled: membership of the Device's own address in the
// bypass alias, written through pfsense-api (queued and applied at once).
// network writes only that member of only the bypass alias; any other
// member is no Device's, and stays as it is.

func (c *component) SetDirect(ctx context.Context, req *networkpb.SetDirectRequest) (*networkpb.SetDirectResponse, error) {
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
	if msg := checkAlias(aliases.GetAliases(), c.bypassAlias); msg != "" {
		return nil, status.Errorf(codes.FailedPrecondition, "bypass alias: %s; pick another in network's config", msg)
	}
	byName := map[string]*pfsenseapipb.Alias{}
	for _, a := range aliases.GetAliases() {
		byName[a.GetName()] = a
	}
	// own is the member that is the Device's address; other the first
	// member that isn't but holds it all the same.
	var own, other string
	seen := map[string]bool{c.bypassAlias: true}
	for _, m := range byName[c.bypassAlias].GetAddresses() {
		if a, err := netip.ParseAddr(strings.TrimSpace(m)); err == nil && a == ip {
			own = m
		} else if other == "" && holds(byName, m, ip, seen) {
			other = m
		}
	}

	switch {
	case req.GetDirect() && own == "" && other == "":
		add := &pfsenseapipb.AddAliasAddressRequest{Alias: c.bypassAlias, Address: ip.String(), Detail: nameOrMAC(d)}
		if _, err := c.pfsense.AddAliasAddress(ctx, add); err != nil {
			return nil, err
		}
	case !req.GetDirect() && other != "":
		return nil, status.Errorf(codes.FailedPrecondition,
			"%s stays Direct: %s holds %s through %s, which is no Device's address and which ddash leaves alone; change it in pfSense",
			nameOrMAC(d), c.bypassAlias, ip, other)
	case !req.GetDirect() && own != "":
		remove := &pfsenseapipb.RemoveAliasAddressRequest{Alias: c.bypassAlias, Address: own}
		if _, err := c.pfsense.RemoveAliasAddress(ctx, remove); err != nil {
			return nil, err
		}
	}
	d, err = c.reread(ctx, mac)
	if err != nil {
		return nil, err
	}
	return &networkpb.SetDirectResponse{Device: d}, nil
}
