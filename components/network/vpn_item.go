package network

import (
	"context"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/network/networkpb"
)

// The VPN Tunnel item, reported to checks so the Operator is notified when
// both tiers are down and again when the VPN Tunnel recovers. Failing over
// to the OpenVPN fallback keeps it up: that is no Notification.
const (
	vpnItem     = "vpn-tunnel"
	vpnItemName = "VPN Tunnel"
	vpnItemPath = "/network"
)

// report reports the VPN Tunnel item to checks when it differs from what
// checks last accepted: so only on change, plus once after Start. Nothing
// is reported while the group can't be read (network's health, never
// notified). A refused report is tried again on the next poll. Only poll
// calls it, so c.reported needs no lock.
func (c *component) report(ctx context.Context, tunnel *networkpb.GetVpnTunnelResponse) {
	req := &checkspb.ReportStateRequest{Item: vpnItem, Name: vpnItemName, State: checkspb.State_STATE_UP, Path: vpnItemPath}
	switch tunnel.GetTier() {
	case networkpb.Tier_TIER_WIREGUARD, networkpb.Tier_TIER_OPENVPN:
	case networkpb.Tier_TIER_DOWN:
		req.State, req.Reason = checkspb.State_STATE_DOWN, whyDown(tunnel)
	default:
		return
	}
	if proto.Equal(req, c.reported) {
		return
	}
	if _, err := c.checks.ReportState(ctx, req); err != nil {
		c.log.Warn("report the VPN Tunnel item", "err", err)
		return
	}
	c.reported = req
}

// whyDown says why each of the group's gateways carries no traffic, e.g.
// "WG_VPN down, OVPN_VPN losing packets".
func whyDown(tunnel *networkpb.GetVpnTunnelResponse) string {
	var out []string
	for _, g := range tunnel.GetGateways() {
		why := strings.ToLower(g.GetStatus())
		switch sub := strings.ToLower(g.GetSubstatus()); {
		case why == "":
			why = "not reported by pfSense"
		case why != "online" && why != "none":
		case sub == "loss" || sub == "highloss":
			why = "losing packets"
		case sub == "delay" || sub == "highdelay":
			why = "high latency"
		}
		out = append(out, g.GetName()+" "+why)
	}
	return strings.Join(out, ", ")
}
