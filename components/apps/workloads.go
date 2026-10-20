package apps

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// hostPort is where an App's traffic goes: a proxy_pass target or a manual
// App's URL. The zero value is nowhere ddash can match.
type hostPort struct {
	host string // canonical: see canonHost
	port int
}

// canonHost makes addresses comparable: an IP in its shortest form (IPv4
// for an IPv4-mapped address), anything else in lower case.
func canonHost(h string) string {
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap().String()
	}
	return strings.ToLower(h)
}

// upstreamOf reads where a proxy_pass forwards: its host and port, or an
// upstream block's first server when it names one. Variables and unix
// sockets go nowhere ddash can match.
func upstreamOf(proxyPass string, upstreams map[string][]string) hostPort {
	if strings.Contains(proxyPass, "$") {
		return hostPort{}
	}
	scheme, rest, ok := strings.Cut(proxyPass, "://")
	if !ok || strings.HasPrefix(rest, "unix:") {
		return hostPort{}
	}
	u, err := url.Parse(proxyPass)
	if err != nil || u.Hostname() == "" {
		return hostPort{}
	}
	if u.Port() == "" {
		if servers, named := upstreams[u.Hostname()]; named {
			if len(servers) == 0 {
				return hostPort{}
			}
			return serverAddr(servers[0])
		}
	}
	return withPort(u.Hostname(), u.Port(), scheme)
}

// serverAddr reads an upstream server's address ("198.18.2.20:3000",
// "198.18.2.20" for port 80).
func serverAddr(addr string) hostPort {
	if strings.HasPrefix(addr, "unix:") {
		return hostPort{}
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return withPort(host, port, "http")
	}
	return withPort(addr, "", "http")
}

// urlTarget reads a manual App's URL host and port.
func urlTarget(raw string) hostPort {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return hostPort{}
	}
	return withPort(u.Hostname(), u.Port(), u.Scheme)
}

func withPort(host, port, scheme string) hostPort {
	p := 80
	if scheme == "https" {
		p = 443
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil {
			return hostPort{}
		}
		p = n
	}
	return hostPort{host: canonHost(host), port: p}
}

// sources is one read of the Workload sources. A source that couldn't be
// read lists nothing, and problems says why, naming it.
type sources struct {
	docker   []*dockerapipb.HostContainers
	guests   []*proxmoxapipb.Workload
	problems []string
}

// readSources reads proxmox-api and docker-api.
func (c *component) readSources(ctx context.Context) sources {
	var s sources
	if resp, err := c.proxmox.ListWorkloads(ctx, &proxmoxapipb.ListWorkloadsRequest{}); err != nil {
		s.problems = append(s.problems, sourceProblem("proxmox-api", err))
	} else {
		for _, g := range resp.GetWorkloads() {
			if !g.GetTemplate() {
				s.guests = append(s.guests, g)
			}
		}
	}
	if resp, err := c.docker.ListContainers(ctx, &dockerapipb.ListContainersRequest{}); err != nil {
		s.problems = append(s.problems, sourceProblem("docker-api", err))
	} else {
		for _, h := range resp.GetHosts() {
			if h.GetError() != "" {
				s.problems = append(s.problems, "docker-api can't read "+h.GetHost()+": "+h.GetError())
				continue
			}
			s.docker = append(s.docker, h)
		}
	}
	return s
}

func sourceProblem(name string, err error) string {
	st := status.Convert(err)
	if st.Code() == codes.FailedPrecondition && st.Message() == "not configured" {
		return name + " not configured"
	}
	return name + " unavailable: " + st.Message()
}

// workloadFor is an App's Workload: the override when there is one, else
// what to matches.
func (s sources) workloadFor(override *workloadpb.WorkloadRef, to hostPort) *appspb.Workload {
	if override != nil {
		return s.resolve(override)
	}
	return s.match(to)
}

// match finds the Workload at to: first a container publishing its port on
// a Docker host at its address (a Docker host is often itself a Proxmox
// guest, so this goes first), then a Proxmox guest with its address.
func (s sources) match(to hostPort) *appspb.Workload {
	if to.host == "" {
		return nil
	}
	for _, h := range s.docker {
		for _, c := range h.GetContainers() {
			if publishes(h, c, to) {
				return containerWorkload(h, c)
			}
		}
	}
	for _, g := range s.guests {
		for _, nic := range g.GetNics() {
			for _, ip := range nic.GetIps() {
				if canonHost(ip) == to.host {
					return guestWorkload(g)
				}
			}
		}
	}
	return nil
}

// publishes reports whether c answers on to: a tcp port published on to's
// port, bound to to's address or, unbound, on its host's address.
func publishes(h *dockerapipb.HostContainers, c *dockerapipb.Container, to hostPort) bool {
	for _, p := range c.GetPorts() {
		if int(p.GetPublicPort()) != to.port || (p.GetProtocol() != "" && p.GetProtocol() != "tcp") {
			continue
		}
		bound := canonHost(p.GetIp())
		if a, err := netip.ParseAddr(bound); bound == "" || (err == nil && a.IsUnspecified()) {
			bound = canonHost(h.GetAddress())
		}
		if bound == to.host {
			return true
		}
	}
	return false
}

// resolve fills in an override's name and state from the sources. One no
// source lists keeps its reference alone.
func (s sources) resolve(ref *workloadpb.WorkloadRef) *appspb.Workload {
	switch ref.GetKind() {
	case workloadpb.Kind_KIND_DOCKER:
		for _, h := range s.docker {
			for _, c := range h.GetContainers() {
				if h.GetHost() == ref.GetHost() && c.GetName() == ref.GetId() {
					return overridden(containerWorkload(h, c))
				}
			}
		}
	case workloadpb.Kind_KIND_LXC, workloadpb.Kind_KIND_VM:
		// VMIDs are unique in a cluster; the node changes on a migration.
		for _, g := range s.guests {
			if kindOf(g) == ref.GetKind() && strconv.FormatUint(uint64(g.GetVmid()), 10) == ref.GetId() {
				return overridden(guestWorkload(g))
			}
		}
	case workloadpb.Kind_KIND_EXTERNAL:
		return &appspb.Workload{Ref: ref, Name: ref.GetHost(), Override: true}
	}
	return &appspb.Workload{Ref: ref, Override: true}
}

func overridden(w *appspb.Workload) *appspb.Workload {
	w.Override = true
	return w
}

// all is every Workload the sources list: containers by host, then guests.
func (s sources) all() []*appspb.Workload {
	var out []*appspb.Workload
	for _, h := range s.docker {
		for _, c := range h.GetContainers() {
			out = append(out, containerWorkload(h, c))
		}
	}
	for _, g := range s.guests {
		out = append(out, guestWorkload(g))
	}
	return out
}

func containerWorkload(h *dockerapipb.HostContainers, c *dockerapipb.Container) *appspb.Workload {
	return &appspb.Workload{
		Ref:   &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: h.GetHost(), Id: c.GetName()},
		Name:  c.GetName(),
		State: c.GetState(),
	}
}

func guestWorkload(g *proxmoxapipb.Workload) *appspb.Workload {
	return &appspb.Workload{
		Ref:   &workloadpb.WorkloadRef{Kind: kindOf(g), Host: g.GetNode(), Id: strconv.FormatUint(uint64(g.GetVmid()), 10)},
		Name:  g.GetName(),
		State: g.GetStatus(),
	}
}

func kindOf(g *proxmoxapipb.Workload) workloadpb.Kind {
	if g.GetKind() == proxmoxapipb.Kind_KIND_VM {
		return workloadpb.Kind_KIND_VM
	}
	return workloadpb.Kind_KIND_LXC
}

// checkWorkloadRef checks a Workload override the Operator gave.
func checkWorkloadRef(r *workloadpb.WorkloadRef) (*workloadpb.WorkloadRef, error) {
	if r == nil {
		return nil, nil
	}
	r = &workloadpb.WorkloadRef{Kind: r.GetKind(), Host: strings.TrimSpace(r.GetHost()), Id: strings.TrimSpace(r.GetId())}
	switch r.GetKind() {
	case workloadpb.Kind_KIND_UNSPECIFIED:
		return nil, nil
	case workloadpb.Kind_KIND_LXC, workloadpb.Kind_KIND_VM:
		if _, err := strconv.ParseUint(r.GetId(), 10, 32); err != nil {
			return nil, status.Error(codes.InvalidArgument, "a Proxmox guest's Workload id must be its VMID, e.g. 101")
		}
	case workloadpb.Kind_KIND_DOCKER:
		if r.GetHost() == "" || r.GetId() == "" {
			return nil, status.Error(codes.InvalidArgument, "a container's Workload needs its docker-api host and its name")
		}
	case workloadpb.Kind_KIND_EXTERNAL:
		if r.GetHost() == "" {
			return nil, status.Error(codes.InvalidArgument, "an external Workload needs its host, e.g. 198.18.1.50")
		}
		r.Id = ""
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown Workload kind %d", r.GetKind())
	}
	return r, nil
}

// upstreamsOf maps each upstream block's name to its servers.
func upstreamsOf(snap *nginxconfigpb.Snapshot) map[string][]string {
	out := map[string][]string{}
	for _, u := range snap.GetUpstreams() {
		if _, dup := out[u.GetName()]; !dup {
			out[u.GetName()] = u.GetServers()
		}
	}
	return out
}

func (c *component) ListWorkloads(ctx context.Context, _ *appspb.ListWorkloadsRequest) (*appspb.ListWorkloadsResponse, error) {
	return &appspb.ListWorkloadsResponse{Workloads: c.readSources(ctx).all()}, nil
}
