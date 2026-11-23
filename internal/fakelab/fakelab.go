// Package fakelab is the homelab faked on localhost: each external system
// serves its Component's recorded baseline fixtures on a port of its own,
// and Config says how to point every Component at it. `make dev` and the
// subprocess test run the core against it; cmd/fakelab serves it alone.
//
// Docker comes first; each later system adds itself to systems. A system whose Actions must stick is served by a
// stateful fake over its fixtures (pfSense: pfsensefake; AdGuard:
// adguardfake; Proxmox: proxmoxfake; Nginx Proxy Manager: npmfake;
// Cloudflare: cloudflarefake; Telegram: telegramfake; calendars:
// calendarfake, whose dates follow the clock; InfluxDB: energyfake, a
// simulated house up to the clock; Anthropic: anthropicfake, a canned
// draft). A
// named scenario (e.g. "vpn-on-fallback") replaces the baseline of every
// system that has a sibling directory of that name, and may change the
// Docker Agent's seed (dockerScenarios). Beside the systems it
// serves fake Host Agents (agentfake) on three of the baseline's Workloads.
package fakelab

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/components/anthropic-api/anthropicapipb"
	"github.com/itsKontra/ddash/components/anthropic-api/anthropicfake"
	"github.com/itsKontra/ddash/components/calendar-api/calendarapipb"
	"github.com/itsKontra/ddash/components/calendar-api/calendarfake"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/components/energy-api/energyfake"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// system is one faked external system.
type system struct {
	// component is the Component that talks to it.
	component string
	// fixtures is its baseline scenario, relative to the repo root.
	fixtures string
	// config points the Component at the system served on addr.
	config func(addr string) proto.Message
	// serve, when set, is a stateful fake serving the fixtures in dir in
	// place of fixture.Scenario.
	serve func(dir string) (http.Handler, error)
}

var systems = []system{
	{
		component: "docker-api",
		fixtures:  "components/docker-api/testdata/baseline",
		config: func(addr string) proto.Message {
			return &dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{{Name: "media", Url: "tcp://" + addr}}}
		},
	},
	{
		component: "ntfy-api",
		fixtures:  "components/ntfy-api/testdata/baseline",
		config: func(addr string) proto.Message {
			server := "http://" + addr
			return &ntfyapipb.NtfyApiConfig{Server: &server, Topic: "ddash-alerts"}
		},
	},
	{
		component: "telegram-api",
		fixtures:  "components/telegram-api/testdata/baseline",
		config: func(addr string) proto.Message {
			server := "http://" + addr
			return &telegramapipb.TelegramApiConfig{Server: &server, Token: telegramfake.DefaultToken, ChatId: telegramfake.DefaultChat}
		},
		// Messages sent are kept: what reached the chat can be read back.
		serve: func(dir string) (http.Handler, error) { return telegramfake.New(dir, telegramfake.Options{}), nil },
	},
	{
		component: "pfsense-api",
		fixtures:  "components/pfsense-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// The fixtures don't check the key.
			return &pfsenseapipb.PfsenseApiConfig{Url: "http://" + addr, ApiKey: "fakelab"}
		},
		// Static mappings and their apply are kept: Adopt and rename stick.
		serve: func(dir string) (http.Handler, error) { return pfsensefake.New(dir, pfsensefake.Options{}) },
	},
	{
		component: "adguard-api",
		fixtures:  "components/adguard-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// The fake checks no credentials.
			return &adguardapipb.AdguardApiConfig{Url: "http://" + addr, Username: "fakelab", Password: "fakelab"}
		},
		// Persistent clients are kept: per-Device ad-blocking sticks.
		serve: func(dir string) (http.Handler, error) { return adguardfake.New(dir, adguardfake.Options{}) },
	},
	{
		component: "proxmox-api",
		fixtures:  "components/proxmox-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// Plain http: the lab has no certificate a Component trusts.
			return &proxmoxapipb.ProxmoxApiConfig{Url: "http://" + addr, TokenId: "ddash@pve!fakelab", TokenSecret: "fakelab"}
		},
		// Workload status and power tasks are kept: Start/Stop/Restart stick.
		serve: func(dir string) (http.Handler, error) { return proxmoxfake.New(dir, proxmoxfake.Options{}) },
	},
	{
		component: "nginx-proxy-manager-api",
		fixtures:  "components/nginx-proxy-manager-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// The fixtures' user, with the fake's password.
			return &nginxproxymanagerapipb.NginxProxyManagerApiConfig{Url: "http://" + addr, Email: "ddash@home.example", Password: "fakelab"}
		},
		// Sign-ins and Proxy Hosts are kept: writes stick.
		serve: func(dir string) (http.Handler, error) { return npmfake.New(dir, npmfake.Options{Password: "fakelab"}) },
	},
	{
		component: "cloudflare-api",
		fixtures:  "components/cloudflare-api/testdata/baseline",
		config: func(addr string) proto.Message {
			url := "http://" + addr
			return &cloudflareapipb.CloudflareApiConfig{AccountId: cloudflarefake.AccountID, TunnelId: cloudflarefake.TunnelID, ApiToken: "fakelab", ApiUrl: &url}
		},
		// The ingress and DNS records are kept: Public Hostname writes stick.
		serve: func(dir string) (http.Handler, error) {
			return cloudflarefake.New(dir, cloudflarefake.Options{Token: "fakelab"})
		},
	},
	{
		component: "calendar-api",
		fixtures:  "components/calendar-api/testdata/baseline",
		config: func(addr string) proto.Message {
			return &calendarapipb.CalendarApiConfig{Calendars: "Home = http://" + addr + "/home.ics; Work = http://" + addr + "/work.ics"}
		},
		// The calendars' dates follow the clock: there is always a today.
		serve: func(dir string) (http.Handler, error) { return calendarfake.New(dir, calendarfake.Options{}) },
	},
	{
		component: "energy-api",
		// No fixtures: the house is simulated.
		fixtures: "components/energy-api/testdata/baseline",
		config: func(addr string) proto.Message {
			return &energyapipb.EnergyApiConfig{Url: "http://" + addr, Token: "fake-token", BuyPrice: "0.2418", SellPrice: "0.05786", BatteryCapacity: "10", BatteryReserve: fmt.Sprint(energyfake.Reserve)}
		},
		// The readings follow the clock: there is always a today. The
		// battery charges from the grid every afternoon, and the VE.Bus
		// counters begin 60 days ago, as after a backfilled history.
		serve: func(string) (http.Handler, error) {
			return energyfake.New(energyfake.Options{GridCharge: [2]float64{13, 15}, VEBusFrom: time.Now().AddDate(0, 0, -60)}), nil
		},
	},
	{
		component: "anthropic-api",
		fixtures:  "components/anthropic-api/testdata/baseline",
		config: func(addr string) proto.Message {
			server := "http://" + addr
			return &anthropicapipb.AnthropicApiConfig{ApiKey: anthropicfake.DefaultKey, Server: &server}
		},
		// Every draft is the canned one: nothing reaches Anthropic.
		serve: func(dir string) (http.Handler, error) { return anthropicfake.New(dir, anthropicfake.Options{}) },
	},
}

// logicConfigs is the config of Components that reach no system
// themselves but must name what the lab's fixtures hold (an alias, a
// range), by Component.
var logicConfigs = map[string]func() proto.Message{
	"network": func() proto.Message {
		return &networkpb.NetworkConfig{
			BypassAlias: "VPN_BYPASS", ReservedRange: "198.18.1.150-198.18.1.199",
			RemoteAccessEndpoint: "vpn.example.org", RemoteAccessDns: []string{"198.18.1.53"},
		}
	},
}

// hostAgents are the fake Host Agents the lab serves, on Workloads of the
// proxmox-api baseline, each on its port when free. host-agent-api has no
// config and no fixtures, so it isn't one of the systems: `make dev`
// registers these with it once the core runs.
var hostAgents = []struct {
	vmid    uint32
	name    string
	port    int
	release string
	seed    agentfake.HostSeed
}{
	{
		// Debian, sshd with a key ddash didn't install, and pending updates
		// whose libc6 and kernel ask for a reboot.
		vmid: 201, name: "docker-host", port: 17451,
		seed: agentfake.HostSeed{
			Hostname: "docker-host", OS: "debian", SSHInstalled: true, SSHRunning: true, PasswordAuth: true,
			Keys: map[string][]string{"root": {"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIb9fWw8x0x1s7I1m2k0q3rS7f0Uu2Zb8oH4cQm6n2aT old-laptop"}},
			Pending: []agent.Package{
				{Name: "libc6", Current: "2.36-9+deb12u7", New: "2.36-9+deb12u9"},
				{Name: "linux-image-amd64", Current: "6.1.112-1", New: "6.1.119-1"},
				{Name: "openssl", Current: "3.0.14-1~deb12u2", New: "3.0.15-1~deb12u1"},
				{Name: "curl", Current: "7.88.1-10+deb12u7", New: "7.88.1-10+deb12u8"},
				{Name: "docker-ce", Current: "5:27.3.1-1~debian.12~bookworm", New: "5:27.4.0-1~debian.12~bookworm"},
			},
		},
	},
	{
		// Alpine without sshd, on storage that can't snapshot (207's
		// feature.json).
		vmid: 207, name: "telegraf", port: 17452,
		seed: agentfake.HostSeed{
			Hostname: "telegraf", OS: "alpine",
			Pending: []agent.Package{{Name: "busybox", Current: "1.36.1-r29", New: "1.36.1-r31"}, {Name: "musl", Current: "1.2.5-r0", New: "1.2.5-r1"}},
		},
	},
	{
		// An Ubuntu VM whose Agent runs an older Release: only Update
		// Agent is allowed.
		vmid: 101, name: "homeassist", port: 17453, release: "v0.0.1",
		seed: agentfake.HostSeed{Hostname: "homeassist", OS: "ubuntu", SSHInstalled: true, SSHRunning: true, PasswordAuth: true},
	},
}

// HostAgent is a fake Host Agent the lab serves.
type HostAgent struct {
	*agentfake.Host
	// VMID and Name are its Workload's in the proxmox-api baseline.
	VMID uint32
	Name string
	// URL is where it answers, https.
	URL string
}

// Lab is the faked homelab, serving until Close.
type Lab struct {
	servers []*http.Server
	config  map[string]proto.Message
	// Addrs maps each Component to the address of the system it talks to.
	Addrs    map[string]string
	handlers map[string]http.Handler
	// DockerAgents are the lab's fake Docker Agents, for ddash to add.
	DockerAgents []DockerAgent
	// HostAgents are the fake Host Agents, in VMID order of hostAgents.
	HostAgents []HostAgent
	cleanup    []func()
}

// startHostAgents serves the fake Host Agents on host. Each keeps its
// certificate and pairing under os.TempDir()/ddash-fakelab, so a ddash
// that paired with it before still can after a restart.
func (l *Lab) startHostAgents(host string) error {
	for _, a := range hostAgents {
		state := stateDir(fmt.Sprintf("host-agent-%d", a.vmid))
		fake, err := agentfake.NewHost("", a.seed, agentfake.Options{StateDir: state, Release: a.release, StepDelay: 1500 * time.Millisecond})
		if err != nil {
			return err
		}
		root := fake.Root
		l.cleanup = append(l.cleanup, func() { os.RemoveAll(root) })
		u, stop, err := serveAgent(fake.Serve, host, a.port)
		if err != nil {
			return err
		}
		l.cleanup = append(l.cleanup, stop)
		l.HostAgents = append(l.HostAgents, HostAgent{Host: fake, VMID: a.vmid, Name: a.name, URL: u})
	}
	return nil
}

// Handler is what serves the HTTP system component talks to, e.g. a
// *pfsensefake.Firewall for "pfsense-api", so a test can read a stateful
// fake's end state; nil for a component with none.
func (l *Lab) Handler(component string) http.Handler { return l.handlers[component] }

// Start serves every system's baseline from the repo at root, each on a
// free port of host (e.g. "127.0.0.1"). Each scenario named replaces the
// baseline of the systems that have it (files it lacks still come from
// baseline); a scenario no system has is an error.
func Start(root, host string, scenarios ...string) (*Lab, error) {
	dirs, err := scenarioDirs(root, scenarios)
	if err != nil {
		return nil, err
	}
	l := &Lab{config: map[string]proto.Message{}, Addrs: map[string]string{}, handlers: map[string]http.Handler{}}
	for component, config := range logicConfigs {
		l.config[component] = config()
	}
	for i, s := range systems {
		handler := fixture.Scenario(dirs[i], nil)
		if s.serve != nil {
			if handler, err = s.serve(dirs[i]); err != nil {
				l.Close()
				return nil, err
			}
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			l.Close()
			return nil, err
		}
		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go srv.Serve(ln) //nolint:errcheck // ends with Close
		l.servers = append(l.servers, srv)
		l.handlers[s.component] = handler
		addr := ln.Addr().String()
		l.Addrs[s.component] = addr
		l.config[s.component] = s.config(addr)
	}
	if err := l.startDocker(host, scenarios); err != nil {
		l.Close()
		return nil, err
	}
	if fw, ok := l.handlers["pfsense-api"].(*pfsensefake.Firewall); ok {
		fw.EditHostOverrides(movedTo(host))
	}
	if err := l.startHostAgents(host); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// recordedReverseProxy is the Reverse Proxy's address in the recordings.
const recordedReverseProxy = "198.18.1.210"

// movedTo points the Host Overrides at the recorded Reverse Proxy's
// address to host, where the lab's Nginx Proxy Manager answers: Apps
// matches the Reverse Proxy's Host Override by that address.
func movedTo(host string) func([]pfsensefake.HostOverride) []pfsensefake.HostOverride {
	return func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			for j, ip := range all[i].IPs {
				if ip == recordedReverseProxy {
					all[i].IPs[j] = host
				}
			}
		}
		return all
	}
}

// Scenarios splits a -scenario flag's comma-separated names.
func Scenarios(flag string) []string {
	var out []string
	for name := range strings.SplitSeq(flag, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// scenarioDirs picks each system's fixtures: the last named scenario it
// has, else its baseline.
func scenarioDirs(root string, scenarios []string) ([]string, error) {
	var baselines []string
	for _, s := range systems {
		baselines = append(baselines, filepath.Join(root, s.fixtures))
	}
	dirs := slices.Clone(baselines)
	for _, name := range scenarios {
		found := false
		for i, baseline := range baselines {
			dir := filepath.Join(filepath.Dir(baseline), name)
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				dirs[i], found = dir, true
			}
		}
		if !found {
			return nil, fmt.Errorf("fakelab: no system has a scenario %q", name)
		}
	}
	return dirs, nil
}

// Config returns, by Component, the config pointing it at the lab.
func (l *Lab) Config() map[string]proto.Message { return l.config }

// Close stops serving.
func (l *Lab) Close() {
	for _, srv := range l.servers {
		srv.Close()
	}
	for _, f := range l.cleanup {
		f()
	}
}
