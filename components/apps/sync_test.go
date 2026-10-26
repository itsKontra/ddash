package apps_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
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

// GetStatus says the Reverse Proxy is at 198.18.1.210.
func (*npmList) GetStatus(context.Context, *npmpb.GetStatusRequest) (*npmpb.GetStatusResponse, error) {
	return &npmpb.GetStatusResponse{Status: npmpb.Status_STATUS_UP, Address: "198.18.1.210"}, nil
}

func (f *npmList) set(hosts ...*npmpb.ProxyHost) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = hosts
}

// pfsenseList is a pfsense-api Component listing the Host Overrides a test
// sets, from memory.
type pfsenseList struct {
	pfpb.UnimplementedPfsenseApiServiceServer
	mu        sync.Mutex
	overrides []*pfpb.HostOverride
}

func (*pfsenseList) Start(context.Context, *sdk.Env, *pfpb.PfsenseApiConfig) error { return nil }

func (*pfsenseList) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (f *pfsenseList) ListHostOverrides(context.Context, *pfpb.ListHostOverridesRequest) (*pfpb.ListHostOverridesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &pfpb.ListHostOverridesResponse{HostOverrides: f.overrides}, nil
}

func (f *pfsenseList) set(overrides ...*pfpb.HostOverride) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides = overrides
}

// listedOverride is a Host Override host.home.example at ip with aliases
// (hosts in home.example).
func listedOverride(host, ip string, aliases ...string) *pfpb.HostOverride {
	o := &pfpb.HostOverride{Host: host, Domain: "home.example", Ips: []string{ip}}
	for _, a := range aliases {
		o.Aliases = append(o.Aliases, &pfpb.HostOverrideAlias{Host: a, Domain: "home.example"})
	}
	return o
}

func listedHost(id int64, upstream string, port int32, domains ...string) *npmpb.ProxyHost {
	return &npmpb.ProxyHost{Id: id, Enabled: true, Fields: &npmpb.ProxyHostFields{
		DomainNames: domains, ForwardScheme: "http", ForwardHost: upstream, ForwardPort: port, CertificateId: 3,
	}}
}

// Without a Refresh, a Proxy Host new in Nginx Proxy Manager, or a Host
// Override new in pfSense, becomes an App, with its Check, within a minute.
func TestANewSourceReachesAppsWithinAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		npm := &npmList{hosts: []*npmpb.ProxyHost{listedHost(3, "198.18.1.204", 3000, "grafana.home.example")}}
		fw := &pfsenseList{overrides: []*pfpb.HostOverride{listedOverride("nginx", "198.18.1.210", "grafana")}}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{
				sdk.Define(sdk.Spec[*npmpb.NginxProxyManagerApiConfig]{
					Manifest: []byte("name: nginx-proxy-manager-api\ndescription: A fake.\nrequires: []\n"),
					Service:  &npmpb.NginxProxyManagerApiService_ServiceDesc,
					New:      func() sdk.Component[*npmpb.NginxProxyManagerApiConfig] { return npm },
				}),
				sdk.Define(sdk.Spec[*pfpb.PfsenseApiConfig]{
					Manifest: []byte("name: pfsense-api\ndescription: A fake.\nrequires: []\n"),
					Service:  &pfpb.PfsenseApiService_ServiceDesc,
					New:      func() sdk.Component[*pfpb.PfsenseApiConfig] { return fw },
				}),
				ntfyapi.Definition(), checks.DefinitionDialing(refuse), apps.Definition(),
				proxmoxApiServing(&proxmoxFake{}), dockerApiServing(&dockerFake{}),
			},
			Plugins: []coretest.Plugin{{Name: "apps", Requires: []string{"apps", "checks"}}},
		})
		h.SeedConfig("nginx-proxy-manager-api", &npmpb.NginxProxyManagerApiConfig{Url: "http://198.18.1.210:81", Email: npmEmail, Password: "secret"})
		h.SeedConfig("proxmox-api", &proxmoxapipb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006", TokenId: "ddash@pve!test", TokenSecret: "test"})
		h.SeedConfig("pfsense-api", &pfpb.PfsenseApiConfig{Url: "https://198.18.1.1", ApiKey: "test"})
		h.Install("apps")
		h.WaitState("apps", runtime.OK)
		l := &lab{t: t, h: h}

		npm.set(listedHost(3, "198.18.1.204", 3000, "grafana.home.example"), listedHost(9, "198.18.2.11", 3000, "wiki.home.example"))
		fw.set(listedOverride("nginx", "198.18.1.210", "grafana", "wiki"), listedOverride("nas", "198.18.2.10"))
		time.Sleep(apps.SyncInterval / 2)
		synctest.Wait()
		if l.has("proxy:wiki.home.example") || len(appsNamed(l.list().GetApps(), "nas.home.example")) > 0 {
			t.Fatal("the new sources are Apps before the next sync")
		}

		time.Sleep(apps.SyncInterval)
		synctest.Wait()
		if !l.has("proxy:wiki.home.example") {
			t.Error("the new Proxy Host is no App a sync later")
		}
		if l.appChecks()["proxy:wiki.home.example"] == nil {
			t.Error("the new App has no Check")
		}
		if nas := l.byURL("https://nas.home.example"); nas.GetReach() != appspb.Reach_REACH_NOT_PROXIED || l.appChecks()[nas.GetId()] == nil {
			t.Errorf("nas = %v, want a Not-proxied App with a Check a sync later", nas)
		}
	})
}
