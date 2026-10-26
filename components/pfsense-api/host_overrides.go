package pfsenseapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The DNS Resolver's Host Overrides. A batch of changes runs as one write
// in the queue (c.write). pfSense's ids are positions, and every write
// re-sorts the Host Overrides by host and the written one's aliases too,
// so each step reads the Host Overrides again right before it is sent and
// finds its target by host and domain. The steps don't apply: each apply
// restarts Unbound, so the batch applies once at the end, also after a
// failed step, so live DNS matches what was written.

const (
	hostOverridesPath = "/api/v2/services/dns_resolver/host_overrides"
	hostOverridePath  = "/api/v2/services/dns_resolver/host_override"
	overrideAliasPath = "/api/v2/services/dns_resolver/host_override/alias"
	dnsApplyPath      = "/api/v2/services/dns_resolver/apply"
)

// applyTimeout bounds the DNS Resolver's apply, which waits for Unbound to
// stop (up to 30 s) and start again. It leaves room inside the host's
// 60 s Action deadline for the rest of an App Action.
const applyTimeout = 30 * time.Second

// hostOverride is one entry of GET services/dns_resolver/host_overrides.
type hostOverride struct {
	Host    string          `json:"host"`
	Domain  string          `json:"domain"`
	IP      stringList      `json:"ip"`
	Descr   *string         `json:"descr"`
	Aliases []overrideAlias `json:"aliases"`
}

// overrideAlias is one of a Host Override's aliases.
type overrideAlias struct {
	Host   string  `json:"host"`
	Domain string  `json:"domain"`
	Descr  *string `json:"descr"`
}

func (h *hostOverride) proto() *pfsenseapipb.HostOverride {
	out := &pfsenseapipb.HostOverride{Host: h.Host, Domain: h.Domain, Ips: h.IP, Description: str(h.Descr)}
	for _, a := range h.Aliases {
		out.Aliases = append(out.Aliases, &pfsenseapipb.HostOverrideAlias{Host: a.Host, Domain: a.Domain, Description: str(a.Descr)})
	}
	return out
}

func (c *component) ListHostOverrides(ctx context.Context, req *pfsenseapipb.ListHostOverridesRequest) (*pfsenseapipb.ListHostOverridesResponse, error) {
	if req.GetFresh() {
		c.overrides.Invalidate()
	}
	return c.overrides.Get(ctx)
}

// readHostOverrides reads the Host Overrides uncached; a step's lookup
// uses it.
func (c *component) readHostOverrides(ctx context.Context) ([]hostOverride, error) {
	var data []hostOverride
	if err := c.api.get(ctx, hostOverridesPath, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *component) fetchHostOverrides(ctx context.Context) (*pfsenseapipb.ListHostOverridesResponse, error) {
	data, err := c.readHostOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListHostOverridesResponse{}
	for i := range data {
		out.HostOverrides = append(out.HostOverrides, data[i].proto())
	}
	return out, nil
}

func (c *component) ChangeHostOverrides(ctx context.Context, req *pfsenseapipb.ChangeHostOverridesRequest) (*pfsenseapipb.ChangeHostOverridesResponse, error) {
	steps := req.GetSteps()
	for i, s := range steps {
		if err := checkStep(s); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "step %d: %v", i+1, err)
		}
	}
	var succeeded int
	var applyFailed bool
	err := c.write(ctx, func() error {
		var stepErr error
		sent := false
		for i, s := range steps {
			wrote, err := c.runStep(ctx, s)
			sent = sent || wrote
			if err != nil {
				stepErr = status.Errorf(status.Code(err), "step %d of %d (%s): %s", i+1, len(steps), describe(s), status.Convert(err).Message())
				break
			}
			succeeded++
		}
		if !sent {
			return stepErr
		}
		applyErr := c.applyDNS(ctx)
		applyFailed = applyErr != nil
		switch {
		case stepErr != nil && applyErr != nil:
			return status.Errorf(status.Code(stepErr), "%s; applying what was written failed too: %s", status.Convert(stepErr).Message(), status.Convert(applyErr).Message())
		case stepErr != nil:
			return status.Errorf(status.Code(stepErr), "%s; what was written is applied", status.Convert(stepErr).Message())
		case applyErr != nil:
			return status.Errorf(status.Code(applyErr), "every step was written, but applying them failed: %s", status.Convert(applyErr).Message())
		}
		return nil
	})
	out := &pfsenseapipb.ChangeHostOverridesResponse{Succeeded: int32(succeeded), ApplyFailed: applyFailed}
	if err != nil {
		if st, derr := status.Convert(err).WithDetails(out); derr == nil {
			return nil, st.Err()
		}
		return nil, err
	}
	return out, nil
}

// applyDNS applies the pending DNS Resolver changes and waits for Unbound
// to be back, up to applyTimeout, even when ctx is done: what was written
// goes live.
func (c *component) applyDNS(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
	defer cancel()
	var state struct {
		Applied bool `json:"applied"`
	}
	return c.api.sendWithin(ctx, applyTimeout, http.MethodPost, dnsApplyPath, map[string]any{"async": false}, &state)
}

// runStep sends one step, looking its target up right before. wrote is
// whether a write was sent, failed or not.
func (c *component) runStep(ctx context.Context, s *pfsenseapipb.HostOverrideStep) (wrote bool, err error) {
	all, err := c.readHostOverrides(ctx)
	if err != nil {
		return false, err
	}
	var discard any
	switch ch := s.GetChange().(type) {
	case *pfsenseapipb.HostOverrideStep_Create:
		h := ch.Create
		if err := free(all, h.GetHost(), h.GetDomain()); err != nil {
			return false, err
		}
		body := map[string]any{"host": h.GetHost(), "domain": h.GetDomain(), "ip": h.GetIps(), "descr": h.GetDescription()}
		return true, c.api.send(ctx, http.MethodPost, hostOverridePath, body, &discard)
	case *pfsenseapipb.HostOverrideStep_Delete:
		id, err := find(all, ch.Delete)
		if err != nil {
			return false, err
		}
		q := url.Values{"id": {strconv.Itoa(id)}}
		return true, c.api.send(ctx, http.MethodDelete, hostOverridePath+"?"+q.Encode(), nil, &discard)
	case *pfsenseapipb.HostOverrideStep_Readdress:
		id, err := find(all, ch.Readdress.GetHostOverride())
		if err != nil {
			return false, err
		}
		body := map[string]any{"id": id, "ip": ch.Readdress.GetIps()}
		return true, c.api.send(ctx, http.MethodPatch, hostOverridePath, body, &discard)
	case *pfsenseapipb.HostOverrideStep_AddAlias:
		parent, err := find(all, ch.AddAlias.GetParent())
		if err != nil {
			return false, err
		}
		a := ch.AddAlias.GetAlias()
		if err := free(all, a.GetHost(), a.GetDomain()); err != nil {
			return false, err
		}
		body := map[string]any{"parent_id": parent, "host": a.GetHost(), "domain": a.GetDomain(), "descr": a.GetDescription()}
		return true, c.api.send(ctx, http.MethodPost, overrideAliasPath, body, &discard)
	case *pfsenseapipb.HostOverrideStep_RemoveAlias:
		parent, err := find(all, ch.RemoveAlias.GetParent())
		if err != nil {
			return false, err
		}
		a := ch.RemoveAlias.GetAlias()
		id := -1
		for i, x := range all[parent].Aliases {
			if same(x.Host, x.Domain, a.GetHost(), a.GetDomain()) {
				id = i
				break
			}
		}
		if id < 0 {
			return false, status.Errorf(codes.NotFound, "%s has no alias %s", fqdn(all[parent].Host, all[parent].Domain), fqdn(a.GetHost(), a.GetDomain()))
		}
		q := url.Values{"parent_id": {strconv.Itoa(parent)}, "id": {strconv.Itoa(id)}}
		return true, c.api.send(ctx, http.MethodDelete, overrideAliasPath+"?"+q.Encode(), nil, &discard)
	}
	return false, status.Error(codes.InvalidArgument, "a step changes nothing")
}

// find is the position of the Host Override named n.
func find(all []hostOverride, n *pfsenseapipb.HostName) (int, error) {
	for i, h := range all {
		if same(h.Host, h.Domain, n.GetHost(), n.GetDomain()) {
			return i, nil
		}
	}
	return -1, status.Errorf(codes.NotFound, "pfSense has no Host Override %s", fqdn(n.GetHost(), n.GetDomain()))
}

// free refuses a hostname some Host Override or alias already registers.
// pfSense itself only refuses a Host Override's own name twice, and an
// alias twice within one parent.
func free(all []hostOverride, host, domain string) error {
	name := fqdn(host, domain)
	for _, h := range all {
		if fqdn(h.Host, h.Domain) == name {
			return status.Errorf(codes.AlreadyExists, "%s is already a Host Override", name)
		}
		for _, a := range h.Aliases {
			if fqdn(a.Host, a.Domain) == name {
				return status.Errorf(codes.AlreadyExists, "%s is already an alias of %s", name, fqdn(h.Host, h.Domain))
			}
		}
	}
	return nil
}

// fqdn is the hostname host and domain name, in lower case without a
// trailing dot; an empty host names the domain.
func fqdn(host, domain string) string {
	name := strings.TrimSuffix(domain, ".")
	if host != "" {
		name = host + "." + name
	}
	return strings.ToLower(name)
}

// same reports whether two host and domain pairs name the same entry.
func same(host, domain, otherHost, otherDomain string) bool {
	return strings.EqualFold(host, otherHost) && strings.EqualFold(strings.TrimSuffix(domain, "."), strings.TrimSuffix(otherDomain, "."))
}

// checkStep refuses a step that names no domain or no valid address.
func checkStep(s *pfsenseapipb.HostOverrideStep) error {
	named := func(what, domain string) error {
		if strings.TrimSuffix(domain, ".") == "" {
			return fmt.Errorf("give the %s's domain", what)
		}
		return nil
	}
	addressed := func(ips []string) error {
		if len(ips) == 0 {
			return errors.New("give at least one address")
		}
		for _, ip := range ips {
			if _, err := netip.ParseAddr(ip); err != nil {
				return fmt.Errorf("%q is no IP address", ip)
			}
		}
		return nil
	}
	switch ch := s.GetChange().(type) {
	case *pfsenseapipb.HostOverrideStep_Create:
		return errors.Join(named("Host Override", ch.Create.GetDomain()), addressed(ch.Create.GetIps()))
	case *pfsenseapipb.HostOverrideStep_Delete:
		return named("Host Override", ch.Delete.GetDomain())
	case *pfsenseapipb.HostOverrideStep_Readdress:
		n := ch.Readdress.GetHostOverride()
		return errors.Join(named("Host Override", n.GetDomain()), addressed(ch.Readdress.GetIps()))
	case *pfsenseapipb.HostOverrideStep_AddAlias:
		p, a := ch.AddAlias.GetParent(), ch.AddAlias.GetAlias()
		return errors.Join(named("parent", p.GetDomain()), named("alias", a.GetDomain()))
	case *pfsenseapipb.HostOverrideStep_RemoveAlias:
		p, a := ch.RemoveAlias.GetParent(), ch.RemoveAlias.GetAlias()
		return errors.Join(named("parent", p.GetDomain()), named("alias", a.GetDomain()))
	}
	return errors.New("it changes nothing")
}

// describe names a step for an error: "add alias grafana.home.example to
// nginx.home.example".
func describe(s *pfsenseapipb.HostOverrideStep) string {
	switch ch := s.GetChange().(type) {
	case *pfsenseapipb.HostOverrideStep_Create:
		return "create " + fqdn(ch.Create.GetHost(), ch.Create.GetDomain())
	case *pfsenseapipb.HostOverrideStep_Delete:
		return "delete " + fqdn(ch.Delete.GetHost(), ch.Delete.GetDomain())
	case *pfsenseapipb.HostOverrideStep_Readdress:
		n := ch.Readdress.GetHostOverride()
		return fmt.Sprintf("readdress %s to %s", fqdn(n.GetHost(), n.GetDomain()), strings.Join(ch.Readdress.GetIps(), ", "))
	case *pfsenseapipb.HostOverrideStep_AddAlias:
		p, a := ch.AddAlias.GetParent(), ch.AddAlias.GetAlias()
		return fmt.Sprintf("add alias %s to %s", fqdn(a.GetHost(), a.GetDomain()), fqdn(p.GetHost(), p.GetDomain()))
	case *pfsenseapipb.HostOverrideStep_RemoveAlias:
		p, a := ch.RemoveAlias.GetParent(), ch.RemoveAlias.GetAlias()
		return fmt.Sprintf("remove alias %s from %s", fqdn(a.GetHost(), a.GetDomain()), fqdn(p.GetHost(), p.GetDomain()))
	}
	return "no change"
}
