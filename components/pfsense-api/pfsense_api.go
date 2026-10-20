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
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
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
	leases     *sdk.Cache[*pfsenseapipb.ListLeasesResponse]
	servers    *sdk.Cache[*pfsenseapipb.ListDhcpServersResponse]
	aliases    *sdk.Cache[*pfsenseapipb.ListAliasesResponse]
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
	c.leases = sdk.Cached(sdk.TTLSlow, c.fetchLeases)
	c.servers = sdk.Cached(sdk.TTLSlow, c.fetchServers)
	c.aliases = sdk.Cached(sdk.TTLSlow, c.fetchAliases)
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

func (c *component) ListLeases(ctx context.Context, _ *pfsenseapipb.ListLeasesRequest) (*pfsenseapipb.ListLeasesResponse, error) {
	return c.leases.Get(ctx)
}

func (c *component) ListDhcpServers(ctx context.Context, _ *pfsenseapipb.ListDhcpServersRequest) (*pfsenseapipb.ListDhcpServersResponse, error) {
	return c.servers.Get(ctx)
}

func (c *component) ListAliases(ctx context.Context, _ *pfsenseapipb.ListAliasesRequest) (*pfsenseapipb.ListAliasesResponse, error) {
	return c.aliases.Get(ctx)
}

// lease is one entry of GET /api/v2/status/dhcp_server/leases. Kea leaves
// most fields null on a dynamic lease.
type lease struct {
	IP           *string `json:"ip"`
	MAC          *string `json:"mac"`
	Hostname     *string `json:"hostname"`
	If           *string `json:"if"`
	Starts       *string `json:"starts"`
	Ends         *string `json:"ends"`
	ActiveStatus *string `json:"active_status"`
	OnlineStatus *string `json:"online_status"`
	Descr        *string `json:"descr"`
}

func (c *component) fetchLeases(ctx context.Context) (*pfsenseapipb.ListLeasesResponse, error) {
	var data []lease
	if err := c.api.get(ctx, "/api/v2/status/dhcp_server/leases", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListLeasesResponse{}
	for _, l := range data {
		out.Leases = append(out.Leases, &pfsenseapipb.Lease{
			Ip:           str(l.IP),
			Mac:          strings.ToLower(str(l.MAC)),
			Hostname:     str(l.Hostname),
			Interface:    str(l.If),
			Starts:       str(l.Starts),
			Ends:         str(l.Ends),
			ActiveStatus: str(l.ActiveStatus),
			OnlineStatus: str(l.OnlineStatus),
			Description:  str(l.Descr),
		})
	}
	return out, nil
}

// dhcpServer is one entry of GET /api/v2/services/dhcp_servers; its id is
// the interface.
type dhcpServer struct {
	ID        string  `json:"id"`
	Enable    bool    `json:"enable"`
	RangeFrom *string `json:"range_from"`
	RangeTo   *string `json:"range_to"`
	Pool      []struct {
		RangeFrom *string `json:"range_from"`
		RangeTo   *string `json:"range_to"`
	} `json:"pool"`
	Staticmap []struct {
		MAC      *string `json:"mac"`
		IPAddr   *string `json:"ipaddr"`
		Hostname *string `json:"hostname"`
		Descr    *string `json:"descr"`
	} `json:"staticmap"`
}

func (c *component) fetchServers(ctx context.Context) (*pfsenseapipb.ListDhcpServersResponse, error) {
	var data []dhcpServer
	if err := c.api.get(ctx, "/api/v2/services/dhcp_servers", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListDhcpServersResponse{}
	for _, s := range data {
		server := &pfsenseapipb.DhcpServer{Interface: s.ID, Enabled: s.Enable}
		addPool := func(from, to *string) {
			if str(from) != "" && str(to) != "" {
				server.Pools = append(server.Pools, &pfsenseapipb.AddressRange{From: str(from), To: str(to)})
			}
		}
		addPool(s.RangeFrom, s.RangeTo)
		for _, p := range s.Pool {
			addPool(p.RangeFrom, p.RangeTo)
		}
		for _, m := range s.Staticmap {
			server.StaticMappings = append(server.StaticMappings, &pfsenseapipb.StaticMapping{
				Interface:   s.ID,
				Mac:         strings.ToLower(str(m.MAC)),
				Ip:          str(m.IPAddr),
				Hostname:    str(m.Hostname),
				Description: str(m.Descr),
			})
		}
		out.Servers = append(out.Servers, server)
	}
	return out, nil
}

// alias is one entry of GET /api/v2/firewall/aliases.
type alias struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Descr   *string  `json:"descr"`
	Address []string `json:"address"`
	Detail  []string `json:"detail"`
}

func (c *component) fetchAliases(ctx context.Context) (*pfsenseapipb.ListAliasesResponse, error) {
	var data []alias
	if err := c.api.get(ctx, "/api/v2/firewall/aliases", &data); err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListAliasesResponse{Choices: &configpb.Choices{}}
	for _, a := range data {
		out.Aliases = append(out.Aliases, &pfsenseapipb.Alias{
			Name:        a.Name,
			Type:        a.Type,
			Description: str(a.Descr),
			Addresses:   a.Address,
			Details:     a.Detail,
		})
		if a.Type == "port" {
			continue
		}
		label := a.Name
		if d := str(a.Descr); d != "" {
			label += " — " + d
		}
		out.Choices.Choices = append(out.Choices.Choices, &configpb.Choice{Value: a.Name, Label: label})
	}
	return out, nil
}

// str is a JSON string pfSense may send as null.
func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
