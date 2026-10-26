// Package adguardfake is a stateful fake of AdGuard Home's HTTP API for
// adguard-api's Action and `make smoke-writes`. It keeps the persistent
// clients (add, update, delete, list) and answers the query log from a
// scenario's recorded entries, filtered and paged as AdGuard filters and
// pages them; anything else is served from the scenario's fixtures
// (fixture.Scenario). adguard-api's tests and fakelab serve it.
//
// Like AdGuard Home, an update replaces the whole client: a field the
// request leaves out is reset to its zero value, except ignore_querylog
// and ignore_statistics, which keep theirs.
package adguardfake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/sdk/fixture"
)

const (
	clientsPath  = "/control/clients"
	addPath      = "/control/clients/add"
	updatePath   = "/control/clients/update"
	deletePath   = "/control/clients/delete"
	querylogPath = "/control/querylog"
	// querylogConfigPath is served from the scenario's fixture; its
	// anonymize_client_ip masks the log's client addresses.
	querylogConfigPath = "/control/querylog/config"
)

// DefaultScanCap is how many log lines AdGuard Home reads for one query
// log request without offset (maxFileScanEntries).
const DefaultScanCap = 50_000

// Options shape an AdGuard.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// Username and Password, when Username is set, are the only Basic auth
	// credentials accepted; anything else is refused with 403 Forbidden,
	// as AdGuard Home refuses an API call it can't authenticate.
	Username, Password string
	// QueryLog, when not nil, is the query log, newest first, instead of
	// the scenario's.
	QueryLog []map[string]any
	// ScanCap is how many log lines a query log request without offset
	// reads at most; 0 is AdGuard's DefaultScanCap.
	ScanCap int
}

// AdGuard is the fake, an http.Handler.
type AdGuard struct {
	fixtures http.Handler
	opts     Options

	mu      sync.Mutex
	clients []map[string]any
	// rest is clients.json without its clients (auto_clients, tags).
	rest    map[string]any
	queries []map[string]any
	log     []string
}

// New serves the scenario in dir: its control/clients.json and
// control/querylog.json (else its sibling baseline's) are the starting
// state.
func New(dir string, opts Options) (*AdGuard, error) {
	a := &AdGuard{fixtures: fixture.Scenario(dir, opts.Miss), opts: opts}
	var clients map[string]any
	if err := load(dir, clientsPath, &clients); err != nil {
		return nil, err
	}
	for _, c := range list(clients["clients"]) {
		a.clients = append(a.clients, normal(c, nil))
	}
	delete(clients, "clients")
	a.rest = clients
	var ql struct {
		Data []map[string]any `json:"data"`
	}
	if err := load(dir, querylogPath, &ql); err != nil {
		return nil, err
	}
	a.queries = ql.Data
	if opts.QueryLog != nil {
		a.queries = opts.QueryLog
	}
	var cfg struct {
		AnonymizeClientIP bool `json:"anonymize_client_ip"`
	}
	if err := load(dir, querylogConfigPath, &cfg); err != nil {
		return nil, err
	}
	if cfg.AnonymizeClientIP {
		a.queries = anonymized(a.queries)
	}
	return a, nil
}

// anonymized is log as an AdGuard anonymizing client addresses logs it:
// the last 2 bytes of an IPv4 address and the last 10 of an IPv6 address
// zeroed (querylog.AnonymizeIP).
func anonymized(log []map[string]any) []map[string]any {
	out := make([]map[string]any, len(log))
	for i, e := range log {
		e = maps.Clone(e)
		if ip, err := netip.ParseAddr(text(e["client"])); err == nil {
			b := ip.As16()
			n := 10
			if ip.Is4() {
				n = 2
			}
			clear(b[16-n:])
			masked := netip.AddrFrom16(b)
			if ip.Is4() {
				masked = masked.Unmap()
			}
			e["client"] = masked.String()
		}
		out[i] = e
	}
	return out
}

// load decodes the scenario's file for path, else baseline's.
func load(dir, path string, out any) error {
	b, err := fixture.Read(dir, path)
	if err != nil {
		return fmt.Errorf("adguardfake: %w", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("adguardfake: %s: %w", path, err)
	}
	return nil
}

// Clients returns the persistent clients as AdGuard holds them, in order.
func (a *AdGuard) Clients() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]map[string]any, len(a.clients))
	for i, c := range a.clients {
		out[i] = clone(c)
	}
	return out
}

// PersistentClient returns the persistent client named name, or nil.
func (a *AdGuard) PersistentClient(name string) map[string]any {
	for _, c := range a.Clients() {
		if c["name"] == name {
			return c
		}
	}
	return nil
}

// Log returns every request so far, in order, as "METHOD /path".
func (a *AdGuard) Log() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.log)
}

func (a *AdGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.log = append(a.log, r.Method+" "+r.URL.Path)
	a.mu.Unlock()

	if a.opts.Username != "" {
		if user, pass, ok := r.BasicAuth(); !ok || user != a.opts.Username || pass != a.opts.Password {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	switch r.Method + " " + r.URL.Path {
	case "GET " + clientsPath:
		a.mu.Lock()
		body := maps.Clone(a.rest)
		body["clients"] = a.clients
		b, err := json.Marshal(body)
		a.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	case "POST " + addPath:
		var c map[string]any
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, "failed to process request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		c = normal(c, nil)
		if msg := a.clash(c, ""); msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		a.clients = append(a.clients, c)
	case "POST " + updatePath:
		var req struct {
			Name string         `json:"name"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "failed to process request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		i := slices.IndexFunc(a.clients, func(c map[string]any) bool { return c["name"] == req.Name })
		if i < 0 {
			http.Error(w, fmt.Sprintf("client %q is not found", req.Name), http.StatusBadRequest)
			return
		}
		c := normal(req.Data, a.clients[i])
		if msg := a.clash(c, req.Name); msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		a.clients[i] = c
	case "POST " + deletePath:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "failed to process request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			http.Error(w, "client's name must be non-empty", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		i := slices.IndexFunc(a.clients, func(c map[string]any) bool { return c["name"] == req.Name })
		if i < 0 {
			http.Error(w, "Client not found", http.StatusBadRequest)
			return
		}
		a.clients = slices.Delete(a.clients, i, i+1)
	case "GET " + querylogPath:
		a.querylog(w, r)
	default:
		a.fixtures.ServeHTTP(w, r)
	}
}

// clash says why c can't be stored beside the other clients (skip is the
// name of the one it replaces), or "".
func (a *AdGuard) clash(c map[string]any, skip string) string {
	name, _ := c["name"].(string)
	ids := strs(c["ids"])
	if name == "" {
		return "invalid name"
	}
	if len(ids) == 0 {
		return "clients: id is required"
	}
	for _, other := range a.clients {
		if other["name"] == skip {
			continue
		}
		if other["name"] == name {
			return fmt.Sprintf("another client uses the same name %q", name)
		}
		for _, id := range strs(other["ids"]) {
			if slices.Contains(ids, id) {
				return fmt.Sprintf("another client %q uses the same ID %q", other["name"], id)
			}
		}
	}
	return ""
}

// querylog answers GET /control/querylog: search (an exact match when
// quoted: client address or name; else a substring of the domain, client
// or name), any of the repeated reasons, older_than, offset and limit
// (default 500), newest first.
//
// Like AdGuard, it reads lines until offset+limit match, and without
// offset (any number, 0 too, lifts it) no more than the scan cap, so a
// sparse filter can answer a short or empty page. oldest is the last row's
// time, else the last line read's, and empty once the log ended: after the
// last rows comes one more call with none. AdGuard's in-memory buffer,
// always read in full, isn't modelled.
func (a *AdGuard) querylog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := q.Get("search")
	exact := len(search) >= 2 && strings.HasPrefix(search, `"`) && strings.HasSuffix(search, `"`)
	if exact {
		search = search[1 : len(search)-1]
	}
	reasons := q["reason"]
	var olderThan time.Time
	if s := q.Get("older_than"); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			http.Error(w, "parsing older_than: "+err.Error(), http.StatusBadRequest)
			return
		}
		olderThan = t
	}
	limit := 500
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		limit = n
	}
	offset, scanCap := 0, a.opts.ScanCap
	if scanCap == 0 {
		scanCap = DefaultScanCap
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil {
		offset, scanCap = n, 0
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	lines := a.queries
	if !olderThan.IsZero() {
		lines = slices.DeleteFunc(slices.Clone(lines), func(e map[string]any) bool {
			t, _ := time.Parse(time.RFC3339Nano, text(e["time"]))
			return !t.Before(olderThan)
		})
	}
	data := []map[string]any{}
	// last is the last line read; nil once the log ended.
	var last map[string]any
	for n := 0; scanCap == 0 || n < scanCap; n++ {
		if n == len(lines) {
			last = nil
			break
		}
		e := lines[n]
		last = e
		if len(reasons) > 0 && !slices.Contains(reasons, text(e["reason"])) {
			continue
		}
		if search != "" && !matches(e, search, exact) {
			continue
		}
		data = append(data, e)
		if len(data) == offset+limit {
			break
		}
	}
	if offset > 0 {
		if len(data) <= offset {
			data, last = []map[string]any{}, nil
		} else {
			data = data[offset:]
		}
	}
	if len(data) > 0 {
		last = data[len(data)-1]
	}
	oldest := ""
	if last != nil {
		oldest = text(last["time"])
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "oldest": oldest})
}

func matches(e map[string]any, search string, exact bool) bool {
	info, _ := e["client_info"].(map[string]any)
	q, _ := e["question"].(map[string]any)
	if exact {
		return strings.EqualFold(text(e["client"]), search) || strings.EqualFold(text(info["name"]), search) || strings.EqualFold(text(q["name"]), search)
	}
	search = strings.ToLower(search)
	for _, s := range []string{text(e["client"]), text(info["name"]), text(q["name"])} {
		if strings.Contains(strings.ToLower(s), search) {
			return true
		}
	}
	return false
}

// zero is every Client field AdGuard keeps, at its zero value.
var zero = map[string]any{
	"name": "", "ids": []any{}, "tags": []any{}, "upstreams": []any{},
	"use_global_settings": false, "filtering_enabled": false, "parental_enabled": false,
	"safebrowsing_enabled": false, "safesearch_enabled": false,
	"safe_search":                 map[string]any{"enabled": false},
	"use_global_blocked_services": false, "blocked_services": []any{},
	"blocked_services_schedule": map[string]any{"time_zone": "Local"},
	"upstreams_cache_enabled":   false, "upstreams_cache_size": float64(0),
	"ignore_querylog": false, "ignore_statistics": false,
}

// normal is c as AdGuard stores it: only the Client fields, a field left
// out at its zero value, except the two "ignore" flags, which keep old's.
func normal(c, old map[string]any) map[string]any {
	out := map[string]any{}
	for k, z := range zero {
		v, ok := c[k]
		switch {
		case ok && v != nil:
			out[k] = v
		case old != nil && (k == "ignore_querylog" || k == "ignore_statistics"):
			out[k] = old[k]
		default:
			out[k] = z
		}
	}
	return clone(out)
}

func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func list(v any) []map[string]any {
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func strs(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		out = append(out, text(x))
	}
	return out
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
