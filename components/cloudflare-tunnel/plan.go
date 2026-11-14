package cloudflaretunnel

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	cfpb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	pb "github.com/itsKontra/ddash/components/cloudflare-tunnel/cloudflaretunnelpb"
)

// The one Plan builder: every Tunnel Action, shown (PlanAction) or run
// (RunAction), gets its Plan from snapshot.build. A planned Action holds
// its Plan as the Operator sees it and how each of its steps runs, one
// run func per PlanStep.

// planned is a Tunnel Action's Plan and its steps, ready to run.
type planned struct {
	plan *pb.Plan
	// runs runs each of the Plan's steps, in its order.
	runs []func(ctx context.Context, c *component) error
}

func (p *planned) block(format string, args ...any) {
	p.plan.Blocked = append(p.plan.Blocked, fmt.Sprintf(format, args...))
}

func (p *planned) note(format string, args ...any) {
	p.plan.Notes = append(p.plan.Notes, fmt.Sprintf(format, args...))
}

// step adds a step to the Plan and how it runs.
func (p *planned) step(system pb.System, title string, values []*pb.PlanValue, run func(ctx context.Context, c *component) error) {
	p.plan.Steps = append(p.plan.Steps, &pb.PlanStep{System: system, Title: title, Values: values})
	p.runs = append(p.runs, run)
}

// CNAMEComment is the comment on each CNAME ddash creates.
const CNAMEComment = "Public Hostname of the Cloudflare Tunnel, added by ddash"

// build plans action from the snapshot.
func (s *snapshot) build(action *pb.TunnelAction) *planned {
	p := &planned{plan: &pb.Plan{}}
	if !s.setUp {
		p.block("Set up cloudflare-api in Plugins first.")
		return p
	}
	if !s.remote {
		p.block("This Cloudflare Tunnel is locally managed (cloudflared's config.yml): ddash changes only remotely managed Tunnels.")
		return p
	}
	switch a := action.GetAction().(type) {
	case *pb.TunnelAction_Add:
		s.add(p, a.Add.GetFields())
	case *pb.TunnelAction_Edit:
		s.edit(p, a.Edit.GetName(), a.Edit.GetFields())
	case *pb.TunnelAction_Remove:
		s.remove(p, a.Remove.GetName())
	case *pb.TunnelAction_Disable:
		s.disable(p, a.Disable.GetName())
	case *pb.TunnelAction_Enable:
		s.enable(p, a.Enable.GetName())
	default:
		p.block("Pick what to do.")
	}
	return p
}

// label is a Public Hostname as the Operator reads it.
func label(hostname, path string) string {
	if path == "" {
		return hostname
	}
	return hostname + " (path " + path + ")"
}

func nameOf(f *pb.PublicHostnameFields) *pb.PublicHostnameName {
	return &pb.PublicHostnameName{Hostname: f.GetHostname(), Path: f.GetPath()}
}

var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// normal is f with its hostname lowercased and its fields trimmed.
func normal(f *pb.PublicHostnameFields) *pb.PublicHostnameFields {
	return &pb.PublicHostnameFields{
		Hostname: strings.TrimSuffix(strings.ToLower(strings.TrimSpace(f.GetHostname())), "."),
		Path:     strings.TrimSpace(f.GetPath()),
		Service:  strings.TrimSpace(f.GetService()),
	}
}

// validHostname: two labels or more, each a DNS label.
func validHostname(h string) bool {
	labels := strings.Split(h, ".")
	if len(labels) < 2 || len(h) > 253 {
		return false
	}
	for _, l := range labels {
		if !labelPattern.MatchString(l) {
			return false
		}
	}
	return true
}

var statusService = regexp.MustCompile(`^http_status:[1-5][0-9]{2}$`)

// serviceSchemes are the origins cloudflared forwards to by URL.
var serviceSchemes = []string{"http", "https", "tcp", "ssh", "rdp", "smb", "unix", "unix+tls"}

// validService: an origin URL cloudflared takes, http_status:NNN or
// hello_world.
func validService(s string) bool {
	if statusService.MatchString(s) || s == "hello_world" {
		return true
	}
	u, err := url.Parse(s)
	if err != nil || !slices.Contains(serviceSchemes, u.Scheme) {
		return false
	}
	if strings.HasPrefix(u.Scheme, "unix") {
		return u.Path != "" || u.Opaque != ""
	}
	return u.Host != "" && (u.Path == "" || u.Path == "/")
}

// checkFields blocks fields a Public Hostname can't have.
func (p *planned) checkFields(f *pb.PublicHostnameFields) {
	if !validHostname(f.GetHostname()) {
		p.block("%q is not a hostname: use a name like app.home.example.", f.GetHostname())
	}
	if !validService(f.GetService()) {
		p.block("%q is not a service cloudflared forwards to: use a URL like http://198.18.1.20:8080, or http_status:404.", f.GetService())
	}
}

// taken blocks a name another Public Hostname has, enabled or disabled,
// but the one at self (nil for none).
func (s *snapshot) taken(p *planned, name, self *pb.PublicHostnameName) {
	same := func(n *pb.PublicHostnameName) bool {
		return self != nil && n.GetHostname() == self.GetHostname() && n.GetPath() == self.GetPath()
	}
	if same(name) {
		return
	}
	if s.findRule(name) != nil {
		p.block("%s is a Public Hostname already.", label(name.GetHostname(), name.GetPath()))
	} else if _, ok := s.findKept(name); ok {
		p.block("%s is a disabled Public Hostname: enable it instead.", label(name.GetHostname(), name.GetPath()))
	}
}

// sharesHostname: another ingress rule than skip has hostname.
func (s *snapshot) sharesHostname(hostname string, skip *pb.PublicHostnameName) bool {
	for _, r := range s.config.GetRules() {
		if r.GetHostname() == hostname && r.GetPath() != skip.GetPath() {
			return true
		}
	}
	return false
}

// yes is a bool as a Plan value.
func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ensureCNAME adds the step giving hostname its proxied CNAME to the
// Tunnel, when it lacks one; it blocks a name another record holds, or
// one in no zone.
func (s *snapshot) ensureCNAME(p *planned, hostname string) {
	d := s.dns(hostname)
	switch {
	case d.zone == nil:
		zones := make([]string, 0, len(s.zones))
		for _, z := range s.zones {
			zones = append(zones, z.GetName())
		}
		p.block("%s is in no zone of this Cloudflare account (%s).", hostname, strings.Join(zones, ", "))
	case len(d.others) > 0 && d.cname == nil:
		p.block("%s has a DNS record already (%s): remove it in Cloudflare first.", hostname, d.detail)
	case d.cname != nil && d.cname.GetFields().GetProxied():
		p.note("%s has its CNAME to the Cloudflare Tunnel already.", hostname)
	case d.cname != nil:
		rec, zone := d.cname, d.zone.GetId()
		p.step(pb.System_SYSTEM_CLOUDFLARE_DNS, "Proxy the CNAME "+hostname, []*pb.PlanValue{{Label: "Proxied", Before: "no", After: "yes"}},
			func(ctx context.Context, c *component) error {
				f := rec.GetFields()
				_, err := c.cf.UpdateDnsRecord(ctx, &cfpb.UpdateDnsRecordRequest{ZoneId: zone, Id: rec.GetId(), Fields: &cfpb.DnsRecordFields{
					Name: f.GetName(), Type: f.GetType(), Content: f.GetContent(), Proxied: true, Comment: f.GetComment(),
				}})
				return err
			})
	default:
		zone, target := d.zone.GetId(), s.target
		p.step(pb.System_SYSTEM_CLOUDFLARE_DNS, "Create the CNAME "+hostname, []*pb.PlanValue{{Label: "Target", After: target}, {Label: "Proxied", After: yes(true)}},
			func(ctx context.Context, c *component) error {
				_, err := c.cf.CreateDnsRecord(ctx, &cfpb.CreateDnsRecordRequest{ZoneId: zone, Fields: &cfpb.DnsRecordFields{
					Name: hostname, Type: "CNAME", Content: target, Proxied: true, Comment: CNAMEComment,
				}})
				return err
			})
	}
}

// dropCNAME adds the step deleting hostname's CNAME to the Tunnel, unless
// another rule than name still serves the hostname; a record that isn't
// the Tunnel's is left, with a note.
func (s *snapshot) dropCNAME(p *planned, hostname string, name *pb.PublicHostnameName) {
	if s.sharesHostname(hostname, name) {
		p.note("%s keeps its CNAME: another Public Hostname with a path serves it.", hostname)
		return
	}
	d := s.dns(hostname)
	switch {
	case d.cname != nil:
		rec, zone := d.cname, d.zone.GetId()
		p.step(pb.System_SYSTEM_CLOUDFLARE_DNS, "Delete the CNAME "+hostname, []*pb.PlanValue{{Label: "Target", Before: rec.GetFields().GetContent()}},
			func(ctx context.Context, c *component) error {
				_, err := c.cf.DeleteDnsRecord(ctx, &cfpb.DeleteDnsRecordRequest{ZoneId: zone, Id: rec.GetId()})
				return err
			})
	case len(d.others) > 0:
		p.note("%s's DNS record (%s) doesn't point at the Cloudflare Tunnel: ddash leaves it.", hostname, d.detail)
	default:
		p.note("%s has no CNAME to delete.", hostname)
	}
}

func ruleValues(before, after *cfpb.IngressRule) []*pb.PlanValue {
	var out []*pb.PlanValue
	add := func(l, b, a string) {
		if b != a || l == "Service" {
			out = append(out, &pb.PlanValue{Label: l, Before: b, After: a})
		}
	}
	add("Hostname", before.GetHostname(), after.GetHostname())
	add("Path", before.GetPath(), after.GetPath())
	add("Service", before.GetService(), after.GetService())
	return out
}

func (p *planned) addRule(rule *cfpb.IngressRule) {
	p.step(pb.System_SYSTEM_CLOUDFLARE_TUNNEL, "Add the ingress rule for "+label(rule.GetHostname(), rule.GetPath()), ruleValues(nil, rule),
		func(ctx context.Context, c *component) error {
			_, err := c.cf.PutIngressRule(ctx, &cfpb.PutIngressRuleRequest{Rule: rule})
			return err
		})
}

func (p *planned) deleteRule(rule *cfpb.IngressRule) {
	p.step(pb.System_SYSTEM_CLOUDFLARE_TUNNEL, "Remove the ingress rule for "+label(rule.GetHostname(), rule.GetPath()), []*pb.PlanValue{{Label: "Service", Before: rule.GetService()}},
		func(ctx context.Context, c *component) error {
			_, err := c.cf.DeleteIngressRule(ctx, &cfpb.DeleteIngressRuleRequest{Rule: &cfpb.RuleName{Hostname: rule.GetHostname(), Path: rule.GetPath()}})
			return err
		})
}

func (s *snapshot) add(p *planned, fields *pb.PublicHostnameFields) {
	f := normal(fields)
	p.checkFields(f)
	if len(p.plan.Blocked) > 0 {
		return
	}
	s.taken(p, nameOf(f), nil)
	rule := &cfpb.IngressRule{Hostname: f.GetHostname(), Path: f.GetPath(), Service: f.GetService()}
	p.addRule(rule)
	s.ensureCNAME(p, f.GetHostname())
}

func (s *snapshot) edit(p *planned, name *pb.PublicHostnameName, fields *pb.PublicHostnameFields) {
	f := normal(fields)
	p.checkFields(f)
	if len(p.plan.Blocked) > 0 {
		return
	}
	rule := s.findRule(name)
	k, isKept := s.findKept(name)
	if rule == nil && !isKept {
		p.block("%s is no Public Hostname now.", label(name.GetHostname(), name.GetPath()))
		return
	}
	s.taken(p, nameOf(f), name)
	if rule == nil {
		before := k.rule()
		if unchanged(before, f) {
			p.block("Nothing changes.")
			return
		}
		next := kept{hostname: f.GetHostname(), path: f.GetPath(), service: f.GetService(), originRequest: k.originRequest}
		p.step(pb.System_SYSTEM_DDASH, "Change the kept settings of "+label(k.hostname, k.path), ruleValues(before, next.rule()),
			func(ctx context.Context, c *component) error { return c.store.replace(ctx, name, next) })
		return
	}
	if unchanged(rule, f) {
		p.block("Nothing changes.")
		return
	}
	next := &cfpb.IngressRule{Hostname: f.GetHostname(), Path: f.GetPath(), Service: f.GetService(), OriginRequest: rule.GetOriginRequest()}
	replace := &cfpb.RuleName{Hostname: rule.GetHostname(), Path: rule.GetPath()}
	p.step(pb.System_SYSTEM_CLOUDFLARE_TUNNEL, "Change the ingress rule for "+label(rule.GetHostname(), rule.GetPath()), ruleValues(rule, next),
		func(ctx context.Context, c *component) error {
			_, err := c.cf.PutIngressRule(ctx, &cfpb.PutIngressRuleRequest{Replace: replace, Rule: next})
			return err
		})
	if next.GetHostname() != rule.GetHostname() {
		s.ensureCNAME(p, next.GetHostname())
		s.dropCNAME(p, rule.GetHostname(), name)
	}
}

// unchanged: rule already has f's fields.
func unchanged(rule *cfpb.IngressRule, f *pb.PublicHostnameFields) bool {
	return rule.GetHostname() == f.GetHostname() && rule.GetPath() == f.GetPath() && rule.GetService() == f.GetService()
}

func (s *snapshot) remove(p *planned, name *pb.PublicHostnameName) {
	if rule := s.findRule(name); rule != nil {
		s.dropCNAME(p, rule.GetHostname(), name)
		p.deleteRule(rule)
		return
	}
	if k, ok := s.findKept(name); ok {
		p.step(pb.System_SYSTEM_DDASH, "Forget the kept settings of "+label(k.hostname, k.path), []*pb.PlanValue{{Label: "Service", Before: k.service}},
			func(ctx context.Context, c *component) error { return c.store.drop(ctx, name) })
		return
	}
	p.block("%s is no Public Hostname now.", label(name.GetHostname(), name.GetPath()))
}

func (s *snapshot) disable(p *planned, name *pb.PublicHostnameName) {
	rule := s.findRule(name)
	if rule == nil {
		if _, ok := s.findKept(name); ok {
			p.block("%s is disabled already.", label(name.GetHostname(), name.GetPath()))
		} else {
			p.block("%s is no Public Hostname now.", label(name.GetHostname(), name.GetPath()))
		}
		return
	}
	k := kept{hostname: rule.GetHostname(), path: rule.GetPath(), service: rule.GetService(), originRequest: rule.GetOriginRequest()}
	p.step(pb.System_SYSTEM_DDASH, "Keep the settings of "+label(k.hostname, k.path), []*pb.PlanValue{{Label: "Service", After: k.service}},
		func(ctx context.Context, c *component) error { return c.store.put(ctx, k) })
	s.dropCNAME(p, rule.GetHostname(), name)
	p.deleteRule(rule)
}

func (s *snapshot) enable(p *planned, name *pb.PublicHostnameName) {
	k, ok := s.findKept(name)
	if !ok {
		if s.findRule(name) != nil {
			p.block("%s is enabled already.", label(name.GetHostname(), name.GetPath()))
		} else {
			p.block("%s is no Public Hostname now.", label(name.GetHostname(), name.GetPath()))
		}
		return
	}
	if s.findRule(name) != nil {
		p.block("%s has an ingress rule again, added in Cloudflare: remove the disabled one to forget its kept settings.", label(k.hostname, k.path))
		return
	}
	p.addRule(k.rule())
	s.ensureCNAME(p, k.hostname)
	p.step(pb.System_SYSTEM_DDASH, "Forget the kept settings of "+label(k.hostname, k.path), []*pb.PlanValue{{Label: "Service", Before: k.service}},
		func(ctx context.Context, c *component) error { return c.store.drop(ctx, name) })
}
