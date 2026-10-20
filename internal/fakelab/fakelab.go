// Package fakelab is the homelab faked on localhost: each external system
// serves its Component's recorded baseline fixtures on a port of its own,
// and Config says how to point every Component at it. `make dev` and the
// subprocess test run the core against it; cmd/fakelab serves it alone.
//
// Docker comes first; each later system adds itself to systems (HTTP) or
// sshSystems (SSH). A named scenario (e.g. "vpn-on-fallback") replaces the
// baseline of every system that has a sibling directory of that name.
package fakelab

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
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
		component: "pfsense-api",
		fixtures:  "components/pfsense-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// The fixtures don't check the key.
			return &pfsenseapipb.PfsenseApiConfig{Url: "http://" + addr, ApiKey: "fakelab"}
		},
	},
	{
		component: "proxmox-api",
		fixtures:  "components/proxmox-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// Plain http: the lab has no certificate a Component trusts.
			return &proxmoxapipb.ProxmoxApiConfig{Url: "http://" + addr, TokenId: "ddash@pve!fakelab", TokenSecret: "fakelab"}
		},
	},
}

// sshSystem is one faked host reached over SSH, answering commands from
// fixtures (fixture.SSH). It lets any client key in, so no setup hint
// needs pasting in `make dev`.
type sshSystem struct {
	component string
	fixtures  string
	// config points the Component at the host served on addr, whose
	// host key is hostKey ("ssh-ed25519 AAAA…").
	config func(addr, hostKey string) proto.Message
}

var sshSystems = []sshSystem{
	{
		component: "nginx-config",
		fixtures:  "components/nginx-config/testdata/baseline",
		config: func(addr, hostKey string) proto.Message {
			return &nginxconfigpb.NginxConfigConfig{Host: addr, HostKey: hostKey}
		},
	},
}

// Lab is the faked homelab, serving until Close.
type Lab struct {
	servers []*http.Server
	ssh     []*fixture.SSH
	config  map[string]proto.Message
	// Addrs maps each Component to the address of the system it talks to.
	Addrs map[string]string
}

// Start serves every system's baseline from the repo at root, each on a
// free port of host (e.g. "127.0.0.1"). Each scenario named replaces the
// baseline of the systems that have it (files it lacks still come from
// baseline); a scenario no system has is an error.
func Start(root, host string, scenarios ...string) (*Lab, error) {
	dirs, err := scenarioDirs(root, scenarios)
	if err != nil {
		return nil, err
	}
	l := &Lab{config: map[string]proto.Message{}, Addrs: map[string]string{}}
	for i, s := range systems {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			l.Close()
			return nil, err
		}
		srv := &http.Server{
			Handler:           fixture.Scenario(dirs[i], nil),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go srv.Serve(ln) //nolint:errcheck // ends with Close
		l.servers = append(l.servers, srv)
		addr := ln.Addr().String()
		l.Addrs[s.component] = addr
		l.config[s.component] = s.config(addr)
	}
	for i, s := range sshSystems {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			l.Close()
			return nil, err
		}
		srv, err := fixture.ServeSSH(ln, dirs[len(systems)+i], fixture.SSHOptions{HostKey: hostKey(s.component), AnyKey: true})
		if err != nil {
			ln.Close()
			l.Close()
			return nil, err
		}
		l.ssh = append(l.ssh, srv)
		l.Addrs[s.component] = srv.Addr()
		l.config[s.component] = s.config(srv.Addr(), srv.HostKey())
	}
	return l, nil
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

// scenarioDirs picks each system's fixtures, systems then sshSystems: the
// last named scenario it has, else its baseline.
func scenarioDirs(root string, scenarios []string) ([]string, error) {
	var baselines []string
	for _, s := range systems {
		baselines = append(baselines, filepath.Join(root, s.fixtures))
	}
	for _, s := range sshSystems {
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

// hostKey is a fixed key per faked host, so a config seeded by an earlier
// `make dev` still pins the right one.
func hostKey(component string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("fakelab host key " + component))
	return ed25519.NewKeyFromSeed(seed[:])
}

// Config returns, by Component, the config pointing it at the lab.
func (l *Lab) Config() map[string]proto.Message { return l.config }

// Close stops serving.
func (l *Lab) Close() {
	for _, srv := range l.servers {
		srv.Close()
	}
	for _, srv := range l.ssh {
		srv.Close()
	}
}
