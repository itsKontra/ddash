package pfsensefake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	hostOverridesPath = "/api/v2/services/dns_resolver/host_overrides"
	hostOverridePath  = "/api/v2/services/dns_resolver/host_override"
	overrideAliasPath = "/api/v2/services/dns_resolver/host_override/alias"
	dnsApplyPath      = "/api/v2/services/dns_resolver/apply"
)

// AppliedDNS is the entry Log records when pending Host Override changes
// take effect (Unbound restarts), from a write's "apply": true or from
// POST services/dns_resolver/apply.
const AppliedDNS = "apply DNS Resolver"

// HostOverride is a DNS Resolver Host Override as the fake holds it.
type HostOverride struct {
	Host, Domain string
	IPs          []string
	Description  string
	Aliases      []HostOverrideAlias
}

// HostOverrideAlias is one of a Host Override's aliases.
type HostOverrideAlias struct {
	Host, Domain, Description string
}

// HostOverrides returns every Host Override in order, each with its
// aliases in order.
func (f *Firewall) HostOverrides() []HostOverride {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]HostOverride, 0, len(f.overrides))
	for _, h := range f.overrides {
		o := HostOverride{Host: text(h["host"]), Domain: text(h["domain"]), IPs: texts(h["ip"]), Description: text(h["descr"])}
		for _, a := range children(h) {
			o.Aliases = append(o.Aliases, HostOverrideAlias{Host: text(a["host"]), Domain: text(a["domain"]), Description: text(a["descr"])})
		}
		out = append(out, o)
	}
	return out
}

// EditHostOverrides changes the Host Overrides as the Operator would in
// pfSense's web UI, saved and applied: edit gets every Host Override in
// order and returns them as they should be, in that order.
func (f *Firewall) EditHostOverrides(edit func([]HostOverride) []HostOverride) {
	all := edit(f.HostOverrides())
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides = make([]map[string]any, 0, len(all))
	for _, o := range all {
		aliases := make([]any, 0, len(o.Aliases))
		for _, a := range o.Aliases {
			aliases = append(aliases, map[string]any{"host": a.Host, "domain": a.Domain, "descr": a.Description})
		}
		f.overrides = append(f.overrides, map[string]any{
			"host": o.Host, "domain": o.Domain, "ip": slices.Clone(o.IPs), "descr": o.Description, "aliases": aliases,
		})
	}
}

// DNSPending reports whether Host Override changes wait for an apply.
func (f *Firewall) DNSPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dnsPending
}

// FailDNSWrite makes the n-th DNS Resolver write from now (1: the next)
// fail with a 500 and change nothing; the writes before and after it
// succeed.
func (f *Firewall) FailDNSWrite(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failDNS = n
}

// serveDNSResolver answers the DNS Resolver's Host Override endpoints,
// reporting whether r was one.
func (f *Firewall) serveDNSResolver(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET " + hostOverridesPath:
		f.mu.Lock()
		b, err := json.Marshal(f.listOverrides())
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return true
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "GET " + dnsApplyPath:
		f.mu.Lock()
		applied := !f.dnsPending
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": applied})
	case "POST " + dnsApplyPath:
		time.Sleep(f.opts.ApplyDelay)
		f.mu.Lock()
		f.applyDNS()
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": true})
	case "POST " + hostOverridePath, "PATCH " + hostOverridePath, "DELETE " + hostOverridePath,
		"POST " + overrideAliasPath, "DELETE " + overrideAliasPath:
		f.dnsWrite(w, r)
	default:
		return false
	}
	return true
}

// dnsWrite runs one Host Override or alias write. As in pfSense, a DELETE
// takes its parameters (id, parent_id, apply) from the query string.
func (f *Firewall) dnsWrite(w http.ResponseWriter, r *http.Request) {
	params := map[string]any{}
	if r.Method == http.MethodDelete {
		for k, v := range r.URL.Query() {
			params[k] = v[0]
		}
	} else if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		reply(w, http.StatusBadRequest, "INVALID_JSON", "The request body is not valid JSON.", nil)
		return
	}
	f.begin()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writing--

	if f.failDNS > 0 {
		f.failDNS--
		if f.failDNS == 0 {
			reply(w, http.StatusInternalServerError, "PFSENSEFAKE_INJECTED", "pfsensefake failed this write as told.", nil)
			return
		}
	}
	var (
		code    int
		id, msg string
		out     map[string]any
	)
	switch {
	case r.URL.Path == hostOverridePath && r.Method == http.MethodPost:
		code, id, msg, out = f.writeOverride(-1, params)
	case r.URL.Path == hostOverridePath && r.Method == http.MethodPatch:
		i, ok := index(params["id"], len(f.overrides))
		if !ok {
			reply(w, http.StatusNotFound, "MODEL_OBJECT_NOT_FOUND", fmt.Sprintf("Object with ID `%v` does not exist.", params["id"]), nil)
			return
		}
		code, id, msg, out = f.writeOverride(i, params)
	case r.URL.Path == hostOverridePath:
		i, ok := index(params["id"], len(f.overrides))
		if !ok {
			reply(w, http.StatusNotFound, "MODEL_OBJECT_NOT_FOUND", fmt.Sprintf("Object with ID `%v` does not exist.", params["id"]), nil)
			return
		}
		out = f.overrides[i]
		out["id"] = i
		f.overrides = slices.Delete(f.overrides, i, i+1)
	default:
		p, ok := index(params["parent_id"], len(f.overrides))
		if !ok {
			reply(w, http.StatusNotFound, "MODEL_PARENT_NOT_FOUND", fmt.Sprintf("Parent object with ID `%v` does not exist.", params["parent_id"]), nil)
			return
		}
		if r.Method == http.MethodPost {
			code, id, msg, out = f.addOverrideAlias(p, params)
		} else {
			all := children(f.overrides[p])
			i, ok := index(params["id"], len(all))
			if !ok {
				reply(w, http.StatusNotFound, "MODEL_OBJECT_NOT_FOUND", fmt.Sprintf("Object with ID `%v` does not exist.", params["id"]), nil)
				return
			}
			out = all[i]
			setChildren(f.overrides[p], slices.Delete(all, i, i+1))
		}
	}
	if code != 0 {
		reply(w, code, id, msg, nil)
		return
	}
	f.dnsPending = true
	if params["apply"] == true || params["apply"] == "true" {
		f.applyDNS()
	}
	reply(w, http.StatusOK, "SUCCESS", "", out)
}

// writeOverride creates (i < 0) or updates the Host Override at i from
// params, re-sorts the list by host and answers the written one with its
// new id; f.mu is held.
func (f *Firewall) writeOverride(i int, params map[string]any) (code int, id, msg string, out map[string]any) {
	h := map[string]any{"host": "", "domain": "", "ip": []string{}, "descr": "", "aliases": []any{}}
	if i >= 0 {
		h = maps.Clone(f.overrides[i])
	} else {
		for _, k := range []string{"host", "domain", "ip"} {
			if _, ok := params[k]; !ok {
				return http.StatusBadRequest, "FIELD_IS_REQUIRED", fmt.Sprintf("Field `%s` is required.", k), nil
			}
		}
	}
	for _, k := range []string{"host", "domain", "ip", "descr", "aliases"} {
		if v, ok := params[k]; ok {
			h[k] = v
		}
	}
	if code, id, msg := checkOverride(h); code != 0 {
		return code, id, msg, nil
	}
	for j, other := range f.overrides {
		if j != i && sameName(other, h) {
			return http.StatusBadRequest, "MODEL_FIELDS_NOT_UNIQUE_TOGETHER", "Field(s) `host, domain` must be unique together.", nil
		}
	}
	all := slices.Clone(f.overrides)
	if i < 0 {
		all = append(all, h)
	} else {
		all[i] = h
	}
	// Writing a Host Override re-sorts its aliases too.
	setChildren(h, children(h))
	f.overrides = sortByHost(all)
	pos := slices.IndexFunc(f.overrides, func(x map[string]any) bool { return sameName(x, h) })
	out = maps.Clone(h)
	out["id"] = pos
	return 0, "", "", out
}

// addOverrideAlias appends an alias to the Host Override at p and
// re-sorts its aliases; f.mu is held.
func (f *Firewall) addOverrideAlias(p int, params map[string]any) (code int, id, msg string, out map[string]any) {
	for _, k := range []string{"host", "domain"} {
		if _, ok := params[k]; !ok {
			return http.StatusBadRequest, "FIELD_IS_REQUIRED", fmt.Sprintf("Field `%s` is required.", k), nil
		}
	}
	a := map[string]any{"host": params["host"], "domain": params["domain"], "descr": ""}
	if d, ok := params["descr"]; ok {
		a["descr"] = d
	}
	if !dnsName.MatchString(text(a["host"])) || text(a["domain"]) == "" || !dnsName.MatchString(text(a["domain"])) {
		return http.StatusBadRequest, "HOSTNAME_VALIDATOR_FAILED", "Field `host` or `domain` must be a valid hostname.", nil
	}
	all := children(f.overrides[p])
	for _, other := range all {
		if sameName(other, a) {
			return http.StatusBadRequest, "MODEL_FIELDS_NOT_UNIQUE_TOGETHER", "Field(s) `host, domain` must be unique together.", nil
		}
	}
	setChildren(f.overrides[p], append(all, a))
	sorted := children(f.overrides[p])
	out = maps.Clone(a)
	out["parent_id"] = p
	out["id"] = slices.IndexFunc(sorted, func(x map[string]any) bool { return sameName(x, a) })
	return 0, "", "", out
}

// dnsName is a hostname or one of its labels, as pfSense's
// HostnameValidator takes it; an empty host is allowed separately.
var dnsName = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// checkOverride validates a Host Override as pfSense does: a hostname
// host (or none), a domain and at least one IP address.
func checkOverride(h map[string]any) (code int, id, msg string) {
	if !dnsName.MatchString(text(h["host"])) || text(h["domain"]) == "" || !dnsName.MatchString(text(h["domain"])) {
		return http.StatusBadRequest, "HOSTNAME_VALIDATOR_FAILED", "Field `host` or `domain` must be a valid hostname."
	}
	ips := texts(h["ip"])
	if len(ips) == 0 {
		return http.StatusBadRequest, "FIELD_IS_REQUIRED", "Field `ip` is required."
	}
	for _, ip := range ips {
		if _, err := netip.ParseAddr(ip); err != nil {
			return http.StatusBadRequest, "IP_ADDRESS_VALIDATOR_FAILED", fmt.Sprintf("Field `ip` must be a valid IP address, received `%s`.", ip)
		}
	}
	return 0, "", ""
}

// listOverrides is the Host Overrides as GET lists them, with their ids;
// f.mu is held.
func (f *Firewall) listOverrides() []map[string]any {
	list := make([]map[string]any, len(f.overrides))
	for i, h := range f.overrides {
		list[i] = maps.Clone(h)
		list[i]["id"] = i
		aliases := []any{}
		for j, a := range children(h) {
			a = maps.Clone(a)
			a["parent_id"], a["id"] = i, j
			aliases = append(aliases, a)
		}
		list[i]["aliases"] = aliases
	}
	return list
}

// applyDNS makes pending Host Override changes take effect; f.mu is held.
func (f *Firewall) applyDNS() {
	if f.dnsPending {
		f.dnsPending = false
		f.log = append(f.log, AppliedDNS)
	}
}

func sameName(a, b map[string]any) bool {
	return strings.EqualFold(text(a["host"]), text(b["host"])) && strings.EqualFold(text(a["domain"]), text(b["domain"]))
}

// sortByHost sorts as pfSense's Model::sort does: by host, ascending,
// keeping equal hosts in order.
func sortByHost(list []map[string]any) []map[string]any {
	slices.SortStableFunc(list, func(a, b map[string]any) int { return strings.Compare(text(a["host"]), text(b["host"])) })
	return list
}

// children is a Host Override's aliases.
func children(h map[string]any) []map[string]any {
	var out []map[string]any
	switch list := h["aliases"].(type) {
	case []any:
		for _, x := range list {
			if a, ok := x.(map[string]any); ok {
				out = append(out, a)
			}
		}
	case []map[string]any:
		out = slices.Clone(list)
	}
	return out
}

// setChildren stores a Host Override's aliases sorted by host, without
// the ids a request or a fixture carried.
func setChildren(h map[string]any, aliases []map[string]any) {
	sorted := make([]map[string]any, 0, len(aliases))
	for _, a := range aliases {
		a = maps.Clone(a)
		delete(a, "id")
		delete(a, "parent_id")
		sorted = append(sorted, a)
	}
	sorted = sortByHost(sorted)
	list := make([]any, len(sorted))
	for i, a := range sorted {
		list[i] = a
	}
	h["aliases"] = list
}

// index reads an id (a JSON number or a query string) below n.
func index(v any, n int) (int, bool) {
	var i int
	switch v := v.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		i = int(v)
	case string:
		var err error
		if i, err = strconv.Atoi(v); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return i, i >= 0 && i < n
}
