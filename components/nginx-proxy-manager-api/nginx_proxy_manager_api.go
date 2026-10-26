// Package nginxproxymanagerapi is the nginx-proxy-manager-api Component:
// it signs in to Nginx Proxy Manager's API on the admin port as a
// dedicated user, reads and writes its Proxy Hosts and reads its
// certificates. It is coded to what v2.10 accepts, with no version
// branching.
package nginxproxymanagerapi

import (
	"context"
	_ "embed"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one API call, so a dead Nginx Proxy Manager can't
// hold a getter past the host's deadline.
const requestTimeout = 5 * time.Second

// Definition is nginx-proxy-manager-api, ready for sdk.Serve.
func Definition() sdk.Definition { return define(time.Now) }

// define is nginx-proxy-manager-api telling the certificates' states by
// now.
func define(now func() time.Time) sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.NginxProxyManagerApiConfig]{
		Manifest: manifest,
		Service:  &pb.NginxProxyManagerApiService_ServiceDesc,
		New:      func() sdk.Component[*pb.NginxProxyManagerApiConfig] { return &component{now: now} },
	})
}

type component struct {
	pb.UnimplementedNginxProxyManagerApiServiceServer
	now   func() time.Time
	api   *api
	host  string // the URL's host, for the Reverse Proxy's address
	hosts *sdk.Cache[*pb.ListProxyHostsResponse]
	certs *sdk.Cache[*pb.ListCertificatesResponse]

	mu     sync.Mutex
	readAt time.Time
	goodAt time.Time
	// lastErr is why the last read failed; nil when it succeeded.
	lastErr error
}

func (c *component) Start(ctx context.Context, _ *sdk.Env, cfg *pb.NginxProxyManagerApiConfig) error {
	base, err := url.Parse(cfg.GetUrl())
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Hostname() == "" {
		var errs sdk.FieldErrors
		errs.Add("url", "must be Nginx Proxy Manager's admin address, like http://198.18.1.210:81")
		return errs.Err()
	}
	c.host = base.Hostname()
	c.api = &api{
		base:     strings.TrimSuffix(base.String(), "/"),
		email:    cfg.GetEmail(),
		password: cfg.GetPassword(),
		http:     &http.Client{Timeout: requestTimeout},
	}
	c.hosts = sdk.Cached(sdk.TTLSlow, c.fetchProxyHosts)
	c.certs = sdk.Cached(sdk.TTLSlow, c.fetchCertificates)
	// Sign in at once, and keep the token renewed while nothing reads.
	sdk.Every(ctx, sdk.TTLSlow, func(ctx context.Context) { _, _ = c.api.current(ctx) })
	return nil
}

// Health follows GetStatus: Down while Nginx Proxy Manager doesn't answer
// or refuses ddash's sign-in, Degraded (Warn) while reads fail after a
// good one. It shares the cached Proxy Host read.
func (c *component) Health(ctx context.Context) sdk.Health {
	_, _ = c.hosts.Get(ctx)
	st, err := c.status()
	switch st {
	case pb.Status_STATUS_DOWN:
		return sdk.Health{State: sdk.Down, Message: err}
	case pb.Status_STATUS_WARN:
		return sdk.Health{State: sdk.Degraded, Message: err}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetStatus(ctx context.Context, _ *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	_, _ = c.hosts.Get(ctx)
	st, msg := c.status()
	version, readOnly := c.api.state()
	out := &pb.GetStatusResponse{Status: st, Error: msg, Version: version, ProxyHostsReadOnly: readOnly, Address: c.address(ctx)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readAt.IsZero() {
		out.ReadAt = timestamppb.New(c.readAt)
	}
	if !c.goodAt.IsZero() {
		out.LastGoodReadAt = timestamppb.New(c.goodAt)
	}
	return out, nil
}

// status is Up after a good read, Down when Nginx Proxy Manager doesn't
// answer or refuses the sign-in (or no read was good yet), else Warn;
// with why the last read failed.
func (c *component) status() (pb.Status, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr == nil {
		return pb.Status_STATUS_UP, ""
	}
	var f *failure
	if errors.As(c.lastErr, &f) && f.down || c.goodAt.IsZero() {
		return pb.Status_STATUS_DOWN, c.lastErr.Error()
	}
	return pb.Status_STATUS_WARN, c.lastErr.Error()
}

// record notes a read's outcome for GetStatus and Health.
func (c *component) record(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readAt, c.lastErr = time.Now(), err
	if err == nil {
		c.goodAt = c.readAt
	}
}

// address is the Reverse Proxy's address: the URL's host, resolved when
// it is a name (IPv4 first); empty when it doesn't resolve.
func (c *component) address(ctx context.Context) string {
	if ip, err := netip.ParseAddr(c.host); err == nil {
		return ip.Unmap().String()
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", c.host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			return ip.Unmap().String()
		}
	}
	return ips[0].String()
}

func (c *component) Refresh(context.Context, *pb.RefreshRequest) (*pb.RefreshResponse, error) {
	c.api.forget(nil)
	c.hosts.Invalidate()
	c.certs.Invalidate()
	return &pb.RefreshResponse{}, nil
}

func (c *component) ListProxyHosts(ctx context.Context, req *pb.ListProxyHostsRequest) (*pb.ListProxyHostsResponse, error) {
	if req.GetFresh() {
		c.hosts.Invalidate()
	}
	return c.hosts.Get(ctx)
}

func (c *component) ListCertificates(ctx context.Context, req *pb.ListCertificatesRequest) (*pb.ListCertificatesResponse, error) {
	if req.GetFresh() {
		c.certs.Invalidate()
	}
	read, err := c.certs.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := proto.CloneOf(read)
	now := c.now()
	for _, cert := range out.GetCertificates() {
		cert.State, cert.StateMessage = certificateState(cert, now)
	}
	return out, nil
}

// day is a certificate's day, for the days it has left.
const day = 24 * time.Hour

// certificateState is a certificate's state at now and what the Operator
// does about it: Expired past its expiry (or without one); Not renewing
// for Let's Encrypt at 20 days or less, which Nginx Proxy Manager should
// have renewed by then; Expires soon for an uploaded one at 30 days or
// less; else OK.
func certificateState(cert *pb.Certificate, now time.Time) (pb.CertificateState, string) {
	left := time.Duration(-1)
	if cert.GetExpiresOn() != nil {
		left = cert.GetExpiresOn().AsTime().Sub(now)
	}
	letsEncrypt := cert.GetProvider() == "letsencrypt"
	msg := "Upload a new certificate in Nginx Proxy Manager."
	if letsEncrypt {
		msg = "Nginx Proxy Manager hasn't renewed this: check its Let's Encrypt log."
	}
	switch {
	case left < 0:
		return pb.CertificateState_CERTIFICATE_STATE_EXPIRED, msg
	case letsEncrypt && left <= 20*day:
		return pb.CertificateState_CERTIFICATE_STATE_NOT_RENEWING, msg
	case !letsEncrypt && left <= 30*day:
		return pb.CertificateState_CERTIFICATE_STATE_EXPIRES_SOON, msg
	}
	return pb.CertificateState_CERTIFICATE_STATE_OK, ""
}

func (c *component) fetchProxyHosts(ctx context.Context) (*pb.ListProxyHostsResponse, error) {
	var rows []proxyHostRow
	err := c.api.call(ctx, http.MethodGet, "/api/nginx/proxy-hosts", nil, &rows)
	c.record(err)
	if err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListProxyHostsResponse{}
	for _, r := range rows {
		out.ProxyHosts = append(out.ProxyHosts, r.proto())
	}
	return out, nil
}

// certificateRow is one certificate as Nginx Proxy Manager sends it.
type certificateRow struct {
	ID          int64    `json:"id"`
	Provider    string   `json:"provider"`
	NiceName    string   `json:"nice_name"`
	DomainNames []string `json:"domain_names"`
	ExpiresOn   string   `json:"expires_on"`
}

func (c *component) fetchCertificates(ctx context.Context) (*pb.ListCertificatesResponse, error) {
	var rows []certificateRow
	err := c.api.call(ctx, http.MethodGet, "/api/nginx/certificates", nil, &rows)
	c.record(err)
	if err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListCertificatesResponse{}
	for _, r := range rows {
		cert := &pb.Certificate{Id: r.ID, Provider: r.Provider, NiceName: r.NiceName, DomainNames: r.DomainNames}
		// The database's "YYYY-MM-DD HH:mm:ss", in UTC.
		if t, err := time.Parse(time.DateTime, r.ExpiresOn); err == nil {
			cert.ExpiresOn = timestamppb.New(t)
		} else if t, err := time.Parse(time.RFC3339, r.ExpiresOn); err == nil {
			cert.ExpiresOn = timestamppb.New(t)
		}
		out.Certificates = append(out.Certificates, cert)
	}
	return out, nil
}

// unavailable is a read's failure as a gRPC error.
func unavailable(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Unavailable, err.Error())
}
