package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
)

// The round trips of `make smoke-writes` (RunWrites). They run the real
// Actions of pfsense-api, adguard-api and proxmox-api, and touch only the
// smoke targets: the DDASH_SMOKE alias, a static mapping and an AdGuard
// client for SMOKE_UNUSED_IP made with the smoke MAC and name, and the
// stopped ddash-smoke LXC. What no Action can undo (deleting the static
// mapping and the client) goes to the system as a raw call. Each round
// trip's cleanup runs before it, removing what an interrupted run left,
// and after it, whether it passed or not.

const (
	// SmokeAlias is the only alias written: an empty host alias nothing
	// refers to, never the bypass alias.
	SmokeAlias = "DDASH_SMOKE"
	// PlaceholderAddress goes into SmokeAlias and out again (TEST-NET-3).
	PlaceholderAddress = "203.0.113.63"
	// SmokeMAC is the static mapping's fake, locally administered MAC.
	SmokeMAC = "02:dd:a5:00:00:63"
	// SmokeName names the static mapping's and the AdGuard client's owner;
	// SmokeLXC is the stopped LXC started and shut down.
	SmokeName = "ddash-smoke"
	SmokeLXC  = "ddash-smoke"
	// UnusedAddressVar is the smoke.env variable holding a LAN address no
	// Device holds (no lease, static mapping or AdGuard client), which the
	// static mapping and the AdGuard client borrow.
	UnusedAddressVar = "SMOKE_UNUSED_IP"
	// TripTimeout bounds one round trip, CleanupTimeout its cleanup (which
	// runs on even after the run is interrupted).
	TripTimeout    = 5 * time.Minute
	CleanupTimeout = 3 * time.Minute
	// ShutdownTimeout is how long ddash-smoke gets to shut down cleanly;
	// cleanup forces it down after ForcedShutdownTimeout.
	ShutdownTimeout       = 60 * time.Second
	ForcedShutdownTimeout = 30 * time.Second
)

// writeSystems are the system Components the round trips use.
var writeSystems = []string{"pfsense-api", "adguard-api", "proxmox-api"}

// roundTrip is one reversible exercise of a system's Actions.
type roundTrip struct {
	component, name string
	// run exercises the Actions and checks what they did. Its detail, if
	// any, goes on the ok line.
	run func(w *writes, ctx context.Context) (detail string, err error)
	// undo puts the target back as before run, whatever run got to; it
	// changes nothing that isn't a smoke target, and reports what it
	// removed ("" for nothing). Nil when run changes nothing.
	undo func(w *writes, ctx context.Context) (removed string, err error)
}

var roundTrips = []roundTrip{
	{component: "pfsense-api", name: "alias " + SmokeAlias + " round trip", run: (*writes).aliasRoundTrip, undo: (*writes).removePlaceholder},
	{component: "pfsense-api", name: "static mapping round trip", run: (*writes).staticMappingRoundTrip, undo: (*writes).deleteStaticMapping},
	{component: "adguard-api", name: "SetClientFiltering round trip", run: (*writes).clientRoundTrip, undo: (*writes).deleteClient},
	{component: "proxmox-api", name: SmokeLXC + " start and shutdown", run: (*writes).powerRoundTrip, undo: (*writes).forceSmokeDown},
	{component: "proxmox-api", name: "Protected refusal", run: (*writes).protectedRefusal},
}

// writes is what the round trips share: the started Components and what
// the raw calls need.
type writes struct {
	ip   netip.Addr
	http *http.Client

	pf    pfsenseapipb.PfsenseApiServiceClient
	pfCfg *pfsenseapipb.PfsenseApiConfig
	ag    adguardapipb.AdguardApiServiceClient
	agCfg *adguardapipb.AdguardApiConfig
	px    proxmoxapipb.ProxmoxApiServiceClient

	// smoke is ddash-smoke once a start was sent to it: only then does
	// cleanup shut it down.
	smoke *proxmoxapipb.Target
}

// RunWrites runs the round trips of `make smoke-writes` against the real
// systems in opts.Env (pfsense-api, adguard-api and proxmox-api, plus
// UnusedAddressVar) and returns every failure, joined. A failing round
// trip doesn't stop the others, and each one's cleanup always runs.
func RunWrites(ctx context.Context, opts Options) error {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	text := strings.TrimSpace(opts.Env[UnusedAddressVar])
	if text == "" {
		return fmt.Errorf("set %s in smoke.env: a LAN address no Device holds, which the round trips borrow", UnusedAddressVar)
	}
	ip, err := netip.ParseAddr(text)
	if err != nil || !ip.Is4() {
		return fmt.Errorf("%s=%q is no IPv4 address", UnusedAddressVar, text)
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return err
	}

	w := &writes{ip: ip, http: &http.Client{Timeout: CallTimeout}}
	var errs []error
	fail := func(err error) {
		fmt.Fprintf(opts.Log, "FAIL %v\n", err)
		errs = append(errs, err)
	}
	started := map[string]bool{}
	for _, name := range writeSystems {
		s := systemNamed(name)
		cfg, err := configOf(s, opts.Env)
		if err == nil {
			conn, stop, lerr := launch(ctx, s.def, cfg, filepath.Join(opts.DataDir, name+".db"), opts.Log)
			if err = lerr; err == nil {
				defer stop()
				started[name] = true
				switch c := cfg.(type) {
				case *pfsenseapipb.PfsenseApiConfig:
					w.pf, w.pfCfg = pfsenseapipb.NewPfsenseApiServiceClient(conn), c
				case *adguardapipb.AdguardApiConfig:
					w.ag, w.agCfg = adguardapipb.NewAdguardApiServiceClient(conn), c
				case *proxmoxapipb.ProxmoxApiConfig:
					w.px = proxmoxapipb.NewProxmoxApiServiceClient(conn)
				}
			}
		}
		if err != nil {
			fail(fmt.Errorf("%s %w", name, err))
		}
	}

	for _, t := range roundTrips {
		if !started[t.component] {
			continue
		}
		label := t.component + " " + t.name
		cleanup := func(when string) {
			if t.undo == nil {
				return
			}
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)
			defer cancel()
			removed, err := t.undo(w, cctx)
			switch {
			case err != nil:
				fail(fmt.Errorf("%s: cleanup %s: %w", label, when, err))
			case removed != "" && when == "before":
				fmt.Fprintf(opts.Log, "cleanup before %s: %s (left by an earlier run)\n", label, removed)
			case removed != "":
				fmt.Fprintf(opts.Log, "cleanup after %s: %s\n", label, removed)
			}
		}
		cleanup("before")
		tctx, cancel := context.WithTimeout(ctx, TripTimeout)
		detail, err := t.run(w, tctx)
		cancel()
		if err != nil {
			fail(fmt.Errorf("%s: %w", label, err))
		}
		cleanup("after")
		if err == nil {
			if detail != "" {
				label += " (" + detail + ")"
			}
			fmt.Fprintf(opts.Log, "ok %s\n", label)
		}
	}
	if len(errs) > 0 {
		fmt.Fprintf(opts.Log, "FAILED: %d problem(s)\n", len(errs))
	}
	return errors.Join(errs...)
}

func systemNamed(name string) system {
	for _, s := range systems {
		if s.def.Name() == name {
			return s
		}
	}
	panic("smoke: no system " + name)
}

// pfSense: the alias.

func (w *writes) aliasRoundTrip(ctx context.Context) (string, error) {
	alias, err := w.smokeAlias(ctx)
	if err != nil {
		return "", err
	}
	if alias.GetType() != "host" {
		return "", fmt.Errorf("%s is a %s alias; it must be an empty host alias", SmokeAlias, alias.GetType())
	}
	if _, err := w.pf.AddAliasAddress(ctx, &pfsenseapipb.AddAliasAddressRequest{Alias: SmokeAlias, Address: PlaceholderAddress, Detail: "make smoke-writes"}); err != nil {
		return "", fmt.Errorf("AddAliasAddress: %w", err)
	}
	if err := w.firewallApplied(ctx); err != nil {
		return "", err
	}
	if alias, err = w.smokeAlias(ctx); err != nil {
		return "", err
	}
	if !slices.Contains(alias.GetAddresses(), PlaceholderAddress) {
		return "", fmt.Errorf("%s doesn't hold %s after AddAliasAddress: %v", SmokeAlias, PlaceholderAddress, alias.GetAddresses())
	}
	if _, err := w.pf.RemoveAliasAddress(ctx, &pfsenseapipb.RemoveAliasAddressRequest{Alias: SmokeAlias, Address: PlaceholderAddress}); err != nil {
		return "", fmt.Errorf("RemoveAliasAddress: %w", err)
	}
	if err := w.firewallApplied(ctx); err != nil {
		return "", err
	}
	if alias, err = w.smokeAlias(ctx); err != nil {
		return "", err
	}
	if slices.Contains(alias.GetAddresses(), PlaceholderAddress) {
		return "", fmt.Errorf("%s still holds %s after RemoveAliasAddress", SmokeAlias, PlaceholderAddress)
	}
	// The same round trip as one move in and one move out, each applied once.
	for _, move := range []*pfsenseapipb.MoveAliasAddressRequest{
		{Address: PlaceholderAddress, To: SmokeAlias, Detail: "make smoke-writes"},
		{Address: PlaceholderAddress, From: []string{SmokeAlias}},
	} {
		if _, err := w.pf.MoveAliasAddress(ctx, move); err != nil {
			return "", fmt.Errorf("MoveAliasAddress: %w", err)
		}
		if err := w.firewallApplied(ctx); err != nil {
			return "", err
		}
		if alias, err = w.smokeAlias(ctx); err != nil {
			return "", err
		}
		if slices.Contains(alias.GetAddresses(), PlaceholderAddress) != (move.GetTo() != "") {
			return "", fmt.Errorf("%s = %v after MoveAliasAddress %v", SmokeAlias, alias.GetAddresses(), move)
		}
	}
	return "", nil
}

// smokeAlias reads SmokeAlias through ListAliases (every alias write
// drops its cache).
func (w *writes) smokeAlias(ctx context.Context) (*pfsenseapipb.Alias, error) {
	r, err := w.pf.ListAliases(ctx, &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		return nil, fmt.Errorf("ListAliases: %w", err)
	}
	for _, a := range r.GetAliases() {
		if a.GetName() == SmokeAlias {
			return a, nil
		}
	}
	return nil, fmt.Errorf("pfSense has no alias %s: create it as an empty host alias (docs/deployment.md, Smoke targets)", SmokeAlias)
}

// firewallApplied checks that no alias change waits for an apply.
func (w *writes) firewallApplied(ctx context.Context) error {
	var state struct {
		Applied bool `json:"applied"`
	}
	if err := w.pfsense(ctx, http.MethodGet, "/api/v2/firewall/apply", nil, &state); err != nil {
		return err
	}
	if !state.Applied {
		return errors.New("pfSense has firewall changes pending after the write: it wasn't applied")
	}
	return nil
}

func (w *writes) removePlaceholder(ctx context.Context) (string, error) {
	var aliases []struct {
		Name    string   `json:"name"`
		Address []string `json:"address"`
	}
	if err := w.pfsense(ctx, http.MethodGet, "/api/v2/firewall/aliases", nil, &aliases); err != nil {
		return "", err
	}
	i := slices.IndexFunc(aliases, func(a struct {
		Name    string   `json:"name"`
		Address []string `json:"address"`
	}) bool {
		return a.Name == SmokeAlias
	})
	if i < 0 || !slices.Contains(aliases[i].Address, PlaceholderAddress) {
		return "", nil
	}
	if _, err := w.pf.RemoveAliasAddress(ctx, &pfsenseapipb.RemoveAliasAddressRequest{Alias: SmokeAlias, Address: PlaceholderAddress}); err != nil {
		return "", fmt.Errorf("RemoveAliasAddress: %w", err)
	}
	return fmt.Sprintf("removed %s from %s", PlaceholderAddress, SmokeAlias), nil
}

// pfSense: the static mapping.

func (w *writes) staticMappingRoundTrip(ctx context.Context) (string, error) {
	iface, err := w.dhcpInterface(ctx)
	if err != nil {
		return "", err
	}
	const name, renamed = "ddash smoke-writes", "ddash smoke-writes, renamed"
	if _, err := w.pf.CreateStaticMapping(ctx, &pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{
		Interface: iface, Mac: SmokeMAC, Ip: w.ip.String(), Description: name,
	}}); err != nil {
		return "", fmt.Errorf("CreateStaticMapping: %w", err)
	}
	if _, err := w.pf.RenameStaticMapping(ctx, &pfsenseapipb.RenameStaticMappingRequest{Interface: iface, Mac: SmokeMAC, Description: renamed}); err != nil {
		return "", fmt.Errorf("RenameStaticMapping: %w", err)
	}
	servers, err := w.pf.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		return "", fmt.Errorf("ListDhcpServers: %w", err)
	}
	m := mappingOf(servers, SmokeMAC)
	if m == nil || m.GetIp() != w.ip.String() || m.GetDescription() != renamed {
		return "", fmt.Errorf("after the rename pfSense holds %v, want %s at %s named %q", m, SmokeMAC, w.ip, renamed)
	}
	removed, err := w.deleteStaticMapping(ctx)
	if err != nil {
		return "", err
	}
	if removed == "" {
		return "", fmt.Errorf("the static mapping of %s was gone before its delete", SmokeMAC)
	}
	left, err := w.rawStaticMappings(ctx)
	if err != nil {
		return "", err
	}
	if _, _, found := findMAC(left, SmokeMAC); found {
		return "", fmt.Errorf("the static mapping of %s is still there after its delete", SmokeMAC)
	}
	return "", nil
}

// dhcpInterface checks that the unused address is unused on pfSense and
// returns the interface whose subnet and enabled DHCP server hold it.
func (w *writes) dhcpInterface(ctx context.Context) (string, error) {
	ip := w.ip.String()
	leases, err := w.pf.ListLeases(ctx, &pfsenseapipb.ListLeasesRequest{})
	if err != nil {
		return "", fmt.Errorf("ListLeases: %w", err)
	}
	for _, l := range leases.GetLeases() {
		if l.GetIp() == ip {
			return "", fmt.Errorf("%s is in use: %s holds a lease on it (pick another %s)", ip, l.GetMac(), UnusedAddressVar)
		}
	}
	servers, err := w.pf.ListDhcpServers(ctx, &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		return "", fmt.Errorf("ListDhcpServers: %w", err)
	}
	for _, s := range servers.GetServers() {
		for _, m := range s.GetStaticMappings() {
			if m.GetIp() == ip {
				return "", fmt.Errorf("%s is in use: the static mapping of %s (pick another %s)", ip, m.GetMac(), UnusedAddressVar)
			}
		}
	}
	ifaces, err := w.pf.ListInterfaces(ctx, &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		return "", fmt.Errorf("ListInterfaces: %w", err)
	}
	for _, i := range ifaces.GetInterfaces() {
		addr, err := netip.ParseAddr(i.GetIpv4Address())
		if err != nil {
			continue
		}
		if addr == w.ip {
			return "", fmt.Errorf("%s is in use: it is pfSense's %s address", ip, i.GetName())
		}
		if !netip.PrefixFrom(addr, int(i.GetIpv4Prefix())).Masked().Contains(w.ip) {
			continue
		}
		for _, s := range servers.GetServers() {
			if s.GetInterface() == i.GetName() && s.GetEnabled() {
				return i.GetName(), nil
			}
		}
	}
	return "", fmt.Errorf("no pfSense interface with an enabled DHCP server holds %s", ip)
}

func mappingOf(r *pfsenseapipb.ListDhcpServersResponse, mac string) *pfsenseapipb.StaticMapping {
	for _, s := range r.GetServers() {
		for _, m := range s.GetStaticMappings() {
			if strings.EqualFold(m.GetMac(), mac) {
				return m
			}
		}
	}
	return nil
}

// rawServer is a DHCP server as pfSense's REST API lists it, with its
// static mappings in id (position) order.
type rawServer struct {
	ID        string `json:"id"`
	Staticmap []struct {
		MAC string `json:"mac"`
	} `json:"staticmap"`
}

func (w *writes) rawStaticMappings(ctx context.Context) ([]rawServer, error) {
	var servers []rawServer
	err := w.pfsense(ctx, http.MethodGet, "/api/v2/services/dhcp_servers", nil, &servers)
	return servers, err
}

func findMAC(servers []rawServer, mac string) (iface string, id int, found bool) {
	for _, s := range servers {
		for i, m := range s.Staticmap {
			if strings.EqualFold(m.MAC, mac) {
				return s.ID, i, true
			}
		}
	}
	return "", -1, false
}

// deleteStaticMapping deletes SmokeMAC's static mapping with a raw DELETE
// (pfsense-api has no Action for it), looking its id up right before, and
// makes sure the change is applied.
func (w *writes) deleteStaticMapping(ctx context.Context) (string, error) {
	servers, err := w.rawStaticMappings(ctx)
	if err != nil {
		return "", err
	}
	iface, id, found := findMAC(servers, SmokeMAC)
	if !found {
		return "", nil
	}
	q := url.Values{"parent_id": {iface}, "id": {strconv.Itoa(id)}, "apply": {"true"}}
	if err := w.pfsense(ctx, http.MethodDelete, "/api/v2/services/dhcp_server/static_mapping", q, nil); err != nil {
		return "", err
	}
	var state struct {
		Applied bool `json:"applied"`
	}
	if err := w.pfsense(ctx, http.MethodGet, "/api/v2/services/dhcp_server/apply", nil, &state); err != nil {
		return "", err
	}
	if !state.Applied {
		if err := w.pfsense(ctx, http.MethodPost, "/api/v2/services/dhcp_server/apply", nil, nil); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("deleted the static mapping of %s on %s", SmokeMAC, iface), nil
}

// pfsense sends a raw request to pfSense's REST API and decodes the data
// of its answer into out (unless nil).
func (w *writes) pfsense(ctx context.Context, method, path string, query url.Values, out any) error {
	target := strings.TrimSuffix(w.pfCfg.GetUrl(), "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", w.pfCfg.GetApiKey())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	b, code, err := w.do(req)
	if err != nil {
		return fmt.Errorf("pfSense %s %s: %w", method, path, err)
	}
	var env struct {
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	jerr := json.Unmarshal(b, &env)
	if code != http.StatusOK {
		msg := env.Message
		if jerr != nil || msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("pfSense answered %s %s with %d: %s", method, path, code, msg)
	}
	if jerr != nil {
		return fmt.Errorf("pfSense %s %s: %w", method, path, jerr)
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("pfSense %s %s: %w", method, path, err)
		}
	}
	return nil
}

// AdGuard Home: the client.

func (w *writes) clientRoundTrip(ctx context.Context) (string, error) {
	ip := w.ip.String()
	clients, err := w.ag.ListClients(ctx, &adguardapipb.ListClientsRequest{})
	if err != nil {
		return "", fmt.Errorf("ListClients: %w", err)
	}
	for _, c := range clients.GetClients() {
		if slices.Contains(c.GetIds(), ip) {
			return "", fmt.Errorf("%s is in use: AdGuard Home's client %q holds it (pick another %s)", ip, c.GetName(), UnusedAddressVar)
		}
	}
	off, err := w.ag.SetClientFiltering(ctx, &adguardapipb.SetClientFilteringRequest{Ip: ip, Name: SmokeName, Enabled: false})
	if err != nil {
		return "", fmt.Errorf("SetClientFiltering off: %w", err)
	}
	if !off.GetCreated() || off.GetClient().GetUseGlobalSettings() || off.GetClient().GetFilteringEnabled() {
		return "", fmt.Errorf("SetClientFiltering off answered %v, want a client created with filtering off", off)
	}
	on, err := w.ag.SetClientFiltering(ctx, &adguardapipb.SetClientFilteringRequest{Ip: ip, Name: SmokeName, Enabled: true})
	if err != nil {
		return "", fmt.Errorf("SetClientFiltering on: %w", err)
	}
	if on.GetCreated() || !on.GetClient().GetFilteringEnabled() {
		return "", fmt.Errorf("SetClientFiltering on answered %v, want the client toggled to filtering on", on)
	}
	if clients, err = w.ag.ListClients(ctx, &adguardapipb.ListClientsRequest{}); err != nil {
		return "", fmt.Errorf("ListClients: %w", err)
	}
	i := slices.IndexFunc(clients.GetClients(), func(c *adguardapipb.Client) bool { return slices.Contains(c.GetIds(), ip) })
	if i < 0 || !clients.GetClients()[i].GetFilteringEnabled() || clients.GetClients()[i].GetUseGlobalSettings() {
		return "", fmt.Errorf("AdGuard Home doesn't list %s's client with filtering on after the toggle", ip)
	}
	removed, err := w.deleteClient(ctx)
	if err != nil {
		return "", err
	}
	if removed == "" {
		return "", fmt.Errorf("the client of %s was gone before its delete", ip)
	}
	if mine, err := w.smokeClients(ctx); err != nil {
		return "", err
	} else if len(mine) > 0 {
		return "", fmt.Errorf("AdGuard Home still lists %v after the delete", mine)
	}
	return "", nil
}

// smokeClients are the names of the persistent clients SetClientFiltering
// made for the unused address: named after SmokeName, holding only it.
func (w *writes) smokeClients(ctx context.Context) ([]string, error) {
	var list struct {
		Clients []struct {
			Name string   `json:"name"`
			IDs  []string `json:"ids"`
		} `json:"clients"`
	}
	if err := w.adguard(ctx, http.MethodGet, "/control/clients", nil, &list); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range list.Clients {
		if strings.HasPrefix(c.Name, SmokeName) && slices.Equal(c.IDs, []string{w.ip.String()}) {
			out = append(out, c.Name)
		}
	}
	return out, nil
}

// deleteClient deletes the smoke client with a raw call (adguard-api has
// no Action for it).
func (w *writes) deleteClient(ctx context.Context) (string, error) {
	names, err := w.smokeClients(ctx)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if err := w.adguard(ctx, http.MethodPost, "/control/clients/delete", map[string]string{"name": name}, nil); err != nil {
			return "", err
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	return fmt.Sprintf("deleted AdGuard Home's client %q", strings.Join(names, `", "`)), nil
}

// adguard sends a raw request to AdGuard Home's API and decodes its answer
// into out (unless nil).
func (w *writes) adguard(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(w.agCfg.GetUrl(), "/")+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(w.agCfg.GetUsername(), w.agCfg.GetPassword())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	b, code, err := w.do(req)
	if err != nil {
		return fmt.Errorf("AdGuard Home %s %s: %w", method, path, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("AdGuard Home answered %s %s with %d: %s", method, path, code, strings.TrimSpace(string(b)))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("AdGuard Home %s %s: %w", method, path, err)
		}
	}
	return nil
}

// do sends req and reads the answer (at most 8 MiB).
func (w *writes) do(req *http.Request) ([]byte, int, error) {
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return b, resp.StatusCode, err
}

// Proxmox: ddash-smoke and a Protected Workload.

func (w *writes) powerRoundTrip(ctx context.Context) (string, error) {
	ws, err := w.workloads(ctx)
	if err != nil {
		return "", err
	}
	var smoke *proxmoxapipb.Workload
	for _, wl := range ws {
		if wl.GetName() == SmokeLXC && wl.GetKind() == proxmoxapipb.Kind_KIND_LXC && !wl.GetTemplate() {
			if smoke != nil {
				return "", fmt.Errorf("two LXCs are named %s in Proxmox", SmokeLXC)
			}
			smoke = wl
		}
	}
	if smoke == nil {
		return "", fmt.Errorf("no LXC is named %s in Proxmox: create it, small and stopped (docs/deployment.md, Smoke targets)", SmokeLXC)
	}
	if smoke.GetStatus() != "stopped" {
		return "", fmt.Errorf("%s is %s: stop it first, it must start stopped", label(smoke), smoke.GetStatus())
	}
	if protected, err := w.protected(ctx, smoke.GetVmid()); err != nil {
		return "", err
	} else if protected {
		return "", fmt.Errorf("%s is Protected: the token lacks VM.PowerMgmt on it", label(smoke))
	}

	target := &proxmoxapipb.Target{Kind: smoke.GetKind(), Node: smoke.GetNode(), Vmid: smoke.GetVmid()}
	w.smoke = target
	if _, err := w.px.StartWorkload(ctx, &proxmoxapipb.StartWorkloadRequest{Workload: target}); err != nil {
		return "", fmt.Errorf("StartWorkload: %w", err)
	}
	if err := w.wantStatus(ctx, smoke, "running"); err != nil {
		return "", err
	}
	if _, err := w.px.ShutdownWorkload(ctx, &proxmoxapipb.ShutdownWorkloadRequest{Workload: target, Timeout: durationpb.New(ShutdownTimeout)}); err != nil {
		return "", fmt.Errorf("ShutdownWorkload: %w", err)
	}
	return "", w.wantStatus(ctx, smoke, "stopped")
}

// forceSmokeDown shuts ddash-smoke down, forced, when this run started it
// and it isn't stopped.
func (w *writes) forceSmokeDown(ctx context.Context) (string, error) {
	if w.smoke == nil {
		return "", nil
	}
	ws, err := w.workloads(ctx)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(ws, func(wl *proxmoxapipb.Workload) bool { return wl.GetVmid() == w.smoke.GetVmid() })
	if i < 0 || ws[i].GetStatus() == "stopped" {
		return "", nil
	}
	w.smoke.Node = ws[i].GetNode()
	if _, err := w.px.ShutdownWorkload(ctx, &proxmoxapipb.ShutdownWorkloadRequest{Workload: w.smoke, Timeout: durationpb.New(ForcedShutdownTimeout), ForceStop: true}); err != nil {
		return "", fmt.Errorf("ShutdownWorkload (forced): %w", err)
	}
	return fmt.Sprintf("forced %s down", label(ws[i])), nil
}

// protectedRefusal tries to start a running Protected Workload: the token
// must be refused. Were it not, Proxmox would only answer that the
// Workload is running already, so nothing changes either way.
func (w *writes) protectedRefusal(ctx context.Context) (string, error) {
	ws, err := w.workloads(ctx)
	if err != nil {
		return "", err
	}
	var target *proxmoxapipb.Workload
	for _, wl := range ws {
		if wl.GetStatus() != "running" || wl.GetTemplate() {
			continue
		}
		protected, err := w.protected(ctx, wl.GetVmid())
		if err != nil {
			return "", err
		}
		if protected {
			target = wl
			break
		}
	}
	if target == nil {
		return "", errors.New("no running Protected Workload to try: ListPermissions shows VM.PowerMgmt on every running one (docs/deployment.md, Protected Workloads)")
	}
	_, err = w.px.StartWorkload(ctx, &proxmoxapipb.StartWorkloadRequest{Workload: &proxmoxapipb.Target{Kind: target.GetKind(), Node: target.GetNode(), Vmid: target.GetVmid()}})
	switch {
	case status.Code(err) == codes.PermissionDenied:
		return label(target), nil
	case err == nil:
		return "", fmt.Errorf("%s started: the token isn't refused on it", label(target))
	default:
		return "", fmt.Errorf("the token wasn't refused on %s: %w", label(target), err)
	}
}

func (w *writes) workloads(ctx context.Context) ([]*proxmoxapipb.Workload, error) {
	r, err := w.px.ListWorkloads(ctx, &proxmoxapipb.ListWorkloadsRequest{})
	if err != nil {
		return nil, fmt.Errorf("ListWorkloads: %w", err)
	}
	return r.GetWorkloads(), nil
}

// protected reports whether the token lacks VM.PowerMgmt on vmid.
func (w *writes) protected(ctx context.Context, vmid uint32) (bool, error) {
	r, err := w.px.ListPermissions(ctx, &proxmoxapipb.ListPermissionsRequest{})
	if err != nil {
		return false, fmt.Errorf("ListPermissions: %w", err)
	}
	for _, p := range r.GetWorkloads() {
		if p.GetVmid() == vmid {
			return !slices.Contains(p.GetPrivileges(), "VM.PowerMgmt"), nil
		}
	}
	return true, nil
}

// wantStatus checks that ListWorkloads (whose cache every power Action
// drops) shows wl at status.
func (w *writes) wantStatus(ctx context.Context, wl *proxmoxapipb.Workload, want string) error {
	ws, err := w.workloads(ctx)
	if err != nil {
		return err
	}
	for _, now := range ws {
		if now.GetVmid() == wl.GetVmid() {
			if now.GetStatus() != want {
				return fmt.Errorf("%s is %s after the task, want %s", label(wl), now.GetStatus(), want)
			}
			return nil
		}
	}
	return fmt.Errorf("ListWorkloads no longer lists %s", label(wl))
}

// label names a Workload as the UI does: "LXC 103 ddash-smoke".
func label(wl *proxmoxapipb.Workload) string {
	kind := "VM"
	if wl.GetKind() == proxmoxapipb.Kind_KIND_LXC {
		kind = "LXC"
	}
	return fmt.Sprintf("%s %d %s", kind, wl.GetVmid(), wl.GetName())
}
