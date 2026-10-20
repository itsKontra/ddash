package apps_test

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

func (s started) proxyHosts(t *testing.T) *appspb.ListProxyHostsResponse {
	t.Helper()
	resp, err := s.c.ListProxyHosts(t.Context(), &appspb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const (
	noWorkload = appspb.ProxyHostWarning_PROXY_HOST_WARNING_NO_WORKLOAD
	noTLS      = appspb.ProxyHostWarning_PROXY_HOST_WARNING_NO_TLS
)

func at(s *nginxconfigpb.Server, file string, line int64) *nginxconfigpb.Server {
	s.File, s.Line = file, line
	return s
}

func TestEveryBlockThatForwardsIsAProxyHostWithTheAppItYields(t *testing.T) {
	redirect := at(block([]string{"jellyfin.home.example"}, "80"), "/etc/nginx/conf.d/jellyfin.conf", 1)
	redirect.Return = "301 https://$host$request_uri"
	jellyfin := at(proxy([]string{"jellyfin.home.example", "media.home.example"}, "http://198.18.2.10:8096", "443 ssl"), "/etc/nginx/conf.d/jellyfin.conf", 6)
	jellyfin.Locations = append([]*nginxconfigpb.Location{{Path: "/socket", ProxyPass: "http://198.18.2.10:8097"}}, jellyfin.Locations...)
	grafana := at(proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl"), "/etc/nginx/conf.d/grafana.conf", 5)
	regex := at(proxy([]string{"~^(?<app>.+)\\.home\\.example$"}, "http://198.18.2.50:80", "443 ssl"), "/etc/nginx/conf.d/regex.conf", 1)
	n := newNginx(redirect, jellyfin, grafana, regex)
	n.snap.Upstreams = []*nginxconfigpb.Upstream{{Name: "grafana", Servers: []string{"198.18.2.30:3000", "198.18.2.31:3000"}}}
	s := startInLab(t, n, newLab())

	want := []*appspb.ProxyHost{
		{
			File: "/etc/nginx/conf.d/grafana.conf", Line: 5, ServerNames: []string{"grafana.home.example"},
			Upstream: "http://grafana", UpstreamServers: []string{"198.18.2.30:3000", "198.18.2.31:3000"},
			App: &appspb.App{Id: "proxy:grafana.home.example", ServerName: "grafana.home.example", Name: "Grafana", Url: "https://grafana.home.example", Workload: lxc("120", "paperless")},
		},
		{
			File: "/etc/nginx/conf.d/jellyfin.conf", Line: 6, ServerNames: []string{"jellyfin.home.example", "media.home.example"},
			Upstream: "http://198.18.2.10:8096",
			App:      &appspb.App{Id: "proxy:jellyfin.home.example", ServerName: "jellyfin.home.example", Name: "Jellyfin", Url: "https://jellyfin.home.example", Aliases: []string{"media.home.example"}, Workload: docker("jellyfin")},
		},
		{
			File: "/etc/nginx/conf.d/regex.conf", Line: 1, ServerNames: []string{"~^(?<app>.+)\\.home\\.example$"},
			Upstream: "http://198.18.2.50:80",
		},
	}
	if d := diff(want, s.proxyHosts(t).GetProxyHosts()); d != "" {
		t.Errorf("Proxy Hosts (-want +got):\n%s", d)
	}
}

func TestAProxyHostWarnsAboutAnUpstreamWithoutAWorkloadAndAMissingTLS(t *testing.T) {
	n := newNginx(
		proxy([]string{"printer.home.example"}, "http://198.18.2.99", "8080"),
		proxy([]string{"wiki.home.example"}, "http://198.18.2.30:3000", "80"),
		proxy([]string{"*.home.example"}, "http://198.18.2.98", "443 ssl"),
	)
	s := startInLab(t, n, newLab())

	got := map[string][]appspb.ProxyHostWarning{}
	for _, h := range s.proxyHosts(t).GetProxyHosts() {
		got[h.GetServerNames()[0]] = h.GetWarnings()
	}
	want := map[string][]appspb.ProxyHostWarning{
		// No App, so no Workload to miss.
		"*.home.example":       nil,
		"printer.home.example": {noWorkload, noTLS},
		"wiki.home.example":    {noTLS},
	}
	if d := diff(want, got); d != "" {
		t.Errorf("warnings (-want +got):\n%s", d)
	}
}

func TestNoWorkloadWarningWhileAWorkloadSourceCantBeRead(t *testing.T) {
	l := newLab()
	l.proxmox.fail(status.Error(codes.Unavailable, "proxmox-api is starting"))
	n := newNginx(proxy([]string{"printer.home.example"}, "http://198.18.2.99", "443 ssl"))
	s := startInLab(t, n, l)

	hosts := s.proxyHosts(t).GetProxyHosts()
	if len(hosts) != 1 || len(hosts[0].GetWarnings()) != 0 {
		t.Errorf("Proxy Hosts %v, want the printer without warnings: proxmox-api may list its Workload", hosts)
	}
}

func TestProxyHostsSayWhyNginxConfigCantBeRead(t *testing.T) {
	n := newNginx(proxy([]string{"wiki.home.example"}, "http://198.18.2.30:3000", "443 ssl"))
	n.fail(status.Error(codes.Unavailable, "nginx-config is starting"))
	s := start(t, n)

	resp := s.proxyHosts(t)
	if len(resp.GetProxyHosts()) != 0 || resp.GetDiscoveryError() != "nginx-config is starting" {
		t.Errorf("after a failed first read: %v, want none and the discovery error", resp)
	}
}
