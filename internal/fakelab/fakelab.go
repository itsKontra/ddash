// Package fakelab is the homelab faked on localhost: each external system
// serves its Component's recorded baseline fixtures on a port of its own,
// and Config says how to point every Component at it. `make dev` and the
// subprocess test run the core against it; cmd/fakelab serves it alone.
//
// Docker comes first; each later system adds itself to systems.
package fakelab

import (
	"net"
	"net/http"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
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
		component: "proxmox-api",
		fixtures:  "components/proxmox-api/testdata/baseline",
		config: func(addr string) proto.Message {
			// Plain http: the lab has no certificate a Component trusts.
			return &proxmoxapipb.ProxmoxApiConfig{Url: "http://" + addr, TokenId: "ddash@pve!fakelab", TokenSecret: "fakelab"}
		},
	},
}

// Lab is the faked homelab, serving until Close.
type Lab struct {
	servers []*http.Server
	config  map[string]proto.Message
	// Addrs maps each Component to the address of the system it talks to.
	Addrs map[string]string
}

// Start serves every system's baseline from the repo at root, each on a
// free port of host (e.g. "127.0.0.1").
func Start(root, host string) (*Lab, error) {
	l := &Lab{config: map[string]proto.Message{}, Addrs: map[string]string{}}
	for _, s := range systems {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			l.Close()
			return nil, err
		}
		srv := &http.Server{
			Handler:           fixture.Scenario(filepath.Join(root, s.fixtures), nil),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go srv.Serve(ln) //nolint:errcheck // ends with Close
		l.servers = append(l.servers, srv)
		addr := ln.Addr().String()
		l.Addrs[s.component] = addr
		l.config[s.component] = s.config(addr)
	}
	return l, nil
}

// Config returns, by Component, the config pointing it at the lab.
func (l *Lab) Config() map[string]proto.Message { return l.config }

// Close stops serving.
func (l *Lab) Close() {
	for _, srv := range l.servers {
		srv.Close()
	}
}
