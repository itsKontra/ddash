// Package pfsensefake is a stateful fake of pfSense's REST API v2 for
// pfsense-api's Actions. It keeps the DHCP servers with their static
// mappings, the firewall aliases, each area's pending-changes flag and its
// apply, and records every request in order; anything else is served from
// a scenario's fixtures (fixture.Scenario). pfsense-api's tests and fakelab
// serve it.
//
// Like pfSense, a write without "apply": true only marks its area pending
// until that area's POST .../apply; ids are positions in a server's
// staticmap array and in the alias list.
package pfsensefake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/sdk/fixture"
)

const (
	serversPath  = "/api/v2/services/dhcp_servers"
	mappingPath  = "/api/v2/services/dhcp_server/static_mapping"
	mappingsPath = "/api/v2/services/dhcp_server/static_mappings"
	applyPath    = "/api/v2/services/dhcp_server/apply"

	aliasesPath       = "/api/v2/firewall/aliases"
	aliasPath         = "/api/v2/firewall/alias"
	firewallApplyPath = "/api/v2/firewall/apply"
)

// Applied is the entry Log records when pending DHCP changes take effect,
// from a write's "apply": true or from POST services/dhcp_server/apply.
const Applied = "apply DHCP"

// AppliedFirewall is the entry Log records when pending alias changes take
// effect (the filter reloads), from a write's "apply": true or from POST
// firewall/apply.
const AppliedFirewall = "apply firewall"

// Options shape a Firewall.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// WriteDelay holds every write this long before it takes effect, so
	// writes that overlap show in MaxConcurrentWrites.
	WriteDelay time.Duration
}

// Firewall is the fake, an http.Handler.
type Firewall struct {
	fixtures http.Handler
	opts     Options

	mu              sync.Mutex
	servers         []map[string]any
	pending         bool
	aliases         []map[string]any
	firewallPending bool
	log             []string
	writing         int
	maxW            int
}

// New serves the scenario in dir: its services/dhcp_servers.json and
// firewall/aliases.json (each else its sibling baseline's) are the
// starting state.
func New(dir string, opts Options) (*Firewall, error) {
	f := &Firewall{fixtures: fixture.Scenario(dir, opts.Miss), opts: opts}
	var err error
	if f.servers, err = seed(dir, serversPath); err != nil {
		return nil, err
	}
	if f.aliases, err = seed(dir, aliasesPath); err != nil {
		return nil, err
	}
	return f, nil
}

// seed reads the data of path's fixture in dir, else in its sibling
// baseline.
func seed(dir, path string) ([]map[string]any, error) {
	file := filepath.Join(dir, filepath.FromSlash(path)+".json")
	if _, err := os.Stat(file); err != nil {
		file = filepath.Join(filepath.Dir(dir), "baseline", filepath.FromSlash(path)+".json")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("pfsensefake: %w", err)
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("pfsensefake: %s: %w", file, err)
	}
	return env.Data, nil
}

// Mapping is a static mapping as the fake holds it.
type Mapping struct {
	MAC, IP, Hostname, Description string
}

// StaticMappings returns the interface's static mappings in order.
func (f *Firewall) StaticMappings(iface string) []Mapping {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.server(iface)
	if s == nil {
		return nil
	}
	var out []Mapping
	for _, m := range staticmap(s) {
		out = append(out, Mapping{MAC: text(m["mac"]), IP: text(m["ipaddr"]), Hostname: text(m["hostname"]), Description: text(m["descr"])})
	}
	return out
}

// Alias is a firewall alias as the fake holds it: its members and their
// descriptions, by position.
type Alias struct {
	Name               string
	Addresses, Details []string
}

// Aliases returns every alias in order.
func (f *Firewall) Aliases() []Alias {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Alias, 0, len(f.aliases))
	for _, a := range f.aliases {
		out = append(out, Alias{Name: text(a["name"]), Addresses: texts(a["address"]), Details: texts(a["detail"])})
	}
	return out
}

// FirewallPending reports whether alias changes wait for an apply.
func (f *Firewall) FirewallPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.firewallPending
}

// Pending reports whether DHCP changes wait for an apply.
func (f *Firewall) Pending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

// Log returns what happened, in order: each request as "METHOD /path" and
// Applied where pending changes took effect.
func (f *Firewall) Log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

// MaxConcurrentWrites is the most writes that were ever in flight at once.
func (f *Firewall) MaxConcurrentWrites() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxW
}

func (f *Firewall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.log = append(f.log, r.Method+" "+r.URL.Path)
	f.mu.Unlock()

	switch r.Method + " " + r.URL.Path {
	case "GET " + serversPath:
		f.mu.Lock()
		b, err := json.Marshal(f.servers)
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "GET " + mappingsPath:
		f.mu.Lock()
		var all []map[string]any
		for _, s := range f.servers {
			all = append(all, staticmap(s)...)
		}
		b, err := json.Marshal(all)
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "POST " + mappingPath, "PATCH " + mappingPath:
		f.write(w, r)
	case "GET " + applyPath:
		f.mu.Lock()
		applied := !f.pending
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": applied})
	case "POST " + applyPath:
		f.mu.Lock()
		f.apply()
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": true})
	case "GET " + aliasesPath:
		f.mu.Lock()
		list := make([]map[string]any, len(f.aliases))
		for i, a := range f.aliases {
			list[i] = maps.Clone(a)
			list[i]["id"] = i
		}
		b, err := json.Marshal(list)
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "PATCH " + aliasPath:
		f.patchAlias(w, r)
	case "GET " + firewallApplyPath:
		f.mu.Lock()
		applied, subsystems := !f.firewallPending, []string{}
		if f.firewallPending {
			subsystems = append(subsystems, "aliases")
		}
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": applied, "pending_subsystems": subsystems})
	case "POST " + firewallApplyPath:
		f.mu.Lock()
		f.applyFirewall()
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": true, "pending_subsystems": []string{}})
	default:
		f.fixtures.ServeHTTP(w, r)
	}
}

// begin counts a write in flight and holds it for WriteDelay; the caller
// takes f.mu after and decrements f.writing under it.
func (f *Firewall) begin() {
	f.mu.Lock()
	f.writing++
	f.maxW = max(f.maxW, f.writing)
	f.mu.Unlock()
	time.Sleep(f.opts.WriteDelay)
}

// patchAlias updates an alias by id. As in pfSense, a list sent replaces
// the stored one, descriptions are padded to the addresses with "Entry
// added <date>", and more descriptions than addresses is refused. The
// "append" and "remove" flags aren't faked, as pfsense-api doesn't send
// them: a request with either is refused, so a change that starts sending
// them fails here first.
func (f *Firewall) patchAlias(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, "INVALID_JSON", "The request body is not valid JSON.", nil)
		return
	}
	f.begin()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writing--

	for _, flag := range []string{"append", "remove"} {
		if body[flag] == true {
			reply(w, http.StatusBadRequest, "PFSENSEFAKE_UNSUPPORTED", fmt.Sprintf("pfsensefake doesn't fake %q.", flag), nil)
			return
		}
	}
	id, ok := body["id"].(float64)
	if !ok || id < 0 || int(id) >= len(f.aliases) || id != float64(int(id)) {
		reply(w, http.StatusNotFound, "MODEL_OBJECT_NOT_FOUND", fmt.Sprintf("Object with ID `%v` does not exist.", body["id"]), nil)
		return
	}
	updated := maps.Clone(f.aliases[int(id)])
	for _, k := range []string{"descr", "address", "detail"} {
		if v, ok := body[k]; ok {
			updated[k] = v
		}
	}
	addresses, details := texts(updated["address"]), texts(updated["detail"])
	if len(details) > len(addresses) {
		reply(w, http.StatusBadRequest, "TOO_MANY_ALIAS_DETAILS", "Aliases cannot contain more 'detail' items than 'address' items.", nil)
		return
	}
	for _, a := range addresses {
		if code, rid, msg := checkAddress(text(updated["type"]), a); code != 0 {
			reply(w, code, rid, msg, nil)
			return
		}
	}
	for len(details) < len(addresses) {
		details = append(details, "Entry added "+time.Now().Format(time.RFC1123Z))
	}
	updated["address"], updated["detail"] = addresses, details
	f.aliases[int(id)] = updated
	f.firewallPending = true
	if body["apply"] == true {
		f.applyFirewall()
	}
	out := maps.Clone(updated)
	out["id"] = int(id)
	reply(w, http.StatusOK, "SUCCESS", "", out)
}

// memberName is a hostname or an alias's name, which any address alias may hold.
var memberName = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`)

// checkAddress validates an alias member as pfSense does for the alias's
// type: an address (host) or a CIDR (network), else a hostname or another
// alias's name. Port aliases aren't checked.
func checkAddress(typ, a string) (code int, id, msg string) {
	switch typ {
	case "host":
		if _, err := netip.ParseAddr(a); err == nil || memberName.MatchString(a) {
			return 0, "", ""
		}
		return http.StatusBadRequest, "INVALID_HOST_ALIAS_ADDRESS", fmt.Sprintf("Host alias 'address' value '%s' is not a valid IP, FQDN, or alias.", a)
	case "network":
		if _, err := netip.ParsePrefix(a); err == nil || memberName.MatchString(a) {
			return 0, "", ""
		}
		return http.StatusBadRequest, "INVALID_NETWORK_ALIAS_ADDRESS", fmt.Sprintf("Network alias 'address' value '%s' is not a valid CIDR, FQDN, or alias.", a)
	}
	return 0, "", ""
}

// applyFirewall makes pending alias changes take effect; f.mu is held.
func (f *Firewall) applyFirewall() {
	if f.firewallPending {
		f.firewallPending = false
		f.log = append(f.log, AppliedFirewall)
	}
}

// write creates (POST) or updates (PATCH) a static mapping.
func (f *Firewall) write(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, "INVALID_JSON", "The request body is not valid JSON.", nil)
		return
	}

	f.begin()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writing--

	s := f.server(text(body["parent_id"]))
	if s == nil {
		reply(w, http.StatusNotFound, "MODEL_PARENT_NOT_FOUND", fmt.Sprintf("Parent object with ID `%v` does not exist.", body["parent_id"]), nil)
		return
	}
	all := staticmap(s)
	// i is the mapping a PATCH updates; -1 for a POST's new one.
	i, updated := -1, map[string]any{"parent_id": s["id"], "mac": "", "ipaddr": "", "cid": "", "hostname": "", "domain": nil, "descr": ""}
	if r.Method == http.MethodPatch {
		id, ok := body["id"].(float64)
		if !ok || id < 0 || int(id) >= len(all) || id != float64(int(id)) {
			reply(w, http.StatusNotFound, "MODEL_OBJECT_NOT_FOUND", fmt.Sprintf("Object with ID `%v` does not exist.", body["id"]), nil)
			return
		}
		i = int(id)
		updated = maps.Clone(all[i])
	}
	for _, k := range []string{"mac", "ipaddr", "hostname", "descr"} {
		if v, ok := body[k]; ok {
			updated[k] = v
		}
	}
	if code, id, msg := check(updated, all, i); code != 0 {
		reply(w, code, id, msg, nil)
		return
	}
	if i < 0 {
		all = append(all, updated)
	} else {
		all[i] = updated
	}
	list := make([]any, len(all))
	for j, x := range all {
		x["id"] = j
		list[j] = x
	}
	s["staticmap"] = list
	f.pending = true
	if body["apply"] == true {
		f.apply()
	}
	reply(w, http.StatusOK, "SUCCESS", "", updated)
}

var hostname = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)?$`)

// check validates a static mapping as pfSense does: a MAC, an IPv4
// address, a hostname, and neither MAC nor address used by another of the
// server's mappings (skip is the one being updated, -1 when creating).
func check(m map[string]any, all []map[string]any, skip int) (code int, id, msg string) {
	if _, err := net.ParseMAC(text(m["mac"])); err != nil {
		return http.StatusBadRequest, "DHCP_SERVER_STATIC_MAPPING_INVALID_MAC", "Field `mac` must be a valid MAC address."
	}
	if a, err := netip.ParseAddr(text(m["ipaddr"])); err != nil || !a.Is4() {
		return http.StatusBadRequest, "DHCP_SERVER_STATIC_MAPPING_INVALID_IPADDR", "Field `ipaddr` must be a valid IPv4 address."
	}
	if !hostname.MatchString(text(m["hostname"])) {
		return http.StatusBadRequest, "DHCP_SERVER_STATIC_MAPPING_INVALID_HOSTNAME", "Field `hostname` must be a valid hostname."
	}
	for j, x := range all {
		if j == skip {
			continue
		}
		if strings.EqualFold(text(x["mac"]), text(m["mac"])) {
			return http.StatusBadRequest, "DHCP_SERVER_STATIC_MAPPING_MAC_IN_USE", fmt.Sprintf("Field `mac` value `%s` is already in use by another static mapping.", text(m["mac"]))
		}
		if text(x["ipaddr"]) == text(m["ipaddr"]) {
			return http.StatusBadRequest, "DHCP_SERVER_STATIC_MAPPING_IPADDR_IN_USE", fmt.Sprintf("Field `ipaddr` value `%s` is already in use by another static mapping.", text(m["ipaddr"]))
		}
	}
	return 0, "", ""
}

// apply makes pending changes take effect; f.mu is held.
func (f *Firewall) apply() {
	if f.pending {
		f.pending = false
		f.log = append(f.log, Applied)
	}
}

// server is the DHCP server on iface; f.mu is held.
func (f *Firewall) server(iface string) map[string]any {
	for _, s := range f.servers {
		if text(s["id"]) == iface {
			return s
		}
	}
	return nil
}

func staticmap(s map[string]any) []map[string]any {
	list, _ := s["staticmap"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// texts reads a JSON list of strings (nil for none).
func texts(v any) []string {
	switch list := v.(type) {
	case []string:
		return slices.Clone(list)
	case []any:
		out := make([]string, 0, len(list))
		for _, x := range list {
			out = append(out, text(x))
		}
		return out
	}
	return nil
}

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// reply writes the REST API's envelope.
func reply(w http.ResponseWriter, code int, responseID, message string, data any) {
	if data == nil {
		data = []any{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	status := "ok"
	if code != http.StatusOK {
		status = strings.ToLower(http.StatusText(code))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "status": status, "response_id": responseID, "message": message, "data": data})
}
