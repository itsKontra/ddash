package apps_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

const (
	notRegisteredKind = appspb.WarningKind_WARNING_KIND_NOT_REGISTERED
	noProxyHost       = appspb.WarningKind_WARNING_KIND_NO_PROXY_HOST
	dnsBypasses       = appspb.WarningKind_WARNING_KIND_DNS_BYPASSES
	registeredTwice   = appspb.WarningKind_WARNING_KIND_REGISTERED_TWICE
	onTwoApps         = appspb.WarningKind_WARNING_KIND_HOSTNAME_ON_TWO_APPS
)

// named is a Warning about hostnames.
func named(w *appspb.Warning, hostnames ...string) *appspb.Warning {
	w.Hostnames = hostnames
	return w
}

// notRegistered is "Not registered" for the hostname n, un-ignored.
func notRegistered(n string) *appspb.Warning {
	return named(warning(notRegisteredKind, false, "", fixed(appspb.FixKind_FIX_KIND_REGISTER, onName(n)), deleteApp), n)
}

func onOverrideOrAlias(e *appspb.HostOverrideOrAlias) func(*appspb.Fix) { return func(f *appspb.Fix) { f.HostOverride = e } }

// addOverride adds a Host Override in pfSense's web UI.
func (l *lab) addOverride(host, ip string, aliases ...string) {
	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		o := pfsensefake.HostOverride{Host: host, Domain: "home.example", IPs: []string{ip}}
		for _, a := range aliases {
			o.Aliases = append(o.Aliases, pfsensefake.HostOverrideAlias{Host: a, Domain: "home.example"})
		}
		return append(all, o)
	})
}

// A proxied App's hostname in no Host Override is Not registered, with
// Register for each and Delete; registering it clears the Warning.
func TestAProxyHostWithoutDNSIsNotRegistered(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	l.npm.AddProxyHost(proxyHost("http://198.18.1.204:3000", "wiki.home.example", "docs.home.example"))
	l.refresh()

	want := []*appspb.Warning{named(warning(notRegisteredKind, false, "",
		fixed(appspb.FixKind_FIX_KIND_REGISTER, onName("docs.home.example")),
		fixed(appspb.FixKind_FIX_KIND_REGISTER, onName("wiki.home.example")),
		deleteApp,
	), "docs.home.example", "wiki.home.example")}
	if d := diff(want, l.app("proxy:docs.home.example").GetWarnings()); d != "" {
		t.Errorf("Warnings (-want +got):\n%s", d)
	}

	l.register("docs.home.example", "wiki.home.example")
	l.refresh()
	if w := l.app("proxy:docs.home.example").GetWarnings(); len(w) != 0 {
		t.Errorf("Warnings once registered = %v, want none", w)
	}
}

// An alias at the Reverse Proxy's address no Proxy Host serves is a
// proxied App with No Proxy Host: Add reverse proxy, or Remove the name.
// A Proxy Host for it clears the Warning and keeps the App.
func TestAnAliasWithoutAProxyHostWarns(t *testing.T) {
	l := newLab(t, labOptions{})
	l.register("wiki.home.example")
	l.refresh()

	wiki := l.byURL("https://wiki.home.example")
	want := []*appspb.Warning{named(warning(noProxyHost, false, "",
		fixed(appspb.FixKind_FIX_KIND_ADD_REVERSE_PROXY),
		fixed(appspb.FixKind_FIX_KIND_REMOVE_NAME, onName("wiki.home.example")),
	), "wiki.home.example")}
	if d := diff(want, wiki.GetWarnings()); d != "" {
		t.Errorf("Warnings (-want +got):\n%s", d)
	}

	host := l.npm.AddProxyHost(proxyHost("http://198.18.2.11:3000", "wiki.home.example"))
	l.refresh()
	got := l.app(wiki.GetId())
	if got.GetLinks().GetProxyHostId() != int64(host) || got.GetReach() != appspb.Reach_REACH_PROXIED {
		t.Errorf("wiki = %v, want its App linked to Proxy Host %d", got, host)
	}
	for _, w := range got.GetWarnings() {
		if w.GetKind() == noProxyHost {
			t.Errorf("wiki still warns %v", w)
		}
	}
}

// A domain added in Nginx Proxy Manager's UI to the hostname a Not-proxied
// App has: the Proxy Host's hostname resolves elsewhere ("DNS bypasses the
// Reverse Proxy"), and two Apps share it. Pointing the name at the
// Reverse Proxy, or deleting the Proxy Host, clears both.
func TestAProxyHostForANotProxiedAppsNameWarnsTwice(t *testing.T) {
	setUp := func(t *testing.T) (*lab, string) {
		l := newLab(t, labOptions{fixtureWorkloads: true})
		l.npm.EditProxyHost(3, func(row map[string]any) {
			row["domain_names"] = []string{"grafana.home.example", "nas.home.example"}
		})
		l.refresh()
		return l, l.byURL("https://nas.home.example").GetId()
	}
	l, nas := setUp(t)

	grafana := l.app("proxy:grafana.home.example")
	want := []*appspb.Warning{
		named(warning(dnsBypasses, false, "",
			fixed(appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY, onName("nas.home.example")),
			fixed(appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST, onProxyHost(3)),
		), "nas.home.example"),
		named(warning(onTwoApps, false, "", fixed(appspb.FixKind_FIX_KIND_MERGE, onApp(nas))), "nas.home.example"),
	}
	if d := diff(want, grafana.GetWarnings()); d != "" {
		t.Errorf("Grafana's Warnings (-want +got):\n%s", d)
	}
	want = []*appspb.Warning{named(warning(onTwoApps, false, "", fixed(appspb.FixKind_FIX_KIND_MERGE, onApp("proxy:grafana.home.example"))), "nas.home.example")}
	if d := diff(want, l.app(nas).GetWarnings()); d != "" {
		t.Errorf("nas's Warnings (-want +got):\n%s", d)
	}

	t.Run("point at the Reverse Proxy", func(t *testing.T) {
		l.unregister("nas.home.example")
		l.register("nas.home.example")
		l.refresh()
		if w := l.app("proxy:grafana.home.example").GetWarnings(); len(w) != 0 {
			t.Errorf("Grafana's Warnings = %v, want none", w)
		}
		if l.has(nas) {
			t.Error("nas's App is still listed once its Host Override is gone")
		}
	})
	t.Run("delete the Proxy Host", func(t *testing.T) {
		l, nas := setUp(t)
		l.npm.DeleteProxyHost(3)
		l.unregister("grafana.home.example")
		l.refresh()
		if w := l.app(nas).GetWarnings(); len(w) != 0 {
			t.Errorf("nas's Warnings = %v, want none", w)
		}
	})
}

// A hostname that is both a Host Override and an alias is Registered
// twice, with a Delete for each entry; deleting one clears it. Here the
// Host Override also points elsewhere, so the DNS bypasses the Reverse
// Proxy, and the App is Not proxied until then.
func TestAHostnameRegisteredTwiceWarns(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	l.addOverride("grafana", "198.18.1.204")
	l.refresh()

	grafana := l.app("proxy:grafana.home.example")
	if grafana.GetReach() != appspb.Reach_REACH_NOT_PROXIED {
		t.Errorf("Grafana is %v, want Not proxied", grafana.GetReach())
	}
	alias := &appspb.HostOverrideOrAlias{Host: "grafana", Domain: "home.example", Parent: &appspb.HostOverrideName{Host: "nginx", Domain: "home.example"}, Ips: []string{rpAddress}}
	override := &appspb.HostOverrideOrAlias{Host: "grafana", Domain: "home.example", Ips: []string{"198.18.1.204"}}
	want := []*appspb.Warning{
		named(warning(dnsBypasses, false, "",
			fixed(appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY, onName("grafana.home.example")),
			fixed(appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST, onProxyHost(3)),
		), "grafana.home.example"),
		named(warning(registeredTwice, false, "",
			fixed(appspb.FixKind_FIX_KIND_DELETE_ENTRY, onOverrideOrAlias(alias)),
			fixed(appspb.FixKind_FIX_KIND_DELETE_ENTRY, onOverrideOrAlias(override)),
		), "grafana.home.example"),
	}
	if d := diff(want, grafana.GetWarnings()); d != "" {
		t.Errorf("Grafana's Warnings (-want +got):\n%s", d)
	}
	if d := diff([]*appspb.HostOverrideOrAlias{alias, override}, grafana.GetDns()); d != "" {
		t.Errorf("Grafana's DNS (-want +got):\n%s", d)
	}

	l.unregister("grafana.home.example")
	l.register("grafana.home.example")
	l.refresh()
	grafana = l.app("proxy:grafana.home.example")
	if len(grafana.GetWarnings()) != 0 || grafana.GetReach() != appspb.Reach_REACH_PROXIED {
		t.Errorf("Grafana = %v, want proxied again without Warnings", grafana)
	}
}

// Merge makes two Apps sharing a hostname one: the chosen App keeps its
// id and settings and gains the other's sources; the other's record and
// Check go. It stays one App across syncs.
func TestMergeKeepsTheChosenApp(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})
	l.npm.EditProxyHost(3, func(row map[string]any) {
		row["domain_names"] = []string{"grafana.home.example", "nas.home.example"}
	})
	l.refresh()
	nas := l.byURL("https://nas.home.example").GetId()
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: nas, Settings: &appspb.AppSettings{Name: "Storage", Group: "Infra"}}, nil)
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:grafana.home.example", Settings: &appspb.AppSettings{Group: "Ops"}}, nil)

	l.call("Merge", &appspb.MergeRequest{Id: nas, From: "proxy:grafana.home.example"}, nil)

	check := func(when string) {
		t.Helper()
		got := l.app(nas)
		if got.GetName() != "Storage" || got.GetGroup() != "Infra" || got.GetUrl() != "https://nas.home.example" {
			t.Errorf("%s: merged App = %v, want nas's settings and URL", when, got)
		}
		if got.GetLinks().GetProxyHostId() != 3 || got.GetLinks().GetHostOverride().GetHost() != "nas" {
			t.Errorf("%s: merged App links %v, want Proxy Host 3 and nas's Host Override", when, got.GetLinks())
		}
		if d := diff([]string{"grafana.home.example"}, got.GetAliases()); d != "" {
			t.Errorf("%s: aliases (-want +got):\n%s", when, d)
		}
		if d := diff([]appspb.WarningKind{dnsBypasses}, kinds(got)); d != "" {
			t.Errorf("%s: Warnings (-want +got):\n%s", when, d)
		}
		if l.has("proxy:grafana.home.example") || l.appChecks()["proxy:grafana.home.example"] != nil {
			t.Errorf("%s: the App merged from is still listed, or its Check", when)
		}
	}
	check("merged")
	l.refresh()
	check("after a sync")
}

// Only two Apps sharing a hostname merge, and not two with a Proxy Host
// each.
func TestMergeRefusesAppsThatCantBeOne(t *testing.T) {
	l := newLab(t, labOptions{})
	cloud := l.byURL("https://cloud.home.example").GetId()
	l.npm.EditProxyHost(11, func(row map[string]any) {
		row["domain_names"] = []string{"backup.home.example", "grafana.home.example"}
	})
	l.refresh()

	for _, tc := range []struct {
		req  *appspb.MergeRequest
		code string
	}{
		{&appspb.MergeRequest{Id: "proxy:grafana.home.example", From: "proxy:backup.home.example"}, "failed_precondition"},
		{&appspb.MergeRequest{Id: cloud, From: "proxy:video.home.example"}, "failed_precondition"},
		{&appspb.MergeRequest{Id: cloud, From: cloud}, "invalid_argument"},
		{&appspb.MergeRequest{Id: cloud, From: "proxy:nowhere"}, "not_found"},
	} {
		if err := l.try("Merge", tc.req, nil); err == nil || err.code != tc.code {
			t.Errorf("Merge %v: %v, want %s", tc.req, err, tc.code)
		}
	}
}
