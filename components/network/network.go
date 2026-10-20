// Package network is the network Component, the logic behind the Network
// Page. It watches the firewall's failover gateway group and derives the
// VPN Tunnel's tier from it (reporting the VPN Tunnel item to checks, so
// both tiers down is notified), samples WAN and LAN throughput into an
// in-memory ring, reads Remote Access, and turns a Device's ad-blocking off
// or on through AdGuard.
package network

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// PollInterval is how often network reads the failover gateway group.
// pfSense's dpinger already applies hysteresis, so a minute is enough.
const PollInterval = time.Minute

// pollTimeout bounds one read of the group and the gateways.
const pollTimeout = 20 * time.Second

// Definition is network, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*networkpb.NetworkConfig]{
		Manifest: manifest,
		Service:  &networkpb.NetworkService_ServiceDesc,
		New:      func() sdk.Component[*networkpb.NetworkConfig] { return &component{ready: make(chan struct{})} },
	})
}

type component struct {
	networkpb.UnimplementedNetworkServiceServer
	pfsense pfsenseapipb.PfsenseApiServiceClient
	adguard adguardapipb.AdguardApiServiceClient
	checks  checkspb.ChecksServiceClient
	log     *slog.Logger

	// ready is closed once the first read finished.
	ready     chan struct{}
	readyOnce sync.Once

	// reported is the VPN Tunnel item as checks last accepted it
	// (vpn_item.go); only poll touches it.
	reported *checkspb.ReportStateRequest

	mu     sync.Mutex
	tunnel *networkpb.GetVpnTunnelResponse

	// The Device join (devices.go).
	bypassAlias string
	devices     *sdk.Cache[*networkpb.ListDevicesResponse]
	// Where Adopt reserves addresses (adopt.go), both ends included.
	reservedRange          string
	reservedLo, reservedHi netip.Addr

	throughput throughput
}

func (c *component) Start(ctx context.Context, env *sdk.Env, cfg *networkpb.NetworkConfig) error {
	c.pfsense = sdk.Require(env, pfsenseapipb.NewPfsenseApiServiceClient)
	c.adguard = sdk.Require(env, adguardapipb.NewAdguardApiServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.log = env.Log
	if err := c.startDevices(ctx, env, cfg); err != nil {
		return err
	}
	sdk.Every(ctx, PollInterval, c.poll)
	sdk.Every(ctx, SampleInterval, func(ctx context.Context) { c.throughput.sample(ctx, c.pfsense) })
	return nil
}

// GetThroughput answers from the ring.
func (c *component) GetThroughput(_ context.Context, req *networkpb.GetThroughputRequest) (*networkpb.GetThroughputResponse, error) {
	return c.throughput.get(req.GetSince()), nil
}

// Health is degraded while the gateway group can't be read or makes no
// sense; the VPN Tunnel being down is the tier's news, not network's.
func (c *component) Health(context.Context) sdk.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tunnel != nil && c.tunnel.GetError() != "" {
		return sdk.Health{State: sdk.Degraded, Message: c.tunnel.GetError()}
	}
	return sdk.Health{State: sdk.OK}
}

// GetVpnTunnel answers from the last read, waiting for the first one.
func (c *component) GetVpnTunnel(ctx context.Context, _ *networkpb.GetVpnTunnelRequest) (*networkpb.GetVpnTunnelResponse, error) {
	select {
	case <-c.ready:
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tunnel == nil {
		return nil, status.Error(codes.Unavailable, "network has no read of the gateway group yet")
	}
	return proto.CloneOf(c.tunnel), nil
}

// poll reads the gateway group and the gateways, derives the tier and
// reports the VPN Tunnel item to checks.
func (c *component) poll(ctx context.Context) {
	// Even a read that panics (sdk.Every reports it) must not leave the
	// getter waiting.
	defer c.readyOnce.Do(func() { close(c.ready) })
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	tunnel, err := c.read(ctx)
	if err != nil {
		tunnel = &networkpb.GetVpnTunnelResponse{Error: err.Error()}
	}
	c.mu.Lock()
	c.tunnel = tunnel
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	c.report(ctx, tunnel)
}

func (c *component) read(ctx context.Context) (*networkpb.GetVpnTunnelResponse, error) {
	groups, err := c.pfsense.ListGatewayGroups(ctx, &pfsenseapipb.ListGatewayGroupsRequest{})
	if err != nil {
		return nil, fmt.Errorf("can't read the gateway groups: %s", status.Convert(err).Message())
	}
	group := failoverGroup(groups.GetGroups())
	if group == nil {
		return nil, errNoFailoverGroup
	}
	gateways, err := c.pfsense.ListGateways(ctx, &pfsenseapipb.ListGatewaysRequest{})
	if err != nil {
		return nil, fmt.Errorf("can't read the gateways: %s", status.Convert(err).Message())
	}
	return derive(group, gateways.GetGateways()), nil
}

var errNoFailoverGroup = errors.New("pfSense has no failover gateway group (a group with gateways on two tiers)")

// failoverGroup is the VPN Tunnel's group: the first whose members sit on
// more than one tier. Other groups (single-tier, load balancing) are not it.
func failoverGroup(groups []*pfsenseapipb.GatewayGroup) *pfsenseapipb.GatewayGroup {
	for _, g := range groups {
		tiers := map[int32]bool{}
		for _, m := range g.GetMembers() {
			tiers[m.GetTier()] = true
		}
		if len(tiers) > 1 {
			return g
		}
	}
	return nil
}

// derive works out which tier carries the VPN Tunnel: the group's first
// tier is WireGuard, any later one the OpenVPN fallback.
func derive(group *pfsenseapipb.GatewayGroup, gateways []*pfsenseapipb.Gateway) *networkpb.GetVpnTunnelResponse {
	byName := map[string]*pfsenseapipb.Gateway{}
	for _, g := range gateways {
		byName[g.GetName()] = g
	}
	members := slices.Clone(group.GetMembers())
	slices.SortStableFunc(members, func(a, b *pfsenseapipb.GatewayGroupMember) int { return int(a.GetTier() - b.GetTier()) })

	out := &networkpb.GetVpnTunnelResponse{Group: group.GetName(), Tier: networkpb.Tier_TIER_DOWN}
	for _, m := range members {
		g := byName[m.GetGateway()]
		tg := &networkpb.TunnelGateway{
			Name:      m.GetGateway(),
			Tier:      m.GetTier(),
			Up:        passes(g, group.GetTrigger()),
			Status:    g.GetStatus(),
			Substatus: g.GetSubstatus(),
		}
		if g != nil {
			tg.DelayMs, tg.LossPercent = g.DelayMs, g.LossPercent
		}
		out.Gateways = append(out.Gateways, tg)
		if tg.GetUp() && out.GetTier() == networkpb.Tier_TIER_DOWN {
			out.Tier = networkpb.Tier_TIER_OPENVPN
			if m.GetTier() == members[0].GetTier() {
				out.Tier = networkpb.Tier_TIER_WIREGUARD
			}
		}
	}
	return out
}

// passes reports whether pfSense routes the group's traffic over g: it is
// online (or not monitored), and the group's trigger doesn't exclude it
// for packet loss or latency. A gateway pfSense didn't report doesn't.
func passes(g *pfsenseapipb.Gateway, trigger string) bool {
	if g == nil {
		return false
	}
	st, sub := strings.ToLower(g.GetStatus()), strings.ToLower(g.GetSubstatus())
	if st != "online" && st != "none" {
		return false
	}
	if strings.Contains(trigger, "loss") && (sub == "loss" || sub == "highloss") {
		return false
	}
	if strings.Contains(trigger, "latency") && (sub == "delay" || sub == "highdelay") {
		return false
	}
	return true
}
