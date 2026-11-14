// Package cloudflarefake is a stateful fake of the parts of Cloudflare's
// API v4 cloudflare-api uses, for its tests, cloudflare-tunnel's, fakelab
// and `make smoke-writes`'s tests. It keeps one account's remotely managed
// Tunnel with its connectors and its ingress configuration (GET, PUT), the
// account's zones and their DNS records (list, create, patch, delete),
// checks the API token, and records every request in order; anything
// else is served from a scenario's fixtures (fixture.Scenario).
//
// Like Cloudflare, every answer is the {success, errors, messages,
// result} envelope; a PUT of the configuration replaces it whole and
// raises its version, and is refused unless its last ingress rule is a
// catch-all (no hostname, no path); a CNAME can't share its name with an
// A, AAAA or CNAME record, nor they with it; a wrong account or Tunnel ID
// is "Could not route".
package cloudflarefake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/sdk/fixture"
)

// The fixtures' account, Tunnel and zone.
const (
	AccountID = "9a7806061c88ada191ed06f989cc3dac"
	TunnelID  = "c1744f8b-faa1-48a4-9e5c-02ac921467fa"
	// ZoneID is home.example's.
	ZoneID = "023e105f4ecef8ad9ca31a8372d0c353"
	// Target is what the Tunnel's CNAMEs point at.
	Target = TunnelID + ".cfargotunnel.com"
)

// DefaultToken is the API token the fake accepts when Options.Token is
// empty.
const DefaultToken = "cloudflarefake"

var (
	tunnelPath      = "/accounts/" + AccountID + "/cfd_tunnel/" + TunnelID
	connectionsPath = tunnelPath + "/connections"
	configPath      = tunnelPath + "/configurations"
)

const zonesPath = "/zones"

// Options shape a Cloudflare.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// Token is the one API token accepted; empty for DefaultToken.
	Token string
}

// Cloudflare is the fake, an http.Handler.
type Cloudflare struct {
	fixtures http.Handler
	opts     Options

	mu          sync.Mutex
	tunnel      map[string]any
	connections any
	config      map[string]any
	zones       []map[string]any
	records     map[string][]map[string]any
	fail        map[string]failure
	log         []string
}

// failure is an injected error answer.
type failure struct {
	code    int
	message string
}

// New serves the scenario in dir: its Tunnel, connections, configuration,
// zones and each zone's DNS records (each else its sibling baseline's)
// are the starting state.
func New(dir string, opts Options) (*Cloudflare, error) {
	if opts.Token == "" {
		opts.Token = DefaultToken
	}
	f := &Cloudflare{fixtures: fixture.Scenario(dir, opts.Miss), opts: opts, records: map[string][]map[string]any{}, fail: map[string]failure{}}
	if err := seed(dir, tunnelPath, &f.tunnel); err != nil {
		return nil, err
	}
	if err := seed(dir, connectionsPath, &f.connections); err != nil {
		return nil, err
	}
	if err := seed(dir, configPath, &f.config); err != nil {
		return nil, err
	}
	if err := seed(dir, zonesPath, &f.zones); err != nil {
		return nil, err
	}
	for _, z := range f.zones {
		id := text(z["id"])
		var recs []map[string]any
		if err := seed(dir, zonesPath+"/"+id+"/dns_records", &recs); err != nil {
			return nil, err
		}
		f.records[id] = recs
	}
	return f, nil
}

// seed reads the result of path's fixture in dir, else in its sibling
// baseline.
func seed(dir, path string, out any) error {
	b, err := fixture.Read(dir, path)
	if err != nil {
		return fmt.Errorf("cloudflarefake: %w", err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("cloudflarefake: %s: %w", path, err)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("cloudflarefake: %s: %w", path, err)
	}
	return nil
}

// Log is every request so far, "METHOD /path?query", in order.
func (f *Cloudflare) Log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

// Writes is every request so far but the GETs, in order.
func (f *Cloudflare) Writes() []string {
	var out []string
	for _, r := range f.Log() {
		if !strings.HasPrefix(r, "GET ") {
			out = append(out, r)
		}
	}
	return out
}

// FailNext makes the next request "METHOD /path" (no query) answer code
// with message as Cloudflare's error, changing nothing.
func (f *Cloudflare) FailNext(request string, code int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[request] = failure{code: code, message: message}
}

// Ingress is the Tunnel's ingress rules as stored, catch-all included.
func (f *Cloudflare) Ingress() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(rulesOf(f.config))
}

// Version is the configuration's version.
func (f *Cloudflare) Version() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int(number(f.config["version"]))
}

// EditIngress changes the ingress rules as the Operator would in
// Cloudflare's dashboard, raising the version.
func (f *Cloudflare) EditIngress(edit func([]map[string]any) []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg, _ := f.config["config"].(map[string]any)
	cfg["ingress"] = toAny(edit(clone(rulesOf(f.config))))
	f.config["version"] = number(f.config["version"]) + 1
}

// Records is a zone's DNS records as stored.
func (f *Cloudflare) Records(zone string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.records[zone])
}

// AddRecord adds a DNS record to a zone as the Operator would, with a new
// id, which it returns.
func (f *Cloudflare) AddRecord(zone, name, typ, content string, proxied bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := newRecord(zone, f.zoneName(zone), map[string]any{"name": name, "type": typ, "content": content, "proxied": proxied})
	f.records[zone] = append(f.records[zone], r)
	return text(r["id"])
}

func (f *Cloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		entry += "?" + r.URL.RawQuery
	}
	f.log = append(f.log, entry)

	if r.Header.Get("Authorization") != "Bearer "+f.opts.Token {
		refuse(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	if fl, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		delete(f.fail, r.Method+" "+r.URL.Path)
		refuse(w, fl.code, 1000, fl.message)
		return
	}

	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/accounts/"):
		f.account(w, r)
	case path == zonesPath:
		if r.Method != http.MethodGet {
			refuse(w, http.StatusMethodNotAllowed, 10405, "Method not allowed")
			return
		}
		f.listZones(w, r)
	case strings.HasPrefix(path, zonesPath+"/"):
		f.zone(w, r)
	default:
		f.fixtures.ServeHTTP(w, r)
	}
}

// account serves the Tunnel's endpoints, and "Could not route" for any
// other account or Tunnel.
func (f *Cloudflare) account(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == tunnelPath && r.Method == http.MethodGet:
		answer(w, f.tunnel, nil)
	case r.URL.Path == connectionsPath && r.Method == http.MethodGet:
		n := len(asList(f.connections))
		answer(w, f.connections, map[string]any{"count": n, "page": 1, "per_page": 20, "total_count": n})
	case r.URL.Path == configPath && r.Method == http.MethodGet:
		answer(w, f.config, nil)
	case r.URL.Path == configPath && r.Method == http.MethodPut:
		f.putConfig(w, r)
	default:
		refuse(w, http.StatusNotFound, 7003, "Could not route to "+r.URL.Path+", perhaps your object identifier is invalid?")
	}
}

func (f *Cloudflare) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Config == nil {
		refuse(w, http.StatusBadRequest, 1001, "Invalid request: config is required")
		return
	}
	if remote, _ := f.tunnel["remote_config"].(bool); !remote {
		refuse(w, http.StatusBadRequest, 1056, "Tunnel is not remotely managed")
		return
	}
	rules := asList(body.Config["ingress"])
	if len(rules) == 0 {
		refuse(w, http.StatusBadRequest, 1055, "Validation failed: At least one ingress rule is required")
		return
	}
	for i, x := range rules {
		rule, _ := x.(map[string]any)
		if text(rule["service"]) == "" {
			refuse(w, http.StatusBadRequest, 1055, fmt.Sprintf("Validation failed: Rule #%d has no service", i+1))
			return
		}
	}
	last, _ := rules[len(rules)-1].(map[string]any)
	if text(last["hostname"]) != "" || text(last["path"]) != "" {
		refuse(w, http.StatusBadRequest, 1055, "Validation failed: The last ingress rule must match all URLs (i.e. it should not have a hostname or path)")
		return
	}
	f.config["config"] = body.Config
	f.config["version"] = number(f.config["version"]) + 1
	f.config["created_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	answer(w, f.config, nil)
}

func (f *Cloudflare) listZones(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var out []map[string]any
	for _, z := range f.zones {
		acct, _ := z["account"].(map[string]any)
		if name := q.Get("name"); name != "" && text(z["name"]) != name {
			continue
		}
		if id := q.Get("account.id"); id != "" && text(acct["id"]) != id {
			continue
		}
		out = append(out, z)
	}
	page(w, q.Get("page"), q.Get("per_page"), 50, out)
}

// zone serves a zone's DNS records.
func (f *Cloudflare) zone(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, zonesPath+"/"), "/")
	zone := parts[0]
	if _, ok := f.records[zone]; !ok || len(parts) < 2 || parts[1] != "dns_records" || len(parts) > 3 {
		refuse(w, http.StatusNotFound, 7003, "Could not route to "+r.URL.Path+", perhaps your object identifier is invalid?")
		return
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			f.listRecords(w, r, zone)
		case http.MethodPost:
			f.createRecord(w, r, zone)
		default:
			refuse(w, http.StatusMethodNotAllowed, 10405, "Method not allowed")
		}
		return
	}
	id := parts[2]
	i := slices.IndexFunc(f.records[zone], func(rec map[string]any) bool { return text(rec["id"]) == id })
	if i < 0 {
		refuse(w, http.StatusNotFound, 81044, "Record does not exist.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		answer(w, f.records[zone][i], nil)
	case http.MethodPatch:
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			refuse(w, http.StatusBadRequest, 1001, "Invalid request body")
			return
		}
		rec := maps.Clone(f.records[zone][i])
		for _, k := range []string{"name", "type", "content", "proxied", "ttl", "comment"} {
			if v, ok := patch[k]; ok {
				rec[k] = v
			}
		}
		if msg := f.conflict(zone, rec, id); msg != "" {
			refuse(w, http.StatusBadRequest, 81053, msg)
			return
		}
		rec["modified_on"] = time.Now().UTC().Format(time.RFC3339Nano)
		f.records[zone][i] = rec
		answer(w, rec, nil)
	case http.MethodDelete:
		f.records[zone] = slices.Delete(f.records[zone], i, i+1)
		answer(w, map[string]any{"id": id}, nil)
	default:
		refuse(w, http.StatusMethodNotAllowed, 10405, "Method not allowed")
	}
}

func (f *Cloudflare) listRecords(w http.ResponseWriter, r *http.Request, zone string) {
	q := r.URL.Query()
	var out []map[string]any
	for _, rec := range f.records[zone] {
		if name := q.Get("name"); name != "" && text(rec["name"]) != name {
			continue
		}
		if typ := q.Get("type"); typ != "" && text(rec["type"]) != typ {
			continue
		}
		out = append(out, rec)
	}
	page(w, q.Get("page"), q.Get("per_page"), 5000000, out)
}

func (f *Cloudflare) createRecord(w http.ResponseWriter, r *http.Request, zone string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		refuse(w, http.StatusBadRequest, 1001, "Invalid request body")
		return
	}
	name := strings.ToLower(text(body["name"]))
	zn := f.zoneName(zone)
	switch {
	case text(body["type"]) == "":
		refuse(w, http.StatusBadRequest, 9101, "DNS record type is invalid.")
		return
	case name == "" || (name != zn && !strings.HasSuffix(name, "."+zn)):
		refuse(w, http.StatusBadRequest, 9005, "Content for "+text(body["type"])+" record is invalid. Must be a name in the zone "+zn+".")
		return
	case text(body["content"]) == "":
		refuse(w, http.StatusBadRequest, 9005, "Content for "+text(body["type"])+" record is invalid.")
		return
	}
	body["name"] = name
	rec := newRecord(zone, zn, body)
	if msg := f.conflict(zone, rec, ""); msg != "" {
		refuse(w, http.StatusBadRequest, 81053, msg)
		return
	}
	f.records[zone] = append(f.records[zone], rec)
	answer(w, rec, nil)
}

// conflict is Cloudflare's refusal of rec beside the zone's other records
// (but self's): a CNAME shares its name with no A, AAAA or CNAME, nor
// they with a CNAME. Empty when there is none.
func (f *Cloudflare) conflict(zone string, rec map[string]any, self string) string {
	address := func(t string) bool { return t == "A" || t == "AAAA" || t == "CNAME" }
	for _, o := range f.records[zone] {
		if text(o["id"]) == self || !strings.EqualFold(text(o["name"]), text(rec["name"])) {
			continue
		}
		if (text(rec["type"]) == "CNAME" && address(text(o["type"]))) || (text(o["type"]) == "CNAME" && address(text(rec["type"]))) {
			return "An A, AAAA, or CNAME record with that host already exists. For more details, refer to <https://developers.cloudflare.com/dns/manage-dns-records/troubleshooting/records-with-same-name/>."
		}
	}
	return ""
}

func (f *Cloudflare) zoneName(zone string) string {
	for _, z := range f.zones {
		if text(z["id"]) == zone {
			return text(z["name"])
		}
	}
	return ""
}

func newRecord(zone, zoneName string, fields map[string]any) map[string]any {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rec := map[string]any{
		"id": hex.EncodeToString(b), "zone_id": zone, "zone_name": zoneName,
		"proxiable": true, "proxied": false, "ttl": 1.0, "settings": map[string]any{}, "meta": map[string]any{},
		"comment": nil, "tags": []any{}, "created_on": now, "modified_on": now,
	}
	for _, k := range []string{"name", "type", "content", "proxied", "ttl", "comment"} {
		if v, ok := fields[k]; ok {
			rec[k] = v
		}
	}
	return rec
}

// page answers one page of all, as Cloudflare's lists do.
func page(w http.ResponseWriter, pageQ, perPageQ string, maxPer int, all []map[string]any) {
	p, _ := strconv.Atoi(pageQ)
	per, _ := strconv.Atoi(perPageQ)
	p = max(p, 1)
	if per <= 0 {
		per = 20
	}
	per = min(per, maxPer)
	from := min((p-1)*per, len(all))
	to := min(from+per, len(all))
	pages := (len(all) + per - 1) / per
	result := all[from:to]
	if result == nil {
		result = []map[string]any{}
	}
	answer(w, result, map[string]any{"page": p, "per_page": per, "count": len(result), "total_count": len(all), "total_pages": pages})
}

func answer(w http.ResponseWriter, result, info any) {
	body := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		body["result_info"] = info
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false, "errors": []any{map[string]any{"code": code, "message": message}}, "messages": []any{}, "result": nil,
	})
}

func rulesOf(config map[string]any) []map[string]any {
	cfg, _ := config["config"].(map[string]any)
	var out []map[string]any
	for _, x := range asList(cfg["ingress"]) {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func toAny(rules []map[string]any) []any {
	out := make([]any, len(rules))
	for i, r := range rules {
		out[i] = r
	}
	return out
}

func clone(in []map[string]any) []map[string]any {
	b, _ := json.Marshal(in)
	var out []map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

func number(v any) float64 {
	n, _ := v.(float64)
	return n
}
