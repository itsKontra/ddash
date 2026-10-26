package apps

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
)

// changes are what a sync writes: the records to put and the ids to drop.
type changes struct {
	put  []*record
	drop []string
}

// attach attaches every Proxy Host of a good read to an App record: by
// known link (its id), else by hostname (any record's URL or alias, for a
// Proxy Host no record links to yet), else to a new record. Records whose
// Proxy Host is gone lose the link: an untouched one is dropped, a touched
// one stays as orphaned App settings, a Link-only one stays as it is.
//
// The first sync after the migration also merges each migrated manual App
// a Proxy Host's hostname matches into that Proxy Host's App, which keeps
// the manual App's name, icon and group; the manual App is dropped.
func attach(recs []*record, hosts []*npmpb.ProxyHost) changes {
	var (
		all     = make([]*record, 0, len(recs))
		dirty   = map[string]bool{}
		dropped = map[string]bool{}
		taken   = map[string]bool{}
		used    = map[string]bool{}
		byHost  = map[int64]*record{}
	)
	for _, r := range recs {
		all = append(all, r.clone())
		taken[r.id] = true
	}
	for _, h := range hosts {
		for _, r := range all {
			if r.proxyHostID == h.GetId() && !used[r.id] {
				byHost[h.GetId()], used[r.id] = r, true
				break
			}
		}
	}
	for _, h := range hosts {
		if byHost[h.GetId()] != nil {
			continue
		}
		var merged, others []*record
		for _, r := range all {
			if used[r.id] || dropped[r.id] || !servesAny(h, r) {
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
			// An orphan before a Link-only App; then by id.
			slices.SortStableFunc(others, func(a, b *record) int {
				return cmp.Or(compareBool(a.linkOnly, b.linkOnly), cmp.Compare(a.id, b.id))
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
		byHost[h.GetId()], used[r.id], dirty[r.id] = r, true, true
	}
	for _, h := range hosts {
		r := byHost[h.GetId()]
		if a := aliasesOf(h, hostname(r.url)); !slices.Equal(a, r.aliases) {
			r.aliases, dirty[r.id] = a, true
		}
	}

	var out changes
	for _, r := range all {
		if dropped[r.id] {
			continue
		}
		if !used[r.id] {
			if r.proxyHostID != 0 {
				r.proxyHostID, dirty[r.id] = 0, true
				if r.linkOnly {
					r.aliases = nil
				}
			}
			if !r.linkOnly && !r.touched {
				dropped[r.id] = true
				continue
			}
		}
		if r.migrated {
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

// aliasesOf are h's domain names but the URL's hostname, sorted.
func aliasesOf(h *npmpb.ProxyHost, urlHost string) []string {
	var out []string
	for _, n := range slices.Sorted(slices.Values(h.GetFields().GetDomainNames())) {
		if canonName(n) != urlHost {
			out = append(out, n)
		}
	}
	return out
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
