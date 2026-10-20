// Package dockerapi is the docker-api Component: it lists the containers on
// each configured Docker host, read through that host's read-only
// linuxserver/socket-proxy with the official Docker Go client.
package dockerapi

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

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
}

type host struct {
	name   string
	client *client.Client
}

// snapshot is one read of every host.
type snapshot struct {
	resp *dockerapipb.ListContainersResponse
	// problems has one line per host that couldn't be read.
	problems []string
}

func (c *component) Start(ctx context.Context, _ *sdk.Env, cfg *dockerapipb.DockerApiConfig) error {
	if err := validate(cfg); err != nil {
		return err
	}
	for _, h := range cfg.GetHosts() {
		cli, err := client.New(client.WithHost(h.GetUrl()), client.WithAPIVersion(apiVersion))
		if err != nil {
			return fmt.Errorf("host %s: %w", h.GetName(), err)
		}
		c.hosts = append(c.hosts, host{name: h.GetName(), client: cli})
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
	wg.Wait()
	s := snapshot{resp: &dockerapipb.ListContainersResponse{Hosts: hosts}}
	for i, err := range errs {
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

// list reads one host's containers. On error the host is listed with it.
func (h host) list(ctx context.Context) (*dockerapipb.HostContainers, error) {
	ctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	out := &dockerapipb.HostContainers{Host: h.name}
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

func toContainer(s container.Summary) *dockerapipb.Container {
	c := &dockerapipb.Container{
		Id:     s.ID,
		Image:  s.Image,
		State:  string(s.State),
		Status: s.Status,
	}
	if len(s.Names) > 0 {
		c.Name = strings.TrimPrefix(s.Names[0], "/")
	}
	for _, p := range s.Ports {
		if p.PublicPort == 0 {
			continue // exposed, not published
		}
		port := &dockerapipb.Port{PublicPort: uint32(p.PublicPort), PrivatePort: uint32(p.PrivatePort), Protocol: p.Type}
		if p.IP.IsValid() {
			port.Ip = p.IP.String()
		}
		c.Ports = append(c.Ports, port)
	}
	return c
}
