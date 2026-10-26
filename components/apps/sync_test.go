package apps_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/checks"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// npmList is an nginx-proxy-manager-api Component listing the Proxy Hosts
// a test sets, from memory, so a host test runs in a synctest bubble.
type npmList struct {
	npmpb.UnimplementedNginxProxyManagerApiServiceServer
	mu    sync.Mutex
	hosts []*npmpb.ProxyHost
}

func (*npmList) Start(context.Context, *sdk.Env, *npmpb.NginxProxyManagerApiConfig) error {
	return nil
}

func (*npmList) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (f *npmList) ListProxyHosts(context.Context, *npmpb.ListProxyHostsRequest) (*npmpb.ListProxyHostsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &npmpb.ListProxyHostsResponse{ProxyHosts: f.hosts}, nil
}

func (f *npmList) set(hosts ...*npmpb.ProxyHost) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = hosts
}

func listedHost(id int64, upstream string, port int32, domains ...string) *npmpb.ProxyHost {
	return &npmpb.ProxyHost{Id: id, Enabled: true, Fields: &npmpb.ProxyHostFields{
		DomainNames: domains, ForwardScheme: "http", ForwardHost: upstream, ForwardPort: port, CertificateId: 3,
	}}
}

// Without a Refresh, a Proxy Host new in Nginx Proxy Manager becomes an
// App, with its Check, within a minute.
func TestANewProxyHostReachesAppsWithinAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		npm := &npmList{hosts: []*npmpb.ProxyHost{listedHost(3, "198.18.1.204", 3000, "grafana.home.example")}}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{
				sdk.Define(sdk.Spec[*npmpb.NginxProxyManagerApiConfig]{
					Manifest: []byte("name: nginx-proxy-manager-api\ndescription: A fake.\nrequires: []\n"),
					Service:  &npmpb.NginxProxyManagerApiService_ServiceDesc,
					New:      func() sdk.Component[*npmpb.NginxProxyManagerApiConfig] { return npm },
				}),
				ntfyapi.Definition(), checks.DefinitionDialing(refuse), apps.Definition(),
				proxmoxApiServing(&proxmoxFake{}), dockerApiServing(&dockerFake{}),
			},
			Plugins: []coretest.Plugin{{Name: "apps", Requires: []string{"apps", "checks"}}},
		})
		h.SeedConfig("nginx-proxy-manager-api", &npmpb.NginxProxyManagerApiConfig{Url: "http://198.18.1.210:81", Email: npmEmail, Password: "secret"})
		h.SeedConfig("proxmox-api", &proxmoxapipb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006", TokenId: "ddash@pve!test", TokenSecret: "test"})
		h.Install("apps")
		h.WaitState("apps", runtime.OK)
		l := &lab{t: t, h: h}

		npm.set(listedHost(3, "198.18.1.204", 3000, "grafana.home.example"), listedHost(9, "198.18.2.11", 3000, "wiki.home.example"))
		time.Sleep(apps.SyncInterval / 2)
		synctest.Wait()
		if l.has("proxy:wiki.home.example") {
			t.Fatal("the new Proxy Host is an App before the next sync")
		}

		time.Sleep(apps.SyncInterval)
		synctest.Wait()
		if !l.has("proxy:wiki.home.example") {
			t.Error("the new Proxy Host is no App a sync later")
		}
		if l.appChecks()["proxy:wiki.home.example"] == nil {
			t.Error("the new App has no Check")
		}
	})
}
