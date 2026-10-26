package apps

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
)

// view is what the Apps are derived from besides their records: the last
// good read of each source and of the Workload sources.
type view struct {
	// hosts are the Proxy Hosts of the last good read, by id; nil before
	// the first good read since apps started.
	hosts map[int64]*npmpb.ProxyHost
	// dns is the last good read of the Host Overrides; nil before the
	// first good read since apps started, or while the Reverse Proxy's
	// address isn't known.
	dns *dns
	src sources
}

// derive makes each record an App: its Reach, sources, Workload and
// Warnings, by name.
func derive(recs []*record, v view) []*appspb.App {
	out := make([]*appspb.App, 0, len(recs))
	for _, r := range recs {
		out = append(out, v.app(r))
	}
	sharedHostnames(out, recs)
	slices.SortFunc(out, func(a, b *appspb.App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName())), cmp.Compare(a.GetId(), b.GetId()))
	})
	return out
}

func (v view) app(r *record) *appspb.App {
	s := r.settings
	a := &appspb.App{
		Id:       r.id,
		Name:     cmp.Or(s.GetName(), defaultName(hostname(r.url))),
		Url:      r.url,
		Aliases:  r.aliases,
		Icon:     s.GetIcon(),
		Group:    s.GetGroup(),
		Hidden:   s.GetHidden(),
		Touched:  r.touched,
		Settings: cloneSettings(s),
		Links:    &appspb.Links{ProxyHostId: r.proxyHostID, HostOverride: r.hostOverride.proto()},
	}
	h := v.hosts[r.proxyHostID]
	o := v.dns.override(r.hostOverride)
	if h != nil || o != nil {
		a.Aliases = sourceAliases(h, o, hostname(r.url))
	}
	if h != nil {
		a.ProxyHost = proxyHostOf(h)
	}
	switch {
	case o != nil:
		a.Reach = appspb.Reach_REACH_NOT_PROXIED
		a.Workload = v.src.workloadFor(s.GetWorkload(), overrideTarget(o.GetIps(), r.url))
	case !r.hostOverride.isZero() && v.dns == nil:
		// Linked, but pfSense wasn't read yet.
		a.Reach = appspb.Reach_REACH_NOT_PROXIED
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	case h != nil:
		a.Reach = appspb.Reach_REACH_PROXIED
		f := h.GetFields()
		a.Workload = v.src.workloadFor(s.GetWorkload(), withPort(f.GetForwardHost(), strconv.Itoa(int(f.GetForwardPort())), f.GetForwardScheme()))
	case r.proxyHostID != 0 && v.hosts == nil:
		// Linked, but Nginx Proxy Manager wasn't read yet.
		a.Reach = appspb.Reach_REACH_PROXIED
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	case v.dns != nil && slices.ContainsFunc(hostnames(r), v.dns.proxied):
		// A name at the Reverse Proxy's address alone.
		a.Reach = appspb.Reach_REACH_PROXIED
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	case r.linkOnly:
		a.Reach = appspb.Reach_REACH_LINK_ONLY
		a.Workload = v.src.workloadFor(s.GetWorkload(), urlTarget(r.url))
	default:
		a.Orphaned = v.hosts != nil && v.dns != nil
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	}
	names := append([]string{hostname(r.url)}, canonNames(a.Aliases)...)
	if v.dns != nil {
		for _, n := range names {
			a.Dns = append(a.Dns, v.dns.registering[n]...)
		}
	}

	warn := func(k appspb.WarningKind, detail string, hostnames []string, fixes ...*appspb.Fix) {
		a.Warnings = append(a.Warnings, &appspb.Warning{Kind: k, Ignored: r.ignores[k], Detail: detail, Hostnames: hostnames, Fixes: fixes})
	}
	if h != nil {
		if a.Workload == nil && len(v.src.problems) == 0 && a.Reach == appspb.Reach_REACH_PROXIED {
			warn(appspb.WarningKind_WARNING_KIND_NO_WORKLOAD, "", nil, fix(appspb.FixKind_FIX_KIND_SET_WORKLOAD_OVERRIDE))
		}
		if h.GetFields().GetCertificateId() == 0 {
			warn(appspb.WarningKind_WARNING_KIND_NO_TLS, "", nil, fix(appspb.FixKind_FIX_KIND_PICK_CERTIFICATE))
		}
		if h.GetOffline() {
			warn(appspb.WarningKind_WARNING_KIND_PROXY_HOST_OFFLINE, h.GetNginxError(), nil,
				&appspb.Fix{Kind: appspb.FixKind_FIX_KIND_SAVE_AGAIN, ProxyHostId: h.GetId()}, fix(appspb.FixKind_FIX_KIND_DELETE_APP))
		}
		if !serves(h, hostname(r.url)) {
			var fixes []*appspb.Fix
			for _, n := range a.GetProxyHost().GetFields().GetDomainNames() {
				fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_USE_AS_URL, Hostname: n})
			}
			warn(appspb.WarningKind_WARNING_KIND_URL_NOT_ON_PROXY_HOST, "", nil, fixes...)
		}
	}
	if a.Orphaned {
		warn(appspb.WarningKind_WARNING_KIND_ORPHANED, "", nil, fix(appspb.FixKind_FIX_KIND_REATTACH), fix(appspb.FixKind_FIX_KIND_DELETE_ORPHAN))
	}
	if v.dns != nil {
		v.dnsWarnings(a, h, names, warn)
	}
	return a
}

// dnsWarnings derives the Warnings needing pfSense's Host Overrides; names
// are the App's hostnames, canonical.
func (v view) dnsWarnings(a *appspb.App, h *npmpb.ProxyHost, names []string, warn func(appspb.WarningKind, string, []string, ...*appspb.Fix)) {
	d := v.dns
	if h != nil && a.GetReach() == appspb.Reach_REACH_PROXIED {
		// The names its Proxy Host serves: a URL no longer among them
		// has its own Warning.
		var missing []string
		for _, n := range canonNames(h.GetFields().GetDomainNames()) {
			if len(d.registering[n]) == 0 {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			var fixes []*appspb.Fix
			for _, n := range missing {
				fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_REGISTER, Hostname: n})
			}
			warn(appspb.WarningKind_WARNING_KIND_NOT_REGISTERED, "", sorted(missing), append(fixes, fix(appspb.FixKind_FIX_KIND_DELETE_APP))...)
		}
	}
	if v.hosts != nil {
		var unserved []string
		for _, n := range names {
			if d.proxied(n) && !v.served(n) {
				unserved = append(unserved, n)
			}
		}
		if len(unserved) > 0 {
			fixes := []*appspb.Fix{fix(appspb.FixKind_FIX_KIND_ADD_REVERSE_PROXY)}
			for _, n := range unserved {
				fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_REMOVE_NAME, Hostname: n})
			}
			warn(appspb.WarningKind_WARNING_KIND_NO_PROXY_HOST, "", sorted(unserved), fixes...)
		}
	}
	if h != nil {
		var bypassed []string
		for _, n := range h.GetFields().GetDomainNames() {
			if n = canonName(n); d.elsewhere(n) {
				bypassed = append(bypassed, n)
			}
		}
		if len(bypassed) > 0 {
			var fixes []*appspb.Fix
			for _, n := range sorted(bypassed) {
				fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY, Hostname: n})
			}
			fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST, ProxyHostId: h.GetId()})
			warn(appspb.WarningKind_WARNING_KIND_DNS_BYPASSES, "", sorted(bypassed), fixes...)
		}
	}
	var twice []string
	var fixes []*appspb.Fix
	for _, n := range sorted(names) {
		if entries := d.registering[n]; len(entries) > 1 {
			twice = append(twice, n)
			for _, e := range entries {
				fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_DELETE_ENTRY, HostOverride: e})
			}
		}
	}
	if len(twice) > 0 {
		warn(appspb.WarningKind_WARNING_KIND_REGISTERED_TWICE, "", twice, fixes...)
	}
}

// served reports whether a Proxy Host of the last good read has the
// domain name n (canonical).
func (v view) served(n string) bool {
	for _, h := range v.hosts {
		if serves(h, n) {
			return true
		}
	}
	return false
}

// sharedHostnames adds "Hostname on two Apps" to each App sharing a
// hostname with another; orphaned App settings share none.
func sharedHostnames(all []*appspb.App, recs []*record) {
	ignores := map[string]map[appspb.WarningKind]bool{}
	for _, r := range recs {
		ignores[r.id] = r.ignores
	}
	byName := map[string][]*appspb.App{}
	for _, a := range all {
		if a.GetOrphaned() {
			continue
		}
		for _, n := range append([]string{hostname(a.GetUrl())}, canonNames(a.GetAliases())...) {
			if n != "" && !slices.Contains(byName[n], a) {
				byName[n] = append(byName[n], a)
			}
		}
	}
	for _, a := range all {
		var shared []string
		var others []string
		for n, apps := range byName {
			if len(apps) < 2 || !slices.Contains(apps, a) {
				continue
			}
			shared = append(shared, n)
			for _, o := range apps {
				if o != a && !slices.Contains(others, o.GetId()) {
					others = append(others, o.GetId())
				}
			}
		}
		if len(shared) == 0 {
			continue
		}
		slices.Sort(others)
		var fixes []*appspb.Fix
		for _, id := range others {
			fixes = append(fixes, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_MERGE, AppId: id})
		}
		k := appspb.WarningKind_WARNING_KIND_HOSTNAME_ON_TWO_APPS
		a.Warnings = append(a.Warnings, &appspb.Warning{Kind: k, Ignored: ignores[a.GetId()][k], Hostnames: sorted(shared), Fixes: fixes})
		slices.SortStableFunc(a.Warnings, func(x, y *appspb.Warning) int { return cmp.Compare(x.GetKind(), y.GetKind()) })
	}
}

func fix(k appspb.FixKind) *appspb.Fix { return &appspb.Fix{Kind: k} }

func sorted(names []string) []string { return slices.Sorted(slices.Values(names)) }

func canonNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = canonName(n)
	}
	return out
}

// overrideTarget is where a Not-proxied App's traffic goes: its Host
// Override's address and its URL's port, explicit or by scheme.
func overrideTarget(ips []string, raw string) hostPort {
	u, err := url.Parse(raw)
	if err != nil || len(ips) == 0 {
		return hostPort{}
	}
	return withPort(ips[0], u.Port(), u.Scheme)
}

func proxyHostOf(h *npmpb.ProxyHost) *appspb.ProxyHost {
	f := proto.CloneOf(h.GetFields())
	slices.Sort(f.DomainNames)
	return &appspb.ProxyHost{Id: h.GetId(), Fields: f, Enabled: h.GetEnabled(), Offline: h.GetOffline(), NginxError: h.GetNginxError()}
}
