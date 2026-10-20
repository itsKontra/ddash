package apps_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/itsKontra/ddash/components/apps"
	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// Through the host: the apps Plugin reads Apps that apps discovered from
// the real nginx-config reading nginx's baseline config over SSH, and every
// overlay or manual App change is an audited Action.
func TestAppsFromTheProxyHostsThroughTheHost(t *testing.T) {
	srv := fixture.SSHServer(t, "../nginx-config/testdata/baseline", fixture.SSHOptions{AnyKey: true})
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{nginxconfig.Definition(), apps.Definition()},
		Plugins:    []coretest.Plugin{{Name: "apps", Requires: []string{"apps"}}},
	})
	h.SeedConfig("nginx-config", &nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), HostKey: srv.HostKey()})
	h.Install("apps")
	h.WaitState("apps", runtime.OK)

	var list struct {
		Apps []struct{ Id, Name, Url string }
	}
	call(t, h, "ListApps", `{}`, &list)
	want := []struct{ Id, Name, Url string }{
		{"proxy:grafana.home.example", "Grafana", "https://grafana.home.example"},
		{"proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example"},
		{"proxy:printer.home.example", "Printer", "http://printer.home.example:8080"},
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
