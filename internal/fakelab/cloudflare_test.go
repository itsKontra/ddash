package fakelab_test

import (
	"encoding/json"
	"net/http"
	"testing"

	cloudflareapi "github.com/itsKontra/ddash/components/cloudflare-api"
	cloudflaretunnel "github.com/itsKontra/ddash/components/cloudflare-tunnel"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

// `make dev` shows a Cloudflare Tunnel with two connectors and three
// Public Hostnames.
func TestTheLabsCloudflareTunnelHasConnectorsAndPublicHostnames(t *testing.T) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{cloudflareapi.Definition(), cloudflaretunnel.Definition()},
		Plugins:    []coretest.Plugin{{Name: "cloudflare-tunnel", Requires: []string{"cloudflare-tunnel", "cloudflare-api"}}},
	})
	h.SeedConfig("cloudflare-api", lab.Config()["cloudflare-api"])
	h.Install("cloudflare-tunnel")
	h.WaitState("cloudflare-api", runtime.OK)
	h.WaitState("cloudflare-tunnel", runtime.OK)

	res := h.Call("cloudflare-tunnel", "cloudflare-api", "GetTunnel", `{}`)
	var tunnel struct {
		Tunnel struct {
			Status     string            `json:"status"`
			Connectors []json.RawMessage `json:"connectors"`
		} `json:"tunnel"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &tunnel) != nil || tunnel.Tunnel.Status != "TUNNEL_STATUS_HEALTHY" || len(tunnel.Tunnel.Connectors) != 2 {
		t.Errorf("GetTunnel = %d %s, want healthy with two connectors", res.Code, res.Body)
	}
	res = h.Call("cloudflare-tunnel", "cloudflare-tunnel", "ListPublicHostnames", `{}`)
	var list struct {
		PublicHostnames []json.RawMessage `json:"publicHostnames"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &list) != nil || len(list.PublicHostnames) != 3 {
		t.Errorf("ListPublicHostnames = %d %s, want three", res.Code, res.Body)
	}
}
