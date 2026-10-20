package smoke

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"

	"google.golang.org/protobuf/proto"

	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// system is one system Component smoke runs.
type system struct {
	def sdk.Definition
	// service is the full name of its API.
	service string
	config  func() proto.Message
	// requests build the request of a getter that needs one, by method,
	// from the answers of the getters before it. Others get an empty one.
	requests map[string]func(answers map[string]proto.Message) (proto.Message, error)
	// checks are the invariants a getter's answer must hold, by method.
	// Every getter runs; one without checks only has to answer.
	checks map[string]func(proto.Message) error
	// proxy points cfg at a recording proxy listening on addr and returns
	// the real system's URL and the name its TLS certificate is checked
	// as. Nil: the Component reaches its system some other way.
	proxy func(cfg proto.Message, addr string) (upstream, tlsName string, err error)
	// capture runs what the Component runs over SSH, for the fixtures,
	// with the Component's DB at dbPath.
	capture func(ctx context.Context, dbPath string, cfg proto.Message) (command string, out fixture.Output, err error)
}

// A new system Component (one that talks to an external system) belongs
// here, with its invariants; TestEverySystemTheLabFakesWithGettersIsSmokeTested
// holds this list to fakelab's.
var systems = []system{
	{
		def:     dockerapi.Definition(),
		service: dockerapipb.DockerApiService_ServiceDesc.ServiceName,
		config:  func() proto.Message { return &dockerapipb.DockerApiConfig{} },
		checks: map[string]func(proto.Message) error{
			"ListContainers": func(m proto.Message) error {
				r := m.(*dockerapipb.ListContainersResponse)
				if len(r.GetHosts()) == 0 {
					return errors.New("no Docker host")
				}
				for _, h := range r.GetHosts() {
					if h.GetError() != "" {
						return fmt.Errorf("host %s: %s", h.GetHost(), h.GetError())
					}
					if len(h.GetContainers()) == 0 {
						return fmt.Errorf("host %s runs no container", h.GetHost())
					}
				}
				return nil
			},
		},
		// Only the first host is recorded: the fixtures hold one.
		proxy: func(m proto.Message, addr string) (string, string, error) {
			cfg := m.(*dockerapipb.DockerApiConfig)
			if len(cfg.GetHosts()) == 0 {
				return "", "", errors.New("no Docker host to record")
			}
			u, err := url.Parse(cfg.GetHosts()[0].GetUrl())
			if err != nil {
				return "", "", err
			}
			if u.Scheme == "tcp" {
				u.Scheme = "http"
			}
			cfg.GetHosts()[0].Url = "tcp://" + addr
			return u.String(), "", nil
		},
	},
	{
		def:     pfsenseapi.Definition(),
		service: pfsenseapipb.PfsenseApiService_ServiceDesc.ServiceName,
		config:  func() proto.Message { return &pfsenseapipb.PfsenseApiConfig{} },
		checks: map[string]func(proto.Message) error{
			"ListGateways": atLeastOne("gateway", func(m proto.Message) int { return len(m.(*pfsenseapipb.ListGatewaysResponse).GetGateways()) }),
			"ListGatewayGroups": func(m proto.Message) error {
				for _, g := range m.(*pfsenseapipb.ListGatewayGroupsResponse).GetGroups() {
					tiers := map[int32]bool{}
					for _, member := range g.GetMembers() {
						tiers[member.GetTier()] = true
					}
					if len(tiers) >= 2 {
						return nil
					}
				}
				return errors.New("no gateway group with members on two tiers (the VPN Tunnel's failover group)")
			},
			"ListLeases": atLeastOne("lease", func(m proto.Message) int { return len(m.(*pfsenseapipb.ListLeasesResponse).GetLeases()) }),
			"ListDhcpServers": func(m proto.Message) error {
				for _, s := range m.(*pfsenseapipb.ListDhcpServersResponse).GetServers() {
					if s.GetEnabled() {
						return nil
					}
				}
				return errors.New("no enabled DHCP server")
			},
			"ListAliases": atLeastOne("alias", func(m proto.Message) int { return len(m.(*pfsenseapipb.ListAliasesResponse).GetAliases()) }),
			"ListInterfaces": func(m proto.Message) error {
				var names []string
				for _, i := range m.(*pfsenseapipb.ListInterfacesResponse).GetInterfaces() {
					names = append(names, i.GetName())
				}
				for _, want := range []string{"wan", "lan"} {
					if !slices.Contains(names, want) {
						return fmt.Errorf("no %s interface among %v", want, names)
					}
				}
				return nil
			},
			"ListFirewallRules": atLeastOne("firewall rule", func(m proto.Message) int { return len(m.(*pfsenseapipb.ListFirewallRulesResponse).GetRules()) }),
		},
		proxy: func(m proto.Message, addr string) (string, string, error) {
			cfg := m.(*pfsenseapipb.PfsenseApiConfig)
			upstream := cfg.GetUrl()
			cfg.Url = "http://" + addr
			return upstream, "", nil
		},
	},
	{
		def:     adguardapi.Definition(),
		service: adguardapipb.AdguardApiService_ServiceDesc.ServiceName,
		config:  func() proto.Message { return &adguardapipb.AdguardApiConfig{} },
		requests: map[string]func(map[string]proto.Message) (proto.Message, error){
			// The most active client's queries.
			"ListQueries": func(answers map[string]proto.Message) (proto.Message, error) {
				stats, _ := answers["GetStats"].(*adguardapipb.GetStatsResponse)
				for _, c := range stats.GetTopClients() {
					if _, err := netip.ParseAddr(c.GetName()); err == nil {
						return &adguardapipb.ListQueriesRequest{Client: c.GetName()}, nil
					}
				}
				return nil, errors.New("GetStats names no active client by address to read the queries of")
			},
		},
		checks: map[string]func(proto.Message) error{
			"GetStatus": func(m proto.Message) error {
				if !m.(*adguardapipb.GetStatusResponse).GetRunning() {
					return errors.New("AdGuard Home isn't running")
				}
				return nil
			},
			"ListQueries": atLeastOne("query of the most active client", func(m proto.Message) int { return len(m.(*adguardapipb.ListQueriesResponse).GetQueries()) }),
		},
		proxy: func(m proto.Message, addr string) (string, string, error) {
			cfg := m.(*adguardapipb.AdguardApiConfig)
			upstream := cfg.GetUrl()
			cfg.Url = "http://" + addr
			return upstream, "", nil
		},
	},
	{
		def:     proxmoxapi.Definition(),
		service: proxmoxapipb.ProxmoxApiService_ServiceDesc.ServiceName,
		config:  func() proto.Message { return &proxmoxapipb.ProxmoxApiConfig{} },
		requests: map[string]func(map[string]proto.Message) (proto.Message, error){
			// The first Workload's running tasks.
			"ListActiveTasks": func(answers map[string]proto.Message) (proto.Message, error) {
				list, _ := answers["ListWorkloads"].(*proxmoxapipb.ListWorkloadsResponse)
				if ws := list.GetWorkloads(); len(ws) > 0 {
					return &proxmoxapipb.ListActiveTasksRequest{Node: ws[0].GetNode(), Vmid: ws[0].GetVmid()}, nil
				}
				return nil, errors.New("ListWorkloads listed no Workload to read the tasks of")
			},
		},
		checks: map[string]func(proto.Message) error{
			"ListWorkloads":   atLeastOne("Workload", func(m proto.Message) int { return len(m.(*proxmoxapipb.ListWorkloadsResponse).GetWorkloads()) }),
			"ListPermissions": atLeastOne("Workload's permissions", func(m proto.Message) int { return len(m.(*proxmoxapipb.ListPermissionsResponse).GetWorkloads()) }),
		},
		proxy: func(m proto.Message, addr string) (string, string, error) {
			cfg := m.(*proxmoxapipb.ProxmoxApiConfig)
			upstream, tlsName := cfg.GetUrl(), cfg.GetTlsServerName()
			cfg.Url, cfg.TlsServerName = "http://"+addr, ""
			return upstream, tlsName, nil
		},
	},
	{
		def:     nginxconfig.Definition(),
		service: nginxconfigpb.NginxConfigService_ServiceDesc.ServiceName,
		config:  func() proto.Message { return &nginxconfigpb.NginxConfigConfig{} },
		checks: map[string]func(proto.Message) error{
			"GetSnapshot": func(m proto.Message) error {
				s := m.(*nginxconfigpb.GetSnapshotResponse).GetSnapshot()
				if s.GetError() != "" {
					return fmt.Errorf("nginx -T failed: %s", s.GetError())
				}
				if len(s.GetServers()) == 0 {
					return errors.New("no server block")
				}
				return nil
			},
		},
		capture: func(ctx context.Context, dbPath string, m proto.Message) (string, fixture.Output, error) {
			stdout, stderr, exit, err := nginxconfig.Capture(ctx, dbPath, m.(*nginxconfigpb.NginxConfigConfig))
			return "nginx -T", fixture.Output{Stdout: []byte(stdout), Stderr: []byte(stderr), Exit: exit}, err
		},
	},
}

func atLeastOne(what string, count func(proto.Message) int) func(proto.Message) error {
	return func(m proto.Message) error {
		if count(m) == 0 {
			return fmt.Errorf("no %s", what)
		}
		return nil
	}
}
