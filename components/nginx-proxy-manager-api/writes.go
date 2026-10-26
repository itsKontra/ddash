package nginxproxymanagerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
)

// Proxy Host writes. Nginx Proxy Manager writes the row first and tests
// nginx's config after: a save nginx rejects still answers 201/200, with
// meta.nginx_online false, and the Proxy Host stays saved but Offline.
// Such a write is an error here all the same, carrying nginx's message.

func (c *component) CreateProxyHost(ctx context.Context, req *pb.CreateProxyHostRequest) (*pb.CreateProxyHostResponse, error) {
	if err := c.writable(ctx, req.GetFields()); err != nil {
		return nil, err
	}
	defer c.hosts.Invalidate()
	var row proxyHostRow
	if err := c.api.call(ctx, http.MethodPost, "/api/nginx/proxy-hosts", body(req.GetFields()), &row); err != nil {
		return nil, writeError(err)
	}
	if err := offline(row); err != nil {
		return nil, err
	}
	return &pb.CreateProxyHostResponse{ProxyHost: row.proto()}, nil
}

func (c *component) UpdateProxyHost(ctx context.Context, req *pb.UpdateProxyHostRequest) (*pb.UpdateProxyHostResponse, error) {
	if err := c.writable(ctx, req.GetFields()); err != nil {
		return nil, err
	}
	defer c.hosts.Invalidate()
	path := "/api/nginx/proxy-hosts/" + strconv.FormatInt(req.GetId(), 10)
	// The PUT is partial: what it doesn't name (advanced config, custom
	// locations, access list, caching, enabled) stays. Reading first
	// makes a Proxy Host that is gone NotFound before anything is sent.
	var current proxyHostRow
	if err := c.api.call(ctx, http.MethodGet, path, nil, &current); err != nil {
		return nil, writeError(err)
	}
	var row proxyHostRow
	if err := c.api.call(ctx, http.MethodPut, path, body(req.GetFields()), &row); err != nil {
		return nil, writeError(err)
	}
	if err := offline(row); err != nil {
		return nil, err
	}
	return &pb.UpdateProxyHostResponse{ProxyHost: row.proto()}, nil
}

func (c *component) DeleteProxyHost(ctx context.Context, req *pb.DeleteProxyHostRequest) (*pb.DeleteProxyHostResponse, error) {
	if err := c.writable(ctx, nil); err != nil {
		return nil, err
	}
	defer c.hosts.Invalidate()
	if err := c.api.call(ctx, http.MethodDelete, "/api/nginx/proxy-hosts/"+strconv.FormatInt(req.GetId(), 10), nil, nil); err != nil {
		return nil, writeError(err)
	}
	return &pb.DeleteProxyHostResponse{}, nil
}

// writable checks that ddash may write Proxy Hosts, signing in first if
// it must, and that fields (unless nil) can be saved.
func (c *component) writable(ctx context.Context, fields *pb.ProxyHostFields) error {
	if fields != nil {
		if msg := invalid(fields); msg != "" {
			return status.Error(codes.InvalidArgument, msg)
		}
	}
	if _, err := c.api.current(ctx); err != nil {
		return unavailable(err)
	}
	if _, readOnly := c.api.state(); readOnly {
		return typed(codes.PermissionDenied, pb.ErrorReason_ERROR_REASON_PROXY_HOSTS_READ_ONLY, nil,
			"ddash's user can only view Proxy Hosts: set them to Manage to change them")
	}
	return nil
}

// invalid says why Nginx Proxy Manager would refuse fields; "" when it
// wouldn't.
func invalid(p *pb.ProxyHostFields) string {
	if len(p.GetDomainNames()) == 0 {
		return "a Proxy Host needs a domain name"
	}
	for _, d := range p.GetDomainNames() {
		if strings.TrimSpace(d) == "" {
			return "a domain name is empty"
		}
	}
	switch {
	case p.GetForwardScheme() != "http" && p.GetForwardScheme() != "https":
		return fmt.Sprintf("the forward scheme is %q, want http or https", p.GetForwardScheme())
	case p.GetForwardHost() == "":
		return "a Proxy Host needs a forward host"
	case p.GetForwardPort() < 1 || p.GetForwardPort() > 65535:
		return fmt.Sprintf("the forward port %d is no port", p.GetForwardPort())
	case p.GetCertificateId() < 0:
		return fmt.Sprintf("the certificate id %d is no id", p.GetCertificateId())
	}
	return ""
}

// body is a POST or PUT body with the fields ddash manages, each named as
// v2.10's schema names it, and nothing else: v2.10 refuses a field it
// doesn't know (such as trust_forwarded_proto, from v2.14).
func body(p *pb.ProxyHostFields) map[string]any {
	return map[string]any{
		"domain_names":            p.GetDomainNames(),
		"forward_scheme":          p.GetForwardScheme(),
		"forward_host":            p.GetForwardHost(),
		"forward_port":            p.GetForwardPort(),
		"block_exploits":          p.GetBlockExploits(),
		"allow_websocket_upgrade": p.GetWebsockets(),
		"certificate_id":          p.GetCertificateId(),
		"ssl_forced":              p.GetForceSsl(),
		"http2_support":           p.GetHttp2(),
		"hsts_enabled":            p.GetHsts(),
		"hsts_subdomains":         p.GetHstsSubdomains(),
	}
}

// offline is the error for a saved Proxy Host nginx rejected; nil when
// nginx took it.
func offline(row proxyHostRow) error {
	if !row.offline() {
		return nil
	}
	nginxErr := ""
	if row.Meta.NginxErr != nil {
		nginxErr = *row.Meta.NginxErr
	}
	id := strconv.FormatInt(row.ID, 10)
	return typed(codes.Aborted, pb.ErrorReason_ERROR_REASON_PROXY_HOST_OFFLINE, map[string]string{"id": id, "nginx_error": nginxErr},
		"Nginx Proxy Manager saved Proxy Host %s, but nginx rejected its config, so it is offline: %s", id, nginxErr)
}

// writeError is a write's failure as a gRPC error: a domain name in use
// is the typed AlreadyExists, an unknown id NotFound, another refusal
// FailedPrecondition with Nginx Proxy Manager's message.
func writeError(err error) error {
	var f *failure
	if !errors.As(err, &f) || f.down || f.status == 0 {
		return unavailable(err)
	}
	switch {
	case f.status == http.StatusNotFound:
		return status.Errorf(codes.NotFound, "Nginx Proxy Manager has no such Proxy Host: %s", f.message)
	case f.status == http.StatusBadRequest && strings.HasSuffix(f.message, " is already in use"):
		domain := strings.TrimSuffix(f.message, " is already in use")
		return typed(codes.AlreadyExists, pb.ErrorReason_ERROR_REASON_DOMAIN_IN_USE, map[string]string{"domain_name": domain},
			"Nginx Proxy Manager refused it: %s", f.message)
	case f.status >= 400 && f.status < 500:
		return status.Errorf(codes.FailedPrecondition, "Nginx Proxy Manager refused it: %s", f.message)
	}
	return unavailable(err)
}

// typed is an error with an ErrorInfo naming reason.
func typed(code codes.Code, reason pb.ErrorReason, metadata map[string]string, format string, args ...any) error {
	st := status.Newf(code, format, args...)
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason.String(), Domain: "nginx-proxy-manager-api", Metadata: metadata})
	if err != nil {
		return st.Err()
	}
	return withInfo.Err()
}
