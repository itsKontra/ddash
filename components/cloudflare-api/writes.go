package cloudflareapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
)

// configuration is the Tunnel's configuration as Cloudflare sent it: the
// whole config object, kept to PUT back with only its ingress changed.
type configuration struct {
	version int64
	// config is every key of the config object, ingress included.
	config map[string]json.RawMessage
	// rules are the ingress rules, each as sent, catch-all included.
	rules []map[string]json.RawMessage
}

// catchAllService is the catch-all written when the configuration has
// none: Cloudflare requires one, last.
const catchAllService = "http_status:404"

func (c *component) fetchConfiguration(ctx context.Context) (*configuration, error) {
	var row struct {
		Version int64                      `json:"version"`
		Config  map[string]json.RawMessage `json:"config"`
	}
	if err := c.api.call(ctx, http.MethodGet, c.tunnelPath()+"/configurations", nil, nil, &row); err != nil {
		return nil, err
	}
	cfg := &configuration{version: row.Version, config: row.Config}
	if cfg.config == nil {
		cfg.config = map[string]json.RawMessage{}
	}
	if raw, ok := cfg.config["ingress"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &cfg.rules); err != nil {
			return nil, status.Errorf(codes.Unavailable, "Cloudflare's ingress rules: %v", err)
		}
	}
	return cfg, nil
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// isCatchAll: the last rule, without a hostname or a path.
func (cfg *configuration) isCatchAll(i int) bool {
	r := cfg.rules[i]
	return i == len(cfg.rules)-1 && str(r["hostname"]) == "" && str(r["path"]) == ""
}

func (cfg *configuration) proto() *pb.GetConfigurationResponse {
	out := &pb.GetConfigurationResponse{Version: cfg.version}
	for i, r := range cfg.rules {
		if cfg.isCatchAll(i) {
			out.CatchAll = str(r["service"])
			continue
		}
		out.Rules = append(out.Rules, ruleProto(r))
	}
	return out
}

func ruleProto(r map[string]json.RawMessage) *pb.IngressRule {
	rule := &pb.IngressRule{Hostname: str(r["hostname"]), Path: str(r["path"]), Service: str(r["service"])}
	if o, ok := r["originRequest"]; ok && string(o) != "null" && string(o) != "{}" {
		rule.OriginRequest = string(o)
	}
	return rule
}

// index is the rule named n, -1 for none. The catch-all is never named.
func (cfg *configuration) index(n *pb.RuleName) int {
	for i, r := range cfg.rules {
		if !cfg.isCatchAll(i) && str(r["hostname"]) == n.GetHostname() && str(r["path"]) == n.GetPath() {
			return i
		}
	}
	return -1
}

// raw is rule as Cloudflare's JSON.
func raw(rule *pb.IngressRule) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	put := func(k, v string) {
		if v != "" {
			b, _ := json.Marshal(v)
			out[k] = b
		}
	}
	put("hostname", rule.GetHostname())
	put("path", rule.GetPath())
	put("service", rule.GetService())
	if o := rule.GetOriginRequest(); o != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(o), &obj); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "origin_request isn't a JSON object: %v", err)
		}
		out["originRequest"] = json.RawMessage(o)
	}
	return out, nil
}

// changeIngress reads the configuration afresh, edits its rules but the
// catch-all, and PUTs it whole, the catch-all last. It returns the new
// version.
func (c *component) changeIngress(ctx context.Context, edit func(cfg *configuration, rules []map[string]json.RawMessage) ([]map[string]json.RawMessage, error)) (int64, error) {
	if c.api == nil {
		return 0, notSetUp
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	cfg, err := c.fetchConfiguration(ctx)
	if err != nil {
		return 0, err
	}
	rules := slices.Clone(cfg.rules)
	var catchAll map[string]json.RawMessage
	if n := len(rules); n > 0 && cfg.isCatchAll(n-1) {
		catchAll, rules = rules[n-1], rules[:n-1]
	} else {
		b, _ := json.Marshal(catchAllService)
		catchAll = map[string]json.RawMessage{"service": b}
	}
	if rules, err = edit(cfg, rules); err != nil {
		return 0, err
	}
	ingress, err := json.Marshal(append(rules, catchAll))
	if err != nil {
		return 0, status.Error(codes.Internal, err.Error())
	}
	body := map[string]json.RawMessage{}
	for k, v := range cfg.config {
		body[k] = v
	}
	body["ingress"] = ingress
	var out struct {
		Version int64 `json:"version"`
	}
	err = c.api.call(ctx, http.MethodPut, c.tunnelPath()+"/configurations", nil, map[string]any{"config": body}, &out)
	c.config.Invalidate()
	if err != nil {
		return 0, err
	}
	return out.Version, nil
}

func (c *component) PutIngressRule(ctx context.Context, req *pb.PutIngressRuleRequest) (*pb.PutIngressRuleResponse, error) {
	rule := req.GetRule()
	if rule.GetHostname() == "" || rule.GetService() == "" {
		return nil, status.Error(codes.InvalidArgument, "a rule needs a hostname and a service")
	}
	next, err := raw(rule)
	if err != nil {
		return nil, err
	}
	name := &pb.RuleName{Hostname: rule.GetHostname(), Path: rule.GetPath()}
	version, err := c.changeIngress(ctx, func(cfg *configuration, rules []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
		at := -1
		if req.GetReplace() != nil {
			if at = cfg.index(req.GetReplace()); at < 0 {
				return nil, status.Errorf(codes.NotFound, "no ingress rule for %s", label(req.GetReplace()))
			}
		}
		if i := cfg.index(name); i >= 0 && i != at {
			return nil, status.Errorf(codes.AlreadyExists, "an ingress rule for %s exists already", label(name))
		}
		if at < 0 {
			return append(rules, next), nil
		}
		rules[at] = next
		return rules, nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.PutIngressRuleResponse{Version: version}, nil
}

func (c *component) DeleteIngressRule(ctx context.Context, req *pb.DeleteIngressRuleRequest) (*pb.DeleteIngressRuleResponse, error) {
	version, err := c.changeIngress(ctx, func(cfg *configuration, rules []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
		at := cfg.index(req.GetRule())
		if at < 0 {
			return nil, status.Errorf(codes.NotFound, "no ingress rule for %s", label(req.GetRule()))
		}
		return slices.Delete(rules, at, at+1), nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteIngressRuleResponse{Version: version}, nil
}

// label is a rule's name as the Operator reads it: "a.home.example" or
// "a.home.example/api".
func label(n *pb.RuleName) string {
	if n.GetPath() == "" {
		return n.GetHostname()
	}
	return n.GetHostname() + "/" + n.GetPath()
}

// recordBody is a DNS record write's body.
func recordBody(f *pb.DnsRecordFields) map[string]any {
	return map[string]any{
		"name": f.GetName(), "type": f.GetType(), "content": f.GetContent(),
		"proxied": f.GetProxied(), "ttl": 1, "comment": f.GetComment(),
	}
}

func (c *component) recordsPath(zone string) string {
	return "/zones/" + url.PathEscape(zone) + "/dns_records"
}

func (c *component) CreateDnsRecord(ctx context.Context, req *pb.CreateDnsRecordRequest) (*pb.CreateDnsRecordResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	var row recordRow
	err := c.api.call(ctx, http.MethodPost, c.recordsPath(req.GetZoneId()), nil, recordBody(req.GetFields()), &row)
	c.recordsOf(req.GetZoneId()).Invalidate()
	if err != nil {
		return nil, err
	}
	return &pb.CreateDnsRecordResponse{Record: row.proto(req.GetZoneId())}, nil
}

func (c *component) UpdateDnsRecord(ctx context.Context, req *pb.UpdateDnsRecordRequest) (*pb.UpdateDnsRecordResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	var row recordRow
	err := c.api.call(ctx, http.MethodPatch, c.recordsPath(req.GetZoneId())+"/"+url.PathEscape(req.GetId()), nil, recordBody(req.GetFields()), &row)
	c.recordsOf(req.GetZoneId()).Invalidate()
	if err != nil {
		return nil, err
	}
	return &pb.UpdateDnsRecordResponse{Record: row.proto(req.GetZoneId())}, nil
}

func (c *component) DeleteDnsRecord(ctx context.Context, req *pb.DeleteDnsRecordRequest) (*pb.DeleteDnsRecordResponse, error) {
	if c.api == nil {
		return nil, notSetUp
	}
	err := c.api.call(ctx, http.MethodDelete, c.recordsPath(req.GetZoneId())+"/"+url.PathEscape(req.GetId()), nil, nil, nil)
	c.recordsOf(req.GetZoneId()).Invalidate()
	if err != nil {
		return nil, err
	}
	return &pb.DeleteDnsRecordResponse{}, nil
}
