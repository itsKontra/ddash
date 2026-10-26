package apps_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	nginxproxymanagerapi "github.com/itsKontra/ddash/components/nginx-proxy-manager-api"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// The Apps host tests run apps through the host against the real
// nginx-proxy-manager-api signed in to npmfake (Nginx Proxy Manager's
// baseline: 23 Proxy Hosts), the real pfsense-api over pfsensefake
// (pfSense's baseline: the Reverse Proxy's Host Override nginx with 23
// aliases, and five others), the real checks (whose probes dial nowhere),
// the real network over that pfSense and the real adguard-api over
// adguardfake's baseline, and Workload sources: the real proxmox-api and
// docker-api over their fixtures, or fakes a test sets.
//
// Nginx Proxy Manager answers on 127.0.0.1, which is therefore the
// Reverse Proxy's address: the lab moves the nginx Host Override (at
// 198.18.1.210 in the baseline) there.

// rpAddress is the Reverse Proxy's address in the lab.
const rpAddress = "127.0.0.1"

// lab is one ddash with apps installed, and the systems behind it.
type lab struct {
	t   *testing.T
	h   *coretest.Harness
	npm *npmfake.NPM
	// failNPM makes Nginx Proxy Manager answer every read of its Proxy
	// Hosts and certificates with a 500.
	failNPM *atomic.Bool
	fw      *pfsensefake.Firewall
	// failPfSense makes pfSense answer every read of its Host Overrides
	// with a 500.
	failPfSense *atomic.Bool
	// stallPfSense makes pfSense answer each read of its Host Overrides
	// after its next DNS Resolver apply a second late.
	stallPfSense atomic.Bool
	applied      atomic.Bool
	proxmox      *proxmoxFake
	docker       *dockerFake
}

type labOptions struct {
	// fixtureWorkloads runs the real proxmox-api and docker-api over
	// their fixtures; else fakes listing proxmox and docker (none by
	// default).
	fixtureWorkloads bool
	proxmox          *proxmoxFake
	docker           *dockerFake
	// noProxyHosts starts Nginx Proxy Manager without Proxy Hosts, and
	// pfSense with the Reverse Proxy's Host Override alone, without
	// aliases or other Host Overrides.
	noProxyHosts bool
	// dataDir holds the host's data, e.g. an older apps DB at
	// <dataDir>/db/apps.db; a temp dir when empty.
	dataDir string
	// npm is the Nginx Proxy Manager to keep (with failNPM), e.g. across
	// a host restart.
	npm     *npmfake.NPM
	failNPM *atomic.Bool
	// npmUnconfigured leaves nginx-proxy-manager-api without config.
	npmUnconfigured bool
	// networkUnconfigured leaves network without config.
	networkUnconfigured bool
	// resync bounds the sync ending an App Action; apps.ResyncTimeout
	// when 0.
	resync time.Duration
}

const npmEmail = "ddash@home.example"

// newLab starts ddash with apps installed and waits for apps to start.
func newLab(t *testing.T, o labOptions) *lab {
	t.Helper()
	l := &lab{t: t, npm: o.npm, failNPM: o.failNPM, proxmox: o.proxmox, docker: o.docker}
	if l.npm == nil {
		l.npm, l.failNPM = newNPM(t)
		if o.noProxyHosts {
			for _, h := range l.npm.ProxyHosts() {
				l.npm.DeleteProxyHost(int(h["id"].(float64)))
			}
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.failNPM.Load() && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/nginx/") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"message":"Internal Error"}}`))
			return
		}
		l.npm.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	l.fw, l.failPfSense = newFirewall(t)
	if o.noProxyHosts {
		l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
			for _, h := range all {
				if h.Host == "nginx" {
					h.Aliases = nil
					return []pfsensefake.HostOverride{h}
				}
			}
			return nil
		})
	}
	fwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.failPfSense.Load() && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v2/services/dns_resolver/") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"status":"server error","response_id":"SERVER_ERROR","message":"Internal Error","data":[]}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/services/dns_resolver/host_overrides" && l.stallPfSense.Load() && l.applied.Load() {
			time.Sleep(time.Second)
		}
		l.fw.ServeHTTP(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/services/dns_resolver/apply" {
			l.applied.Store(true)
		}
	}))
	t.Cleanup(fwSrv.Close)

	adguard, err := adguardfake.New("../adguard-api/testdata/baseline", adguardfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	agSrv := httptest.NewServer(adguard)
	t.Cleanup(agSrv.Close)

	components := []sdk.Definition{
		nginxproxymanagerapi.Definition(), pfsenseapi.Definition(), network.Definition(), adguardapi.Definition(),
		ntfyapi.Definition(), checks.DefinitionDialing(refuse), apps.Definition(),
	}
	if o.resync != 0 {
		components[len(components)-1] = apps.DefinitionResyncingWithin(o.resync)
	}
	if o.fixtureWorkloads {
		components = append(components, proxmoxapi.Definition(), dockerapi.Definition())
	} else {
		if l.proxmox == nil {
			l.proxmox = &proxmoxFake{}
		}
		if l.docker == nil {
			l.docker = &dockerFake{}
		}
		components = append(components, proxmoxApiServing(l.proxmox), dockerApiServing(l.docker))
	}
	l.h = coretest.New(t, coretest.Options{
		Components: components,
		Plugins:    []coretest.Plugin{{Name: "apps", Requires: []string{"apps", "checks"}}},
		DataDir:    o.dataDir,
	})
	if !o.npmUnconfigured {
		l.h.SeedConfig("nginx-proxy-manager-api", &nginxproxymanagerapipb.NginxProxyManagerApiConfig{Url: srv.URL, Email: npmEmail, Password: npmfake.DefaultPassword})
	}
	l.h.SeedConfig("pfsense-api", &pfsenseapipb.PfsenseApiConfig{Url: fwSrv.URL, ApiKey: "test"})
	l.h.SeedConfig("adguard-api", &adguardapipb.AdguardApiConfig{Url: agSrv.URL, Username: "test", Password: "test"})
	if !o.networkUnconfigured {
		l.h.SeedConfig("network", &networkpb.NetworkConfig{BypassAlias: "VPN_BYPASS", BlockAlias: "KILL_SWITCH", ReservedRange: "198.18.1.150-198.18.1.199"})
	}
	if o.fixtureWorkloads {
		pve := fixture.Server(t, "../proxmox-api/testdata/baseline")
		media := fixture.Server(t, "../docker-api/testdata/published-on-lan")
		l.h.SeedConfig("proxmox-api", &proxmoxapipb.ProxmoxApiConfig{Url: pve.URL, TokenId: "ddash@pve!test", TokenSecret: "test"})
		l.h.SeedConfig("docker-api", &dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{{Name: "docker-host", Url: "tcp://" + media.Listener.Addr().String()}}})
	} else {
		// The fake proxmox-api still has the real config's required
		// fields: unconfigured, it would leave apps degraded.
		l.h.SeedConfig("proxmox-api", &proxmoxapipb.ProxmoxApiConfig{Url: "https://198.18.1.2:8006", TokenId: "ddash@pve!test", TokenSecret: "test"})
	}
	l.h.Install("apps")
	l.waitStarted()
	return l
}

// newNPM is Nginx Proxy Manager's baseline, and the switch failing its
// reads (lab.failNPM).
func newNPM(t *testing.T) (*npmfake.NPM, *atomic.Bool) {
	t.Helper()
	f, err := npmfake.New("../nginx-proxy-manager-api/testdata/baseline", npmfake.Options{
		Miss: func(request string, err error) { t.Errorf("Nginx Proxy Manager fixture: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, &atomic.Bool{}
}

// newFirewall is pfSense's baseline with the nginx Host Override at the
// lab's Reverse Proxy address, and the switch failing its Host Override
// reads (lab.failPfSense).
func newFirewall(t *testing.T) (*pfsensefake.Firewall, *atomic.Bool) {
	t.Helper()
	fw, err := pfsensefake.New("../pfsense-api/testdata/baseline", pfsensefake.Options{
		Miss: func(request string, err error) { t.Errorf("pfSense fixture: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fw.EditHostOverrides(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			if all[i].Host == "nginx" {
				all[i].IPs = []string{rpAddress}
			}
		}
		return all
	})
	return fw, &atomic.Bool{}
}

// editDNS changes pfSense's Host Overrides as the Operator would in its
// web UI.
func (l *lab) editDNS(edit func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride) {
	l.fw.EditHostOverrides(edit)
}

// register adds each hostname as an alias of the Reverse Proxy's Host
// Override, as the Operator would in pfSense's web UI.
func (l *lab) register(names ...string) {
	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			if all[i].Host != "nginx" {
				continue
			}
			for _, n := range names {
				host, domain, _ := strings.Cut(n, ".")
				all[i].Aliases = append(all[i].Aliases, pfsensefake.HostOverrideAlias{Host: host, Domain: domain})
			}
		}
		return all
	})
}

// unregister deletes each Host Override and alias for a hostname, as the
// Operator would in pfSense's web UI.
func (l *lab) unregister(names ...string) {
	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		gone := func(host, domain string) bool { return slices.Contains(names, host+"."+domain) }
		all = slices.DeleteFunc(all, func(h pfsensefake.HostOverride) bool { return gone(h.Host, h.Domain) })
		for i := range all {
			all[i].Aliases = slices.DeleteFunc(all[i].Aliases, func(a pfsensefake.HostOverrideAlias) bool { return gone(a.Host, a.Domain) })
		}
		return all
	})
}

// waitStarted waits until apps runs, healthy or not.
func (l *lab) waitStarted() {
	l.t.Helper()
	for range 2000 {
		switch l.h.Status("apps").State {
		case runtime.OK, runtime.Degraded:
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.t.Fatalf("apps is %v, want it running", l.h.Status("apps"))
}

// call calls apps' method as the apps Plugin, decoding the answer into out
// (unless nil); it fails the test unless the call succeeds.
func (l *lab) call(method string, req, out proto.Message) {
	l.t.Helper()
	if err := l.try(method, req, out); err != nil {
		l.t.Fatalf("%s: %v", method, err)
	}
}

// callError is an Action's or getter's failure: its Connect code, message
// and details.
type callError struct {
	code, message string
	details       []detail
}

// detail is a Connect error detail: a message's full name and bytes.
type detail struct {
	typ   string
	value []byte
}

func (e *callError) Error() string { return e.code + ": " + e.message }

// detail decodes the error's detail of out's type into out, reporting
// whether it has one.
func (e *callError) detail(out proto.Message) bool {
	for _, d := range e.details {
		if d.typ == string(out.ProtoReflect().Descriptor().FullName()) {
			return proto.Unmarshal(d.value, out) == nil
		}
	}
	return false
}

// try calls apps' method, returning its failure.
func (l *lab) try(method string, req, out proto.Message) *callError {
	l.t.Helper()
	body, err := protojson.Marshal(req)
	if err != nil {
		l.t.Fatal(err)
	}
	res := l.h.Call("apps", "apps", method, string(body))
	if res.Code != http.StatusOK {
		var e struct {
			Code, Message string
			Details       []struct{ Type, Value string }
		}
		_ = json.Unmarshal(res.Body.Bytes(), &e)
		out := &callError{code: e.Code, message: e.Message}
		for _, d := range e.Details {
			b, err := base64.RawStdEncoding.DecodeString(d.Value)
			if err != nil {
				l.t.Fatalf("%s's detail %s: %v", method, d.Type, err)
			}
			out.details = append(out.details, detail{d.Type, b})
		}
		return out
	}
	if out != nil {
		if err := protojson.Unmarshal(res.Body.Bytes(), out); err != nil {
			l.t.Fatal(err)
		}
	}
	return nil
}

func (l *lab) list() *appspb.ListAppsResponse {
	l.t.Helper()
	resp := &appspb.ListAppsResponse{}
	l.call("ListApps", &appspb.ListAppsRequest{}, resp)
	return resp
}

// app is the App with id; it fails the test without one.
func (l *lab) app(id string) *appspb.App {
	l.t.Helper()
	for _, a := range l.list().GetApps() {
		if a.GetId() == id {
			return a
		}
	}
	l.t.Fatalf("no App %s", id)
	return nil
}

// has reports whether apps lists an App with id.
func (l *lab) has(id string) bool {
	l.t.Helper()
	for _, a := range l.list().GetApps() {
		if a.GetId() == id {
			return true
		}
	}
	return false
}

// refresh runs apps' Refresh: the Apps are current when it returns.
func (l *lab) refresh() {
	l.t.Helper()
	l.call("Refresh", &appspb.RefreshRequest{}, nil)
}

// appChecks are the App Checks checks lists now, by App id.
func (l *lab) appChecks() map[string]*checkspb.Check {
	l.t.Helper()
	res := l.h.Call("apps", "checks", "ListChecks", `{}`)
	if res.Code != http.StatusOK {
		l.t.Fatalf("ListChecks: %d %s", res.Code, res.Body)
	}
	var resp checkspb.ListChecksResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		l.t.Fatal(err)
	}
	out := map[string]*checkspb.Check{}
	for _, c := range resp.GetChecks() {
		if c.GetAppId() != "" {
			out[c.GetAppId()] = c
		}
	}
	return out
}

// proxyHost is an edit making a Proxy Host serve domains from upstream
// ("http://198.18.2.10:8096"), with certificate 3 (the wildcard) unless
// noCert.
func proxyHost(upstream string, domains ...string) func(map[string]any) {
	return func(row map[string]any) {
		scheme, rest, _ := strings.Cut(upstream, "://")
		host, port, _ := net.SplitHostPort(rest)
		n := 0
		for _, r := range port {
			n = n*10 + int(r-'0')
		}
		row["domain_names"] = domains
		row["forward_scheme"], row["forward_host"], row["forward_port"] = scheme, host, n
		row["certificate_id"] = 3
	}
}

// noCert is an edit taking a Proxy Host's certificate away.
func noCert(row map[string]any) {
	row["certificate_id"], row["ssl_forced"], row["http2_support"] = 0, false, false
}

func diff(want, got any) string {
	return cmp.Diff(want, got, protocmp.Transform())
}

// refuse is the network App Checks dial: every address refuses, so nothing
// reaches the real network.
func refuse(context.Context, string, string) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// proxmoxFake is a proxmox-api Component listing the guests a test sets.
type proxmoxFake struct {
	proxmoxapipb.UnimplementedProxmoxApiServiceServer
	mu     sync.Mutex
	guests []*proxmoxapipb.Workload
	err    error
}

// fail makes ListWorkloads fail with err; nil answers again.
func (f *proxmoxFake) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (*proxmoxFake) Start(context.Context, *sdk.Env, *proxmoxapipb.ProxmoxApiConfig) error {
	return nil
}

func (*proxmoxFake) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (f *proxmoxFake) ListWorkloads(context.Context, *proxmoxapipb.ListWorkloadsRequest) (*proxmoxapipb.ListWorkloadsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &proxmoxapipb.ListWorkloadsResponse{Workloads: f.guests}, nil
}

func proxmoxApiServing(f *proxmoxFake) sdk.Definition {
	return sdk.Define(sdk.Spec[*proxmoxapipb.ProxmoxApiConfig]{
		Manifest: []byte("name: proxmox-api\ndescription: A fake.\nrequires: []\n"),
		Service:  &proxmoxapipb.ProxmoxApiService_ServiceDesc,
		New:      func() sdk.Component[*proxmoxapipb.ProxmoxApiConfig] { return f },
	})
}

// dockerFake is a docker-api Component listing the containers a test sets.
type dockerFake struct {
	dockerapipb.UnimplementedDockerApiServiceServer
	mu    sync.Mutex
	hosts []*dockerapipb.HostContainers
}

func (*dockerFake) Start(context.Context, *sdk.Env, *dockerapipb.DockerApiConfig) error {
	return nil
}

func (*dockerFake) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (f *dockerFake) ListContainers(context.Context, *dockerapipb.ListContainersRequest) (*dockerapipb.ListContainersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &dockerapipb.ListContainersResponse{Hosts: f.hosts}, nil
}

func dockerApiServing(f *dockerFake) sdk.Definition {
	return sdk.Define(sdk.Spec[*dockerapipb.DockerApiConfig]{
		Manifest: []byte("name: docker-api\ndescription: A fake.\nrequires: []\n"),
		Service:  &dockerapipb.DockerApiService_ServiceDesc,
		New:      func() sdk.Component[*dockerapipb.DockerApiConfig] { return f },
	})
}
