// Package dockerapi is the docker-api Component: it lists the containers on
// each configured Docker host, read through that host's read-only
// linuxserver/socket-proxy with the official Docker Go client, and on each
// host with a paired Docker Agent, read through docker-agent-api. A host
// with an Agent is read only through it: a socket-proxy host of the same
// name is dropped.
package dockerapi

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/components/internal/dockerread"
	containerpb "github.com/itsKontra/ddash/proto/ddash/container"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// apiVersion is pinned, so no request is spent negotiating it. Docker 25
// and later serve it.
const apiVersion = "1.44"

// hostTimeout bounds one host's read, so one dead host can't hold a getter
// past the host's 10 s deadline.
const hostTimeout = 5 * time.Second

// Definition is docker-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*dockerapipb.DockerApiConfig]{
		Manifest: manifest,
		Service:  &dockerapipb.DockerApiService_ServiceDesc,
		New:      func() sdk.Component[*dockerapipb.DockerApiConfig] { return &component{} },
	})
}

type component struct {
	dockerapipb.UnimplementedDockerApiServiceServer
	hosts      []host
	containers *sdk.Cache[snapshot]
	agents     dockeragentapipb.DockerAgentApiServiceClient
	log        *slog.Logger
}

type host struct {
	name string
	// address is the socket-proxy URL's host, e.g. "198.18.2.10".
	address string
	client  *client.Client
}

// snapshot is one read of every host.
type snapshot struct {
	resp *dockerapipb.ListContainersResponse
	// problems has one line per host that couldn't be read.
	problems []string
}

func (c *component) Start(ctx context.Context, env *sdk.Env, cfg *dockerapipb.DockerApiConfig) error {
	if err := validate(cfg); err != nil {
		return err
	}
	c.agents = sdk.Require(env, dockeragentapipb.NewDockerAgentApiServiceClient)
	c.log = env.Log
	for _, h := range cfg.GetHosts() {
		cli, err := client.New(client.WithHost(h.GetUrl()), client.WithAPIVersion(apiVersion))
		if err != nil {
			return fmt.Errorf("host %s: %w", h.GetName(), err)
		}
		u, _ := url.Parse(h.GetUrl()) // validate parsed it
		c.hosts = append(c.hosts, host{name: h.GetName(), address: u.Hostname(), client: cli})
	}
	context.AfterFunc(ctx, func() {
		for _, h := range c.hosts {
			h.client.Close()
		}
	})
	c.containers = sdk.Cached(sdk.TTLFast, c.fetch)
	return nil
}

func validate(cfg *dockerapipb.DockerApiConfig) error {
	var errs sdk.FieldErrors
	names := map[string]bool{}
	for i, h := range cfg.GetHosts() {
		switch {
		case h.GetName() == "":
			errs.Add(fmt.Sprintf("hosts[%d].name", i), "is required")
		case names[h.GetName()]:
			errs.Add(fmt.Sprintf("hosts[%d].name", i), "is already used by another host")
		}
		names[h.GetName()] = true
		if u, err := url.Parse(h.GetUrl()); err != nil || u.Scheme != "tcp" || u.Host == "" {
			errs.Add(fmt.Sprintf("hosts[%d].url", i), "must be a socket-proxy URL like tcp://198.18.2.10:2375")
		}
	}
	return errs.Err()
}

func (c *component) ListContainers(ctx context.Context, _ *dockerapipb.ListContainersRequest) (*dockerapipb.ListContainersResponse, error) {
	s, err := c.containers.Get(ctx)
	return s.resp, err
}

// Health is down when no host can be read, and degraded when some can't.
func (c *component) Health(ctx context.Context) sdk.Health {
	s, err := c.containers.Get(ctx)
	if err != nil {
		return sdk.Health{State: sdk.Down, Message: err.Error()}
	}
	switch {
	case len(s.problems) == 0:
		return sdk.Health{State: sdk.OK}
	case len(s.problems) == len(c.hosts):
		return sdk.Health{State: sdk.Down, Message: strings.Join(s.problems, "; ")}
	default:
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(s.problems, "; ")}
	}
}

// fetch reads every host in parallel. A host that fails is listed with its
// error, so fetch itself never fails.
func (c *component) fetch(ctx context.Context) (snapshot, error) {
	hosts := make([]*dockerapipb.HostContainers, len(c.hosts))
	errs := make([]error, len(c.hosts))
	var wg sync.WaitGroup
	for i, h := range c.hosts {
		wg.Go(func() { hosts[i], errs[i] = h.list(ctx) })
	}
	var agentHosts []*dockerapipb.HostContainers
	wg.Go(func() { agentHosts = c.agentHosts(ctx) })
	wg.Wait()
	s := snapshot{resp: &dockerapipb.ListContainersResponse{}}
	byAgent := map[string]bool{}
	for _, h := range agentHosts {
		byAgent[h.GetHost()] = true
	}
	for _, h := range hosts {
		if !byAgent[h.GetHost()] {
			s.resp.Hosts = append(s.resp.Hosts, h)
		}
	}
	s.resp.Hosts = append(s.resp.Hosts, agentHosts...)
	for i, err := range errs {
		if byAgent[c.hosts[i].name] {
			continue
		}
		switch {
		case err == nil:
		case client.IsErrConnectionFailed(err) || errors.Is(err, context.DeadlineExceeded):
			s.problems = append(s.problems, c.hosts[i].name+" unreachable: "+err.Error())
		default:
			s.problems = append(s.problems, c.hosts[i].name+": "+err.Error())
		}
	}
	return s, nil
}

// agentHosts reads the hosts with a paired Docker Agent. docker-agent-api
// failing (or not running) leaves them out: the socket-proxy hosts still
// list.
func (c *component) agentHosts(ctx context.Context) []*dockerapipb.HostContainers {
	ctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	res, err := c.agents.ListContainers(ctx, &dockeragentapipb.ListContainersRequest{})
	if err != nil {
		c.log.Warn("can't read the Docker Agents", "err", err)
		return nil
	}
	var out []*dockerapipb.HostContainers
	for _, h := range res.GetHosts() {
		hc := &dockerapipb.HostContainers{Host: h.GetHost(), Address: h.GetAddress(), Error: h.GetError()}
		// The same message: what docker-api doesn't read itself (the
		// image's digests, the labels) comes along.
		hc.Containers = append(hc.Containers, h.GetContainers()...)
		out = append(out, hc)
	}
	return out
}

// list reads one host's containers. On error the host is listed with it.
func (h host) list(ctx context.Context) (*dockerapipb.HostContainers, error) {
	ctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	out := &dockerapipb.HostContainers{Host: h.name, Address: h.address}
	res, err := h.client.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		out.Error = err.Error()
		return out, err
	}
	for _, s := range res.Items {
		out.Containers = append(out.Containers, toContainer(s))
	}
	return out, nil
}

func toContainer(s container.Summary) *containerpb.Container {
	c := &containerpb.Container{
		Id:        s.ID,
		Image:     s.Image,
		State:     dockerread.State(string(s.State)),
		Status:    s.Status,
		Health:    dockerread.Health(s.Status),
		Protected: dockerread.Protected(s.Labels),
	}
	if len(s.Names) > 0 {
		c.Name = strings.TrimPrefix(s.Names[0], "/")
	}
	for _, p := range s.Ports {
		if p.PublicPort == 0 {
			continue // exposed, not published
		}
		port := &containerpb.Port{PublicPort: uint32(p.PublicPort), PrivatePort: uint32(p.PrivatePort), Protocol: dockerread.Protocol(p.Type)}
		if p.IP.IsValid() {
			port.Ip = p.IP.String()
		}
		c.Ports = append(c.Ports, port)
	}
	return c
}
