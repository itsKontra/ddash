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
