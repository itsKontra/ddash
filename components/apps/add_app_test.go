package apps_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	proxyhostpb "github.com/itsKontra/ddash/proto/ddash/proxyhost"
)

// addApp is the App Action adding a.
func addApp(a *appspb.AddApp) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_Add{Add: a}}
}

// plan is the Plan PlanAction gives action.
func (l *lab) plan(action *appspb.AppAction) *appspb.Plan {
	l.t.Helper()
	resp := &appspb.PlanActionResponse{}
	l.call("PlanAction", &appspb.PlanActionRequest{Action: action}, resp)
	return resp.GetPlan()
}

// runStream runs action, confirming plan, and returns what RunAction
// streamed: each step-state message, the last one its result, then its
// failure.
func (l *lab) runStream(action *appspb.AppAction, plan *appspb.Plan) ([]*appspb.RunActionResponse, *callError) {
	l.t.Helper()
	return stream(l, "RunAction", &appspb.RunActionRequest{Action: action, Plan: plan}, func() *appspb.RunActionResponse { return &appspb.RunActionResponse{} })
}

// tryRun runs action, confirming plan: its result is RunAction's last
// message.
func (l *lab) tryRun(action *appspb.AppAction, plan *appspb.Plan) (*appspb.RunActionResponse, *callError) {
	l.t.Helper()
	msgs, err := l.runStream(action, plan)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		l.t.Fatal("RunAction ended without a message")
	}
	return msgs[len(msgs)-1], nil
}

// run runs action, confirming the Plan PlanAction gives it now; it fails
// the test unless the Action succeeds.
func (l *lab) run(action *appspb.AppAction) *appspb.RunActionResponse {
	l.t.Helper()
	resp, err := l.tryRun(action, l.plan(action))
	if err != nil {
		l.t.Fatalf("RunAction: %v", err)
	}
	return resp
}

// addLinkOnly adds a Link-only App named name at url, returning its id.
func (l *lab) addLinkOnly(name, url string) string {
	l.t.Helper()
	return l.run(addApp(&appspb.AddApp{Name: name, Url: url, Reach: appspb.Reach_REACH_LINK_ONLY})).GetAppId()
}

// writes are the requests but reads in a fake's log after the first n.
func writes(log []string, n int) []string {
	var out []string
	for _, e := range log[n:] {
		if !strings.HasPrefix(e, "GET ") {
			out = append(out, e)
		}
	}
	return out
}

// titles are a Plan's steps as "system: title".
func titles(p *appspb.Plan) []string {
	var out []string
	for _, s := range p.GetSteps() {
		out = append(out, s.GetSystem().String()+": "+s.GetTitle())
	}
	return out
}

// The pfSense log entries of a Host Override batch: an alias added, a Host
// Override created, the apply asked for and done.
const (
	pfAddAlias = "POST /api/v2/services/dns_resolver/host_override/alias"
	pfCreate   = "POST /api/v2/services/dns_resolver/host_override"
	pfApply    = "POST /api/v2/services/dns_resolver/apply"
)

// wiki is a Proxy Host's fields for wiki.home.example: an upstream with
// the wildcard certificate (3), and Block Common Exploits, Force SSL and
// HTTP/2.
var wiki = &proxyhostpb.ProxyHostFields{
	ForwardScheme: "http", ForwardHost: "198.18.2.11", ForwardPort: 3000,
	CertificateId: 3, BlockExploits: true, ForceSsl: true, Http2: true,
}

// Adding a proxied App creates its Proxy Host in Nginx Proxy Manager, adds
// its hostname to the Reverse Proxy's Host Override in one pfSense batch
// applied once, then records the App, linked to its Proxy Host.
func TestAddAProxiedApp(t *testing.T) {
	l := newLab(t, labOptions{})
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := addApp(&appspb.AddApp{Url: "https://wiki.home.example", Name: "Wiki", Group: "Docs", Reach: appspb.Reach_REACH_PROXIED, ProxyHost: wiki})

	p := l.plan(action)
	want := []string{
		"SYSTEM_NGINX_PROXY_MANAGER: Create Proxy Host wiki.home.example",
		"SYSTEM_PFSENSE: Add alias wiki.home.example to nginx.home.example",
		"SYSTEM_DDASH: Create the App Wiki",
	}
	if got := titles(p); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	wantValues := [][]*appspb.PlanValue{
		{
			{Label: "Domain names", After: "wiki.home.example"},
			{Label: "Upstream", After: "http://198.18.2.11:3000"},
			{Label: "Certificate", After: "#3"},
			{Label: "Options", After: "Block Common Exploits · Force SSL · HTTP/2"},
		},
		{
			{Label: "Host Override", Before: "nginx.home.example → 127.0.0.1", After: "nginx.home.example → 127.0.0.1"},
			{Label: "Alias", After: "wiki.home.example"},
		},
		{{Label: "URL", After: "https://wiki.home.example"}, {Label: "Name", After: "Wiki"}, {Label: "Group", After: "Docs"}},
	}
	for i, s := range p.GetSteps() {
		if d := diff(wantValues[i], s.GetValues()); d != "" {
			t.Errorf("step %d's values (-want +got):\n%s", i+1, d)
		}
	}

	resp := l.run(action)
	if resp.GetDone() != 3 || resp.GetReread() != "" || resp.GetAppId() != "proxy:wiki.home.example" {
		t.Errorf("RunAction = %v, want 3 steps done for proxy:wiki.home.example", resp)
	}
	if d := diff([]string{"POST /api/nginx/proxy-hosts"}, writes(l.npm.Log(), npmLog)); d != "" {
		t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
	}
	if d := diff([]string{pfAddAlias, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}

	a := l.app(resp.GetAppId())
	ph := a.GetProxyHost()
	if a.GetReach() != appspb.Reach_REACH_PROXIED || a.GetName() != "Wiki" || a.GetGroup() != "Docs" || ph.GetId() != a.GetLinks().GetProxyHostId() {
		t.Errorf("App = %v, want proxied Wiki in Docs, linked to its Proxy Host", a)
	}
	wantHost := &appspb.ProxyHost{
		Id: ph.GetId(), Enabled: true, Fields: &proxyhostpb.ProxyHostFields{
			DomainNames: []string{"wiki.home.example"}, ForwardScheme: "http", ForwardHost: "198.18.2.11", ForwardPort: 3000,
			CertificateId: 3, BlockExploits: true, ForceSsl: true, Http2: true,
		},
	}
	// The wildcard's state depends on the day.
	if d := cmp.Diff(wantHost, ph, protocmp.Transform(), protocmp.IgnoreFields(&appspb.ProxyHost{}, "certificate")); d != "" {
		t.Errorf("Proxy Host (-want +got):\n%s", d)
	}
	if d := diff([]*appspb.HostOverrideOrAlias{{Host: "wiki", Domain: "home.example", Parent: &appspb.HostOverrideName{Host: "nginx", Domain: "home.example"}, Ips: []string{rpAddress}}}, a.GetDns()); d != "" {
		t.Errorf("DNS (-want +got):\n%s", d)
	}
	if _, ok := l.appChecks()[a.GetId()]; !ok {
		t.Error("the new App has no Check")
	}
}

// Adding a Not-proxied App creates its own Host Override at its address in
// one pfSense batch applied once, then records the App, linked to it;
// Nginx Proxy Manager is left alone.
func TestAddANotProxiedApp(t *testing.T) {
	l := newLab(t, labOptions{})
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := addApp(&appspb.AddApp{Url: "http://printer.home.example:631", Reach: appspb.Reach_REACH_NOT_PROXIED, Address: "198.18.1.60"})

	p := l.plan(action)
	if got, want := titles(p), []string{"SYSTEM_PFSENSE: Create Host Override printer.home.example", "SYSTEM_DDASH: Create the App Printer"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if d := diff([]*appspb.PlanValue{{Label: "Host Override", After: "printer.home.example → 198.18.1.60"}}, p.GetSteps()[0].GetValues()); d != "" {
		t.Errorf("pfSense step's values (-want +got):\n%s", d)
	}

	resp := l.run(action)
	if resp.GetDone() != 2 || resp.GetReread() != "" {
		t.Errorf("RunAction = %v, want both steps done", resp)
	}
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes: %q, want none", w)
	}
	if d := diff([]string{pfCreate, pfApply, pfsensefake.AppliedDNS}, writes(l.fw.Log(), fwLog)); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	a := l.app(resp.GetAppId())
	if a.GetReach() != appspb.Reach_REACH_NOT_PROXIED || a.GetName() != "Printer" || a.GetUrl() != "http://printer.home.example:631" ||
		a.GetLinks().GetHostOverride().GetHost() != "printer" || a.GetLinks().GetHostOverride().GetDomain() != "home.example" {
		t.Errorf("App = %v, want Not-proxied Printer linked to its Host Override", a)
	}
	if d := diff([]*appspb.HostOverrideOrAlias{{Host: "printer", Domain: "home.example", Ips: []string{"198.18.1.60"}}}, a.GetDns()); d != "" {
		t.Errorf("DNS (-want +got):\n%s", d)
	}
}

// The Plan shows the Proxy Host as Nginx Proxy Manager will save it: no
// certificate turns Force SSL and HTTP/2 off, no Force SSL turns HSTS off.
func TestAProxiedAddsPlanFollowsNginxProxyManagersToggleRules(t *testing.T) {
	l := newLab(t, labOptions{})
	plain := &proxyhostpb.ProxyHostFields{ForwardScheme: "http", ForwardHost: "198.18.2.11", ForwardPort: 3000, Websockets: true, ForceSsl: true, Http2: true, Hsts: true, HstsSubdomains: true}

	p := l.plan(addApp(&appspb.AddApp{Url: "http://wiki.home.example", Reach: appspb.Reach_REACH_PROXIED, ProxyHost: plain}))
	want := []*appspb.PlanValue{
		{Label: "Domain names", After: "wiki.home.example"},
		{Label: "Upstream", After: "http://198.18.2.11:3000"},
		{Label: "Certificate", After: "None: plain HTTP"},
		{Label: "Options", After: "Websockets"},
	}
	if d := diff(want, p.GetSteps()[0].GetValues()); d != "" {
		t.Errorf("Proxy Host step's values (-want +got):\n%s", d)
	}
}

// A Link-only App changes nothing outside ddash: its Plan is the App
// record alone, and it is listed at once with its settings.
func TestAddALinkOnlyApp(t *testing.T) {
	l := newLab(t, labOptions{})
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())
	action := addApp(&appspb.AddApp{Url: "https://198.18.0.1", Name: " pfSense ", Icon: "pfsense", Group: "Network", Reach: appspb.Reach_REACH_LINK_ONLY})

	p := l.plan(action)
	if got, want := titles(p), []string{"SYSTEM_DDASH: Create the App pfSense"}; !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if len(p.GetNames()) != 0 || len(p.GetBlocked()) != 0 {
		t.Errorf("Plan = %v, want nothing taken or blocking", p)
	}

	resp := l.run(action)
	if resp.GetDone() != 1 || resp.GetReread() != "" {
		t.Errorf("RunAction = %v, want its one step done and the Apps re-read", resp)
	}
	a := l.app(resp.GetAppId())
	if a.GetReach() != appspb.Reach_REACH_LINK_ONLY || a.GetName() != "pfSense" || a.GetIcon() != "pfsense" || a.GetGroup() != "Network" || a.GetUrl() != "https://198.18.0.1" {
		t.Errorf("App = %v, want Link-only pfSense with its settings", a)
	}
	if _, ok := l.appChecks()[a.GetId()]; !ok {
		t.Error("the new App has no Check")
	}
	if w := append(writes(l.npm.Log(), npmLog), writes(l.fw.Log(), fwLog)...); len(w) != 0 {
		t.Errorf("writes to Nginx Proxy Manager and pfSense: %q, want none", w)
	}
}
