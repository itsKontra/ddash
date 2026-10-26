// Package fakelab is the homelab faked on localhost: each external system
// serves its Component's recorded baseline fixtures on a port of its own,
// and Config says how to point every Component at it. `make dev` and the
// subprocess test run the core against it; cmd/fakelab serves it alone.
//
// Docker comes first; each later system adds itself to systems. A system whose Actions must stick is served by a
// stateful fake over its fixtures (pfSense: pfsensefake; AdGuard:
// adguardfake; Proxmox: proxmoxfake; Nginx Proxy Manager: npmfake). A
// named scenario (e.g. "vpn-on-fallback") replaces the baseline of every
// system that has a sibling directory of that name.
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

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
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
}

// logicConfigs is the config of Components that reach no system
// themselves but must name what the lab's fixtures hold (an alias, a
// range), by Component.
var logicConfigs = map[string]func() proto.Message{
	"network": func() proto.Message {
		return &networkpb.NetworkConfig{BypassAlias: "VPN_BYPASS", ReservedRange: "198.18.1.150-198.18.1.199"}
	},
}

// Lab is the faked homelab, serving until Close.
type Lab struct {
	servers []*http.Server
	config  map[string]proto.Message
	// Addrs maps each Component to the address of the system it talks to.
	Addrs    map[string]string
	handlers map[string]http.Handler
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
	if fw, ok := l.handlers["pfsense-api"].(*pfsensefake.Firewall); ok {
		fw.EditHostOverrides(movedTo(host))
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
}
