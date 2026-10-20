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
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	api      *api
	gateways *sdk.Cache[*pfsenseapipb.ListGatewaysResponse]
	groups   *sdk.Cache[*pfsenseapipb.ListGatewayGroupsResponse]
	leases   *sdk.Cache[*pfsenseapipb.ListLeasesResponse]
	servers  *sdk.Cache[*pfsenseapipb.ListDhcpServersResponse]
	aliases  *sdk.Cache[*pfsenseapipb.ListAliasesResponse]
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
