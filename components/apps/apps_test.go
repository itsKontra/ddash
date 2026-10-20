package apps_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// nginx fakes nginx-config: a snapshot the test sets, whose revision grows
// with every change, as nginx-config's does.
type nginx struct {
	nginxconfigpb.NginxConfigServiceClient

	mu    sync.Mutex
	snap  *nginxconfigpb.Snapshot
	err   error
	reads int
}

func newNginx(servers ...*nginxconfigpb.Server) *nginx {
	return &nginx{snap: &nginxconfigpb.Snapshot{Revision: 1, Servers: servers}}
}

// set replaces the Proxy Hosts, as an edited conf file read by nginx-config would.
func (n *nginx) set(servers ...*nginxconfigpb.Server) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.snap = &nginxconfigpb.Snapshot{Revision: n.snap.GetRevision() + 1, Servers: servers}
}

// fail makes nginx-config unavailable.
func (n *nginx) fail(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.err = err
}

func (n *nginx) GetSnapshot(context.Context, *nginxconfigpb.GetSnapshotRequest, ...grpc.CallOption) (*nginxconfigpb.GetSnapshotResponse, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reads++
	if n.err != nil {
		return nil, n.err
	}
	return &nginxconfigpb.GetSnapshotResponse{Snapshot: n.snap}, nil
}

// proxy is a server block forwarding to upstream, listening on listens
// ("443 ssl", "80", "8080 default_server").
func proxy(names []string, upstream string, listens ...string) *nginxconfigpb.Server {
	s := block(names, listens...)
	s.Locations = []*nginxconfigpb.Location{{Path: "/", ProxyPass: upstream}}
	return s
}

func block(names []string, listens ...string) *nginxconfigpb.Server {
	s := &nginxconfigpb.Server{File: "/etc/nginx/conf.d/test.conf", Line: 1, ServerNames: names}
	for _, l := range listens {
		var ln nginxconfigpb.Listen
		for i, f := range strings.Fields(l) {
			switch {
			case i == 0:
				ln.Address = f
			case f == "ssl":
				ln.Ssl = true
			case f == "default_server":
				ln.DefaultServer = true
			}
		}
		s.Listens = append(s.Listens, &ln)
	}
	return s
}

type started struct {
	h *sdktest.Harness
	c appspb.AppsServiceClient
}

func start(t *testing.T, n *nginx, opts ...sdktest.Option) started {
	t.Helper()
	h := sdktest.New(t, apps.Definition(), append([]sdktest.Option{sdktest.WithClient[nginxconfigpb.NginxConfigServiceClient](n)}, opts...)...)
	h.MustStart(&appspb.AppsConfig{})
	return started{h, appspb.NewAppsServiceClient(h.Conn)}
}

func (s started) list(t *testing.T) *appspb.ListAppsResponse {
	t.Helper()
	resp, err := s.c.ListApps(t.Context(), &appspb.ListAppsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (s started) apps(t *testing.T) []*appspb.App {
	t.Helper()
	return s.list(t).GetApps()
}

func diff(want, got any) string {
	return cmp.Diff(want, got, protocmp.Transform())
}

func codeOf(err error) codes.Code { return status.Code(err) }

func TestEachProxyHostIsOneApp(t *testing.T) {
	n := newNginx(
		proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl", "[::]:443 ssl"),
		proxy([]string{"jellyfin.home.example", "media.home.example"}, "http://198.18.2.10:8096", "443 ssl"),
		proxy([]string{"wiki.home.example"}, "http://198.18.2.11:3000", "80"),
	)
	s := start(t, n)

	want := []*appspb.App{
		{Id: "proxy:grafana.home.example", ServerName: "grafana.home.example", Name: "Grafana", Url: "https://grafana.home.example"},
		{Id: "proxy:jellyfin.home.example", ServerName: "jellyfin.home.example", Name: "Jellyfin", Url: "https://jellyfin.home.example", Aliases: []string{"media.home.example"}},
		{Id: "proxy:wiki.home.example", ServerName: "wiki.home.example", Name: "Wiki", Url: "http://wiki.home.example"},
	}
	if d := diff(want, s.apps(t)); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}
}

func TestRedirectOnlyAndCatchAllBlocksAreNoApps(t *testing.T) {
	redirect := block([]string{"grafana.home.example"}, "80")
	redirect.Return = "301 https://$host$request_uri"
	n := newNginx(
		redirect,
		proxy([]string{"_"}, "http://198.18.2.1", "443 ssl"),
		proxy([]string{"fallback.home.example"}, "http://198.18.2.2", "443 ssl default_server"),
		proxy(nil, "http://198.18.2.3", "8443 ssl"),
		proxy([]string{"*.home.example"}, "http://198.18.2.4", "443 ssl"),
		block([]string{"static.home.example"}, "443 ssl"), // serves files, proxies nothing
		proxy([]string{"grafana.home.example", "_", "~^graf"}, "http://grafana", "443 ssl"),
	)
	s := start(t, n)

	want := []*appspb.App{
		{Id: "proxy:grafana.home.example", ServerName: "grafana.home.example", Name: "Grafana", Url: "https://grafana.home.example"},
	}
	if d := diff(want, s.apps(t)); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}
}

func TestBlocksSharingAFirstServerNameAreOneAppOnHttps(t *testing.T) {
	n := newNginx(
		proxy([]string{"vault.home.example"}, "http://198.18.2.20:8080", "80"),
		proxy([]string{"vault.home.example", "pw.home.example"}, "http://198.18.2.20:8080", "443 ssl"),
	)
	s := start(t, n)

	want := []*appspb.App{
		{Id: "proxy:vault.home.example", ServerName: "vault.home.example", Name: "Vault", Url: "https://vault.home.example", Aliases: []string{"pw.home.example"}},
	}
	if d := diff(want, s.apps(t)); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}
}

func TestAnAppOnAnotherPortKeepsItInItsURL(t *testing.T) {
	n := newNginx(
		proxy([]string{"printer.home.example"}, "http://198.18.1.40", "8080"),
		proxy([]string{"home-assistant.home.example"}, "http://198.18.1.41", "198.18.1.2:8443 ssl", "[::]:8443 ssl"),
		proxy([]string{"nas.home.example"}, "http://198.18.1.42", "[::]:443 ssl", "8443 ssl"),
	)
	s := start(t, n)

	got := map[string]string{}
	for _, a := range s.apps(t) {
		got[a.GetName()] = a.GetUrl()
	}
	want := map[string]string{
		"Printer":        "http://printer.home.example:8080",
		"Home Assistant": "https://home-assistant.home.example:8443",
		"Nas":            "https://nas.home.example",
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("name → URL (-want +got):\n%s", d)
	}
}
