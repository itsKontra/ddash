package apps

import (
	"fmt"
	"slices"
	"strings"

	"github.com/itsKontra/ddash/components/apps/appspb"
	pfpb "github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// hostName names a Host Override as pfSense does, by host and domain;
// the zero value names none.
type hostName struct {
	host, domain string
}

func (n hostName) isZero() bool { return n.domain == "" }

// fqdn is the hostname n names, canonical: host.domain, or the domain
// alone for an empty host.
func (n hostName) fqdn() string {
	if n.host == "" {
		return canonName(n.domain)
	}
	return canonName(n.host + "." + n.domain)
}

func (n hostName) same(o hostName) bool { return n.fqdn() == o.fqdn() && !n.isZero() && !o.isZero() }

func (n hostName) proto() *appspb.HostOverrideName {
	if n.isZero() {
		return nil
	}
	return &appspb.HostOverrideName{Host: n.host, Domain: n.domain}
}

func nameOf(o *pfpb.HostOverride) hostName { return hostName{o.GetHost(), o.GetDomain()} }

// dns is a good read of pfSense's Host Overrides, seen from the Reverse
// Proxy's address.
type dns struct {
	// rpAddress is the Reverse Proxy's address, canonical.
	rpAddress string
	overrides []*pfpb.HostOverride
	// registering are the Host Overrides and aliases registering each
	// hostname, by canonical hostname, in pfSense's order.
	registering map[string][]*appspb.HostOverrideOrAlias
}

// newDNS indexes a read of the Host Overrides; rpAddress is the Reverse
// Proxy's address.
func newDNS(overrides []*pfpb.HostOverride, rpAddress string) *dns {
	d := &dns{rpAddress: canonHost(rpAddress), overrides: overrides, registering: map[string][]*appspb.HostOverrideOrAlias{}}
	for _, o := range overrides {
		own := nameOf(o)
		d.registering[own.fqdn()] = append(d.registering[own.fqdn()], &appspb.HostOverrideOrAlias{Host: o.GetHost(), Domain: o.GetDomain(), Ips: o.GetIps()})
		for _, a := range o.GetAliases() {
			n := hostName{a.GetHost(), a.GetDomain()}.fqdn()
			d.registering[n] = append(d.registering[n], &appspb.HostOverrideOrAlias{Host: a.GetHost(), Domain: a.GetDomain(), Parent: own.proto(), Ips: o.GetIps()})
		}
	}
	return d
}

// atReverseProxy reports whether ips hold the Reverse Proxy's address.
func (d *dns) atReverseProxy(ips []string) bool {
	return slices.ContainsFunc(ips, func(ip string) bool { return canonHost(ip) == d.rpAddress })
}

// reverseProxy are the Host Overrides at the Reverse Proxy's address: the
// Reverse Proxy's Host Override when there is exactly one. Their own
// names are the Reverse Proxy's, their aliases proxied hostnames.
func (d *dns) reverseProxy() []*pfpb.HostOverride {
	var out []*pfpb.HostOverride
	for _, o := range d.overrides {
		if d.atReverseProxy(o.GetIps()) {
			out = append(out, o)
		}
	}
	return out
}

// notProxied are the Host Overrides pointing elsewhere: each with its
// aliases is a Not-proxied App's source.
func (d *dns) notProxied() []*pfpb.HostOverride {
	var out []*pfpb.HostOverride
	for _, o := range d.overrides {
		if !d.atReverseProxy(o.GetIps()) {
			out = append(out, o)
		}
	}
	return out
}

// override is the Not-proxied Host Override n names; nil for none.
func (d *dns) override(n hostName) *pfpb.HostOverride {
	for _, o := range d.notProxied() {
		if nameOf(o).same(n) {
			return o
		}
	}
	return nil
}

// named is the Host Override n names, wherever it points; nil for none.
func (d *dns) named(n hostName) *pfpb.HostOverride {
	for _, o := range d.overrides {
		if nameOf(o).same(n) {
			return o
		}
	}
	return nil
}

// proxiedNames are the aliases at the Reverse Proxy's address, canonical,
// in pfSense's order: each is a proxied App's hostname.
func (d *dns) proxiedNames() []string {
	var out []string
	for _, o := range d.reverseProxy() {
		for _, a := range o.GetAliases() {
			if n := (hostName{a.GetHost(), a.GetDomain()}).fqdn(); !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// proxied reports whether the hostname n (canonical) is an alias at the
// Reverse Proxy's address.
func (d *dns) proxied(n string) bool {
	return slices.ContainsFunc(d.registering[n], func(e *appspb.HostOverrideOrAlias) bool { return e.GetParent() != nil && d.atReverseProxy(e.GetIps()) })
}

// elsewhere reports whether the hostname n (canonical) is registered at
// an address other than the Reverse Proxy's.
func (d *dns) elsewhere(n string) bool {
	return slices.ContainsFunc(d.registering[n], func(e *appspb.HostOverrideOrAlias) bool { return !d.atReverseProxy(e.GetIps()) })
}

// overrideNames are a Host Override's own name and its aliases',
// canonical.
func overrideNames(o *pfpb.HostOverride) []string {
	out := []string{nameOf(o).fqdn()}
	for _, a := range o.GetAliases() {
		out = append(out, hostName{a.GetHost(), a.GetDomain()}.fqdn())
	}
	return out
}

// notice is why proxied writes are blocked: no Host Override, or several,
// at the Reverse Proxy's address; empty when exactly one is.
func (d *dns) notice() string {
	rp := d.reverseProxy()
	switch len(rp) {
	case 1:
		return ""
	case 0:
		return fmt.Sprintf("No Host Override points at the Reverse Proxy (%s): create one to add proxied Apps.", d.rpAddress)
	}
	names := make([]string, len(rp))
	for i, o := range rp {
		names[i] = nameOf(o).fqdn()
	}
	return fmt.Sprintf("%d Host Overrides point at the Reverse Proxy (%s): %s. Keep one to add proxied Apps.", len(rp), d.rpAddress, strings.Join(names, ", "))
}

// reverseProxyOf is ListApps' view of the Reverse Proxy; d may be nil
// (not read yet), with the address known or not.
func reverseProxyOf(d *dns, address string) *appspb.ReverseProxy {
	out := &appspb.ReverseProxy{Address: address}
	if d == nil {
		return out
	}
	for _, o := range d.reverseProxy() {
		out.HostOverrides = append(out.HostOverrides, nameOf(o).fqdn())
	}
	out.Notice = d.notice()
	return out
}
