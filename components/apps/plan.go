package apps

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/itsKontra/ddash/components/apps/appspb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The one Plan builder: every App Action, dry-run (PlanAction) or run
// (RunAction), gets its Plan from planning.build. A planned Action holds
// its Plan as the Operator sees it and how each of its steps runs, in the
// one order every App Action has: the Nginx Proxy Manager write, then one
// ChangeHostOverrides batch, then the App record. A new kind of App Action
// is a case in build returning a planned.

// planned is an App Action's Plan and its steps, ready to run.
type planned struct {
	plan *appspb.Plan
	// npm is the Nginx Proxy Manager write, returning the Proxy Host as
	// written (nil for a delete); nil for none. It is the Plan's first
	// step.
	npm func(ctx context.Context, c npmpb.NginxProxyManagerApiServiceClient) (*npmpb.ProxyHost, error)
	// dns is the one pfSense batch, a Plan step each after npm's; empty
	// for none.
	dns []*pfpb.HostOverrideStep
	// record is the App record step, the Plan's last: the records to put
	// and drop given every record now and the Proxy Host npm wrote (nil
	// without one), and the App's id.
	record func(recs []*record, written *npmpb.ProxyHost) (changes, string)
}

// blocked is why the Plan can't run: its blocks and taken names, as the
// Operator reads them; empty when it can.
func (p *planned) blocked() []string {
	out := slices.Clone(p.plan.GetBlocked())
	for _, n := range p.plan.GetNames() {
		if n.GetKind() != appspb.NameNoteKind_NAME_NOTE_KIND_REATTACH {
			out = append(out, n.GetText())
		}
	}
	return out
}

// planning is what a Plan is built from: the App records, the last good
// reads, and whether they are good enough to write by.
type planning struct {
	recs []*record
	v    view
	// apps are the records, derived.
	apps []*appspb.App
	// npmRead and pfRead: the last read of Nginx Proxy Manager's Proxy
	// Hosts, of pfSense's Host Overrides and DHCP servers, succeeded.
	npmRead, pfRead bool
	// dns is the last good read of the Host Overrides, also while the
	// Reverse Proxy's address isn't known; nil before the first.
	dns       *dns
	rpAddress string
	// readOnly: Nginx Proxy Manager refuses ddash's Proxy Host writes.
	readOnly bool
	devices  []device
}

// device is a Device's reservation as "the name is free" sees it.
type device struct {
	// hostname is the reservation's hostname in its DHCP domain,
	// canonical.
	hostname, name, mac string
}

// planning reads what a Plan is built from now: the records, the last
// good reads, and the DHCP servers through pfsense-api's cache.
func (c *component) planning(ctx context.Context) (planning, error) {
	recs, err := c.store.records(ctx)
	if err != nil {
		return planning{}, err
	}
	servers, devicesErr := c.pfsense.ListDhcpServers(ctx, &pfpb.ListDhcpServersRequest{})
	p := planning{recs: recs, v: c.view(), devices: devicesOf(servers)}
	c.mu.Lock()
	p.npmRead = c.hosts != nil && c.hostsRead.err == nil
	p.pfRead = c.overrides != nil && c.overridesRead.err == nil && devicesErr == nil
	if c.overrides != nil {
		p.dns = newDNS(c.overrides, c.rpAddress)
	}
	p.rpAddress, p.readOnly = c.rpAddress, c.hostsReadOnly
	c.mu.Unlock()
	p.apps = derive(recs, p.v)
	return p, nil
}

// devicesOf are the reservations of the DHCP servers with a hostname and
// a domain.
func devicesOf(servers *pfpb.ListDhcpServersResponse) []device {
	var out []device
	for _, s := range servers.GetServers() {
		for _, m := range s.GetStaticMappings() {
			if m.GetHostname() == "" || s.GetDomain() == "" {
				continue
			}
			name := m.GetDescription()
			if name == "" {
				name = m.GetHostname()
			}
			out = append(out, device{hostname: canonName(m.GetHostname() + "." + s.GetDomain()), name: name, mac: m.GetMac()})
		}
	}
	return out
}

// build plans action.
func (p planning) build(action *appspb.AppAction) *planned {
	switch a := action.GetAction().(type) {
	case *appspb.AppAction_Add:
		return p.add(a.Add)
	}
	return &planned{plan: &appspb.Plan{Blocked: []string{"Choose what to do."}}}
}

// add plans Add App: for Reverse proxy a Proxy Host and an alias of the
// Reverse Proxy's Host Override, for Host Override a Host Override of its
// own, then the App record, which reattaches orphaned App settings kept
// for the hostname.
func (p planning) add(a *appspb.AddApp) *planned {
	out := &planned{plan: &appspb.Plan{}}
	block := func(why string) { out.plan.Blocked = append(out.plan.Blocked, why) }
	reach := a.GetReach()
	s := &appspb.AppSettings{
		Name:  strings.TrimSpace(a.GetName()),
		Icon:  strings.ToLower(strings.TrimSpace(a.GetIcon())),
		Group: strings.TrimSpace(a.GetGroup()),
	}

	raw := strings.TrimSpace(a.GetUrl())
	var host string
	if u, err := url.Parse(raw); raw == "" {
		block("Give the App's URL.")
	} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		block("The URL must be an http or https URL, e.g. https://jellyfin.home.example.")
	} else {
		host = canonName(u.Hostname())
	}
	if checkIcon(s.GetIcon()) != nil {
		block("The icon must be a dashboard-icons name, e.g. jellyfin or home-assistant.")
	}
	writes := reach == appspb.Reach_REACH_PROXIED || reach == appspb.Reach_REACH_NOT_PROXIED
	if !writes && reach != appspb.Reach_REACH_LINK_ONLY {
		block("Choose how the App is reached.")
	}
	if reach == appspb.Reach_REACH_PROXIED && !p.npmRead {
		block(cantCheck("Nginx Proxy Manager"))
	}
	if writes && !p.pfRead {
		block(cantCheck("pfSense"))
	}

	// "The name is free".
	var orphan *appspb.App
	switch home := p.homeDomain(); {
	case host == "":
	case writes && !isHostname(host):
		out.plan.Names = append(out.plan.Names, &appspb.NameNote{Hostname: host, Kind: appspb.NameNoteKind_NAME_NOTE_KIND_NOT_A_HOSTNAME, Text: "Not a hostname."})
	case writes && home != "" && host != home && !strings.HasSuffix(host, "."+home):
		block(fmt.Sprintf("%s isn't in %s, the home domain: choose Link only.", host, home))
	default:
		if n := p.nameNote(host, reach, ""); n != nil {
			out.plan.Names = append(out.plan.Names, n)
			if n.GetKind() == appspb.NameNoteKind_NAME_NOTE_KIND_REATTACH {
				orphan = p.app(n.GetAppId())
			}
		}
	}

	switch reach {
	case appspb.Reach_REACH_PROXIED:
		f, why := proxyHostFields(a.GetProxyHost())
		if why != "" {
			block(why)
		}
		if p.readOnly {
			block("ddash's user in Nginx Proxy Manager can only view Proxy Hosts: set them to Manage to add proxied Apps.")
		}
		rp := p.reverseProxy(block)
		f.DomainNames = []string{host}
		out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{
			System: appspb.System_SYSTEM_NGINX_PROXY_MANAGER,
			Title:  "Create Proxy Host " + host,
			Values: proxyHostValues(nil, f),
		})
		out.npm = func(ctx context.Context, c npmpb.NginxProxyManagerApiServiceClient) (*npmpb.ProxyHost, error) {
			resp, err := c.CreateProxyHost(ctx, &npmpb.CreateProxyHostRequest{Fields: f})
			return resp.GetProxyHost(), err
		}
		if rp != nil {
			out.addDNS(addAlias(rp, host))
		}
	case appspb.Reach_REACH_NOT_PROXIED:
		ip, err := netip.ParseAddr(strings.TrimSpace(a.GetAddress()))
		switch {
		case err != nil:
			block("Give the address the Host Override resolves to, e.g. 198.18.1.60.")
		case p.rpAddress != "" && ip.String() == canonHost(p.rpAddress):
			block(fmt.Sprintf("%s is the Reverse Proxy's address: choose Reverse proxy.", ip))
		case host != "":
			out.addDNS(createOverride(splitName(host), ip.String()))
		}
	}

	name := s.GetName()
	if name == "" {
		name = defaultName(host)
	}
	step := &appspb.PlanStep{System: appspb.System_SYSTEM_DDASH, Title: "Create the App " + name, Values: settingValues(raw, s)}
	if orphan != nil {
		step.Title = "Reattach " + orphan.GetName()
		step.Values = append([]*appspb.PlanValue{{Label: "App", Before: orphan.GetId(), After: orphan.GetId()}}, step.Values...)
	}
	out.plan.Steps = append(out.plan.Steps, step)
	out.record = func(recs []*record, written *npmpb.ProxyHost) (changes, string) {
		r := &record{settings: s, touched: s.GetName() != "" || s.GetIcon() != "" || s.GetGroup() != ""}
		if i := slices.IndexFunc(recs, func(r *record) bool { return r.id == orphan.GetId() }); orphan != nil && i >= 0 {
			// The orphan's id, Ignores and settings, those given over them.
			r = recs[i].clone()
			if s.GetName() != "" {
				r.settings.Name = s.GetName()
			}
			if s.GetIcon() != "" {
				r.settings.Icon = s.GetIcon()
			}
			if s.GetGroup() != "" {
				r.settings.Group = s.GetGroup()
			}
			r.aliases = nil
		} else if reach == appspb.Reach_REACH_PROXIED {
			r.id = newID("proxy:"+host, ids(recs))
		} else {
			r.id = newID("", ids(recs))
		}
		r.url = raw
		r.linkOnly = reach == appspb.Reach_REACH_LINK_ONLY
		r.touched = r.touched || r.linkOnly
		if written != nil {
			r.proxyHostID = written.GetId()
		}
		if reach == appspb.Reach_REACH_NOT_PROXIED {
			r.hostOverride = splitName(host)
		}
		return changes{put: []*record{r}}, r.id
	}
	return out
}

// addDNS adds a step to the pfSense batch, and to the Plan.
func (out *planned) addDNS(step *pfpb.HostOverrideStep, title string, values []*appspb.PlanValue) {
	out.dns = append(out.dns, step)
	out.plan.Steps = append(out.plan.Steps, &appspb.PlanStep{System: appspb.System_SYSTEM_PFSENSE, Title: title, Values: values})
}

// addAlias adds the hostname n (canonical) as an alias of the Reverse
// Proxy's Host Override rp, expecting rp at its address.
func addAlias(rp *pfpb.HostOverride, n string) (*pfpb.HostOverrideStep, string, []*appspb.PlanValue) {
	parent, alias := nameOf(rp), splitName(n)
	step := &pfpb.HostOverrideStep{Change: &pfpb.HostOverrideStep_AddAlias{AddAlias: &pfpb.AliasChange{
		Parent: &pfpb.HostName{Host: parent.host, Domain: parent.domain},
		Alias:  &pfpb.HostOverrideAlias{Host: alias.host, Domain: alias.domain},
	}}}
	at := overrideText(rp)
	return step, fmt.Sprintf("Add alias %s to %s", n, parent.fqdn()), []*appspb.PlanValue{
		{Label: "Host Override", Before: at, After: at},
		{Label: "Alias", After: n},
	}
}

// createOverride creates a Host Override named n at ip.
func createOverride(n hostName, ip string) (*pfpb.HostOverrideStep, string, []*appspb.PlanValue) {
	step := &pfpb.HostOverrideStep{Change: &pfpb.HostOverrideStep_Create{Create: &pfpb.HostOverride{Host: n.host, Domain: n.domain, Ips: []string{ip}}}}
	return step, "Create Host Override " + n.fqdn(), []*appspb.PlanValue{{Label: "Host Override", After: n.fqdn() + " → " + ip}}
}

// overrideText is "nginx.home.example → 198.18.1.210".
func overrideText(o *pfpb.HostOverride) string {
	return nameOf(o).fqdn() + " → " + strings.Join(o.GetIps(), ", ")
}

// splitName names the hostname n (canonical) as pfSense does: its first
// label as the host, the rest as the domain.
func splitName(n string) hostName {
	host, domain, ok := strings.Cut(n, ".")
	if !ok {
		return hostName{domain: n}
	}
	return hostName{host, domain}
}

// reverseProxy is the Reverse Proxy's Host Override, which proxied
// hostnames are aliases of; nil, blocking with why, when there isn't
// exactly one.
func (p planning) reverseProxy(block func(string)) *pfpb.HostOverride {
	switch {
	case p.dns == nil || !p.npmRead:
		return nil // blocked as unreadable
	case p.rpAddress == "":
		block("ddash doesn't know the Reverse Proxy's address: Nginx Proxy Manager's URL doesn't resolve.")
		return nil
	case p.dns.notice() != "":
		block(p.dns.notice())
		return nil
	}
	return p.dns.reverseProxy()[0]
}

// homeDomain is the Reverse Proxy's Host Override's domain, which proxied
// and Not-proxied hostnames are in; "" while there isn't exactly one.
func (p planning) homeDomain() string {
	if p.dns == nil || p.rpAddress == "" {
		return ""
	}
	if rp := p.dns.reverseProxy(); len(rp) == 1 {
		return canonName(rp[0].GetDomain())
	}
	return ""
}

func (p planning) app(id string) *appspb.App {
	for _, a := range p.apps {
		if a.GetId() == id {
			return a
		}
	}
	return nil
}

// nameNote is "the name is free" for the hostname n (canonical), added to
// the App self ("" for a new one) with reach: why it is taken, or that it
// reattaches orphaned App settings; nil when it is free. The App's own
// names never count against it. Link only is checked against Apps alone.
func (p planning) nameNote(n string, reach appspb.Reach, self string) *appspb.NameNote {
	note := func(k appspb.NameNoteKind, format string, args ...any) *appspb.NameNote {
		return &appspb.NameNote{Hostname: n, Kind: k, Text: fmt.Sprintf(format, args...)}
	}
	for _, a := range p.apps {
		if a.GetId() == self || a.GetOrphaned() || !slices.Contains(appHostnames(a), n) {
			continue
		}
		out := note(appspb.NameNoteKind_NAME_NOTE_KIND_APP, "%s is taken by %s.", n, a.GetName())
		if a.GetHidden() {
			out.Text = fmt.Sprintf("%s is taken by %s (Hidden).", n, a.GetName())
		}
		out.AppId, out.Name, out.Hidden = a.GetId(), a.GetName(), a.GetHidden()
		return out
	}
	if reach != appspb.Reach_REACH_LINK_ONLY {
		if p.dns != nil {
			for _, o := range p.dns.reverseProxy() {
				if nameOf(o).fqdn() == n {
					return note(appspb.NameNoteKind_NAME_NOTE_KIND_REVERSE_PROXY, "%s is the Reverse Proxy's own name.", n)
				}
			}
			if len(p.dns.entries[n]) > 0 {
				return note(appspb.NameNoteKind_NAME_NOTE_KIND_HOST_OVERRIDE, "%s is already registered in pfSense.", n)
			}
		}
		if p.v.served(n) {
			return note(appspb.NameNoteKind_NAME_NOTE_KIND_PROXY_HOST, "%s is already a Proxy Host's name in Nginx Proxy Manager.", n)
		}
		for _, d := range p.devices {
			if d.hostname == n {
				out := note(appspb.NameNoteKind_NAME_NOTE_KIND_DEVICE, "%s is the Device %s's name. To link to it as it is, choose Link only.", n, d.name)
				out.Name, out.DeviceMac = d.name, d.mac
				return out
			}
		}
	}
	for _, a := range p.apps {
		if a.GetId() != self && a.GetOrphaned() && slices.Contains(appHostnames(a), n) {
			out := note(appspb.NameNoteKind_NAME_NOTE_KIND_REATTACH, "Settings were kept for %s: adding it reattaches them, with its Check history.", a.GetName())
			out.AppId, out.Name = a.GetId(), a.GetName()
			return out
		}
	}
	return nil
}

func cantCheck(source string) string {
	return fmt.Sprintf("Can't check %s right now: its last read failed.", source)
}

// hostnameRE is a DNS hostname with at least two labels, canonical.
var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// isHostname reports whether n (canonical) is a hostname a Proxy Host or
// Host Override can have: no IP address.
func isHostname(n string) bool {
	if _, err := netip.ParseAddr(n); err == nil {
		return false
	}
	return hostnameRE.MatchString(n)
}

// proxyHostFields are the Proxy Host fields as Nginx Proxy Manager will
// save them, with its toggle rules applied, and why they can't be saved
// ("" when they can).
func proxyHostFields(in *appspb.ProxyHostFields) (*npmpb.ProxyHostFields, string) {
	f := &npmpb.ProxyHostFields{
		ForwardScheme:  strings.ToLower(strings.TrimSpace(in.GetForwardScheme())),
		ForwardHost:    strings.TrimSpace(in.GetForwardHost()),
		ForwardPort:    in.GetForwardPort(),
		CertificateId:  in.GetCertificateId(),
		BlockExploits:  in.GetBlockExploits(),
		Websockets:     in.GetWebsockets(),
		ForceSsl:       in.GetForceSsl(),
		Http2:          in.GetHttp2(),
		Hsts:           in.GetHsts(),
		HstsSubdomains: in.GetHstsSubdomains(),
	}
	if f.GetCertificateId() == 0 {
		f.ForceSsl, f.Http2 = false, false
	}
	if !f.GetForceSsl() {
		f.Hsts = false
	}
	if !f.GetHsts() {
		f.HstsSubdomains = false
	}
	switch {
	case f.GetForwardScheme() != "http" && f.GetForwardScheme() != "https":
		return f, "The upstream's scheme must be http or https."
	case f.GetForwardHost() == "":
		return f, "Give the upstream's host, e.g. 198.18.2.10."
	case f.GetForwardPort() < 1 || f.GetForwardPort() > 65535:
		return f, "The upstream's port must be 1 to 65535."
	case f.GetCertificateId() < 0:
		return f, "Pick one of Nginx Proxy Manager's certificates, or none."
	}
	return f, ""
}

// proxyHostValues are a Proxy Host's managed fields as a step expects them
// (before; nil for no Proxy Host) and writes them (after).
func proxyHostValues(before, after *npmpb.ProxyHostFields) []*appspb.PlanValue {
	texts := func(f *npmpb.ProxyHostFields) []string {
		if f == nil {
			return make([]string, 4)
		}
		cert := "None: plain HTTP"
		if f.GetCertificateId() != 0 {
			cert = "#" + strconv.FormatInt(f.GetCertificateId(), 10)
		}
		var opts []string
		for _, o := range []struct {
			on   bool
			name string
		}{
			{f.GetBlockExploits(), "Block Common Exploits"}, {f.GetWebsockets(), "Websockets"}, {f.GetForceSsl(), "Force SSL"},
			{f.GetHttp2(), "HTTP/2"}, {f.GetHsts(), "HSTS"}, {f.GetHstsSubdomains(), "HSTS subdomains"},
		} {
			if o.on {
				opts = append(opts, o.name)
			}
		}
		options := "None"
		if len(opts) > 0 {
			options = strings.Join(opts, " · ")
		}
		upstream := fmt.Sprintf("%s://%s:%d", f.GetForwardScheme(), f.GetForwardHost(), f.GetForwardPort())
		return []string{strings.Join(f.GetDomainNames(), ", "), upstream, cert, options}
	}
	b, a := texts(before), texts(after)
	var out []*appspb.PlanValue
	for i, label := range []string{"Domain names", "Upstream", "Certificate", "Options"} {
		out = append(out, &appspb.PlanValue{Label: label, Before: b[i], After: a[i]})
	}
	return out
}

// settingValues are an App record's URL and settings, as a step writes
// them.
func settingValues(raw string, s *appspb.AppSettings) []*appspb.PlanValue {
	out := []*appspb.PlanValue{{Label: "URL", After: raw}}
	for _, v := range []struct{ label, value string }{{"Name", s.GetName()}, {"Icon", s.GetIcon()}, {"Group", s.GetGroup()}} {
		if v.value != "" {
			out = append(out, &appspb.PlanValue{Label: v.label, After: v.value})
		}
	}
	return out
}

// ids are the records' ids, taken.
func ids(recs []*record) map[string]bool {
	out := map[string]bool{}
	for _, r := range recs {
		out[r.id] = true
	}
	return out
}
