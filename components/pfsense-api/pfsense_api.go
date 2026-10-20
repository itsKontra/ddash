// Package pfsenseapi is the pfsense-api Component: it reads the pfSense
// firewall through the pfSense-pkg-RESTAPI package's REST API v2, with an
// API key sent as X-API-Key.
package pfsenseapi

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one REST call, so a dead firewall can't hold a
// getter past the host's deadline.
const requestTimeout = 5 * time.Second

// Definition is pfsense-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pfsenseapipb.PfsenseApiConfig]{
		Manifest: manifest,
		Service:  &pfsenseapipb.PfsenseApiService_ServiceDesc,
		New:      func() sdk.Component[*pfsenseapipb.PfsenseApiConfig] { return &component{} },
	})
}

type component struct {
	pfsenseapipb.UnimplementedPfsenseApiServiceServer
	api        *api
	gateways   *sdk.Cache[*pfsenseapipb.ListGatewaysResponse]
	groups     *sdk.Cache[*pfsenseapipb.ListGatewayGroupsResponse]
	interfaces *sdk.Cache[*pfsenseapipb.ListInterfacesResponse]
	tunnels    *sdk.Cache[*pfsenseapipb.ListWireguardTunnelsResponse]
	rules      *sdk.Cache[*pfsenseapipb.ListFirewallRulesResponse]
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *pfsenseapipb.PfsenseApiConfig) error {
	base, err := url.Parse(cfg.GetUrl())
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		var errs sdk.FieldErrors
		errs.Add("url", "must be the firewall's web address, like https://198.18.1.1")
		return errs.Err()
	}
	c.api = &api{base: strings.TrimSuffix(base.String(), "/"), key: cfg.GetApiKey(), http: &http.Client{Timeout: requestTimeout}}
	c.gateways = sdk.Cached(sdk.TTLFast, c.fetchGateways)
	c.groups = sdk.Cached(sdk.TTLFast, c.fetchGroups)
	c.interfaces = sdk.Cached(countersTTL, c.fetchInterfaces)
	c.tunnels = sdk.Cached(sdk.TTLSlow, c.fetchTunnels)
	c.rules = sdk.Cached(sdk.TTLSlow, c.fetchRules)
	return nil
}

// Health is down while the firewall can't be read with the key. It shares
// the gateway read, so it costs no extra request on the FAST tier.
func (c *component) Health(ctx context.Context) sdk.Health {
	if _, err := c.gateways.Get(ctx); err != nil {
		return sdk.Health{State: sdk.Down, Message: status.Convert(err).Message()}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) ListGateways(ctx context.Context, _ *pfsenseapipb.ListGatewaysRequest) (*pfsenseapipb.ListGatewaysResponse, error) {
	return c.gateways.Get(ctx)
}

func (c *component) ListGatewayGroups(ctx context.Context, _ *pfsenseapipb.ListGatewayGroupsRequest) (*pfsenseapipb.ListGatewayGroupsResponse, error) {
	return c.groups.Get(ctx)
}

// gatewayStatus is one entry of GET /api/v2/status/gateways. pfSense sends
// null for what dpinger hasn't measured.
type gatewayStatus struct {
	Name      string   `json:"name"`
	SrcIP     string   `json:"srcip"`
	MonitorIP string   `json:"monitorip"`
	Delay     *float64 `json:"delay"`
	Stddev    *float64 `json:"stddev"`
	Loss      *float64 `json:"loss"`
	Status    string   `json:"status"`
	Substatus string   `json:"substatus"`
}

func (c *component) fetchGateways(ctx context.Context) (*pfsenseapipb.ListGatewaysResponse, error) {
	var data []gatewayStatus
	if err := c.api.get(ctx, "/api/v2/status/gateways", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListGatewaysResponse{}
	for _, g := range data {
		out.Gateways = append(out.Gateways, &pfsenseapipb.Gateway{
			Name:        g.Name,
			Status:      g.Status,
			Substatus:   g.Substatus,
			DelayMs:     g.Delay,
			StddevMs:    g.Stddev,
			LossPercent: g.Loss,
			MonitorIp:   g.MonitorIP,
			SourceIp:    g.SrcIP,
		})
	}
	return out, nil
}

// gatewayGroup is one entry of GET /api/v2/routing/gateway/groups.
type gatewayGroup struct {
	Name       string `json:"name"`
	Trigger    string `json:"trigger"`
	Descr      string `json:"descr"`
	Priorities []struct {
		Gateway string `json:"gateway"`
		Tier    int32  `json:"tier"`
	} `json:"priorities"`
}

func (c *component) fetchGroups(ctx context.Context) (*pfsenseapipb.ListGatewayGroupsResponse, error) {
	var data []gatewayGroup
	if err := c.api.get(ctx, "/api/v2/routing/gateway/groups", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListGatewayGroupsResponse{}
	for _, g := range data {
		group := &pfsenseapipb.GatewayGroup{Name: g.Name, Description: g.Descr, Trigger: g.Trigger}
		for _, p := range g.Priorities {
			group.Members = append(group.Members, &pfsenseapipb.GatewayGroupMember{Gateway: p.Gateway, Tier: p.Tier})
		}
		slices.SortStableFunc(group.Members, func(a, b *pfsenseapipb.GatewayGroupMember) int {
			return cmp.Compare(a.GetTier(), b.GetTier())
		})
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

// countersTTL keeps interface counters for less than network's 5 s
// sampling interval, so every sample reads fresh counters.
const countersTTL = 2 * time.Second

func (c *component) ListInterfaces(ctx context.Context, _ *pfsenseapipb.ListInterfacesRequest) (*pfsenseapipb.ListInterfacesResponse, error) {
	return c.interfaces.Get(ctx)
}

func (c *component) ListWireguardTunnels(ctx context.Context, _ *pfsenseapipb.ListWireguardTunnelsRequest) (*pfsenseapipb.ListWireguardTunnelsResponse, error) {
	return c.tunnels.Get(ctx)
}

// ListFirewallRules reads every rule once per TTL and filters by interface,
// so asking for several interfaces costs one request.
func (c *component) ListFirewallRules(ctx context.Context, req *pfsenseapipb.ListFirewallRulesRequest) (*pfsenseapipb.ListFirewallRulesResponse, error) {
	all, err := c.rules.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListFirewallRulesResponse{}
	for _, r := range all.GetRules() {
		if req.GetInterface() == "" || slices.Contains(r.GetInterfaces(), req.GetInterface()) {
			out.Rules = append(out.Rules, r)
		}
	}
	return out, nil
}

// interfaceStats is one entry of GET /api/v2/status/interfaces.
type interfaceStats struct {
	Name     string `json:"name"`
	Descr    string `json:"descr"`
	HWIf     string `json:"hwif"`
	Status   string `json:"status"`
	IPAddr   string `json:"ipaddr"`
	Subnet   number `json:"subnet"`
	Gateway  string `json:"gateway"`
	InBytes  number `json:"inbytes"`
	OutBytes number `json:"outbytes"`
	InPkts   number `json:"inpkts"`
	OutPkts  number `json:"outpkts"`
}

func (c *component) fetchInterfaces(ctx context.Context) (*pfsenseapipb.ListInterfacesResponse, error) {
	var data []interfaceStats
	if err := c.api.get(ctx, "/api/v2/status/interfaces", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListInterfacesResponse{}
	for _, i := range data {
		out.Interfaces = append(out.Interfaces, &pfsenseapipb.Interface{
			Name:        i.Name,
			Description: i.Descr,
			HwInterface: i.HWIf,
			Status:      i.Status,
			Ipv4Address: i.IPAddr,
			Ipv4Prefix:  int32(i.Subnet),
			Gateway:     i.Gateway,
			InBytes:     uint64(i.InBytes),
			OutBytes:    uint64(i.OutBytes),
			InPackets:   uint64(i.InPkts),
			OutPackets:  uint64(i.OutPkts),
		})
	}
	return out, nil
}

// wireguardTunnel is one entry of GET /api/v2/status/wireguard/tunnels.
type wireguardTunnel struct {
	Name       string `json:"name"`
	Descr      string `json:"descr"`
	Status     string `json:"status"`
	ListenPort number `json:"listen_port"`
	RX         number `json:"transfer_rx"`
	TX         number `json:"transfer_tx"`
	Peers      []struct {
		PublicKey       string     `json:"public_key"`
		Descr           string     `json:"descr"`
		Endpoint        string     `json:"endpoint"`
		AllowedIPs      stringList `json:"allowed_ips"`
		LatestHandshake number     `json:"latest_handshake"`
		RX              number     `json:"transfer_rx"`
		TX              number     `json:"transfer_tx"`
	} `json:"peers"`
}

func (c *component) fetchTunnels(ctx context.Context) (*pfsenseapipb.ListWireguardTunnelsResponse, error) {
	var data []wireguardTunnel
	if err := c.api.get(ctx, "/api/v2/status/wireguard/tunnels", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListWireguardTunnelsResponse{}
	for _, t := range data {
		tunnel := &pfsenseapipb.WireguardTunnel{
			Name:        t.Name,
			Description: t.Descr,
			Status:      t.Status,
			ListenPort:  int32(t.ListenPort),
			RxBytes:     uint64(t.RX),
			TxBytes:     uint64(t.TX),
		}
		for _, p := range t.Peers {
			peer := &pfsenseapipb.WireguardPeer{
				PublicKey:   p.PublicKey,
				Description: p.Descr,
				AllowedIps:  p.AllowedIPs,
				RxBytes:     uint64(p.RX),
				TxBytes:     uint64(p.TX),
			}
			// wg shows "(none)" for a peer that never connected.
			if p.Endpoint != "(none)" {
				peer.Endpoint = p.Endpoint
			}
			if p.LatestHandshake > 0 {
				peer.LatestHandshake = timestamppb.New(time.Unix(int64(p.LatestHandshake), 0))
			}
			tunnel.Peers = append(tunnel.Peers, peer)
		}
		out.Tunnels = append(out.Tunnels, tunnel)
	}
	return out, nil
}

// firewallRule is one entry of GET /api/v2/firewall/rules.
type firewallRule struct {
	Type            string     `json:"type"`
	Interface       stringList `json:"interface"`
	IPProtocol      string     `json:"ipprotocol"`
	Protocol        string     `json:"protocol"`
	Source          string     `json:"source"`
	SourcePort      string     `json:"source_port"`
	Destination     string     `json:"destination"`
	DestinationPort string     `json:"destination_port"`
	Descr           string     `json:"descr"`
	Disabled        bool       `json:"disabled"`
}

func (c *component) fetchRules(ctx context.Context) (*pfsenseapipb.ListFirewallRulesResponse, error) {
	var data []firewallRule
	if err := c.api.get(ctx, "/api/v2/firewall/rules", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListFirewallRulesResponse{}
	for _, r := range data {
		out.Rules = append(out.Rules, &pfsenseapipb.FirewallRule{
			Type:            r.Type,
			Interfaces:      r.Interface,
			IpProtocol:      r.IPProtocol,
			Protocol:        r.Protocol,
			Source:          r.Source,
			SourcePort:      r.SourcePort,
			Destination:     r.Destination,
			DestinationPort: r.DestinationPort,
			Description:     r.Descr,
			Disabled:        r.Disabled,
		})
	}
	return out, nil
}

// number decodes a whole JSON number, a numeric string (pfSense sends some
// numbers as text, e.g. a subnet "24" or a listen port "51820"), or null
// or "" as 0.
type number uint64

func (n *number) UnmarshalJSON(b []byte) error {
	text := strings.Trim(string(b), `"`)
	if text == "null" || text == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("not a whole number: %s", b)
	}
	*n = number(v)
	return nil
}

// stringList decodes a JSON list of strings, a comma-separated string or null.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = nil
	switch v := v.(type) {
	case string:
		for p := range strings.SplitSeq(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				*s = append(*s, p)
			}
		}
	case []any:
		for _, p := range v {
			if p, ok := p.(string); ok {
				*s = append(*s, p)
			}
		}
	}
	return nil
}

// api is the firewall's REST API v2.
type api struct {
	base string
	key  string
	http *http.Client
}

// envelope is the JSON every REST API v2 answer is wrapped in.
type envelope struct {
	Code       int             `json:"code"`
	Status     string          `json:"status"`
	ResponseID string          `json:"response_id"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

// get reads path and decodes the envelope's data into out. Any failure is
// Unavailable, naming what the firewall said.
func (a *api) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", a.key)
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return status.Errorf(codes.Unavailable, "pfSense unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return status.Errorf(codes.Unavailable, "pfSense: reading %s: %v", path, err)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return status.Errorf(codes.Unavailable, "pfSense answered %s with %d and no REST API JSON", path, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return status.Errorf(codes.Unavailable, "pfSense answered %s with %d %s: %s", path, resp.StatusCode, env.ResponseID, env.Message)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return status.Errorf(codes.Unavailable, "pfSense: %s: %v", path, fmt.Errorf("decode data: %w", err))
	}
	return nil
}
