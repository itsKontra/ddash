package apps

import (
	"cmp"
	"context"
	"net/url"
	"slices"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

// proxyHost is a server block that forwards to an upstream, as read.
type proxyHost struct {
	server *nginxconfigpb.Server
	// upstream is the block's proxy_pass (proxyPass), servers the
	// upstream block it names, if any.
	upstream string
	servers  []string
	// app is the first server_name of the App the block yields; empty
	// when it yields none.
	app string
}

// proxyHostsOf finds every block in snap that forwards to an upstream, in
// nginx's order.
func proxyHostsOf(snap *nginxconfigpb.Snapshot) []proxyHost {
	upstreams := upstreamsOf(snap)
	var out []proxyHost
	for _, s := range snap.GetServers() {
		if !proxies(s) {
			continue
		}
		h := proxyHost{server: s, upstream: proxyPass(s)}
		if u, err := url.Parse(h.upstream); err == nil && u.Port() == "" {
			h.servers = upstreams[u.Hostname()]
		}
		if d, ok := fromServer(s, upstreams); ok {
			h.app = d.serverName
		}
		out = append(out, h)
	}
	return out
}

func (c *component) ListProxyHosts(ctx context.Context, _ *appspb.ListProxyHostsRequest) (*appspb.ListProxyHostsResponse, error) {
	found, readErr, err := c.discovered(ctx)
	if err != nil {
		return nil, err
	}
	all, _, err := c.joined(ctx, found)
	if err != nil {
		return nil, err
	}
	byName := map[string]*appspb.App{}
	for _, a := range all {
		if a.GetServerName() != "" {
			byName[a.GetServerName()] = a
		}
	}
	c.mu.Lock()
	hosts, sourcesRead := c.hosts, len(c.src.problems) == 0
	c.mu.Unlock()

	resp := &appspb.ListProxyHostsResponse{DiscoveryError: readErr}
	for _, h := range hosts {
		p := &appspb.ProxyHost{
			File:            h.server.GetFile(),
			Line:            h.server.GetLine(),
			ServerNames:     h.server.GetServerNames(),
			Upstream:        h.upstream,
			UpstreamServers: h.servers,
			App:             byName[h.app],
		}
		if p.App != nil && p.App.GetWorkload() == nil && sourcesRead {
			p.Warnings = append(p.Warnings, appspb.ProxyHostWarning_PROXY_HOST_WARNING_NO_WORKLOAD)
		}
		if !slices.ContainsFunc(h.server.GetListens(), (*nginxconfigpb.Listen).GetSsl) {
			p.Warnings = append(p.Warnings, appspb.ProxyHostWarning_PROXY_HOST_WARNING_NO_TLS)
		}
		resp.ProxyHosts = append(resp.ProxyHosts, p)
	}
	// Stable: blocks sharing a first server_name keep nginx's order.
	slices.SortStableFunc(resp.ProxyHosts, func(a, b *appspb.ProxyHost) int {
		return cmp.Compare(first(a.GetServerNames()), first(b.GetServerNames()))
	})
	return resp, nil
}

func first(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
