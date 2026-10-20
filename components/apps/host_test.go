package apps_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/checks"
	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// Through the host: the apps Plugin reads Apps that apps discovered from
// the real nginx-config reading nginx's baseline config (Nginx Proxy
// Manager's) over SSH, linked to the Workloads the real proxmox-api and
// docker-api read from their fixtures, and every overlay or manual App
// change is an audited Action.
//
// The docker-api fake listens on loopback, so a port Docker publishes on
// every address can't be on the Docker host's LAN address here:
// published-on-lan is the recorded containers with homepage's port bound to
// docker-host's address, as `-p 198.18.1.201:3000:3000` would.
func TestAppsFromTheProxyHostsThroughTheHost(t *testing.T) {
	srv := fixture.SSHServer(t, "../nginx-config/testdata/baseline", fixture.SSHOptions{AnyKey: true})
	pve := fixture.Server(t, "../proxmox-api/testdata/baseline")
	media := fixture.Server(t, "../docker-api/testdata/published-on-lan")
	h := coretest.New(t, coretest.Options{
		// The Apps' Checks dial nowhere: every upstream refuses.
		Components: []sdk.Definition{
			nginxconfig.Definition(), ntfyapi.Definition(), checks.DefinitionDialing((&upstreams{}).dial),
			proxmoxapi.Definition(), dockerapi.Definition(), apps.Definition(),
		},
		Plugins: []coretest.Plugin{{Name: "apps", Requires: []string{"apps", "checks"}}},
	})
	h.SeedConfig("nginx-config", &nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), HostKey: srv.HostKey()})
	h.SeedConfig("proxmox-api", &proxmoxapipb.ProxmoxApiConfig{Url: pve.URL, TokenId: "ddash@pve!test", TokenSecret: "test"})
	h.SeedConfig("docker-api", &dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{{Name: "docker-host", Url: "tcp://" + media.Listener.Addr().String()}}})
	h.Install("apps")
	h.WaitState("apps", runtime.OK)

	type ref struct{ Kind, Host, Id string }
	type workload struct {
		Ref         ref
		Name, State string
	}
	var list struct {
		Apps []struct {
			Id, Name, Url string
			Workload      *workload
		}
	}
	call(t, h, "ListApps", `{}`, &list)
	// homepage's upstream 198.18.1.201:3000 is LXC 201 docker-host's address
	// and the port its homepage container publishes there: the container.
	// Every other upstream on docker-host is the guest; n8n's is the stopped
	// LXC 206's static address. The default-server and return-only blocks
	// are no App; NPM's fallback block proxies its assets, so it is one.
	lxc := func(vmid, name, state string) *workload { return &workload{ref{"KIND_LXC", "pve", vmid}, name, state} }
	dockerHost, mediaStack := lxc("201", "docker-host", "running"), lxc("212", "media-stack", "running")
	want := []struct {
		Id, Name, Url string
		Workload      *workload
	}{
		{"proxy:backup.home.example", "Backup", "https://backup.home.example", nil},
		{"proxy:download.home.example", "Download", "https://download.home.example", mediaStack},
		{"proxy:fritz.home.example", "Fritz", "https://fritz.home.example", nil},
		{"proxy:fullstack.home.example", "Fullstack", "https://fullstack.home.example", dockerHost},
		{"proxy:grafana.home.example", "Grafana", "https://grafana.home.example", lxc("204", "grafana", "running")},
		{"proxy:ha.home.example", "Ha", "https://ha.home.example", nil},
		{"proxy:home.example", "Home", "https://home.example", lxc("210", "nginx", "running")},
		{"proxy:home.home.example", "Home", "https://home.home.example", &workload{ref{"KIND_DOCKER", "docker-host", "homepage"}, "homepage", "running"}},
		{"proxy:indexer.home.example", "Indexer", "https://indexer.home.example", mediaStack},
		{"proxy:influxdb.home.example", "Influxdb", "https://influxdb.home.example", lxc("203", "influxdb01", "running")},
		{"proxy:keycloak.home.example", "Keycloak", "https://keycloak.home.example", dockerHost},
		{"proxy:localhost-nginx-proxy-manager", "Localhost Nginx Proxy Manager", "http://localhost-nginx-proxy-manager", nil},
		{"proxy:md.home.example", "Md", "https://md.home.example", dockerHost},
		{"proxy:movie.home.example", "Movie", "https://movie.home.example", mediaStack},
		{"proxy:n8n.home.example", "N8n", "https://n8n.home.example", lxc("206", "n8n", "stopped")},
		{"proxy:nodered.home.example", "Nodered", "https://nodered.home.example", nil},
		{"proxy:portainer.home.example", "Portainer", "https://portainer.home.example", dockerHost},
		{"proxy:pv.home.example", "Pv", "https://pv.home.example", nil},
		{"proxy:pve.home.example", "Pve", "https://pve.home.example", nil},
		{"proxy:request.home.example", "Request", "https://request.home.example", mediaStack},
		{"proxy:series.home.example", "Series", "https://series.home.example", mediaStack},
		{"proxy:video.home.example", "Video", "https://video.home.example", mediaStack},
		{"proxy:wlan0.home.example", "Wlan0", "https://wlan0.home.example", nil},
		{"proxy:wlan1.home.example", "Wlan1", "https://wlan1.home.example", nil},
	}
	if d := diff(want, list.Apps); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}

	call(t, h, "SetOverlay", `{"overlay": {"serverName": "video.home.example", "group": "Media"}}`, nil)
	call(t, h, "CreateApp", `{"app": {"name": "pfSense", "url": "https://198.18.0.1"}}`, nil)

	var audited []string
	for _, e := range h.AuditLog() {
		if e.Target == "apps" {
			audited = append(audited, e.Method+" by "+e.ActorKind+":"+e.Username+": "+e.Outcome)
		}
	}
	wantAudited := []string{"CreateApp by operator:test: ok", "SetOverlay by operator:test: ok"}
	if d := diff(wantAudited, audited); d != "" {
		t.Errorf("Audit Log entries for apps, newest first (-want +got):\n%s", d)
	}
}

func call(t *testing.T, h *coretest.Harness, method, body string, out any) {
	t.Helper()
	res := h.Call("apps", "apps", method, body)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", method, res.Code, res.Body)
	}
	if out != nil {
		if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}
