// Package cloudflareapi is the cloudflare-api Component: it reads and
// writes one remotely managed Cloudflare Tunnel through Cloudflare's API
// v4 with an API token: the Tunnel and its connectors, its ingress
// configuration, and the account's zones and DNS records. Its config is
// valid when empty, and it then starts not set up.
package cloudflareapi

import (
	"context"
	_ "embed"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one API call.
const requestTimeout = 10 * time.Second

// Definition is cloudflare-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.CloudflareApiConfig]{
		Manifest: manifest,
		Service:  &pb.CloudflareApiService_ServiceDesc,
		New:      func() sdk.Component[*pb.CloudflareApiConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedCloudflareApiServiceServer

	// api is nil while not set up.
	api               *api
	account, tunnelID string

	tunnel *sdk.Cache[*pb.GetTunnelResponse]
	config *sdk.Cache[*configuration]
	zones  *sdk.Cache[*pb.ListZonesResponse]

	mu sync.Mutex
	// records caches each zone's DNS records, by zone id.
	records map[string]*sdk.Cache[*pb.ListDnsRecordsResponse]
	// lastErr is why the last Tunnel read failed; nil when it worked.
	lastErr error
	// writeMu makes the ingress writes take turns: each reads the
	// configuration and PUTs it whole.
	writeMu sync.Mutex
}

var (
	accountPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// notSetUp is every call's answer but GetTunnel's while the config is
// empty.
var notSetUp = status.Error(codes.FailedPrecondition, "not set up")

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *pb.CloudflareApiConfig) error {
	account, tunnel, token := strings.TrimSpace(cfg.GetAccountId()), strings.ToLower(strings.TrimSpace(cfg.GetTunnelId())), cfg.GetApiToken()
	if account == "" && tunnel == "" && token == "" {
		return nil
	}
	var errs sdk.FieldErrors
	if !accountPattern.MatchString(account) {
		errs.Add("account_id", "must be the account's ID: 32 hex digits")
	}
	if !uuidPattern.MatchString(tunnel) {
		errs.Add("tunnel_id", "must be the Tunnel's ID, a UUID")
	}
	if token == "" {
		errs.Add("api_token", "is required with an account and a Tunnel")
	}
	base, err := url.Parse(cfg.GetApiUrl())
	if cfg.ApiUrl == nil {
		base, err = url.Parse("https://api.cloudflare.com/client/v4")
	}
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		errs.Add("api_url", "must be an http or https URL")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	c.account, c.tunnelID = account, tunnel
	c.api = &api{base: strings.TrimSuffix(base.String(), "/"), token: token, http: &http.Client{Timeout: requestTimeout}}
	c.tunnel = sdk.Cached(sdk.TTLFast, c.fetchTunnel)
	c.config = sdk.Cached(sdk.TTLSlow, c.fetchConfiguration)
	c.zones = sdk.Cached(sdk.TTLSlow, c.fetchZones)
	c.records = map[string]*sdk.Cache[*pb.ListDnsRecordsResponse]{}
	return nil
}

// Health is down while the last read of the Tunnel failed: Cloudflare
// doesn't answer or refuses the token. Not set up is OK.
func (c *component) Health(ctx context.Context) sdk.Health {
	if c.api == nil {
		return sdk.Health{State: sdk.OK, Message: "not set up"}
	}
	_, _ = c.tunnel.Get(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != nil {
		return sdk.Health{State: sdk.Down, Message: status.Convert(c.lastErr).Message()}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) tunnelPath() string {
	return "/accounts/" + c.account + "/cfd_tunnel/" + c.tunnelID
}

func (c *component) GetTunnel(ctx context.Context, req *pb.GetTunnelRequest) (*pb.GetTunnelResponse, error) {
	if c.api == nil {
		return &pb.GetTunnelResponse{}, nil
	}
	if req.GetFresh() {
		c.tunnel.Invalidate()
	}
	return c.tunnel.Get(ctx)
}

// tunnelRow is the Tunnel as Cloudflare sends it.
type tunnelRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	RemoteConfig bool   `json:"remote_config"`
}

// connectorRow is one connector of .../connections.
type connectorRow struct {
	ID      string    `json:"id"`
	Version string    `json:"version"`
	Arch    string    `json:"arch"`
	RunAt   time.Time `json:"run_at"`
	Conns   []struct {
		ID       string    `json:"id"`
		ColoName string    `json:"colo_name"`
		OriginIP string    `json:"origin_ip"`
		OpenedAt time.Time `json:"opened_at"`
	} `json:"conns"`
}

var tunnelStatuses = map[string]pb.TunnelStatus{
	"healthy":  pb.TunnelStatus_TUNNEL_STATUS_HEALTHY,
	"degraded": pb.TunnelStatus_TUNNEL_STATUS_DEGRADED,
	"down":     pb.TunnelStatus_TUNNEL_STATUS_DOWN,
	"inactive": pb.TunnelStatus_TUNNEL_STATUS_INACTIVE,
}

func (c *component) fetchTunnel(ctx context.Context) (*pb.GetTunnelResponse, error) {
	t, err := c.readTunnel(ctx)
	c.mu.Lock()
	c.lastErr = err
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &pb.GetTunnelResponse{SetUp: true, Tunnel: t}, nil
}

func (c *component) readTunnel(ctx context.Context) (*pb.Tunnel, error) {
	var row tunnelRow
	if err := c.api.call(ctx, http.MethodGet, c.tunnelPath(), nil, nil, &row); err != nil {
		return nil, err
	}
	var conns []connectorRow
	if err := c.api.call(ctx, http.MethodGet, c.tunnelPath()+"/connections", nil, nil, &conns); err != nil {
		return nil, err
	}
	t := &pb.Tunnel{
		Id:              row.ID,
		Name:            row.Name,
		Status:          tunnelStatuses[row.Status],
		RemotelyManaged: row.RemoteConfig,
		CnameTarget:     row.ID + ".cfargotunnel.com",
	}
	for _, r := range conns {
		k := &pb.Connector{Id: r.ID, Version: r.Version, Arch: r.Arch, RunAt: stamp(r.RunAt)}
		for _, x := range r.Conns {
			k.Connections = append(k.Connections, &pb.Connection{Id: x.ID, ColoName: x.ColoName, OriginIp: x.OriginIP, OpenedAt: stamp(x.OpenedAt)})
		}
		t.Connectors = append(t.Connectors, k)
	}
	return t, nil
}

func stamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func (c *component) GetConfiguration(ctx context.Context, req *pb.GetConfigurationRequest) (*pb.GetConfigurationResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	if req.GetFresh() {
		c.config.Invalidate()
	}
	cfg, err := c.config.Get(ctx)
	if err != nil {
		return nil, err
	}
	return cfg.proto(), nil
}

func (c *component) ListZones(ctx context.Context, req *pb.ListZonesRequest) (*pb.ListZonesResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	if req.GetFresh() {
		c.zones.Invalidate()
	}
	return c.zones.Get(ctx)
}

func (c *component) fetchZones(ctx context.Context) (*pb.ListZonesResponse, error) {
	rows, err := list[struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}](ctx, c.api, "/zones", url.Values{"account.id": {c.account}}, 50)
	if err != nil {
		return nil, err
	}
	out := &pb.ListZonesResponse{}
	for _, r := range rows {
		out.Zones = append(out.Zones, &pb.Zone{Id: r.ID, Name: r.Name, Status: r.Status})
	}
	slices.SortFunc(out.Zones, func(a, b *pb.Zone) int { return strings.Compare(a.GetName(), b.GetName()) })
	return out, nil
}

func (c *component) ListDnsRecords(ctx context.Context, req *pb.ListDnsRecordsRequest) (*pb.ListDnsRecordsResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	if req.GetZoneId() == "" {
		return nil, status.Error(codes.InvalidArgument, "zone_id is required")
	}
	cache := c.recordsOf(req.GetZoneId())
	if req.GetFresh() {
		cache.Invalidate()
	}
	return cache.Get(ctx)
}

// recordsOf is zone's DNS record cache.
func (c *component) recordsOf(zone string) *sdk.Cache[*pb.ListDnsRecordsResponse] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.records[zone]; ok {
		return r
	}
	r := sdk.Cached(sdk.TTLSlow, func(ctx context.Context) (*pb.ListDnsRecordsResponse, error) { return c.fetchRecords(ctx, zone) })
	c.records[zone] = r
	return r
}

// recordRow is a DNS record as Cloudflare sends it.
type recordRow struct {
	ID      string `json:"id"`
	ZoneID  string `json:"zone_id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (r recordRow) proto(zone string) *pb.DnsRecord {
	if r.ZoneID != "" {
		zone = r.ZoneID
	}
	return &pb.DnsRecord{Id: r.ID, ZoneId: zone, Fields: &pb.DnsRecordFields{Name: r.Name, Type: r.Type, Content: r.Content, Proxied: r.Proxied, Comment: r.Comment}}
}

func (c *component) fetchRecords(ctx context.Context, zone string) (*pb.ListDnsRecordsResponse, error) {
	rows, err := list[recordRow](ctx, c.api, "/zones/"+url.PathEscape(zone)+"/dns_records", nil, 500)
	if err != nil {
		return nil, err
	}
	out := &pb.ListDnsRecordsResponse{}
	for _, r := range rows {
		out.Records = append(out.Records, r.proto(zone))
	}
	slices.SortStableFunc(out.Records, func(a, b *pb.DnsRecord) int {
		if n := strings.Compare(a.GetFields().GetName(), b.GetFields().GetName()); n != 0 {
			return n
		}
		return strings.Compare(a.GetFields().GetType(), b.GetFields().GetType())
	})
	return out, nil
}

func (c *component) VerifyAccess(ctx context.Context, _ *pb.VerifyAccessRequest) (*pb.VerifyAccessResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	t, err := c.GetTunnel(ctx, &pb.GetTunnelRequest{Fresh: true})
	if err != nil {
		return nil, err
	}
	if _, err := c.GetConfiguration(ctx, &pb.GetConfigurationRequest{Fresh: true}); err != nil {
		return nil, err
	}
	zones, err := c.ListZones(ctx, &pb.ListZonesRequest{Fresh: true})
	if err != nil {
		return nil, err
	}
	return &pb.VerifyAccessResponse{
		TunnelName:      t.GetTunnel().GetName(),
		RemotelyManaged: t.GetTunnel().GetRemotelyManaged(),
		Zones:           int32(len(zones.GetZones())),
	}, nil
}
