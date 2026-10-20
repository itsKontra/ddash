package apps_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// fakeNginxConfig is an nginx-config Component serving a snapshot the test
// sets, so a host test runs in a synctest bubble.
type fakeNginxConfig struct {
	nginxconfigpb.UnimplementedNginxConfigServiceServer
	n *nginx
}

func (f *fakeNginxConfig) Start(context.Context, *sdk.Env, *nginxconfigpb.NginxConfigConfig) error {
	return nil
}

func (f *fakeNginxConfig) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (f *fakeNginxConfig) GetSnapshot(ctx context.Context, req *nginxconfigpb.GetSnapshotRequest) (*nginxconfigpb.GetSnapshotResponse, error) {
	return f.n.GetSnapshot(ctx, req)
}

func nginxConfigServing(n *nginx) sdk.Definition {
	return sdk.Define(sdk.Spec[*nginxconfigpb.NginxConfigConfig]{
		Manifest: []byte("name: nginx-config\ndescription: A fake.\nrequires: []\n"),
		Service:  &nginxconfigpb.NginxConfigService_ServiceDesc,
		New: func() sdk.Component[*nginxconfigpb.NginxConfigConfig] {
			return &fakeNginxConfig{n: n}
		},
	})
}

// upstreams fakes the network App Checks dial: listed addresses answer HTTP
// 200, the rest refuse. Nothing reaches the real network.
type upstreams struct {
	mu sync.Mutex
	up []string
}

func (u *upstreams) answer(addr string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.up = append(u.up, addr)
}

func (u *upstreams) dial(_ context.Context, _, addr string) (net.Conn, error) {
	u.mu.Lock()
	up := slices.Contains(u.up, addr)
	u.mu.Unlock()
	if !up {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err == nil {
			_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		}
	}()
	return client, nil
}

type listedCheck struct {
	AppID    string `json:"appId"`
	State    string
	LastBeat string
}

// Through the host: a Proxy Host new in nginx-config's snapshot becomes an
// App within a poll, and apps declares its Check to checks, which probes it.
func TestANewProxyHostYieldsAnAppWithARunningCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		ups := &upstreams{}
		ups.answer("printer.home.example:8080")
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{nginxConfigServing(n), ntfyapi.Definition(), checks.DefinitionDialing(ups.dial), apps.Definition()},
			Plugins:    []coretest.Plugin{{Name: "apps", Requires: []string{"apps", "checks"}}},
		})
		h.SeedConfig("nginx-config", &nginxconfigpb.NginxConfigConfig{Host: "198.18.0.10", HostKey: "ssh-ed25519 AAAA"})
		h.Install("apps")
		h.WaitState("apps", runtime.OK)

		n.set(jellyfin, printer)
		afterPoll()

		var list struct{ Apps []struct{ Id string } }
		call(t, h, "ListApps", `{}`, &list)
		if !slices.ContainsFunc(list.Apps, func(a struct{ Id string }) bool { return a.Id == "proxy:printer.home.example" }) {
			t.Fatalf("Apps %v, want the new Proxy Host's", list.Apps)
		}

		res := h.Call("apps", "checks", "ListChecks", `{}`)
		if res.Code != http.StatusOK {
			t.Fatalf("ListChecks: %d %s", res.Code, res.Body)
		}
		var checks struct{ Checks []listedCheck }
		if err := json.Unmarshal(res.Body.Bytes(), &checks); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(checks.Checks, func(c listedCheck) bool { return c.AppID == "proxy:printer.home.example" })
		if i < 0 {
			t.Fatalf("Checks %v, want one for the new App", checks.Checks)
		}
		if got := checks.Checks[i]; got.State != "STATE_UP" || got.LastBeat == "" {
			t.Errorf("its Check: %+v, want probed and up", got)
		}

		var declared []string
		for _, e := range h.AuditLog() {
			if e.Target == "checks" && e.Method == "SetAppChecks" {
				declared = append(declared, e.ActorKind+":"+e.Username+": "+e.Outcome)
			}
		}
		want := []string{"component:apps: ok", "component:apps: ok"}
		if d := diff(want, declared); d != "" {
			t.Errorf("SetAppChecks in the Audit Log, after Start and after the new Proxy Host (-want +got):\n%s", d)
		}
	})
}
