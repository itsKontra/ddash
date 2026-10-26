package network

import (
	"context"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/network/networkpb"
)

// GetNetworkInfo answers by address from the cached read the Devices list
// comes from, so a server with a fixed address gets its path and Ads
// Allowed as a Device does.
func (c *component) GetNetworkInfo(ctx context.Context, req *networkpb.GetNetworkInfoRequest) (*networkpb.GetNetworkInfoResponse, error) {
	ips := make([]netip.Addr, len(req.GetAddresses()))
	for i, a := range req.GetAddresses() {
		ip, err := netip.ParseAddr(strings.TrimSpace(a))
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%q is no IP address", a)
		}
		ips[i] = ip
	}
	s, err := c.devices.Get(ctx)
	if err != nil {
		return nil, err
	}
	devices := map[netip.Addr]*networkpb.Device{}
	for _, d := range s.list.GetDevices() {
		if ip, err := netip.ParseAddr(d.GetIp()); err == nil {
			devices[ip] = d
		}
	}
	paths := c.paths(s.aliases)
	out := &networkpb.GetNetworkInfoResponse{AdguardError: s.list.GetAdguardError()}
	for i, ip := range ips {
		info := &networkpb.NetworkInfo{Address: req.GetAddresses()[i], InternetPath: paths.of(ip), AdsAllowed: s.adsAllowed[ip]}
		if d := devices[ip]; d != nil {
			info.Device = proto.CloneOf(d)
		}
		out.Infos = append(out.Infos, info)
	}
	return out, nil
}
