package apps_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// seen is what a test reads off an App: its id, name, URL, aliases and
// Workload's name.
type seen struct {
	ID, Name, URL string
	Aliases       []string
	Workload      string
}

func seenOf(a *appspb.App) seen {
	return seen{a.GetId(), a.GetName(), a.GetUrl(), a.GetAliases(), a.GetWorkload().GetName()}
}

// Each of the baseline's Proxy Hosts becomes an App of its own, keyed by
// the id its Check had before App records (so upgrading keeps every
// Check), on https with a certificate, its other domain names as aliases,
// linked to the Workload at its upstream: homepage's 198.18.1.201:3000 is
// the port the homepage container publishes on LXC 201 docker-host, every
// other upstream there is the guest, n8n's is the stopped LXC 206.
func TestEveryProxyHostIsAnApp(t *testing.T) {
	l := newLab(t, labOptions{fixtureWorkloads: true})

	var got []seen
	for _, a := range l.list().GetApps() {
		if a.GetReach() == appspb.Reach_REACH_PROXIED {
			got = append(got, seenOf(a))
		}
	}
	want := []seen{
		{"proxy:backup.home.example", "Backup", "https://backup.home.example", nil, ""},
		{"proxy:download.home.example", "Download", "https://download.home.example", nil, "media-stack"},
		{"proxy:fritz.home.example", "Fritz", "https://fritz.home.example", nil, ""},
		{"proxy:fullstack.home.example", "Fullstack", "https://fullstack.home.example", nil, "docker-host"},
		{"proxy:grafana.home.example", "Grafana", "https://grafana.home.example", nil, "grafana"},
		{"proxy:ha.home.example", "Ha", "https://ha.home.example", nil, ""},
		{"proxy:home.example", "Home", "https://home.example", []string{"nginx.home.example"}, "nginx"},
		{"proxy:home.home.example", "Home", "https://home.home.example", nil, "homepage"},
		{"proxy:indexer.home.example", "Indexer", "https://indexer.home.example", nil, "media-stack"},
		{"proxy:influxdb.home.example", "Influxdb", "https://influxdb.home.example", nil, "influxdb01"},
		{"proxy:keycloak.home.example", "Keycloak", "https://keycloak.home.example", []string{"local.home.example"}, "docker-host"},
		{"proxy:md.home.example", "Md", "https://md.home.example", nil, "docker-host"},
		{"proxy:movie.home.example", "Movie", "https://movie.home.example", nil, "media-stack"},
		{"proxy:n8n.home.example", "N8n", "https://n8n.home.example", nil, "n8n"},
		{"proxy:nodered.home.example", "Nodered", "https://nodered.home.example", nil, ""},
		{"proxy:portainer.home.example", "Portainer", "https://portainer.home.example", nil, "docker-host"},
		{"proxy:pv.home.example", "Pv", "https://pv.home.example", nil, ""},
		{"proxy:pve.home.example", "Pve", "https://pve.home.example", nil, ""},
		{"proxy:request.home.example", "Request", "https://request.home.example", nil, "media-stack"},
		{"proxy:series.home.example", "Series", "https://series.home.example", nil, "media-stack"},
		{"proxy:video.home.example", "Video", "https://video.home.example", nil, "media-stack"},
		{"proxy:wlan0.home.example", "Wlan0", "https://wlan0.home.example", nil, ""},
		{"proxy:wlan1.home.example", "Wlan1", "https://wlan1.home.example", nil, ""},
	}
	if d := diff(want, got); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}

	grafana := l.app("proxy:grafana.home.example")
	if grafana.GetReach() != appspb.Reach_REACH_PROXIED || grafana.GetLinks().GetProxyHostId() != 3 || grafana.GetProxyHost().GetForwardHost() != "198.18.1.204" {
		t.Errorf("Grafana = %v, want proxied by Proxy Host 3 at 198.18.1.204", grafana)
	}
	if got := grafana.GetWorkload().GetRef(); got.GetKind() != workloadpb.Kind_KIND_LXC || got.GetId() != "204" {
		t.Errorf("Grafana's Workload = %v, want LXC 204", got)
	}
}

// A Proxy Host without a certificate yields an http App; its first domain
// name in sorted order is the URL.
func TestANewProxyHostsURLIsItsFirstDomainName(t *testing.T) {
	l := newLab(t, labOptions{noProxyHosts: true})
	l.npm.AddProxyHost(func(row map[string]any) {
		proxyHost("http://198.18.2.11:3000", "wiki.home.example", "docs.home.example")(row)
		noCert(row)
	})
	l.refresh()

	var got []seen
	for _, a := range l.list().GetApps() {
		got = append(got, seenOf(a))
	}
	want := []seen{{"proxy:docs.home.example", "Docs", "http://docs.home.example", []string{"wiki.home.example"}, ""}}
	if d := diff(want, got); d != "" {
		t.Errorf("Apps (-want +got):\n%s", d)
	}
}
