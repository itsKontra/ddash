package apps

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// changes are what a sync writes: the records to put and the ids to drop.
type changes struct {
	put  []*record
	drop []string
}

// attach attaches the sources of the last good reads to the App records:
// every Proxy Host, every Not-proxied Host Override with its aliases, and
// every alias at the Reverse Proxy's address. Each goes by known link
// (a Proxy Host's id, a Host Override's host and domain), else by hostname
// (any record's URL or alias, for a source no record links to yet), else
// to a new record; a proxied alias has no link, and needs a new record
// only when no App has its hostname. So a Proxy Host and the Host
// Override or alias for its hostname are one App, and a Link-only App
// absorbs a Host Override for its hostname. Records whose source is gone
// lose the link: an untouched one is dropped, a touched one stays as
// orphaned App settings, a Link-only one stays as it is.
//
// A source not read since apps started (v.hosts or v.dns nil) keeps its
// links as they are, and nothing is dropped until both were read.
//
// The first sync after the migration also merges each migrated manual App
// a Proxy Host's hostname matches into that Proxy Host's App, which keeps
// the manual App's name, icon and group; the manual App is dropped.
func attach(recs []*record, v view) changes {
	var (
		all     = make([]*record, 0, len(recs))
		dirty   = map[string]bool{}
		dropped = map[string]bool{}
		taken   = map[string]bool{}
		// byHost and byOverride: the records linked this sync, by
		// source; proxiedBy: by a proxied alias.
		byHost     = map[int64]*record{}
		withHost   = map[string]bool{}
		byOverride = map[string]*record{}
		withHO     = map[string]bool{}
		proxiedBy  = map[string]bool{}
	)
	for _, r := range recs {
		all = append(all, r.clone())
		taken[r.id] = true
	}
	used := func(r *record) bool { return withHost[r.id] || withHO[r.id] || proxiedBy[r.id] }
	hosts := sortedHosts(v.hosts)
	var overrides []*pfpb.HostOverride
	if v.dns != nil {
		overrides = v.dns.notProxied()
	}

	// By known link; a source not read keeps its links.
	for _, r := range all {
		if v.hosts == nil && r.proxyHostID != 0 {
			withHost[r.id] = true
		}
		if v.dns == nil && !r.hostOverride.isZero() {
			withHO[r.id] = true
		}
	}
	for _, h := range hosts {
		for _, r := range all {
			if r.proxyHostID == h.GetId() && !withHost[r.id] {
				byHost[h.GetId()], withHost[r.id] = r, true
				break
			}
		}
	}
	for _, o := range overrides {
		for _, r := range all {
			if r.hostOverride.same(nameOf(o)) && !withHO[r.id] {
				byOverride[nameOf(o).fqdn()], withHO[r.id] = r, true
				break
			}
		}
	}

	// Proxy Hosts by hostname, else new.
	for _, h := range hosts {
		if byHost[h.GetId()] != nil {
			continue
		}
		var merged, others []*record
		for _, r := range all {
			if withHost[r.id] || dropped[r.id] || !servesAny(h, r) {
				continue
			}
			if r.linkOnly && r.migrated {
				merged = append(merged, r)
			} else {
				others = append(others, r)
			}
		}
		var r *record
		if len(others) > 0 {
			// A Host Override's App, then an orphan, then a Link-only
			// App; then by id.
			slices.SortStableFunc(others, func(a, b *record) int {
				return cmp.Or(compareBool(!withHO[a.id], !withHO[b.id]), compareBool(a.linkOnly, b.linkOnly), cmp.Compare(a.id, b.id))
			})
			r = others[0]
		} else {
			r = &record{id: newID("proxy:"+firstName(h), taken), url: urlOf(h, firstName(h))}
			r.settings, r.ignores = cloneSettings(nil), nil
			taken[r.id] = true
			all = append(all, r)
		}
		for _, m := range merged {
			r.settings.Name, r.settings.Icon, r.settings.Group = m.settings.GetName(), m.settings.GetIcon(), m.settings.GetGroup()
			r.touched = true
			dropped[m.id] = true
		}
		if r.migrated && !r.linkOnly {
			// An overlay knew only its server_name: the scheme follows
			// the certificate, as a new App's does.
			r.url = urlOf(h, hostname(r.url))
		}
		r.proxyHostID = h.GetId()
		byHost[h.GetId()], withHost[r.id], dirty[r.id] = r, true, true
	}

	// Not-proxied Host Overrides by hostname, else new.
	for _, o := range overrides {
		if byOverride[nameOf(o).fqdn()] != nil {
			continue
		}
		names := overrideNames(o)
		var others []*record
		for _, r := range all {
			if withHO[r.id] || dropped[r.id] || !slices.ContainsFunc(hostnames(r), func(n string) bool { return slices.Contains(names, n) }) {
				continue
			}
			others = append(others, r)
		}
		var r *record
		if len(others) > 0 {
			// A Proxy Host's App, then an orphan, then a Link-only App;
			// then by id.
			slices.SortStableFunc(others, func(a, b *record) int {
				return cmp.Or(compareBool(!withHost[a.id], !withHost[b.id]), compareBool(a.linkOnly, b.linkOnly), cmp.Compare(a.id, b.id))
			})
			r = others[0]
		} else {
			r = &record{id: newID("", taken), url: "https://" + nameOf(o).fqdn(), settings: cloneSettings(nil)}
			taken[r.id] = true
			all = append(all, r)
		}
		r.hostOverride = nameOf(o)
		byOverride[nameOf(o).fqdn()], withHO[r.id], dirty[r.id] = r, true, true
	}

	// The aliases of every record a source is linked to, as read.
	for _, r := range all {
		if dropped[r.id] || (!withHost[r.id] && !withHO[r.id]) {
			continue
		}
		h, o := v.hosts[r.proxyHostID], (*pfpb.HostOverride)(nil)
		if v.dns != nil {
			o = v.dns.override(r.hostOverride)
		}
		if (h == nil && r.proxyHostID != 0) || (o == nil && !r.hostOverride.isZero()) {
			continue // a source not read: its names aren't known
		}
		if a := sourceAliases(h, o, hostname(r.url)); !slices.Equal(a, r.aliases) {
			r.aliases, dirty[r.id] = a, true
		}
	}

	// Each alias at the Reverse Proxy's address no linked App has: by
	// hostname, else new.
	if v.dns != nil {
		for _, n := range v.dns.proxiedNames() {
			var others []*record
			covered := false
			for _, r := range all {
				if dropped[r.id] || !slices.Contains(hostnames(r), n) {
					continue
				}
				if withHost[r.id] || withHO[r.id] || proxiedBy[r.id] {
					covered = true
					break
				}
				others = append(others, r)
			}
			if covered {
				continue
			}
			var r *record
			if len(others) > 0 {
				slices.SortStableFunc(others, func(a, b *record) int {
					return cmp.Or(compareBool(a.linkOnly, b.linkOnly), cmp.Compare(a.id, b.id))
				})
				r = others[0]
			} else {
				r = &record{id: newID("", taken), url: "https://" + n, settings: cloneSettings(nil)}
				taken[r.id], dirty[r.id] = true, true
				all = append(all, r)
			}
			proxiedBy[r.id] = true
		}
	}

	var out changes
	for _, r := range all {
		if dropped[r.id] {
			continue
		}
		lost := false
		if v.hosts != nil && !withHost[r.id] && r.proxyHostID != 0 {
			r.proxyHostID, lost = 0, true
		}
		if v.dns != nil && !withHO[r.id] && !r.hostOverride.isZero() {
			r.hostOverride, lost = hostName{}, true
		}
		if lost {
			dirty[r.id] = true
		}
		if !used(r) {
			if lost && r.linkOnly {
				r.aliases = nil
			}
			if !r.linkOnly && !r.touched && v.hosts != nil && v.dns != nil {
				dropped[r.id] = true
				continue
			}
		}
		if r.migrated && v.hosts != nil {
			r.migrated, dirty[r.id] = false, true
		}
		if dirty[r.id] {
			out.put = append(out.put, r)
		}
	}
	for _, r := range recs {
		if dropped[r.id] {
			out.drop = append(out.drop, r.id)
		}
	}
	return out
}

// sortedHosts are the Proxy Hosts of a read, by id.
func sortedHosts(hosts map[int64]*npmpb.ProxyHost) []*npmpb.ProxyHost {
	out := slices.Collect(maps.Values(hosts))
	slices.SortFunc(out, func(a, b *npmpb.ProxyHost) int { return cmp.Compare(a.GetId(), b.GetId()) })
	return out
}

// sourceAliases are an App's further hostnames from its sources: its
// Proxy Host's domain names and its Host Override's own name and aliases,
// but urlHost (canonical), sorted. h and o may be nil.
func sourceAliases(h *npmpb.ProxyHost, o *pfpb.HostOverride, urlHost string) []string {
	var out []string
	add := func(n string) {
		if c := canonName(n); c != urlHost && !slices.ContainsFunc(out, func(x string) bool { return canonName(x) == c }) {
			out = append(out, n)
		}
	}
	for _, n := range h.GetFields().GetDomainNames() {
		add(n)
	}
	if o != nil {
		for _, n := range overrideNames(o) {
			add(n)
		}
	}
	slices.Sort(out)
	return out
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// newID is preferred when no record has it, else a generated id.
func newID(preferred string, taken map[string]bool) string {
	if preferred != "" && !taken[preferred] {
		return preferred
	}
	for {
		b := make([]byte, 8)
		_, _ = rand.Read(b) // never fails (crypto/rand)
		if id := "app:" + hex.EncodeToString(b); !taken[id] {
			return id
		}
	}
}

// firstName is a Proxy Host's first domain name in sorted order.
func firstName(h *npmpb.ProxyHost) string {
	names := slices.Sorted(slices.Values(h.GetFields().GetDomainNames()))
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// urlOf is the URL an App on h opens at host: https with a certificate,
// http without.
func urlOf(h *npmpb.ProxyHost, host string) string {
	if h.GetFields().GetCertificateId() != 0 {
		return "https://" + host
	}
	return "http://" + host
}

// servesAny reports whether h serves the URL's hostname or an alias of r.
func servesAny(h *npmpb.ProxyHost, r *record) bool {
	return slices.ContainsFunc(hostnames(r), func(n string) bool { return serves(h, n) })
}

// serves reports whether h has the domain name n (canonical).
func serves(h *npmpb.ProxyHost, n string) bool {
	return slices.ContainsFunc(h.GetFields().GetDomainNames(), func(d string) bool { return canonName(d) == n })
}

// hostnames are the URL's hostname and the aliases of r, canonical.
func hostnames(r *record) []string {
	out := []string{hostname(r.url)}
	for _, a := range r.aliases {
		out = append(out, canonName(a))
	}
	return out
}

// hostname is a URL's hostname, canonical; empty for a URL without one.
func hostname(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return canonName(u.Hostname())
}

// canonName makes hostnames comparable: lower case, no trailing dot.
func canonName(n string) string {
	return strings.TrimSuffix(strings.ToLower(n), ".")
}

// withHostname is raw with its hostname replaced, scheme and port kept.
func withHostname(raw, host string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if p := u.Port(); p != "" {
		u.Host = net.JoinHostPort(host, p)
	} else {
		u.Host = host
	}
	return u.String()
}

// defaultName reads a display name off a hostname's first label:
// "grafana.home.example" → "Grafana", "home-assistant.…" → "Home Assistant".
func defaultName(host string) string {
	label, _, _ := strings.Cut(host, ".")
	words := strings.FieldsFunc(label, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	return strings.Join(words, " ")
}
