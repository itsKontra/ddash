// Package cloudflaretunnel is the cloudflare-tunnel Component: the
// Cloudflare Tunnel's Public Hostnames. A Public Hostname is one ingress
// rule of the Tunnel plus its proxied CNAME to "<tunnel id>.cfargotunnel.com",
// both read and written through cloudflare-api. Disabling one removes
// both and keeps their settings in this Component's DB, from which
// Enable restores them. Every change is a Tunnel Action with a Plan.
package cloudflaretunnel

import (
	"context"
	"database/sql"
	"embed"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cfpb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	pb "github.com/itsKontra/ddash/components/cloudflare-tunnel/cloudflaretunnelpb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// Definition is cloudflare-tunnel, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.CloudflareTunnelConfig]{
		Manifest:   manifest,
		Service:    &pb.CloudflareTunnelService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*pb.CloudflareTunnelConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedCloudflareTunnelServiceServer
	cf    cfpb.CloudflareApiServiceClient
	store store
	// turn makes Tunnel Actions run one at a time: each reads, plans and
	// writes alone.
	turn sync.Mutex
}

func (c *component) Start(_ context.Context, env *sdk.Env, _ *pb.CloudflareTunnelConfig) error {
	c.cf = sdk.Require(env, cfpb.NewCloudflareApiServiceClient)
	c.store = store{db: env.DB}
	return nil
}

// Health is cloudflare-api's to tell: this Component holds only the kept
// settings.
func (c *component) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

// snapshot is what the Public Hostnames and a Plan are built from: the
// Tunnel, its configuration, the zones with their DNS records, and the
// kept settings.
type snapshot struct {
	setUp, remote bool
	target        string
	config        *cfpb.GetConfigurationResponse
	zones         []*cfpb.Zone
	records       map[string][]*cfpb.DnsRecord
	disabled      []kept
}

// read takes a snapshot, through cloudflare-api's caches or, fresh, from
// Cloudflare. Not set up, only setUp is false.
func (c *component) read(ctx context.Context, fresh bool) (*snapshot, error) {
	t, err := c.cf.GetTunnel(ctx, &cfpb.GetTunnelRequest{Fresh: fresh})
	if err != nil {
		return nil, err
	}
	if !t.GetSetUp() {
		return &snapshot{}, nil
	}
	s := &snapshot{setUp: true, remote: t.GetTunnel().GetRemotelyManaged(), target: t.GetTunnel().GetCnameTarget(), records: map[string][]*cfpb.DnsRecord{}}
	if s.config, err = c.cf.GetConfiguration(ctx, &cfpb.GetConfigurationRequest{Fresh: fresh}); err != nil {
		return nil, err
	}
	zones, err := c.cf.ListZones(ctx, &cfpb.ListZonesRequest{Fresh: fresh})
	if err != nil {
		return nil, err
	}
	s.zones = zones.GetZones()
	// One read per zone, made together; the first zone that fails is the
	// error.
	records := make([]*cfpb.ListDnsRecordsResponse, len(s.zones))
	errs := make([]error, len(s.zones))
	var reads sync.WaitGroup
	for i, z := range s.zones {
		reads.Go(func() {
			records[i], errs[i] = c.cf.ListDnsRecords(ctx, &cfpb.ListDnsRecordsRequest{ZoneId: z.GetId(), Fresh: fresh})
		})
	}
	reads.Wait()
	for i, z := range s.zones {
		if errs[i] != nil {
			return nil, errs[i]
		}
		s.records[z.GetId()] = records[i].GetRecords()
	}
	if s.disabled, err = c.store.all(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *component) ListPublicHostnames(ctx context.Context, _ *pb.ListPublicHostnamesRequest) (*pb.ListPublicHostnamesResponse, error) {
	s, err := c.read(ctx, false)
	if err != nil {
		return nil, err
	}
	if !s.setUp {
		return &pb.ListPublicHostnamesResponse{}, nil
	}
	out := &pb.ListPublicHostnamesResponse{SetUp: true, RemotelyManaged: s.remote, CatchAll: s.config.GetCatchAll(), CnameTarget: s.target}
	for _, z := range s.zones {
		out.Zones = append(out.Zones, z.GetName())
	}
	for _, r := range s.config.GetRules() {
		d := s.dns(r.GetHostname())
		ph := &pb.PublicHostname{Fields: fieldsOf(r), Enabled: true, Dns: d.state, DnsDetail: d.detail}
		if d.zone != nil {
			ph.Zone = d.zone.GetName()
		}
		out.PublicHostnames = append(out.PublicHostnames, ph)
	}
	for _, k := range s.disabled {
		ph := &pb.PublicHostname{Fields: k.fields()}
		if z := s.zoneOf(k.hostname); z != nil {
			ph.Zone = z.GetName()
		}
		out.PublicHostnames = append(out.PublicHostnames, ph)
	}
	return out, nil
}

func fieldsOf(r *cfpb.IngressRule) *pb.PublicHostnameFields {
	return &pb.PublicHostnameFields{Hostname: r.GetHostname(), Path: r.GetPath(), Service: r.GetService()}
}

// zoneOf is the zone hostname is in: the one with the longest name it
// ends in; nil for none.
func (s *snapshot) zoneOf(hostname string) *cfpb.Zone {
	var best *cfpb.Zone
	for _, z := range s.zones {
		n := z.GetName()
		if (hostname == n || strings.HasSuffix(hostname, "."+n)) && len(n) > len(best.GetName()) {
			best = z
		}
	}
	return best
}

// dnsInfo is what holds a hostname's name in DNS.
type dnsInfo struct {
	zone  *cfpb.Zone
	state pb.DnsState
	// detail names the records of another kind, for ELSEWHERE.
	detail string
	// cname is its CNAME to the Tunnel; nil for none.
	cname *cfpb.DnsRecord
	// others are the records holding the name that aren't that CNAME.
	others []*cfpb.DnsRecord
}

// dns is hostname's DNS as last read.
func (s *snapshot) dns(hostname string) dnsInfo {
	d := dnsInfo{zone: s.zoneOf(hostname)}
	if d.zone == nil {
		d.state = pb.DnsState_DNS_STATE_NO_ZONE
		return d
	}
	for _, r := range s.records[d.zone.GetId()] {
		f := r.GetFields()
		if !strings.EqualFold(f.GetName(), hostname) {
			continue
		}
		if f.GetType() == "CNAME" && strings.EqualFold(f.GetContent(), s.target) && d.cname == nil {
			d.cname = r
		} else {
			d.others = append(d.others, r)
		}
	}
	var held []string
	for _, r := range d.others {
		held = append(held, r.GetFields().GetType()+" "+r.GetFields().GetContent())
	}
	switch {
	case d.cname != nil && d.cname.GetFields().GetProxied():
		d.state = pb.DnsState_DNS_STATE_OK
	case d.cname != nil:
		d.state = pb.DnsState_DNS_STATE_NOT_PROXIED
	case len(d.others) > 0:
		d.state, d.detail = pb.DnsState_DNS_STATE_ELSEWHERE, strings.Join(held, ", ")
	default:
		d.state = pb.DnsState_DNS_STATE_MISSING
	}
	return d
}

// kept is a disabled Public Hostname's settings as stored.
type kept struct {
	hostname, path, service, originRequest string
}

func (k kept) fields() *pb.PublicHostnameFields {
	return &pb.PublicHostnameFields{Hostname: k.hostname, Path: k.path, Service: k.service}
}

func (k kept) rule() *cfpb.IngressRule {
	return &cfpb.IngressRule{Hostname: k.hostname, Path: k.path, Service: k.service, OriginRequest: k.originRequest}
}

// store is the kept settings of the disabled Public Hostnames.
type store struct {
	db *sql.DB
}

// all are the kept settings, by hostname then path.
func (s store) all(ctx context.Context) ([]kept, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hostname, path, service, origin_request FROM disabled_public_hostname ORDER BY hostname, path`)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read the kept settings: %v", err)
	}
	defer rows.Close()
	var out []kept
	for rows.Next() {
		var k kept
		if err := rows.Scan(&k.hostname, &k.path, &k.service, &k.originRequest); err != nil {
			return nil, status.Errorf(codes.Internal, "read the kept settings: %v", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// put keeps k, replacing what was kept for its hostname and path.
func (s store) put(ctx context.Context, k kept) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO disabled_public_hostname (hostname, path, service, origin_request, disabled_at) VALUES (?, ?, ?, ?, ?)`,
		k.hostname, k.path, k.service, k.originRequest, time.Now().Unix())
	return err
}

// replace swaps what was kept for name for k, keeping when it was
// disabled.
func (s store) replace(ctx context.Context, name *pb.PublicHostnameName, k kept) error {
	_, err := s.db.ExecContext(ctx, `UPDATE disabled_public_hostname SET hostname = ?, path = ?, service = ? WHERE hostname = ? AND path = ?`,
		k.hostname, k.path, k.service, name.GetHostname(), name.GetPath())
	return err
}

// drop forgets what was kept for name.
func (s store) drop(ctx context.Context, name *pb.PublicHostnameName) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM disabled_public_hostname WHERE hostname = ? AND path = ?`, name.GetHostname(), name.GetPath())
	return err
}

// findKept is the kept settings for name; ok false for none.
func (s *snapshot) findKept(name *pb.PublicHostnameName) (kept, bool) {
	i := slices.IndexFunc(s.disabled, func(k kept) bool { return k.hostname == name.GetHostname() && k.path == name.GetPath() })
	if i < 0 {
		return kept{}, false
	}
	return s.disabled[i], true
}

// findRule is the ingress rule for name; nil for none.
func (s *snapshot) findRule(name *pb.PublicHostnameName) *cfpb.IngressRule {
	for _, r := range s.config.GetRules() {
		if r.GetHostname() == name.GetHostname() && r.GetPath() == name.GetPath() {
			return r
		}
	}
	return nil
}
