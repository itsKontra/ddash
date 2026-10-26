package pfsenseapi_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// proxyAliases are the aliases of nginx.home.example, the Reverse Proxy's
// Host Override in the baseline fixtures, in the order the web UI added
// them.
var proxyAliases = []string{"home", "pve", "portainer", "fritz", "wlan0", "wlan1", "backup", "fullstack", "n8n", "md", "ha", "pv", "nodered", "grafana", "request", "movie", "series", "download", "indexer", "video", "influxdb", "keycloak", "local"}

func aliasesOf(hosts ...string) []*pfsenseapipb.HostOverrideAlias {
	var out []*pfsenseapipb.HostOverrideAlias
	for _, h := range hosts {
		out = append(out, &pfsenseapipb.HostOverrideAlias{Host: h, Domain: "home.example"})
	}
	return out
}

func override(host, ip string, aliases ...string) *pfsenseapipb.HostOverride {
	return &pfsenseapipb.HostOverride{Host: host, Domain: "home.example", Ips: []string{ip}, Aliases: aliasesOf(aliases...)}
}

// baselineOverrides are the baseline fixtures' Host Overrides.
func baselineOverrides() []*pfsenseapipb.HostOverride {
	nginx := override("nginx", "198.18.1.210", proxyAliases...)
	nginx.Description = "Reverse proxy"
	return []*pfsenseapipb.HostOverride{
		override("cloud", "198.18.1.209"),
		override("docker", "198.18.1.201"),
		override("nas", "198.18.2.10"),
		nginx,
		override("pfsense", "198.18.1.1"),
		override("proxmox", "198.18.1.200"),
	}
}

func name(host string) *pfsenseapipb.HostName {
	return &pfsenseapipb.HostName{Host: host, Domain: "home.example"}
}

func create(host, ip string) *pfsenseapipb.HostOverrideStep {
	return &pfsenseapipb.HostOverrideStep{Change: &pfsenseapipb.HostOverrideStep_Create{Create: override(host, ip)}}
}

func remove(host string) *pfsenseapipb.HostOverrideStep {
	return &pfsenseapipb.HostOverrideStep{Change: &pfsenseapipb.HostOverrideStep_Delete{Delete: name(host)}}
}

func readdress(host string, ips ...string) *pfsenseapipb.HostOverrideStep {
	return &pfsenseapipb.HostOverrideStep{Change: &pfsenseapipb.HostOverrideStep_Readdress{Readdress: &pfsenseapipb.Readdress{HostOverride: name(host), Ips: ips}}}
}

func addAlias(parent, host string) *pfsenseapipb.HostOverrideStep {
	return &pfsenseapipb.HostOverrideStep{Change: &pfsenseapipb.HostOverrideStep_AddAlias{AddAlias: &pfsenseapipb.AliasChange{Parent: name(parent), Alias: aliasesOf(host)[0]}}}
}

func removeAlias(parent, host string) *pfsenseapipb.HostOverrideStep {
	return &pfsenseapipb.HostOverrideStep{Change: &pfsenseapipb.HostOverrideStep_RemoveAlias{RemoveAlias: &pfsenseapipb.AliasChange{Parent: name(parent), Alias: aliasesOf(host)[0]}}}
}

func change(steps ...*pfsenseapipb.HostOverrideStep) *pfsenseapipb.ChangeHostOverridesRequest {
	return &pfsenseapipb.ChangeHostOverridesRequest{Steps: steps}
}

// fakeOverride is a Host Override in home.example as the fake holds it.
func fakeOverride(host, ip string, aliases ...string) pfsensefake.HostOverride {
	h := pfsensefake.HostOverride{Host: host, Domain: "home.example", IPs: []string{ip}}
	for _, a := range aliases {
		h.Aliases = append(h.Aliases, pfsensefake.HostOverrideAlias{Host: a, Domain: "home.example"})
	}
	return h
}

// dnsApplies counts the times the fake applied the DNS Resolver.
func dnsApplies(fw *fakeFirewall) int {
	n := 0
	for _, e := range fw.Log() {
		if e == pfsensefake.AppliedDNS {
			n++
		}
	}
	return n
}

// Every write re-sorts the Host Overrides by host, and every write to
// nginx's aliases re-sorts them: each step still finds its target.
func TestABatchFindsEachTargetThoughEveryWriteMovesTheIds(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.ChangeHostOverrides(t.Context(), change(
		create("dns", "198.18.1.53"),
		addAlias("nginx", "jellyfin"),
		removeAlias("nginx", "grafana"),
		remove("cloud"),
		addAlias("nginx", "photos"),
		readdress("nas", "198.18.2.11"),
		removeAlias("nginx", "backup"),
	))
	if err != nil {
		t.Fatal(err)
	}

	if got.GetSucceeded() != 7 {
		t.Errorf("succeeded = %d, want 7", got.GetSucceeded())
	}
	nginxAliases := slices.DeleteFunc(slices.Concat(proxyAliases, []string{"jellyfin", "photos"}), func(h string) bool { return h == "grafana" || h == "backup" })
	slices.Sort(nginxAliases)
	nginx := fakeOverride("nginx", "198.18.1.210", nginxAliases...)
	nginx.Description = "Reverse proxy"
	want := []pfsensefake.HostOverride{
		fakeOverride("dns", "198.18.1.53"),
		fakeOverride("docker", "198.18.1.201"),
		fakeOverride("nas", "198.18.2.11"),
		nginx,
		fakeOverride("pfsense", "198.18.1.1"),
		fakeOverride("proxmox", "198.18.1.200"),
	}
	if diff := cmp.Diff(want, fw.HostOverrides()); diff != "" {
		t.Errorf("Host Overrides (-want +got):\n%s", diff)
	}
	if fw.DNSPending() {
		t.Error("changes still pending, want applied")
	}
	if n := dnsApplies(fw); n != 1 {
		t.Errorf("applied %d times, want once", n)
	}
	if log := fw.Log(); log[len(log)-1] != pfsensefake.AppliedDNS {
		t.Errorf("the apply isn't last: %v", log)
	}
}

func TestListsHostOverridesWithTheirAliases(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.ListHostOverrides(t.Context(), &pfsenseapipb.ListHostOverridesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListHostOverridesResponse{HostOverrides: baselineOverrides()}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListHostOverrides (-want +got):\n%s", diff)
	}
}

// succeededOf is the steps a failed batch's error says succeeded.
func succeededOf(t *testing.T, err error) int32 {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfsenseapipb.ChangeHostOverridesResponse); ok {
			return r.GetSucceeded()
		}
	}
	t.Fatalf("%v carries no ChangeHostOverridesResponse", err)
	return 0
}

func hosts(fw *fakeFirewall) []string {
	var out []string
	for _, h := range fw.HostOverrides() {
		out = append(out, h.Host)
		for _, a := range h.Aliases {
			out = append(out, a.Host+" of "+h.Host)
		}
	}
	return out
}

// A failed step ends the batch, and what the steps before it wrote is
// still applied, once.
func TestAFailedStepEndsTheBatchAndWhatWasWrittenIsAppliedOnce(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	fw.FailDNSWrite(2)

	_, err := c.ChangeHostOverrides(t.Context(), change(
		create("dns", "198.18.1.53"),
		addAlias("nginx", "jellyfin"),
		removeAlias("nginx", "grafana"),
	))

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "step 2 of 3 (add alias jellyfin.home.example to nginx.home.example)") {
		t.Errorf("err = %v, want step 2 Unavailable", err)
	}
	if n := succeededOf(t, err); n != 1 {
		t.Errorf("succeeded = %d, want 1", n)
	}
	got := hosts(fw)
	if !slices.Contains(got, "dns") || slices.Contains(got, "jellyfin of nginx") || !slices.Contains(got, "grafana of nginx") {
		t.Errorf("Host Overrides = %v, want dns created and nothing after", got)
	}
	if n := dnsApplies(fw); n != 1 || fw.DNSPending() {
		t.Errorf("applied %d times, pending %v; want once, nothing pending", n, fw.DNSPending())
	}
}

// pfSense would take each of these: it checks a name only against the
// Host Overrides, or against the aliases of the same parent.
func TestAStepRegisteringARegisteredHostnameIsRefusedBeforeItIsSent(t *testing.T) {
	for _, tc := range []struct {
		name string
		step *pfsenseapipb.HostOverrideStep
		want string
	}{
		{"a Host Override named like an alias", create("Grafana", "198.18.1.53"), "grafana.home.example is already an alias of nginx.home.example"},
		{"an alias named like a Host Override", addAlias("nginx", "pfsense"), "pfsense.home.example is already a Host Override"},
		{"an alias named like another parent's alias", addAlias("docker", "grafana"), "grafana.home.example is already an alias of nginx.home.example"},
		{"a Host Override twice", create("nas", "198.18.2.11"), "nas.home.example is already a Host Override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fw := firewall(t, "baseline")
			_, c := start(t, fw.URL)

			_, err := c.ChangeHostOverrides(t.Context(), change(tc.step))

			if status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want AlreadyExists: %s", err, tc.want)
			}
			if n := succeededOf(t, err); n != 0 {
				t.Errorf("succeeded = %d, want 0", n)
			}
			for _, e := range fw.Log() {
				if !strings.HasPrefix(e, "GET ") {
					t.Errorf("sent %q, want only reads", e)
				}
			}
		})
	}
}

// A later step sees what the earlier ones wrote: a name freed by a delete
// is free for the next step, and a name created is taken.
func TestAStepSeesTheStepsBeforeIt(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	if _, err := c.ChangeHostOverrides(t.Context(), change(removeAlias("nginx", "grafana"), create("grafana", "198.18.1.205"))); err != nil {
		t.Fatalf("moving grafana from an alias to a Host Override: %v", err)
	}
	_, err := c.ChangeHostOverrides(t.Context(), change(create("photos", "198.18.1.206"), addAlias("nginx", "photos")))
	if status.Code(err) != codes.AlreadyExists || succeededOf(t, err) != 1 {
		t.Errorf("err = %v, want the alias refused after the create", err)
	}
	if n := dnsApplies(fw); n != 2 {
		t.Errorf("applied %d times, want once per batch", n)
	}
}

func TestAStepOnAMissingTargetIsNotFound(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	for _, step := range []*pfsenseapipb.HostOverrideStep{remove("ghost"), readdress("ghost", "198.18.1.9"), addAlias("ghost", "x"), removeAlias("nginx", "ghost")} {
		if _, err := c.ChangeHostOverrides(t.Context(), change(step)); status.Code(err) != codes.NotFound {
			t.Errorf("%v: err = %v, want NotFound", step, err)
		}
	}
	if n := dnsApplies(fw); n != 0 {
		t.Errorf("applied %d times with nothing written", n)
	}
}

func TestAStepWithoutADomainOrAValidAddressIsRefusedUpFront(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	for _, steps := range [][]*pfsenseapipb.HostOverrideStep{
		{create("dns", "198.18.1.53"), create("cloud2", "not-an-ip")},
		{create("dns", "198.18.1.53"), {Change: &pfsenseapipb.HostOverrideStep_Delete{Delete: &pfsenseapipb.HostName{Host: "dns"}}}},
		{readdress("nas")},
		{{}},
	} {
		if _, err := c.ChangeHostOverrides(t.Context(), change(steps...)); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%v: err = %v, want InvalidArgument", steps, err)
		}
	}
	if fw.hits.Load() != 0 {
		t.Errorf("%d requests, want none", fw.hits.Load())
	}
}

// slowFirewall starts pfsense-api against the baseline fake shaped by
// opts, in memory: run it inside synctest.Test, so the delays pass on the
// fake clock.
func slowFirewall(t *testing.T, opts pfsensefake.Options) (*pfsensefake.Firewall, pfsenseapipb.PfsenseApiServiceClient) {
	t.Helper()
	opts.Miss = func(request string, err error) { t.Errorf("fixture: %v", err) }
	fw, err := pfsensefake.New("testdata/baseline", opts)
	if err != nil {
		t.Fatal(err)
	}
	h := sdktest.New(t, pfsenseapi.DefinitionServing(fw))
	h.MustStart(&pfsenseapipb.PfsenseApiConfig{Url: "https://198.18.1.1", ApiKey: apiKey})
	return fw, pfsenseapipb.NewPfsenseApiServiceClient(h.Conn)
}

// Unbound may take up to 30 s to stop: the apply waits that long, though
// any other request gives up after 5 s.
func TestTheApplyWaitsUpTo30SecondsAndOtherRequests5(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw, c := slowFirewall(t, pfsensefake.Options{ApplyDelay: 29 * time.Second})

		if _, err := c.ChangeHostOverrides(t.Context(), change(create("dns", "198.18.1.53"))); err != nil {
			t.Fatalf("a 29 s apply: %v", err)
		}
		if fw.DNSPending() {
			t.Error("changes still pending after the apply")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		_, c := slowFirewall(t, pfsensefake.Options{ApplyDelay: 31 * time.Second})

		_, err := c.ChangeHostOverrides(t.Context(), change(create("dns", "198.18.1.53")))

		if err == nil || !strings.Contains(err.Error(), "every step was written, but applying them failed") {
			t.Errorf("a 31 s apply: err = %v, want the apply timed out", err)
		}
		if n := succeededOf(t, err); n != 1 {
			t.Errorf("succeeded = %d, want 1", n)
		}
		time.Sleep(time.Second) // pfSense finishes the apply ddash gave up on.
	})
	synctest.Test(t, func(t *testing.T) {
		_, c := slowFirewall(t, pfsensefake.Options{WriteDelay: 6 * time.Second})

		_, err := c.ChangeHostOverrides(t.Context(), change(create("dns", "198.18.1.53")))

		if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "step 1 of 1") {
			t.Errorf("a 6 s write: err = %v, want it timed out", err)
		}
		time.Sleep(time.Second)
	})
}

func TestABatchDropsTheCacheAndFreshReadsPastIt(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	list := func(fresh bool) []string {
		t.Helper()
		r, err := c.ListHostOverrides(t.Context(), &pfsenseapipb.ListHostOverridesRequest{Fresh: fresh})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range r.GetHostOverrides() {
			out = append(out, h.GetHost())
		}
		return out
	}
	list(false)

	if _, err := c.ChangeHostOverrides(t.Context(), change(remove("cloud"))); err != nil {
		t.Fatal(err)
	}
	if got := list(false); slices.Contains(got, "cloud") {
		t.Errorf("after the batch ListHostOverrides = %v, want cloud gone", got)
	}

	// The web UI deletes docker behind ddash's back.
	fw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/api/v2/services/dns_resolver/host_override?id=0&apply=true", nil))
	if got := list(false); !slices.Contains(got, "docker") {
		t.Errorf("cached ListHostOverrides = %v, want docker still there", got)
	}
	if got := list(true); slices.Contains(got, "docker") {
		t.Errorf("fresh ListHostOverrides = %v, want docker gone", got)
	}
	if got := list(false); slices.Contains(got, "docker") {
		t.Errorf("ListHostOverrides after a fresh one = %v, want the cache refilled", got)
	}
}

// A failed apply after every step was written says so: all the steps
// succeeded, but what they wrote isn't applied.
func TestAFailedApplyIsReportedAsSuch(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	fw.FailDNSApply()

	_, err := c.ChangeHostOverrides(t.Context(), change(create("dns", "198.18.1.53"), addAlias("nginx", "jellyfin")))

	if err == nil || !strings.Contains(err.Error(), "every step was written, but applying them failed") {
		t.Fatalf("err = %v, want the apply failed", err)
	}
	var resp *pfsenseapipb.ChangeHostOverridesResponse
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfsenseapipb.ChangeHostOverridesResponse); ok {
			resp = r
		}
	}
	if resp.GetSucceeded() != 2 || !resp.GetApplyFailed() {
		t.Errorf("details = %v, want 2 steps succeeded and the apply failed", resp)
	}
	if !fw.DNSPending() {
		t.Error("nothing pending, want the written steps waiting for an apply")
	}
}
