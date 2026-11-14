package apps

import (
	"context"
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	cfpb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
)

// fillPublicHostnames gives each App the Cloudflare Tunnel's Public
// Hostnames among its hostnames, from cloudflare-api's (cached) ingress
// rules: the App is on the internet. It returns why cloudflare-api
// couldn't be read; the Apps then go without. Not set up (or not
// configured) is no error: nothing is on the internet through it.
func (c *component) fillPublicHostnames(ctx context.Context, apps []*appspb.App) string {
	cfg, err := c.cloudflare.GetConfiguration(ctx, &cfpb.GetConfigurationRequest{})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			return ""
		}
		return status.Convert(err).Message()
	}
	for _, a := range apps {
		if a.GetOrphaned() {
			continue
		}
		names := map[string]bool{}
		if u, err := url.Parse(a.GetUrl()); err == nil && u.Hostname() != "" {
			names[strings.ToLower(u.Hostname())] = true
		}
		for _, alias := range a.GetAliases() {
			names[strings.ToLower(alias)] = true
		}
		seen := map[string]bool{}
		for _, r := range cfg.GetRules() {
			h := strings.ToLower(r.GetHostname())
			if names[h] && !seen[h] {
				seen[h] = true
				a.PublicHostnames = append(a.PublicHostnames, &appspb.PublicHostname{Hostname: h, Service: r.GetService()})
			}
		}
	}
	return ""
}
