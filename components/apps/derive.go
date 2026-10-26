package apps

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/itsKontra/ddash/components/apps/appspb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
)

// view is what the Apps are derived from besides their records: the last
// good read of each source and of the Workload sources.
type view struct {
	// hosts are the Proxy Hosts of the last good read, by id; nil before
	// the first good read since apps started.
	hosts map[int64]*npmpb.ProxyHost
	src   sources
}

// derive makes each record an App: its Reach, Proxy Host, Workload and
// Warnings, by name.
func derive(recs []*record, v view) []*appspb.App {
	out := make([]*appspb.App, 0, len(recs))
	for _, r := range recs {
		out = append(out, v.app(r))
	}
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
		Links:    &appspb.Links{ProxyHostId: r.proxyHostID},
	}
	read := v.hosts != nil
	h := v.hosts[r.proxyHostID]
	switch {
	case h != nil:
		a.Reach = appspb.Reach_REACH_PROXIED
		a.ProxyHost = proxyHostOf(h)
		a.Aliases = aliasesOf(h, hostname(r.url))
		f := h.GetFields()
		a.Workload = v.src.workloadFor(s.GetWorkload(), withPort(f.GetForwardHost(), strconv.Itoa(int(f.GetForwardPort())), f.GetForwardScheme()))
	case r.proxyHostID != 0:
		// Linked, but Nginx Proxy Manager wasn't read yet.
		a.Reach = appspb.Reach_REACH_PROXIED
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	case r.linkOnly:
		a.Reach = appspb.Reach_REACH_LINK_ONLY
		a.Workload = v.src.workloadFor(s.GetWorkload(), urlTarget(r.url))
	default:
		a.Orphaned = read
		a.Workload = v.src.workloadFor(s.GetWorkload(), hostPort{})
	}

	warn := func(k appspb.WarningKind, detail string) {
		a.Warnings = append(a.Warnings, &appspb.Warning{Kind: k, Ignored: r.ignores[k], Detail: detail})
	}
	if h != nil {
		if a.Workload == nil && len(v.src.problems) == 0 {
			warn(appspb.WarningKind_WARNING_KIND_NO_WORKLOAD, "")
		}
		if h.GetFields().GetCertificateId() == 0 {
			warn(appspb.WarningKind_WARNING_KIND_NO_TLS, "")
		}
		if h.GetOffline() {
			warn(appspb.WarningKind_WARNING_KIND_PROXY_HOST_OFFLINE, h.GetNginxError())
		}
		if !serves(h, hostname(r.url)) {
			warn(appspb.WarningKind_WARNING_KIND_URL_NOT_ON_PROXY_HOST, "")
		}
	}
	if a.Orphaned {
		warn(appspb.WarningKind_WARNING_KIND_ORPHANED, "")
	}
	return a
}

func proxyHostOf(h *npmpb.ProxyHost) *appspb.ProxyHost {
	f := h.GetFields()
	return &appspb.ProxyHost{
		Id:             h.GetId(),
		DomainNames:    slices.Sorted(slices.Values(f.GetDomainNames())),
		ForwardScheme:  f.GetForwardScheme(),
		ForwardHost:    f.GetForwardHost(),
		ForwardPort:    f.GetForwardPort(),
		CertificateId:  f.GetCertificateId(),
		BlockExploits:  f.GetBlockExploits(),
		Websockets:     f.GetWebsockets(),
		ForceSsl:       f.GetForceSsl(),
		Http2:          f.GetHttp2(),
		Hsts:           f.GetHsts(),
		HstsSubdomains: f.GetHstsSubdomains(),
		Enabled:        h.GetEnabled(),
		Offline:        h.GetOffline(),
		NginxError:     h.GetNginxError(),
	}
}
