package smoke

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
)

// Cloudflare: a throwaway Public Hostname for SMOKE_PUBLIC_HOSTNAME,
// forwarding to the unused address, with its proxied CNAME marked by
// SmokeCNAMEComment. That pair marks it as the smoke target: cleanup
// removes only an ingress rule for that hostname forwarding there, and
// only a CNAME of that name carrying the comment.

const (
	// PublicHostnameVar is the smoke.env variable holding a hostname in a
	// zone of the Cloudflare account that no ingress rule or DNS record
	// holds: the round trip adds it as a Public Hostname and removes it.
	PublicHostnameVar = "SMOKE_PUBLIC_HOSTNAME"
	// SmokeCNAMEComment is the comment on the smoke CNAME.
	SmokeCNAMEComment = "ddash smoke-writes"
)

func (w *writes) publicHostnameRoundTrip(ctx context.Context) (string, error) {
	h := w.publicHostname
	if h == "" {
		return "", fmt.Errorf("set %s in smoke.env: a hostname in a zone of the account that no ingress rule or DNS record holds", PublicHostnameVar)
	}
	t, err := w.cf.GetTunnel(ctx, &cloudflareapipb.GetTunnelRequest{Fresh: true})
	switch {
	case err != nil:
		return "", fmt.Errorf("GetTunnel: %w", err)
	case !t.GetSetUp():
		return "", errors.New("cloudflare-api isn't set up")
	case !t.GetTunnel().GetRemotelyManaged():
		return "", errors.New("the Tunnel is locally managed: ddash can't change it")
	}
	cfg, err := w.ingress(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range cfg.GetRules() {
		if strings.EqualFold(r.GetHostname(), h) {
			return "", fmt.Errorf("%s has an ingress rule already (pick another %s)", h, PublicHostnameVar)
		}
	}
	zone, err := w.zoneOf(ctx, h)
	if err != nil {
		return "", err
	}
	recs, err := w.records(ctx, zone)
	if err != nil {
		return "", err
	}
	for _, r := range recs {
		if strings.EqualFold(r.GetFields().GetName(), h) {
			return "", fmt.Errorf("%s has a DNS record already: %s %s (pick another %s)", h, r.GetFields().GetType(), r.GetFields().GetContent(), PublicHostnameVar)
		}
	}

	rule := &cloudflareapipb.IngressRule{Hostname: h, Service: "http://" + w.ip.String() + ":80"}
	if _, err := w.cf.PutIngressRule(ctx, &cloudflareapipb.PutIngressRuleRequest{Rule: rule}); err != nil {
		return "", fmt.Errorf("PutIngressRule: %w", err)
	}
	created, err := w.cf.CreateDnsRecord(ctx, &cloudflareapipb.CreateDnsRecordRequest{ZoneId: zone, Fields: &cloudflareapipb.DnsRecordFields{
		Name: h, Type: "CNAME", Content: t.GetTunnel().GetCnameTarget(), Proxied: true, Comment: SmokeCNAMEComment,
	}})
	if err != nil {
		return "", fmt.Errorf("CreateDnsRecord: %w", err)
	}
	edited := &cloudflareapipb.IngressRule{Hostname: h, Service: "http://" + w.ip.String() + ":8080"}
	if _, err := w.cf.PutIngressRule(ctx, &cloudflareapipb.PutIngressRuleRequest{Replace: &cloudflareapipb.RuleName{Hostname: h}, Rule: edited}); err != nil {
		return "", fmt.Errorf("PutIngressRule (edit): %w", err)
	}
	if cfg, err = w.ingress(ctx); err != nil {
		return "", err
	}
	if !hasRule(cfg, h, edited.GetService()) || cfg.GetCatchAll() == "" {
		return "", fmt.Errorf("GetConfiguration doesn't list %s → %s before the catch-all", h, edited.GetService())
	}
	if _, err := w.cf.DeleteDnsRecord(ctx, &cloudflareapipb.DeleteDnsRecordRequest{ZoneId: zone, Id: created.GetRecord().GetId()}); err != nil {
		return "", fmt.Errorf("DeleteDnsRecord: %w", err)
	}
	if _, err := w.cf.DeleteIngressRule(ctx, &cloudflareapipb.DeleteIngressRuleRequest{Rule: &cloudflareapipb.RuleName{Hostname: h}}); err != nil {
		return "", fmt.Errorf("DeleteIngressRule: %w", err)
	}
	if cfg, err = w.ingress(ctx); err != nil {
		return "", err
	}
	if hasRule(cfg, h, "") {
		return "", fmt.Errorf("GetConfiguration still lists %s after its delete", h)
	}
	return h, nil
}

// deleteSmokePublicHostname removes the smoke target's CNAME and ingress
// rule, whatever the round trip got to.
func (w *writes) deleteSmokePublicHostname(ctx context.Context) (string, error) {
	h := w.publicHostname
	if h == "" {
		return "", nil
	}
	var removed []string
	if zone, err := w.zoneOf(ctx, h); err == nil {
		recs, err := w.records(ctx, zone)
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			if f := r.GetFields(); strings.EqualFold(f.GetName(), h) && f.GetComment() == SmokeCNAMEComment {
				if _, err := w.cf.DeleteDnsRecord(ctx, &cloudflareapipb.DeleteDnsRecordRequest{ZoneId: zone, Id: r.GetId()}); err != nil {
					return "", fmt.Errorf("DeleteDnsRecord %s: %w", r.GetId(), err)
				}
				removed = append(removed, "CNAME "+f.GetName())
			}
		}
	}
	cfg, err := w.ingress(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range cfg.GetRules() {
		u, err := url.Parse(r.GetService())
		if !strings.EqualFold(r.GetHostname(), h) || err != nil || u.Hostname() != w.ip.String() {
			continue
		}
		if _, err := w.cf.DeleteIngressRule(ctx, &cloudflareapipb.DeleteIngressRuleRequest{Rule: &cloudflareapipb.RuleName{Hostname: r.GetHostname(), Path: r.GetPath()}}); err != nil {
			return "", fmt.Errorf("DeleteIngressRule %s: %w", r.GetHostname(), err)
		}
		removed = append(removed, "ingress rule "+r.GetHostname())
	}
	if len(removed) == 0 {
		return "", nil
	}
	return "removed " + strings.Join(removed, ", "), nil
}

// hasRule: cfg has a rule for hostname, forwarding to service unless
// that is empty.
func hasRule(cfg *cloudflareapipb.GetConfigurationResponse, hostname, service string) bool {
	for _, r := range cfg.GetRules() {
		if strings.EqualFold(r.GetHostname(), hostname) && (service == "" || r.GetService() == service) {
			return true
		}
	}
	return false
}

// ingress reads the Tunnel's ingress fresh.
func (w *writes) ingress(ctx context.Context) (*cloudflareapipb.GetConfigurationResponse, error) {
	cfg, err := w.cf.GetConfiguration(ctx, &cloudflareapipb.GetConfigurationRequest{Fresh: true})
	if err != nil {
		return nil, fmt.Errorf("GetConfiguration: %w", err)
	}
	return cfg, nil
}

// zoneOf is the id of the account's zone hostname is in.
func (w *writes) zoneOf(ctx context.Context, hostname string) (string, error) {
	zones, err := w.cf.ListZones(ctx, &cloudflareapipb.ListZonesRequest{Fresh: true})
	if err != nil {
		return "", fmt.Errorf("ListZones: %w", err)
	}
	best, id := "", ""
	for _, z := range zones.GetZones() {
		n := z.GetName()
		if (strings.EqualFold(hostname, n) || strings.HasSuffix(strings.ToLower(hostname), "."+n)) && len(n) > len(best) {
			best, id = n, z.GetId()
		}
	}
	if id == "" {
		return "", fmt.Errorf("%s is in no zone of the Cloudflare account", hostname)
	}
	return id, nil
}

// records reads a zone's DNS records fresh.
func (w *writes) records(ctx context.Context, zone string) ([]*cloudflareapipb.DnsRecord, error) {
	r, err := w.cf.ListDnsRecords(ctx, &cloudflareapipb.ListDnsRecordsRequest{ZoneId: zone, Fresh: true})
	if err != nil {
		return nil, fmt.Errorf("ListDnsRecords: %w", err)
	}
	return r.GetRecords(), nil
}
