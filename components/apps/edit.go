package apps

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The Plans of the App Actions changing an App that is there: its
// Address, its Proxy Host, adding or removing its reverse proxy, Delete,
// and the external Warning fixes. Each names the App's sources as the
// last reads found them in its steps' before values, so a change to them
// since refuses the run.

// target is the App id, as derived, and its record; nil, blocking, when
// no App has the id or it is orphaned App settings.
func (p planning) target(id string, block func(string)) (*appspb.App, *record) {
	a := p.app(id)
	i := slices.IndexFunc(p.recs, func(r *record) bool { return r.id == id })
	switch {
	case a == nil || i < 0:
		block(fmt.Sprintf("No App has the id %q.", id))
		return nil, nil
	case a.GetOrphaned():
		block(fmt.Sprintf("%s is orphaned App settings: reattach them to an App, or delete them.", a.GetName()))
		return nil, nil
	}
	return a, p.recs[i]
}

// npmWritable blocks unless ddash can write Proxy Hosts now.
func (p planning) npmWritable(block func(string)) {
	switch {
	case !p.npmRead:
		block(cantCheck("Nginx Proxy Manager"))
	case p.readOnly:
		block("ddash's user in Nginx Proxy Manager can only view Proxy Hosts: set them to Manage to change them.")
	}
}

// pfWritable blocks unless ddash can write Host Overrides now.
func (p planning) pfWritable(block func(string)) {
	if !p.pfRead || p.dns == nil {
		block(cantCheck("pfSense"))
	}
}

// proxyHost is the record's Proxy Host as last read; nil for none.
func (p planning) proxyHost(r *record) *npmpb.ProxyHost {
	if r.proxyHostID == 0 {
		return nil
	}
	return p.v.hosts[r.proxyHostID]
}

// delete plans Delete: the App's Proxy Host, its own Host Override or the
// Reverse Proxy's aliases for it, then its record, its Check with it. A
// Link-only App is deleted without a Plan (DeleteRecord).
func (p planning) delete(d *appspb.DeleteApp) *planned {
	out := &planned{plan: &appspb.Plan{}}
	a, r := p.target(d.GetId(), out.block)
	if a == nil {
		return out
	}
	if a.GetReach() == appspb.Reach_REACH_LINK_ONLY {
		out.block(a.GetName() + " is Link only: delete it at once, without a Plan.")
		return out
	}
	if h := p.proxyHost(r); h != nil {
		p.npmWritable(out.block)
		out.deleteProxyHost(h)
	}
	if len(a.GetDns()) > 0 {
		p.pfWritable(out.block)
		p.unregister(out, a, r)
	}
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_DDASH,
		Title:  "Delete the App " + a.GetName() + " and its Check",
		Values: []*appspb.PlanValue{{Label: "URL", Before: a.GetUrl()}},
	})
	out.record = func([]*record, *npmpb.ProxyHost) (changes, string) {
		return changes{drop: []string{r.id}}, r.id
	}
	return out
}

// unregister removes the App's DNS: its own Host Override, its aliases
// with it, and its hostnames' aliases at the Reverse Proxy's address.
// Other entries for its names are other Apps'.
func (p planning) unregister(out *planned, a *appspb.App, r *record) {
	if p.dns == nil {
		return
	}
	own := p.dns.override(r.hostOverride)
	if own != nil {
		out.addDNS(deleteOverride(own))
	}
	for _, e := range a.GetDns() {
		parent := e.GetParent()
		if parent == nil || !p.dns.atReverseProxy(e.GetIps()) || (own != nil && nameOf(own).same(hostName{parent.GetHost(), parent.GetDomain()})) {
			continue
		}
		if o := p.dns.named(hostName{parent.GetHost(), parent.GetDomain()}); o != nil {
			out.addDNS(removeAlias(o, e))
		}
	}
}

// deleteProxyHost adds the step deleting h, expecting its managed fields
// as last read.
func (out *planned) deleteProxyHost(h *npmpb.ProxyHost) {
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_NGINX_PROXY_MANAGER,
		Title:  "Delete Proxy Host " + firstName(h),
		Values: proxyHostValues(h.GetFields(), nil),
	})
	id := h.GetId()
	out.deletes = id
	out.npm = func(ctx context.Context, c npmpb.NginxProxyManagerApiServiceClient) (*npmpb.ProxyHost, error) {
		_, err := c.DeleteProxyHost(ctx, &npmpb.DeleteProxyHostRequest{Id: id})
		return nil, err
	}
}

// deleteOverride deletes the Host Override o, its aliases with it.
func deleteOverride(o *pfpb.HostOverride) (*pfpb.HostOverrideStep, string, []*appspb.PlanValue) {
	n := nameOf(o)
	step := &pfpb.HostOverrideStep{Change: &pfpb.HostOverrideStep_Delete{Delete: &pfpb.HostName{Host: n.host, Domain: n.domain}}}
	values := []*appspb.PlanValue{{Label: "Host Override", Before: overrideText(o)}}
	if names := overrideNames(o)[1:]; len(names) > 0 {
		values = append(values, &appspb.PlanValue{Label: "Aliases", Before: strings.Join(names, ", ")})
	}
	return step, "Delete Host Override " + n.fqdn(), values
}

// removeAlias removes the alias e from its Host Override o.
func removeAlias(o *pfpb.HostOverride, e *appspb.DnsEntry) (*pfpb.HostOverrideStep, string, []*appspb.PlanValue) {
	parent, n := nameOf(o), hostName{e.GetHost(), e.GetDomain()}
	step := &pfpb.HostOverrideStep{Change: &pfpb.HostOverrideStep_RemoveAlias{RemoveAlias: &pfpb.AliasChange{
		Parent: &pfpb.HostName{Host: parent.host, Domain: parent.domain},
		Alias:  &pfpb.HostOverrideAlias{Host: n.host, Domain: n.domain},
	}}}
	at := overrideText(o)
	return step, fmt.Sprintf("Remove alias %s from %s", n.fqdn(), parent.fqdn()), []*appspb.PlanValue{
		{Label: "Host Override", Before: at, After: at},
		{Label: "Alias", Before: n.fqdn()},
	}
}

// changeAddress plans the Address Panel's Edit: each hostname added
// checked free, the Proxy Host's names updated, the new names registered
// and the old ones removed in one batch, then the record. A proxied App's
// names are aliases of the Reverse Proxy's Host Override; a Not-proxied
// App's, of its own Host Override, which is renamed when its own name
// goes: deleted, its aliases with it, and created again under the URL's
// hostname with the other names as its aliases.
func (p planning) changeAddress(c *appspb.ChangeAddress) *planned {
	out := &planned{plan: &appspb.Plan{}}
	a, r := p.target(c.GetId(), out.block)
	if a == nil {
		return out
	}
	raw := strings.TrimSpace(c.GetUrl())
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		out.block("The URL must be an http or https URL, e.g. https://jellyfin.home.example.")
		return out
	}
	host := canonName(u.Hostname())
	names := []string{host}
	var aliases []string
	for _, n := range c.GetAliases() {
		if n = canonName(strings.TrimSpace(n)); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
			aliases = append(aliases, n)
		}
	}
	slices.Sort(aliases)
	old := appHostnames(a)
	added := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return slices.Contains(old, n) })
	removed := slices.DeleteFunc(slices.Clone(old), func(n string) bool { return slices.Contains(names, n) })
	if raw == a.GetUrl() && len(added) == 0 && len(removed) == 0 {
		out.block("Nothing to change.")
	}

	// "The name is free" for each name added.
	reach := a.GetReach()
	writes := reach != appspb.Reach_REACH_LINK_ONLY
	home := p.homeDomain()
	for _, n := range added {
		switch {
		case writes && !isHostname(n):
			text := n + " is not a hostname."
			if n == host {
				text = "Not a hostname."
			}
			out.plan.Names = append(out.plan.Names, &appspb.NameNote{Hostname: n, Kind: appspb.NameNoteKind_NAME_NOTE_KIND_NOT_A_HOSTNAME, Text: text})
		case writes && home != "" && n != home && !strings.HasSuffix(n, "."+home):
			out.block(fmt.Sprintf("%s isn't in %s, the home domain.", n, home))
		default:
			if note := p.nameNote(n, reach, a.GetId()); note != nil {
				if note.GetKind() == appspb.NameNoteKind_NAME_NOTE_KIND_REATTACH {
					note.Kind = appspb.NameNoteKind_NAME_NOTE_KIND_ORPHAN
					note.Text = fmt.Sprintf("%s is kept for the orphaned App settings %s: reattach or delete them first.", n, note.GetName())
				}
				out.plan.Names = append(out.plan.Names, note)
			}
		}
	}

	if h := p.proxyHost(r); h != nil && (len(added) > 0 || len(removed) > 0) {
		p.npmWritable(out.block)
		f := proto.CloneOf(h.GetFields())
		f.DomainNames = slices.DeleteFunc(f.DomainNames, func(n string) bool { return slices.Contains(removed, canonName(n)) })
		f.DomainNames = append(f.DomainNames, added...)
		slices.Sort(f.DomainNames)
		if len(f.GetDomainNames()) == 0 {
			out.block("A Proxy Host needs a domain name: keep one of its names.")
		}
		out.updateProxyHost(h, f, "Update Proxy Host %s")
	}

	renamed := hostName{}
	if writes && (len(added) > 0 || len(removed) > 0) {
		p.pfWritable(out.block)
	}
	switch {
	case p.dns == nil || (len(added) == 0 && len(removed) == 0):
	case reach == appspb.Reach_REACH_PROXIED:
		if len(added) > 0 {
			if rp := p.reverseProxy(out.block); rp != nil {
				for _, n := range added {
					out.addDNS(addAlias(rp, n))
				}
			}
		}
		for _, n := range removed {
			p.removeProxied(out, n)
		}
	case reach == appspb.Reach_REACH_NOT_PROXIED:
		own := p.dns.override(r.hostOverride)
		if own == nil {
			out.block(cantCheck("pfSense"))
			break
		}
		if slices.Contains(names, nameOf(own).fqdn()) {
			for _, n := range added {
				out.addDNS(addAlias(own, n))
			}
			for _, n := range removed {
				for _, e := range p.dns.entries[n] {
					if e.GetParent() != nil && nameOf(own).same(hostName{e.GetParent().GetHost(), e.GetParent().GetDomain()}) {
						out.addDNS(removeAlias(own, e))
					}
				}
			}
			break
		}
		renamed = splitName(host)
		created := &pfpb.HostOverride{Host: renamed.host, Domain: renamed.domain, Ips: own.GetIps()}
		out.addDNS(deleteOverride(own))
		out.addDNS(createOverride(renamed, own.GetIps()))
		for _, n := range aliases {
			out.addDNS(addAlias(created, n))
		}
	}

	var values []*appspb.PlanValue
	if raw != a.GetUrl() {
		values = append(values, &appspb.PlanValue{Label: "URL", Before: a.GetUrl(), After: raw})
	}
	if before, after := strings.Join(a.GetAliases(), ", "), strings.Join(aliases, ", "); before != after {
		values = append(values, &appspb.PlanValue{Label: "Aliases", Before: before, After: after})
	}
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{System: appspb.System_SYSTEM_DDASH, Title: "Update the App " + a.GetName(), Values: values})
	name := a.GetName()
	out.record = func(recs []*record, _ *npmpb.ProxyHost) (changes, string) {
		i := slices.IndexFunc(recs, func(x *record) bool { return x.id == r.id })
		if i < 0 {
			return changes{}, r.id
		}
		n := recs[i].clone()
		n.url, n.aliases = raw, aliases
		if !renamed.isZero() {
			n.hostOverride = renamed
		}
		// The name derived from the old URL stays the App's, as
		// PromoteAlias keeps it.
		if n.settings.GetName() == "" && name != defaultName(host) {
			n.settings.Name, n.touched = name, true
		}
		return changes{put: []*record{n}}, n.id
	}
	return out
}

// changeProxyHost plans the Reverse proxy Panel's Edit: the App's Proxy
// Host updated with the fields, its domain names kept. The step expects
// every managed field as last read, so a change to any since refuses it;
// the PUT names only those, so the others stay as they are.
func (p planning) changeProxyHost(c *appspb.ChangeProxyHost) *planned {
	out := &planned{plan: &appspb.Plan{}, id: c.GetId()}
	a, r := p.target(c.GetId(), out.block)
	if a == nil {
		return out
	}
	h := p.proxyHost(r)
	if h == nil {
		out.block(a.GetName() + " has no Proxy Host: add a reverse proxy instead.")
		return out
	}
	p.npmWritable(out.block)
	f, why := proxyHostFields(c.GetProxyHost())
	if why != "" {
		out.block(why)
	}
	f.DomainNames = slices.Clone(h.GetFields().GetDomainNames())
	changed := slices.ContainsFunc(proxyHostValues(h.GetFields(), f), func(v *appspb.PlanValue) bool { return v.GetBefore() != v.GetAfter() })
	if why == "" && !changed {
		out.block("Nothing to change.")
	}
	out.updateProxyHost(h, f, "Update Proxy Host %s")
	return out
}

// updateProxyHost adds the step updating h with the fields f, its title
// the format title of h's first name, expecting h's managed fields as last
// read.
func (out *planned) updateProxyHost(h *npmpb.ProxyHost, f *npmpb.ProxyHostFields, title string) {
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_NGINX_PROXY_MANAGER,
		Title:  fmt.Sprintf(title, firstName(h)),
		Values: proxyHostValues(h.GetFields(), f),
	})
	id := h.GetId()
	out.npm = func(ctx context.Context, c npmpb.NginxProxyManagerApiServiceClient) (*npmpb.ProxyHost, error) {
		resp, err := c.UpdateProxyHost(ctx, &npmpb.UpdateProxyHostRequest{Id: id, Fields: f})
		return resp.GetProxyHost(), err
	}
}

// reachText names a Reach in a Plan's values.
var reachText = map[appspb.Reach]string{
	appspb.Reach_REACH_PROXIED:     "Proxied",
	appspb.Reach_REACH_NOT_PROXIED: "Not proxied",
	appspb.Reach_REACH_LINK_ONLY:   "Link only",
}

// addReverseProxy plans Add reverse proxy: a Proxy Host serving the App's
// hostnames, its own Host Override deleted, and each of its hostnames
// not at the Reverse Proxy's address yet an alias of the Reverse Proxy's
// Host Override; the record then links the Proxy Host in place of the
// Host Override. It is also No Proxy Host's fix, for an App whose names
// are aliases at the Reverse Proxy's address already.
func (p planning) addReverseProxy(c *appspb.AddReverseProxy) *planned {
	out := &planned{plan: &appspb.Plan{}}
	a, r := p.target(c.GetId(), out.block)
	if a == nil {
		return out
	}
	switch {
	case r.proxyHostID != 0:
		out.block(a.GetName() + " has a Proxy Host already.")
		return out
	case a.GetReach() == appspb.Reach_REACH_LINK_ONLY:
		out.block(a.GetName() + " is Link only: nothing in pfSense to put behind the Reverse Proxy. Add it as a new App instead.")
		return out
	}
	p.npmWritable(out.block)
	p.pfWritable(out.block)
	f, why := proxyHostFields(c.GetProxyHost())
	if why != "" {
		out.block(why)
	}
	names := appHostnames(a)
	for _, n := range names {
		if note := p.nameNote(n, appspb.Reach_REACH_PROXIED, a.GetId()); note != nil && note.GetKind() == appspb.NameNoteKind_NAME_NOTE_KIND_APP {
			out.plan.Names = append(out.plan.Names, note)
		}
	}
	rp := p.reverseProxy(out.block)
	f.DomainNames = sorted(names)
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_NGINX_PROXY_MANAGER,
		Title:  "Create Proxy Host " + names[0],
		Values: proxyHostValues(nil, f),
	})
	out.npm = func(ctx context.Context, c npmpb.NginxProxyManagerApiServiceClient) (*npmpb.ProxyHost, error) {
		resp, err := c.CreateProxyHost(ctx, &npmpb.CreateProxyHostRequest{Fields: f})
		return resp.GetProxyHost(), err
	}
	if p.dns != nil {
		if own := p.dns.override(r.hostOverride); own != nil {
			out.addDNS(deleteOverride(own))
		}
		for _, n := range names {
			if rp != nil && !p.dns.proxied(n) {
				out.addDNS(addAlias(rp, n))
			}
		}
	}
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_DDASH,
		Title:  "Put " + a.GetName() + " behind the Reverse Proxy",
		Values: []*appspb.PlanValue{{Label: "Reach", Before: reachText[a.GetReach()], After: reachText[appspb.Reach_REACH_PROXIED]}},
	})
	out.record = func(recs []*record, written *npmpb.ProxyHost) (changes, string) {
		return p.relink(recs, r.id, written.GetId(), hostName{})
	}
	return out
}

// relink links the record id to the Proxy Host proxyHost (0 for none) and
// the Host Override o (zero for none) alone.
func (p planning) relink(recs []*record, id string, proxyHost int64, o hostName) (changes, string) {
	i := slices.IndexFunc(recs, func(x *record) bool { return x.id == id })
	if i < 0 {
		return changes{}, id
	}
	n := recs[i].clone()
	n.proxyHostID, n.hostOverride = proxyHost, o
	return changes{put: []*record{n}}, id
}

// removeReverseProxy plans Remove reverse proxy: the App's Proxy Host
// deleted, its hostnames' aliases at the Reverse Proxy's address
// removed, and a Host Override of its own at the address created for its
// URL's hostname, its other names as aliases; the record then links that
// in place of the Proxy Host.
func (p planning) removeReverseProxy(c *appspb.RemoveReverseProxy) *planned {
	out := &planned{plan: &appspb.Plan{}}
	a, r := p.target(c.GetId(), out.block)
	if a == nil {
		return out
	}
	h := p.proxyHost(r)
	if h == nil || a.GetReach() != appspb.Reach_REACH_PROXIED {
		out.block(a.GetName() + " isn't behind the Reverse Proxy.")
		return out
	}
	p.npmWritable(out.block)
	p.pfWritable(out.block)
	ip, err := netip.ParseAddr(strings.TrimSpace(c.GetAddress()))
	switch {
	case err != nil:
		out.block("Give the address its Host Override resolves to, e.g. 198.18.1.60.")
	case p.rpAddress != "" && ip.String() == canonHost(p.rpAddress):
		out.block(fmt.Sprintf("%s is the Reverse Proxy's address: give the App's own.", ip))
	}
	out.deleteProxyHost(h)
	names := appHostnames(a)
	own := splitName(names[0])
	if p.dns != nil && err == nil {
		for _, n := range names {
			p.removeProxied(out, n)
		}
		ips := []string{ip.String()}
		out.addDNS(createOverride(own, ips))
		created := &pfpb.HostOverride{Host: own.host, Domain: own.domain, Ips: ips}
		for _, n := range names[1:] {
			out.addDNS(addAlias(created, n))
		}
	}
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
		System: appspb.System_SYSTEM_DDASH,
		Title:  "Take " + a.GetName() + " from behind the Reverse Proxy",
		Values: []*appspb.PlanValue{{Label: "Reach", Before: reachText[appspb.Reach_REACH_PROXIED], After: reachText[appspb.Reach_REACH_NOT_PROXIED]}},
	})
	out.record = func(recs []*record, _ *npmpb.ProxyHost) (changes, string) {
		return p.relink(recs, r.id, 0, own)
	}
	return out
}

// fix plans an external fix one of the App's Warnings offers now. It
// writes the sources alone: the sync ending it brings the record along.
func (p planning) fix(c *appspb.FixWarning) *planned {
	out := &planned{plan: &appspb.Plan{}, id: c.GetId()}
	a, _ := p.target(c.GetId(), out.block)
	if a == nil {
		return out
	}
	f := c.GetFix()
	offered := slices.ContainsFunc(a.GetWarnings(), func(w *appspb.Warning) bool {
		return slices.ContainsFunc(w.GetFixes(), func(x *appspb.Fix) bool { return proto.Equal(x, f) })
	})
	if !offered {
		out.block(a.GetName() + "'s Warnings don't offer that fix now.")
		return out
	}
	n := canonName(f.GetHostname())
	switch f.GetKind() {
	case appspb.FixKind_FIX_KIND_REGISTER:
		p.pfWritable(out.block)
		if rp := p.reverseProxy(out.block); rp != nil {
			out.addDNS(addAlias(rp, n))
		}
	case appspb.FixKind_FIX_KIND_REMOVE_NAME:
		p.pfWritable(out.block)
		p.removeProxied(out, n)
	case appspb.FixKind_FIX_KIND_SAVE_AGAIN:
		p.npmWritable(out.block)
		if h := p.v.hosts[f.GetProxyHostId()]; h != nil {
			out.updateProxyHost(h, proto.CloneOf(h.GetFields()), "Save Proxy Host %s again")
		}
	case appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY:
		p.pfWritable(out.block)
		rp := p.reverseProxy(out.block)
		if rp == nil {
			break
		}
		// A bypassing alias moves alone. A bypassing Host Override goes
		// with its aliases, which move too: unless one isn't only the
		// App's name, which the Operator removes or moves first.
		moved := []string{n}
		for _, e := range p.dns.entries[n] {
			if p.dns.atReverseProxy(e.GetIps()) {
				continue
			}
			if parent := e.GetParent(); parent != nil {
				if o := p.dns.named(hostName{parent.GetHost(), parent.GetDomain()}); o != nil {
					out.addDNS(removeAlias(o, e))
				}
				continue
			}
			o := p.dns.named(hostName{e.GetHost(), e.GetDomain()})
			if others := p.notOnlyOf(a, overrideNames(o)[1:]); len(others) > 0 {
				out.block(fmt.Sprintf("Pointing %s at the Reverse Proxy deletes its Host Override, and with it %s, which %s only %s's: remove or move %s in pfSense first.",
					n, strings.Join(others, ", "), plural(len(others), "isn't", "aren't"), a.GetName(), plural(len(others), "it", "them")))
				continue
			}
			out.addDNS(deleteOverride(o))
			for _, m := range overrideNames(o)[1:] {
				if slices.Contains(appHostnames(a), m) && !slices.Contains(moved, m) {
					moved = append(moved, m)
				}
			}
		}
		for _, m := range moved {
			if !p.dns.proxied(m) {
				out.addDNS(addAlias(rp, m))
			}
		}
	case appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST:
		p.npmWritable(out.block)
		h := p.v.hosts[f.GetProxyHostId()]
		if h == nil {
			break
		}
		out.deleteProxyHost(h)
		// Its names at the Reverse Proxy's address stay, unserved.
		for _, m := range sorted(canonNames(h.GetFields().GetDomainNames())) {
			if p.dns != nil && p.dns.proxied(m) {
				out.plan.Notes = append(out.plan.Notes, m+" stays an alias of the Reverse Proxy's Host Override, with no Proxy Host to serve it: remove it there, or add a reverse proxy again.")
			}
		}
	case appspb.FixKind_FIX_KIND_DELETE_ENTRY:
		p.pfWritable(out.block)
		if p.dns == nil {
			break
		}
		e := f.GetEntry()
		if parent := e.GetParent(); parent != nil {
			if o := p.dns.named(hostName{parent.GetHost(), parent.GetDomain()}); o != nil {
				out.addDNS(removeAlias(o, e))
			}
		} else if o := p.dns.named(hostName{e.GetHost(), e.GetDomain()}); o != nil {
			out.addDNS(deleteOverride(o))
		}
	default:
		out.block("That fix doesn't write to pfSense or Nginx Proxy Manager: run it from its Warning.")
	}
	return out
}

// notOnlyOf are the hostnames of names that aren't the App a's alone:
// not its names, or another App's too.
func (p planning) notOnlyOf(a *appspb.App, names []string) []string {
	var out []string
	for _, n := range names {
		other := slices.ContainsFunc(p.apps, func(x *appspb.App) bool {
			return x.GetId() != a.GetId() && !x.GetOrphaned() && slices.Contains(appHostnames(x), n)
		})
		if other || !slices.Contains(appHostnames(a), n) {
			out = append(out, n)
		}
	}
	return out
}

// plural is one for n == 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// removeProxied removes the hostname n's aliases at the Reverse Proxy's
// address.
func (p planning) removeProxied(out *planned, n string) {
	if p.dns == nil {
		return
	}
	for _, e := range p.dns.entries[n] {
		if parent := e.GetParent(); parent != nil && p.dns.atReverseProxy(e.GetIps()) {
			if o := p.dns.named(hostName{parent.GetHost(), parent.GetDomain()}); o != nil {
				out.addDNS(removeAlias(o, e))
			}
		}
	}
}
