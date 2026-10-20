// Package proxmoxapi is the proxmox-api Component: it reads the Workloads
// (LXCs and VMs) of a Proxmox VE node with an API token, through
// go-proxmox.
package proxmoxapi

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one request, so a hung node can't hold a getter
// past the host's deadline.
const requestTimeout = 5 * time.Second

// Definition is proxmox-api, ready for sdk.Serve.
func Definition() sdk.Definition { return define(nil) }

// define is proxmox-api sending its requests through rt, or over the
// network to the configured URL when rt is nil.
func define(rt http.RoundTripper) sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.ProxmoxApiConfig]{
		Manifest: manifest,
		Service:  &pb.ProxmoxApiService_ServiceDesc,
		New:      func() sdk.Component[*pb.ProxmoxApiConfig] { return &component{rt: rt} },
	})
}

type component struct {
	pb.UnimplementedProxmoxApiServiceServer
	rt          http.RoundTripper
	client      *proxmox.Client
	workloads   *sdk.Cache[*pb.ListWorkloadsResponse]
	permissions *sdk.Cache[permissions]
}

// rootCAs verifies the node's certificate; nil means the system's roots.
var rootCAs *x509.CertPool

func (c *component) Start(ctx context.Context, _ *sdk.Env, cfg *pb.ProxmoxApiConfig) error {
	if err := validate(cfg); err != nil {
		return err
	}
	base := strings.TrimSuffix(strings.TrimRight(cfg.GetUrl(), "/"), "/api2/json") + "/api2/json"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Dial the address, verify the certificate as the server name: so
	// neither DNS nor nginx sits between ddash and Proxmox.
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.GetTlsServerName(),
		RootCAs:    rootCAs,
	}
	context.AfterFunc(ctx, transport.CloseIdleConnections)
	var rt http.RoundTripper = transport
	if c.rt != nil {
		rt = c.rt
	}
	c.client = proxmox.NewClient(base,
		proxmox.WithHTTPClient(&http.Client{Timeout: requestTimeout, Transport: rt}),
		proxmox.WithAPIToken(cfg.GetTokenId(), cfg.GetTokenSecret()),
	)
	c.workloads = sdk.Cached(sdk.TTLFast, c.fetch)
	c.permissions = sdk.Cached(sdk.TTLFast, c.fetchPermissions)
	return nil
}

// validate checks what the host's required and format rules don't. An
// http URL is allowed for fakelab; a real node only serves https.
func validate(cfg *pb.ProxmoxApiConfig) error {
	var errs sdk.FieldErrors
	if u, err := url.Parse(cfg.GetUrl()); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs.Add("url", "must be the API's URL, like https://198.18.1.2:8006")
	}
	user, name, ok := strings.Cut(cfg.GetTokenId(), "!")
	if !ok || name == "" || !strings.Contains(user, "@") {
		errs.Add("token_id", "must be <user>@<realm>!<token name>, like ddash@pve!dashboard")
	}
	return errs.Err()
}

func (c *component) ListWorkloads(ctx context.Context, _ *pb.ListWorkloadsRequest) (*pb.ListWorkloadsResponse, error) {
	return c.workloads.Get(ctx)
}

// ListPermissions resolves each Workload's privileges from the token's
// effective permissions. /access/permissions lists /vms and every path
// with an ACL (pool members as /vms/<vmid> too), each already resolved for
// inheritance, so a Workload's privileges are those of the nearest of
// /vms/<vmid>, /vms and /: on its own path all of them, from a parent only
// those that propagate.
func (c *component) ListPermissions(ctx context.Context, _ *pb.ListPermissionsRequest) (*pb.ListPermissionsResponse, error) {
	workloads, err := c.workloads.Get(ctx)
	if err != nil {
		return nil, err
	}
	perms, err := c.permissions.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListPermissionsResponse{}
	for _, w := range workloads.GetWorkloads() {
		wp := &pb.WorkloadPermissions{Vmid: w.GetVmid()}
		for i, path := range []string{fmt.Sprintf("/vms/%d", w.GetVmid()), "/vms", "/"} {
			if privs, ok := perms[path]; ok {
				for p, propagate := range privs {
					if i == 0 || propagate == 1 {
						wp.Privileges = append(wp.Privileges, p)
					}
				}
				slices.Sort(wp.Privileges)
				break
			}
		}
		out.Workloads = append(out.Workloads, wp)
	}
	return out, nil
}

// permissions maps an ACL path to the privileges held on it. The value is
// 1 when the privilege propagates to child paths, 0 when not; held either
// way.
type permissions map[string]map[string]int

func (c *component) fetchPermissions(ctx context.Context) (permissions, error) {
	var perms permissions
	if err := c.client.Get(ctx, "/access/permissions", &perms); err != nil {
		return nil, unavailable(err)
	}
	return perms, nil
}

// unavailable is a failed read of the Proxmox API as a gRPC error.
func unavailable(err error) error {
	if proxmox.IsNotAuthorized(err) {
		return status.Error(codes.Unavailable, "Proxmox refused the API token")
	}
	return status.Error(codes.Unavailable, err.Error())
}

// Health is ok while the API answers with the token.
func (c *component) Health(ctx context.Context) sdk.Health {
	if _, err := c.workloads.Get(ctx); err != nil {
		return sdk.Health{State: sdk.Down, Message: status.Convert(err).Message()}
	}
	return sdk.Health{State: sdk.OK}
}

// fetch lists every Workload the token may audit, by VMID.
func (c *component) fetch(ctx context.Context) (*pb.ListWorkloadsResponse, error) {
	resources, err := (&proxmox.Cluster{}).New(c.client).Resources(ctx, "vm")
	if err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListWorkloadsResponse{}
	for _, r := range resources {
		out.Workloads = append(out.Workloads, toWorkload(r))
	}
	slices.SortFunc(out.Workloads, func(a, b *pb.Workload) int { return cmp.Compare(a.GetVmid(), b.GetVmid()) })
	var g errgroup.Group
	g.SetLimit(addressReaders)
	for _, w := range out.Workloads {
		g.Go(func() error {
			c.readAddresses(ctx, w)
			return nil
		})
	}
	_ = g.Wait()
	return out, nil
}

// addressReaders is how many Workloads' addresses are read at once.
const addressReaders = 4

// readAddresses fills w's nics from the running guest.
func (c *component) readAddresses(ctx context.Context, w *pb.Workload) {
	if w.GetStatus() == "running" {
		var nics []*pb.Nic
		var err error
		switch w.GetKind() {
		case pb.Kind_KIND_LXC:
			nics, err = c.lxcInterfaces(ctx, w)
		case pb.Kind_KIND_VM:
			nics, err = c.agentInterfaces(ctx, w)
		}
		if err == nil {
			w.Nics, w.Addresses = nics, pb.AddressSource_ADDRESS_SOURCE_LIVE
			return
		}
	}
	nics, err := c.configNics(ctx, w)
	if err != nil {
		w.AddressesError = status.Convert(unavailable(err)).Message()
		return
	}
	w.Nics, w.Addresses = nics, pb.AddressSource_ADDRESS_SOURCE_CONFIG
}

// configNics reads a Workload's nics from its config: each netN's MAC,
// and its static address if it has one (an LXC's ip/ip6 in netN, a VM's
// cloud-init ipconfigN).
func (c *component) configNics(ctx context.Context, w *pb.Workload) ([]*pb.Nic, error) {
	kind := "lxc"
	if w.GetKind() == pb.Kind_KIND_VM {
		kind = "qemu"
	}
	var config map[string]any
	if err := c.client.Get(ctx, fmt.Sprintf("/nodes/%s/%s/%d/config", w.GetNode(), kind, w.GetVmid()), &config); err != nil {
		return nil, err
	}
	var nics []*pb.Nic
	for n := range maxNets {
		spec, ok := config[fmt.Sprintf("net%d", n)].(string)
		if !ok {
			continue // netN may skip numbers
		}
		opts := options(spec)
		nic := &pb.Nic{Name: fmt.Sprintf("net%d", n)}
		if name := opts["name"]; name != "" {
			nic.Name = name
		}
		addressed := opts
		if kind == "qemu" {
			ipconfig, _ := config[fmt.Sprintf("ipconfig%d", n)].(string)
			addressed = options(ipconfig)
		}
		// An LXC's is hwaddr=<MAC>, a VM's <model>=<MAC>.
		for _, value := range opts {
			if hw, err := net.ParseMAC(value); err == nil {
				nic.Mac = hw.String()
			}
		}
		for _, key := range []string{"ip", "ip6"} {
			if p, err := netip.ParsePrefix(addressed[key]); err == nil {
				nic.Ips = append(nic.Ips, p.Addr().String())
			}
		}
		nics = append(nics, nic)
	}
	return nics, nil
}

// maxNets is how many netN a guest config can have: Proxmox allows net0
// to net31.
const maxNets = 32

// options splits Proxmox's "key=value,key=value" property string.
func options(s string) map[string]string {
	opts := map[string]string{}
	for part := range strings.SplitSeq(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			opts[k] = v
		}
	}
	return opts
}

// agentInterfaces asks a VM's QEMU guest agent. It fails when the VM has
// no agent configured or the agent isn't running.
func (c *component) agentInterfaces(ctx context.Context, w *pb.Workload) ([]*pb.Nic, error) {
	vm := &proxmox.VirtualMachine{}
	vm.New(c.client, w.GetNode(), int(w.GetVmid()))
	ifaces, err := vm.AgentGetNetworkIFaces(ctx)
	if err != nil {
		return nil, err
	}
	var nics []*pb.Nic
	for _, i := range ifaces {
		var ips []string
		for _, a := range i.IPAddresses {
			ips = append(ips, a.IPAddress)
		}
		if nic := toNic(i.Name, i.HardwareAddress, ips); nic != nil {
			nics = append(nics, nic)
		}
	}
	return nics, nil
}

// lxcInterface is one entry of /nodes/{node}/lxc/{vmid}/interfaces. It is
// read here rather than through go-proxmox, whose ContainerInterface keeps
// only the deprecated inet/inet6 fields: one address per family.
type lxcInterface struct {
	Name        string `json:"name"`
	HWAddr      string `json:"hwaddr"`
	IPAddresses []struct {
		IPAddress string `json:"ip-address"`
	} `json:"ip-addresses"`
}

func (c *component) lxcInterfaces(ctx context.Context, w *pb.Workload) ([]*pb.Nic, error) {
	var ifaces []lxcInterface
	if err := c.client.Get(ctx, fmt.Sprintf("/nodes/%s/lxc/%d/interfaces", w.GetNode(), w.GetVmid()), &ifaces); err != nil {
		return nil, err
	}
	var nics []*pb.Nic
	for _, i := range ifaces {
		var ips []string
		for _, a := range i.IPAddresses {
			ips = append(ips, a.IPAddress)
		}
		if nic := toNic(i.Name, i.HWAddr, ips); nic != nil {
			nics = append(nics, nic)
		}
	}
	return nics, nil
}

// toNic keeps the routable addresses of an interface, and none of a
// loopback one: Linux's lo, or any interface holding a loopback address
// (FreeBSD's lo0, which may carry a global address too).
func toNic(name, mac string, ips []string) *pb.Nic {
	if name == "lo" {
		return nil
	}
	nic := &pb.Nic{Name: name, Mac: normalMAC(mac)}
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		if a.IsLoopback() {
			return nil
		}
		if a.IsGlobalUnicast() {
			nic.Ips = append(nic.Ips, a.String())
		}
	}
	return nic
}

// normalMAC writes a MAC in lower case, as leases do.
func normalMAC(s string) string {
	if hw, err := net.ParseMAC(s); err == nil {
		return hw.String()
	}
	return strings.ToLower(s)
}

func toWorkload(r *proxmox.ClusterResource) *pb.Workload {
	w := &pb.Workload{
		Vmid:          uint32(r.VMID),
		Node:          r.Node,
		Name:          r.Name,
		Status:        r.Status,
		Template:      r.Template == 1,
		Tags:          tags(r.Tags),
		Cpu:           r.CPU,
		MaxCpu:        uint32(r.MaxCPU),
		Mem:           r.Mem,
		MaxMem:        r.MaxMem,
		Disk:          r.Disk,
		MaxDisk:       r.MaxDisk,
		NetIn:         r.NetIn,
		NetOut:        r.NetOut,
		UptimeSeconds: r.Uptime,
	}
	switch r.Type {
	case "lxc":
		w.Kind = pb.Kind_KIND_LXC
	case "qemu":
		w.Kind = pb.Kind_KIND_VM
	}
	return w
}

// tags splits Proxmox's tag list, which it separates with ";" (older
// releases also with "," or spaces).
func tags(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == ' ' })
}
