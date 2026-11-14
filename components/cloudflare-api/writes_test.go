package cloudflareapi_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
)

// rules is the fake's ingress as "hostname → service" lines, the
// catch-all as "* → service".
func rules(l *lab) []string {
	var out []string
	for _, r := range l.fake.Ingress() {
		h, _ := r["hostname"].(string)
		if h == "" {
			h = "*"
		}
		out = append(out, h+" → "+r["service"].(string))
	}
	return out
}

func TestPutIngressRuleAddsTheRuleBeforeTheCatchAllKeepingTheRest(t *testing.T) {
	l := start(t, "baseline")

	got, err := l.c.PutIngressRule(t.Context(), &pb.PutIngressRuleRequest{Rule: &pb.IngressRule{Hostname: "grafana.home.example", Service: "http://198.18.1.204:3000"}})
	if err != nil {
		t.Fatal(err)
	}

	if got.GetVersion() != 8 {
		t.Errorf("version = %d, want 8", got.GetVersion())
	}
	want := []string{
		"request.home.example → http://198.18.1.212:5055",
		"video.home.example → http://198.18.1.212:8096",
		"ha.home.example → http://198.18.1.101:8123",
		"grafana.home.example → http://198.18.1.204:3000",
		"* → http_status:404",
	}
	if diff := cmp.Diff(want, rules(l)); diff != "" {
		t.Errorf("ingress (-want +got):\n%s", diff)
	}
	// The other rules' settings stay as they were.
	if o := l.fake.Ingress()[1]["originRequest"].(map[string]any); o["connectTimeout"] != 30.0 {
		t.Errorf("video's originRequest = %v, want it kept", o)
	}
	// What a fresh read shows.
	cfg, err := l.c.GetConfiguration(t.Context(), &pb.GetConfigurationRequest{})
	if err != nil || len(cfg.GetRules()) != 4 || cfg.GetVersion() != 8 {
		t.Errorf("GetConfiguration after the write = %v, %v; want the new rule at version 8", cfg, err)
	}
}

func TestPutIngressRuleReplacesARuleInItsPlace(t *testing.T) {
	l := start(t, "baseline")

	_, err := l.c.PutIngressRule(t.Context(), &pb.PutIngressRuleRequest{
		Replace: &pb.RuleName{Hostname: "video.home.example"},
		Rule:    &pb.IngressRule{Hostname: "films.home.example", Service: "http://198.18.1.212:8097", OriginRequest: `{"connectTimeout":30}`},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"request.home.example → http://198.18.1.212:5055",
		"films.home.example → http://198.18.1.212:8097",
		"ha.home.example → http://198.18.1.101:8123",
		"* → http_status:404",
	}
	if diff := cmp.Diff(want, rules(l)); diff != "" {
		t.Errorf("ingress (-want +got):\n%s", diff)
	}
	if o, _ := json.Marshal(l.fake.Ingress()[1]["originRequest"]); string(o) != `{"connectTimeout":30}` {
		t.Errorf("originRequest = %s, want the request's", o)
	}
}

func TestATakenOrMissingRuleIsRefusedWritingNothing(t *testing.T) {
	l := start(t, "baseline")

	_, err := l.c.PutIngressRule(t.Context(), &pb.PutIngressRuleRequest{Rule: &pb.IngressRule{Hostname: "ha.home.example", Service: "http://198.18.1.101:8124"}})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("adding a rule for a hostname with one = %v, want ALREADY_EXISTS", err)
	}
	_, err = l.c.PutIngressRule(t.Context(), &pb.PutIngressRuleRequest{Replace: &pb.RuleName{Hostname: "gone.home.example"}, Rule: &pb.IngressRule{Hostname: "gone.home.example", Service: "http://198.18.1.9:80"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("replacing a missing rule = %v, want NOT_FOUND", err)
	}
	_, err = l.c.DeleteIngressRule(t.Context(), &pb.DeleteIngressRuleRequest{Rule: &pb.RuleName{Hostname: "gone.home.example"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("deleting a missing rule = %v, want NOT_FOUND", err)
	}
	if w := l.fake.Writes(); len(w) != 0 {
		t.Errorf("writes %v, want none", w)
	}
}

func TestDeleteIngressRuleKeepsTheCatchAllLast(t *testing.T) {
	l := start(t, "baseline")

	if _, err := l.c.DeleteIngressRule(t.Context(), &pb.DeleteIngressRuleRequest{Rule: &pb.RuleName{Hostname: "request.home.example"}}); err != nil {
		t.Fatal(err)
	}

	want := []string{"video.home.example → http://198.18.1.212:8096", "ha.home.example → http://198.18.1.101:8123", "* → http_status:404"}
	if diff := cmp.Diff(want, rules(l)); diff != "" {
		t.Errorf("ingress (-want +got):\n%s", diff)
	}
}

func TestATunnelWithoutRulesGetsTheCatchAll(t *testing.T) {
	l := start(t, "baseline")
	l.fake.EditIngress(func([]map[string]any) []map[string]any { return nil })

	if _, err := l.c.PutIngressRule(t.Context(), &pb.PutIngressRuleRequest{Rule: &pb.IngressRule{Hostname: "request.home.example", Service: "http://198.18.1.212:5055"}}); err != nil {
		t.Fatal(err)
	}

	want := []string{"request.home.example → http://198.18.1.212:5055", "* → http_status:404"}
	if diff := cmp.Diff(want, rules(l)); diff != "" {
		t.Errorf("ingress (-want +got):\n%s", diff)
	}
}

func TestDNSRecordsAreCreatedPatchedAndDeleted(t *testing.T) {
	l := start(t, "baseline")
	zone := cloudflarefake.ZoneID

	created, err := l.c.CreateDnsRecord(t.Context(), &pb.CreateDnsRecordRequest{ZoneId: zone, Fields: &pb.DnsRecordFields{
		Name: "grafana.home.example", Type: "CNAME", Content: cloudflarefake.Target, Proxied: true, Comment: "ddash",
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetRecord().GetId()
	if id == "" || !created.GetRecord().GetFields().GetProxied() {
		t.Errorf("created = %v, want an id, proxied", created)
	}
	if _, err := l.c.UpdateDnsRecord(t.Context(), &pb.UpdateDnsRecordRequest{ZoneId: zone, Id: id, Fields: &pb.DnsRecordFields{
		Name: "grafana.home.example", Type: "CNAME", Content: cloudflarefake.Target, Proxied: false,
	}}); err != nil {
		t.Fatal(err)
	}
	recs, err := l.c.ListDnsRecords(t.Context(), &pb.ListDnsRecordsRequest{ZoneId: zone})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs.GetRecords()) != 6 || recs.GetRecords()[0].GetFields().GetName() != "grafana.home.example" || recs.GetRecords()[0].GetFields().GetProxied() {
		t.Errorf("records = %v, want grafana's, no longer proxied", recs)
	}
	if _, err := l.c.DeleteDnsRecord(t.Context(), &pb.DeleteDnsRecordRequest{ZoneId: zone, Id: id}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /zones/" + zone + "/dns_records",
		"PATCH /zones/" + zone + "/dns_records/" + id,
		"DELETE /zones/" + zone + "/dns_records/" + id,
	}
	if diff := cmp.Diff(want, l.fake.Writes()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
}

func TestACNAMEOnATakenNameIsRefusedWithCloudflaresReason(t *testing.T) {
	l := start(t, "baseline")

	_, err := l.c.CreateDnsRecord(t.Context(), &pb.CreateDnsRecordRequest{ZoneId: cloudflarefake.ZoneID, Fields: &pb.DnsRecordFields{
		Name: "vpn.home.example", Type: "CNAME", Content: cloudflarefake.Target, Proxied: true,
	}})

	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CreateDnsRecord = %v, want FAILED_PRECONDITION", err)
	}
	if msg := status.Convert(err).Message(); msg[:len("Cloudflare refused it: An A, AAAA, or CNAME record with that host already exists")] != "Cloudflare refused it: An A, AAAA, or CNAME record with that host already exists" {
		t.Errorf("message = %q", msg)
	}
}
