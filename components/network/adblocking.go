package network

import (
	"context"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/network/networkpb"
)

// Per-Device ad-blocking: AdGuard's persistent client for the Device's
// address, joined by IP. Like Direct it works on Adopted Devices only, so
// the address the client names stays the Device's.

func (c *component) SetAdBlocking(ctx context.Context, req *networkpb.SetAdBlockingRequest) (*networkpb.SetAdBlockingResponse, error) {
	d, err := c.adopted(ctx, req.GetMac())
	if err != nil {
		return nil, err
	}
	if _, err := c.adguard.SetClientFiltering(ctx, &adguardapipb.SetClientFilteringRequest{Ip: d.GetIp(), Name: nameOrMAC(d), Enabled: req.GetEnabled()}); err != nil {
		return nil, err
	}
	d, err = c.reread(ctx, d.GetMac())
	if err != nil {
		return nil, err
	}
	return &networkpb.SetAdBlockingResponse{Device: d}, nil
}

// adopted returns mac's Device, Adopting a New one first at the lowest
// free reserved address under its name (its MAC when it has none): what a
// toggle on a New Device does.
func (c *component) adopted(ctx context.Context, mac string) (*networkpb.Device, error) {
	all, err := c.readDevices(ctx)
	if err != nil {
		return nil, err
	}
	mac = strings.ToLower(mac)
	d := find(all.GetDevices(), mac)
	switch {
	case d == nil:
		return nil, status.Errorf(codes.NotFound, "no Device has the MAC %s", mac)
	case d.GetAdopted():
		return d, nil
	}
	return c.adopt(ctx, mac, nameOrMAC(d), "")
}

// joinAdGuard marks the Devices whose address has a persistent client of
// its own that doesn't filter. A client for a network holding the address
// is not the Device's.
func joinAdGuard(devices []*networkpb.Device, clients []*adguardapipb.Client) {
	off := map[netip.Addr]bool{}
	for _, cl := range clients {
		for _, id := range cl.GetIds() {
			if ip, err := netip.ParseAddr(id); err == nil {
				off[ip] = !cl.GetUseGlobalSettings() && !cl.GetFilteringEnabled()
			}
		}
	}
	for _, d := range devices {
		if ip, err := netip.ParseAddr(d.GetIp()); err == nil {
			d.AdBlockingOff = off[ip]
		}
	}
}
