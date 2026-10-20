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
// the real nginx-config reading nginx's baseline config over SSH, linked to
// the Workloads the real proxmox-api and docker-api read from their
// baselines, and every overlay or manual App change is an audited Action.
func TestAppsFromTheProxyHostsThroughTheHost(t *testing.T) {
	srv := fixture.SSHServer(t, "../nginx-config/testdata/baseline", fixture.SSHOptions{AnyKey: true})
	pve := fixture.Server(t, "../proxmox-api/testdata/baseline")
	media := fixture.Server(t, "../docker-api/testdata/baseline")
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
	h.SeedConfig("docker-api", &dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{{Name: "media", Url: "tcp://" + media.Listener.Addr().String()}}})
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
	// jellyfin's upstream 198.18.2.10:8096 is LXC 102 media-stack's address
	// and the port its jellyfin container publishes there: the container.
	want := []struct {
		Id, Name, Url string
		Workload      *workload
	}{
		{"proxy:grafana.home.example", "Grafana", "https://grafana.home.example", nil},
		{"proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example", &workload{ref{"KIND_DOCKER", "media", "jellyfin"}, "jellyfin", "running"}},
		{"proxy:printer.home.example", "Printer", "http://printer.home.example:8080", nil},
	}
	if d := diff(want, list.Apps); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}

	call(t, h, "SetOverlay", `{"overlay": {"serverName": "jellyfin.home.example", "group": "Media"}}`, nil)
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
