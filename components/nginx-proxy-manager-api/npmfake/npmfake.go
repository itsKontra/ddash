// Package npmfake is a stateful fake of Nginx Proxy Manager's v2.10 API
// for nginx-proxy-manager-api's tests, fakelab and `make smoke-writes`'s
// tests. It keeps ddash's user with its password, 2FA and permissions,
// the tokens it issued with their expiry, the Proxy Hosts (create, update,
// delete), the certificates and the version GET /api reports, and records
// every request in order; anything else is served from a scenario's
// fixtures (fixture.Scenario).
//
// Like v2.10 it sorts domain_names on every save, refuses a body with a
// field its schema lacks, forces Force SSL and HTTP/2 off without a
// certificate and HSTS off without Force SSL, refuses a domain name any
// Proxy Host, Redirection Host or 404 Host holds with a 400, and answers
// a save nginx rejects (RejectNextSave) with meta.nginx_online false and
// nginx's error, the row saved all the same. Before v2.12 its flags read
// 0/1, from v2.12 true/false.
package npmfake

import (
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

const (
	versionPath      = "/api"
	tokensPath       = "/api/tokens"
	mePath           = "/api/users/me"
	proxyHostsPath   = "/api/nginx/proxy-hosts"
	certificatesPath = "/api/nginx/certificates"
)

// DefaultPassword is the user's password when Options.Password is empty.
const DefaultPassword = "npmfake"

// Options shape an NPM.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// Password is the user's password; empty for DefaultPassword.
	Password string
	// TokenLifetime is how long a token lasts; 0 for Nginx Proxy
	// Manager's one day.
	TokenLifetime time.Duration
}

// User is ddash's user as the fake holds it.
type User struct {
	ID              int
	Email, Password string
	// Admin users pass every permission check.
	Admin bool
	// TwoFactor makes a sign-in answer with a 2FA challenge (v2.13.6+).
	TwoFactor bool
	// Visibility is "all" or "user" (only the items it owns).
	Visibility string
	// ProxyHosts and Certificates are "manage", "view" or "hidden".
	ProxyHosts, Certificates string
}

// NPM is the fake, an http.Handler.
type NPM struct {
	fixtures http.Handler
	opts     Options

	mu           sync.Mutex
	version      [3]int
	user         User
	userRaw      map[string]any
	tokens       map[string]time.Time
	issued       int
	hosts        []map[string]any
	nextID       int
	certificates []map[string]any
	// others are the domain names of each Redirection Host and 404 Host.
	others [][]string
	reject string
	log    []string
}

// New serves the scenario in dir: its api.json (the version),
// api/users/me.json (the user), api/nginx/proxy-hosts.json and
// api/nginx/certificates.json (each else its sibling baseline's) are the
// starting state.
func New(dir string, opts Options) (*NPM, error) {
	if opts.Password == "" {
		opts.Password = DefaultPassword
	}
	if opts.TokenLifetime == 0 {
		opts.TokenLifetime = 24 * time.Hour
	}
	f := &NPM{fixtures: fixture.Scenario(dir, opts.Miss), opts: opts, tokens: map[string]time.Time{}}

	var api struct {
		Version struct{ Major, Minor, Revision int } `json:"version"`
	}
	if err := seed(dir, versionPath, &api); err != nil {
		return nil, err
	}
	f.version = [3]int{api.Version.Major, api.Version.Minor, api.Version.Revision}

	if err := seed(dir, mePath, &f.userRaw); err != nil {
		return nil, err
	}
	perms, _ := f.userRaw["permissions"].(map[string]any)
	roles, _ := f.userRaw["roles"].([]any)
	f.user = User{
		ID:           int(number(f.userRaw["id"])),
		Email:        text(f.userRaw["email"]),
		Password:     opts.Password,
		Admin:        slices.Contains(roles, any("admin")),
		Visibility:   text(perms["visibility"]),
		ProxyHosts:   text(perms["proxy_hosts"]),
		Certificates: text(perms["certificates"]),
	}

	if err := seed(dir, proxyHostsPath, &f.hosts); err != nil {
		return nil, err
	}
	for _, h := range f.hosts {
		f.nextID = max(f.nextID, int(number(h["id"])))
		for _, k := range flags {
			h[k] = truthy(h[k])
		}
	}
	f.nextID++
	if err := seed(dir, certificatesPath, &f.certificates); err != nil {
		return nil, err
	}
	return f, nil
}

func seed(dir, path string, out any) error {
	b, err := fixture.Read(dir, path)
	if err != nil {
		return fmt.Errorf("npmfake: %w", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("npmfake: %s: %w", path, err)
	}
	return nil
}

// EditUser changes ddash's user, e.g. its permissions or password.
func (f *NPM) EditUser(edit func(*User)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(&f.user)
}

// SetVersion changes the version GET /api reports, and with it how flags
// read (0/1 before 2.12).
func (f *NPM) SetVersion(major, minor, revision int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = [3]int{major, minor, revision}
}

// ExpireTokens makes every token issued so far expired.
func (f *NPM) ExpireTokens() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for t := range f.tokens {
		f.tokens[t] = time.Now().Add(-time.Second)
	}
}

// AddRedirectionHost adds a Redirection Host holding domains; AddDeadHost
// a 404 Host. ddash never reads either: they only hold domain names.
func (f *NPM) AddRedirectionHost(domains ...string) { f.addOther(domains) }

// AddDeadHost adds a 404 Host holding domains.
func (f *NPM) AddDeadHost(domains ...string) { f.addOther(domains) }

func (f *NPM) addOther(domains []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.others = append(f.others, domains)
}

// RejectNextSave makes nginx reject the config of the next Proxy Host
// saved with nginxErr: the row is saved, Offline.
func (f *NPM) RejectNextSave(nginxErr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reject = nginxErr
}

// ProxyHost returns the Proxy Host with id as stored, nil when there is
// none.
func (f *NPM) ProxyHost(id int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.find(id); i >= 0 {
		return clone(f.hosts[i])
	}
	return nil
}

// ProxyHosts returns every Proxy Host as stored, by domain names.
func (f *NPM) ProxyHosts() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.hosts))
	for _, h := range f.sorted() {
		out = append(out, clone(h))
	}
	return out
}

// AddProxyHost adds a Proxy Host as the Operator would in Nginx Proxy
// Manager's UI: enabled, online, no certificate, with edit applied to
// its row, its domain_names then sorted. It returns the new id.
func (f *NPM) AddProxyHost(edit func(row map[string]any)) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	f.nextID++
	row := map[string]any{
		"id": id, "created_on": time.Now().UTC().Format(time.DateTime), "owner_user_id": 1,
		"domain_names": []any{}, "forward_scheme": "http", "forward_host": "", "forward_port": 80,
		"access_list_id": 0, "certificate_id": 0, "advanced_config": "", "locations": []any{},
		"meta":       map[string]any{"letsencrypt_agree": false, "dns_challenge": false, "nginx_online": true, "nginx_err": nil},
		"ssl_forced": false, "caching_enabled": false, "block_exploits": false, "allow_websocket_upgrade": false,
		"http2_support": false, "enabled": true, "hsts_enabled": false, "hsts_subdomains": false,
	}
	f.hosts = append(f.hosts, f.edited(row, edit))
	return id
}

// EditProxyHost changes the Proxy Host with id as the Operator would in
// Nginx Proxy Manager's UI: edit gets its stored row (flags as bools),
// and its domain_names are sorted after. It reports whether there was one.
func (f *NPM) EditProxyHost(id int, edit func(row map[string]any)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(id)
	if i < 0 {
		return false
	}
	f.hosts[i] = f.edited(clone(f.hosts[i]), edit)
	return true
}

// DeleteProxyHost deletes the Proxy Host with id as the Operator would in
// Nginx Proxy Manager's UI, reporting whether there was one.
func (f *NPM) DeleteProxyHost(id int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(id)
	if i >= 0 {
		f.hosts = slices.Delete(f.hosts, i, i+1)
	}
	return i >= 0
}

func (f *NPM) edited(row map[string]any, edit func(row map[string]any)) map[string]any {
	edit(row)
	names := texts(row["domain_names"])
	slices.Sort(names)
	row["domain_names"] = names
	row["modified_on"] = time.Now().UTC().Format(time.DateTime)
	return row
}

// Log returns each request as "METHOD /path", in order.
func (f *NPM) Log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *NPM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r.Method+" "+r.URL.Path)

	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && path == versionPath:
		reply(w, http.StatusOK, map[string]any{"status": "OK", "version": map[string]int{"major": f.version[0], "minor": f.version[1], "revision": f.version[2]}})
		return
	case r.Method == http.MethodPost && path == tokensPath:
		f.signIn(w, r)
		return
	case path == mePath, path == proxyHostsPath, strings.HasPrefix(path, proxyHostsPath+"/"), path == certificatesPath:
	default:
		f.mu.Unlock()
		f.fixtures.ServeHTTP(w, r)
		f.mu.Lock()
		return
	}

	if !f.authorized(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && path == mePath:
		me := clone(f.userRaw)
		me["email"] = f.user.Email
		if strings.Contains(r.URL.Query().Get("expand"), "permissions") {
			perms, _ := me["permissions"].(map[string]any)
			perms = maps.Clone(perms)
			if perms == nil {
				perms = map[string]any{}
			}
			perms["visibility"], perms["proxy_hosts"], perms["certificates"] = f.user.Visibility, f.user.ProxyHosts, f.user.Certificates
			me["permissions"] = perms
		} else {
			delete(me, "permissions")
		}
		roles := []string{}
		if f.user.Admin {
			roles = append(roles, "admin")
		}
		me["roles"] = roles
		reply(w, http.StatusOK, me)
	case path == certificatesPath && r.Method == http.MethodGet:
		if !f.may(w, f.user.Certificates, false) {
			return
		}
		list := []map[string]any{}
		for _, c := range f.certificates {
			if f.visible(c) {
				list = append(list, c)
			}
		}
		slices.SortStableFunc(list, func(a, b map[string]any) int { return strings.Compare(text(a["nice_name"]), text(b["nice_name"])) })
		reply(w, http.StatusOK, list)
	case path == proxyHostsPath && r.Method == http.MethodGet:
		if !f.may(w, f.user.ProxyHosts, false) {
			return
		}
		list := []map[string]any{}
		for _, h := range f.sorted() {
			if f.visible(h) {
				list = append(list, f.out(h))
			}
		}
		reply(w, http.StatusOK, list)
	case path == proxyHostsPath && r.Method == http.MethodPost:
		if f.may(w, f.user.ProxyHosts, true) {
			f.save(w, r, -1)
		}
	case strings.HasPrefix(path, proxyHostsPath+"/"):
		id, err := strconv.Atoi(strings.TrimPrefix(path, proxyHostsPath+"/"))
		i := f.find(id)
		if err != nil || i < 0 || !f.visible(f.hosts[i]) {
			fail(w, http.StatusNotFound, "Not Found - "+strings.TrimPrefix(path, proxyHostsPath+"/"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			if f.may(w, f.user.ProxyHosts, false) {
				reply(w, http.StatusOK, f.out(f.hosts[i]))
			}
		case http.MethodPut:
			if f.may(w, f.user.ProxyHosts, true) {
				f.save(w, r, i)
			}
		case http.MethodDelete:
			if f.may(w, f.user.ProxyHosts, true) {
				f.hosts = slices.Delete(f.hosts, i, i+1)
				reply(w, http.StatusOK, true)
			}
		default:
			fail(w, http.StatusNotFound, "Not Found")
		}
	default:
		fail(w, http.StatusNotFound, "Not Found")
	}
}

// signIn is POST /api/tokens: identity and secret for a token.
func (f *NPM) signIn(w http.ResponseWriter, r *http.Request) {
	body, ok := decode(w, r, []string{"identity", "secret", "scope"}, []string{"identity", "secret"})
	if !ok {
		return
	}
	if !strings.EqualFold(text(body["identity"]), f.user.Email) || text(body["secret"]) != f.user.Password {
		fail(w, http.StatusBadRequest, "Invalid email or password")
		return
	}
	if f.user.TwoFactor {
		reply(w, http.StatusOK, map[string]any{"requires_2fa": true, "challenge_token": "npmfake-challenge"})
		return
	}
	f.issued++
	token := fmt.Sprintf("npmfake-token-%d", f.issued)
	expires := time.Now().Add(f.opts.TokenLifetime)
	f.tokens[token] = expires
	reply(w, http.StatusOK, map[string]any{"token": token, "expires": expires.UTC().Format("2006-01-02T15:04:05.000Z")})
}

// authorized checks the request's bearer token as Nginx Proxy Manager
// does: none is a 403, an unknown one a 500 (jsonwebtoken's error), an
// expired one a 400.
func (f *NPM) authorized(w http.ResponseWriter, r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		fail(w, http.StatusForbidden, "Permission Denied")
		return false
	}
	expires, known := f.tokens[token]
	switch {
	case !known:
		fail(w, http.StatusInternalServerError, "Internal Error")
		return false
	case time.Now().After(expires):
		fail(w, http.StatusBadRequest, "Token has expired")
		return false
	}
	return true
}

// may checks a permission ("manage", "view" or "hidden") for a read or a
// write; admins may do anything.
func (f *NPM) may(w http.ResponseWriter, permission string, write bool) bool {
	if f.user.Admin || permission == "manage" || permission == "view" && !write {
		return true
	}
	fail(w, http.StatusForbidden, "Permission Denied")
	return false
}

// visible is whether the user sees an item: all of them, or with
// visibility "user" only those it owns.
func (f *NPM) visible(item map[string]any) bool {
	return f.user.Admin || f.user.Visibility != "user" || int(number(item["owner_user_id"])) == f.user.ID
}

// writable are the fields v2.10's Proxy Host schema accepts in a POST or
// PUT body; required, those a POST must have.
var (
	writable = []string{"domain_names", "forward_scheme", "forward_host", "forward_port", "access_list_id", "certificate_id", "ssl_forced", "hsts_enabled", "hsts_subdomains", "http2_support", "block_exploits", "caching_enabled", "allow_websocket_upgrade", "advanced_config", "meta", "locations"}
	required = []string{"domain_names", "forward_scheme", "forward_host", "forward_port"}
	flags    = []string{"ssl_forced", "caching_enabled", "block_exploits", "allow_websocket_upgrade", "http2_support", "enabled", "hsts_enabled", "hsts_subdomains"}
)

// save creates (i < 0) or updates the Proxy Host at i: the duplicate
// check, the row written, then nginx's config test.
func (f *NPM) save(w http.ResponseWriter, r *http.Request, i int) {
	var need []string
	if i < 0 {
		need = required
	}
	body, ok := decode(w, r, writable, need)
	if !ok {
		return
	}
	if msg := check(body); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	self := -1
	if i >= 0 {
		self = int(number(f.hosts[i]["id"]))
	}
	if names, ok := body["domain_names"].([]any); ok {
		for _, n := range names {
			if f.taken(text(n), self) {
				fail(w, http.StatusBadRequest, text(n)+" is already in use")
				return
			}
		}
	}

	now := time.Now().UTC().Format(time.DateTime)
	var row map[string]any
	if i < 0 {
		row = map[string]any{
			"id": f.nextID, "created_on": now, "owner_user_id": f.user.ID,
			"access_list_id": 0, "certificate_id": 0, "advanced_config": "",
			"meta": map[string]any{"letsencrypt_agree": false, "dns_challenge": false}, "locations": []any{},
			"ssl_forced": false, "caching_enabled": false, "block_exploits": false, "allow_websocket_upgrade": false,
			"http2_support": false, "enabled": true, "hsts_enabled": false, "hsts_subdomains": false,
		}
		f.nextID++
	} else {
		row = clone(f.hosts[i])
	}
	for k, v := range body {
		if k == "meta" {
			meta, _ := row["meta"].(map[string]any)
			meta = maps.Clone(meta)
			if meta == nil {
				meta = map[string]any{}
			}
			maps.Copy(meta, v.(map[string]any))
			row["meta"] = meta
			continue
		}
		row[k] = v
	}
	row["modified_on"] = now
	names := texts(row["domain_names"])
	slices.Sort(names)
	row["domain_names"] = names
	// cleanSslHstsData
	if number(row["certificate_id"]) == 0 {
		row["ssl_forced"], row["http2_support"] = false, false
	}
	if !truthy(row["ssl_forced"]) {
		row["hsts_enabled"] = false
	}
	if !truthy(row["hsts_enabled"]) {
		row["hsts_subdomains"] = false
	}
	for _, k := range flags {
		row[k] = truthy(row[k])
	}
	if truthy(row["enabled"]) {
		meta := maps.Clone(row["meta"].(map[string]any))
		if f.reject != "" {
			meta["nginx_online"], meta["nginx_err"] = false, f.reject
			f.reject = ""
		} else {
			meta["nginx_online"], meta["nginx_err"] = true, nil
		}
		row["meta"] = meta
	}

	code := http.StatusOK
	if i < 0 {
		f.hosts = append(f.hosts, row)
		code = http.StatusCreated
	} else {
		f.hosts[i] = row
	}
	reply(w, code, f.out(row))
}

// check validates the types of a Proxy Host body as v2.10's schema does
// (AJV with coerceTypes, so flags may be 0/1).
func check(body map[string]any) string {
	if v, ok := body["domain_names"]; ok {
		names, ok := v.([]any)
		if !ok || len(names) < 1 || len(names) > 100 {
			return "data.domain_names should NOT have fewer than 1 items"
		}
		seen := map[string]bool{}
		for _, n := range names {
			s, ok := n.(string)
			if !ok || s == "" || seen[s] {
				return "data.domain_names should NOT have duplicate items"
			}
			seen[s] = true
		}
	}
	if v, ok := body["forward_scheme"]; ok && v != "http" && v != "https" {
		return "data.forward_scheme should be equal to one of the allowed values"
	}
	if v, ok := body["forward_host"]; ok {
		if s, ok := v.(string); !ok || s == "" || len(s) > 255 {
			return "data.forward_host should NOT be shorter than 1 characters"
		}
	}
	if v, ok := body["forward_port"]; ok {
		if n, ok := v.(float64); !ok || n < 1 || n > 65535 || n != float64(int(n)) {
			return "data.forward_port should be <= 65535"
		}
	}
	if v, ok := body["certificate_id"]; ok {
		if n, ok := v.(float64); !ok || n < 0 || n != float64(int(n)) {
			return "data.certificate_id should be integer (npmfake doesn't fake \"new\")"
		}
	}
	for _, k := range flags {
		if v, ok := body[k]; ok {
			switch v {
			case true, false, float64(0), float64(1):
			default:
				return "data." + k + " should be boolean"
			}
		}
	}
	if v, ok := body["meta"]; ok {
		if _, ok := v.(map[string]any); !ok {
			return "data.meta should be object"
		}
	}
	return ""
}

// taken reports whether a Proxy Host other than self, a Redirection Host
// or a 404 Host holds name, ignoring case.
func (f *NPM) taken(name string, self int) bool {
	for _, h := range f.hosts {
		if int(number(h["id"])) == self {
			continue
		}
		for _, n := range texts(h["domain_names"]) {
			if strings.EqualFold(n, name) {
				return true
			}
		}
	}
	for _, other := range f.others {
		for _, n := range other {
			if strings.EqualFold(n, name) {
				return true
			}
		}
	}
	return false
}

// out is a row as the API answers it: flags as 0/1 before v2.12.
func (f *NPM) out(row map[string]any) map[string]any {
	out := clone(row)
	if f.version[0] > 2 || f.version[0] == 2 && f.version[1] >= 12 {
		for _, k := range flags {
			out[k] = truthy(row[k])
		}
		return out
	}
	for _, k := range flags {
		out[k] = 0
		if truthy(row[k]) {
			out[k] = 1
		}
	}
	return out
}

func (f *NPM) find(id int) int {
	return slices.IndexFunc(f.hosts, func(h map[string]any) bool { return int(number(h["id"])) == id })
}

// sorted is the Proxy Hosts by domain names, as the list query orders them.
func (f *NPM) sorted() []map[string]any {
	list := slices.Clone(f.hosts)
	slices.SortStableFunc(list, func(a, b map[string]any) int {
		return strings.Compare(strings.Join(texts(a["domain_names"]), ","), strings.Join(texts(b["domain_names"]), ","))
	})
	return list
}

// decode reads a JSON object body, refusing (400, as AJV words it) a
// field not in allowed and a missing one in need.
func decode(w http.ResponseWriter, r *http.Request, allowed, need []string) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
		fail(w, http.StatusBadRequest, "data should be object")
		return nil, false
	}
	for k := range body {
		if !slices.Contains(allowed, k) {
			fail(w, http.StatusBadRequest, "data should NOT have additional properties ("+k+")")
			return nil, false
		}
	}
	for _, k := range need {
		if _, ok := body[k]; !ok {
			fail(w, http.StatusBadRequest, "data should have required property '"+k+"'")
			return nil, false
		}
	}
	return body, true
}

func truthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return false
}

func number(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

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

// clone deep-copies a JSON object through JSON.
func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes Nginx Proxy Manager's error envelope.
func fail(w http.ResponseWriter, code int, message string) {
	reply(w, code, map[string]any{"error": map[string]any{"code": code, "message": message}})
}
