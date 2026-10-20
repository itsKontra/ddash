package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

var reverseProxy = coretest.Plugin{Name: "reverse-proxy", Requires: []string{"nginx-config"}}

func TestTheConfigFormShowsTheSetupHintOnceTheComponentStarted(t *testing.T) {
	srv := fixture.SSHServer(t, "../components/nginx-config/testdata/baseline", fixture.SSHOptions{})
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{nginxconfig.Definition()}, Plugins: []coretest.Plugin{reverseProxy}})
	h.Install("reverse-proxy")
	h.WaitState("nginx-config", runtime.Unconfigured)

	if form := coreJSON(t, h, "GetConfigForm", `{"owner":"nginx-config"}`); form["setupHint"] != nil {
		t.Errorf("setup hint %v before nginx-config started, want none", form["setupHint"])
	}

	values, _ := json.Marshal(map[string]any{"host": srv.Addr(), "host_key": srv.HostKey()})
	saved := coreJSON(t, h, "SaveConfig", `{"owner":"nginx-config","values":`+string(values)+`}`)

	status := saved["status"].(map[string]any)
	if status["state"] != "down" || status["message"] != "permission denied (publickey)" {
		t.Errorf("status after save = %v, want down: permission denied (publickey)", status)
	}
	hint, _ := coreJSON(t, h, "GetConfigForm", `{"owner":"nginx-config"}`)["setupHint"].(map[string]any)
	block, _ := hint["block"].(string)
	if !strings.HasPrefix(block, `restrict,from="127.0.0.1",command="/usr/sbin/nginx -T" ssh-ed25519 `) {
		t.Errorf("setup hint block = %q, want the authorized_keys line", block)
	}
	// The user's default applies: the hint names root.
	if text, _ := hint["text"].(string); !strings.Contains(text, "root's") {
		t.Errorf("setup hint text = %q, want it to name root", text)
	}
	if err := srv.Authorize(block); err != nil {
		t.Fatal(err)
	}
	if res := h.Call("reverse-proxy", "nginx-config", "Refresh", `{}`); !strings.Contains(res.Body.String(), "grafana.home.example") {
		t.Errorf("Refresh after pasting the line: %d %s, want the config", res.Code, res.Body)
	}
}
