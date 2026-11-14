package cloudflaretunnel_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	cloudflareapi "github.com/itsKontra/ddash/components/cloudflare-api"
	cfpb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
	cloudflaretunnel "github.com/itsKontra/ddash/components/cloudflare-tunnel"
	pb "github.com/itsKontra/ddash/components/cloudflare-tunnel/cloudflaretunnelpb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// lab is cloudflare-tunnel over a real cloudflare-api over cloudflarefake.
type lab struct {
	c    pb.CloudflareTunnelServiceClient
	fake *cloudflarefake.Cloudflare
}

func start(t *testing.T, scenario string) *lab {
	t.Helper()
	fake, err := cloudflarefake.New("../cloudflare-api/testdata/"+scenario, cloudflarefake.Options{Miss: func(r string, err error) { t.Errorf("unanswered %s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	url := srv.URL
	return startWith(t, &cfpb.CloudflareApiConfig{AccountId: cloudflarefake.AccountID, TunnelId: cloudflarefake.TunnelID, ApiToken: cloudflarefake.DefaultToken, ApiUrl: &url}, fake)
}

func startWith(t *testing.T, cfg *cfpb.CloudflareApiConfig, fake *cloudflarefake.Cloudflare) *lab {
	t.Helper()
	api := sdktest.New(t, cloudflareapi.Definition())
	api.MustStart(cfg)
	h := sdktest.New(t, cloudflaretunnel.Definition(), sdktest.WithClient(cfpb.NewCloudflareApiServiceClient(api.Conn)))
	h.MustStart(&pb.CloudflareTunnelConfig{})
	return &lab{c: pb.NewCloudflareTunnelServiceClient(h.Conn), fake: fake}
}

func name(hostname string) *pb.PublicHostnameName { return &pb.PublicHostnameName{Hostname: hostname} }

func add(hostname, service string) *pb.TunnelAction {
	return &pb.TunnelAction{Action: &pb.TunnelAction_Add{Add: &pb.AddPublicHostname{Fields: &pb.PublicHostnameFields{Hostname: hostname, Service: service}}}}
}

func edit(from string, to *pb.PublicHostnameFields) *pb.TunnelAction {
	return &pb.TunnelAction{Action: &pb.TunnelAction_Edit{Edit: &pb.EditPublicHostname{Name: name(from), Fields: to}}}
}

func remove(hostname string) *pb.TunnelAction {
	return &pb.TunnelAction{Action: &pb.TunnelAction_Remove{Remove: &pb.RemovePublicHostname{Name: name(hostname)}}}
}

func disable(hostname string) *pb.TunnelAction {
	return &pb.TunnelAction{Action: &pb.TunnelAction_Disable{Disable: &pb.DisablePublicHostname{Name: name(hostname)}}}
}

func enable(hostname string) *pb.TunnelAction {
	return &pb.TunnelAction{Action: &pb.TunnelAction_Enable{Enable: &pb.EnablePublicHostname{Name: name(hostname)}}}
}

func (l *lab) plan(t *testing.T, a *pb.TunnelAction) *pb.Plan {
	t.Helper()
	resp, err := l.c.PlanAction(t.Context(), &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatalf("PlanAction: %v", err)
	}
	return resp.GetPlan()
}

// run runs a with plan and returns every message streamed and how the
// stream ended.
func (l *lab) run(t *testing.T, a *pb.TunnelAction, plan *pb.Plan) ([]*pb.RunActionResponse, error) {
	t.Helper()
	stream, err := l.c.RunAction(t.Context(), &pb.RunActionRequest{Action: a, Plan: plan})
	if err != nil {
		return nil, err
	}
	var got []*pb.RunActionResponse
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		got = append(got, m)
	}
}

// planRun plans a and runs it, failing the test unless it ran through.
func (l *lab) planRun(t *testing.T, a *pb.TunnelAction) *pb.Plan {
	t.Helper()
	p := l.plan(t, a)
	if len(p.GetBlocked()) > 0 {
		t.Fatalf("Plan blocked: %v", p.GetBlocked())
	}
	msgs, err := l.run(t, a, p)
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if last := msgs[len(msgs)-1]; int(last.GetDone()) != len(p.GetSteps()) {
		t.Fatalf("last message %v, want every step done", last)
	}
	return p
}

// titles are a Plan's step titles.
func titles(p *pb.Plan) []string {
	var out []string
	for _, s := range p.GetSteps() {
		out = append(out, s.GetTitle())
	}
	return out
}

// rules is the fake's ingress as "hostname → service", the catch-all as
// "* → service".
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

// record is the fake's DNS record named hostname, "TYPE content
// proxied=…"; empty for none.
func record(l *lab, hostname string) string {
	for _, r := range l.fake.Records(cloudflarefake.ZoneID) {
		if r["name"] == hostname {
			proxied, _ := r["proxied"].(bool)
			return r["type"].(string) + " " + r["content"].(string) + map[bool]string{true: " proxied", false: " dns-only"}[proxied]
		}
	}
	return ""
}

func list(t *testing.T, l *lab) *pb.ListPublicHostnamesResponse {
	t.Helper()
	got, err := l.c.ListPublicHostnames(t.Context(), &pb.ListPublicHostnamesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestThePublicHostnamesAreTheIngressRulesWithTheirDNS(t *testing.T) {
	l := start(t, "baseline")
	l.fake.AddRecord(cloudflarefake.ZoneID, "plain.home.example", "CNAME", cloudflarefake.Target, false)
	l.fake.EditIngress(func(r []map[string]any) []map[string]any {
		extra := []map[string]any{
			{"hostname": "plain.home.example", "service": "http://198.18.1.30:80"},
			{"hostname": "vpn.home.example", "service": "http://198.18.1.31:80"},
			{"hostname": "nodns.home.example", "service": "http://198.18.1.32:80"},
			{"hostname": "app.elsewhere.example", "service": "http://198.18.1.33:80"},
		}
		return append(append(r[:len(r)-1], extra...), r[len(r)-1])
	})

	got := list(t, l)

	want := &pb.ListPublicHostnamesResponse{
		SetUp: true, RemotelyManaged: true, CatchAll: "http_status:404", Zones: []string{"home.example"}, CnameTarget: cloudflarefake.Target,
		PublicHostnames: []*pb.PublicHostname{
			{Fields: &pb.PublicHostnameFields{Hostname: "request.home.example", Service: "http://198.18.1.212:5055"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_OK, Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "video.home.example", Service: "http://198.18.1.212:8096"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_OK, Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "ha.home.example", Service: "http://198.18.1.101:8123"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_OK, Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "plain.home.example", Service: "http://198.18.1.30:80"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_NOT_PROXIED, Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "vpn.home.example", Service: "http://198.18.1.31:80"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_ELSEWHERE, DnsDetail: "A 203.0.113.7", Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "nodns.home.example", Service: "http://198.18.1.32:80"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_MISSING, Zone: "home.example"},
			{Fields: &pb.PublicHostnameFields{Hostname: "app.elsewhere.example", Service: "http://198.18.1.33:80"}, Enabled: true, Dns: pb.DnsState_DNS_STATE_NO_ZONE},
		},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListPublicHostnames (-want +got):\n%s", diff)
	}
}

func TestNotSetUpListsNothingAndBlocksEveryPlan(t *testing.T) {
	l := startWith(t, &cfpb.CloudflareApiConfig{}, nil)

	if got := list(t, l); got.GetSetUp() || len(got.GetPublicHostnames()) != 0 {
		t.Errorf("ListPublicHostnames = %v, want not set up", got)
	}
	if p := l.plan(t, add("grafana.home.example", "http://198.18.1.204:3000")); len(p.GetSteps()) != 0 || len(p.GetBlocked()) != 1 || !strings.Contains(p.GetBlocked()[0], "Set up cloudflare-api") {
		t.Errorf("Plan = %v, want blocked: set up cloudflare-api", p)
	}
}

func TestALocallyManagedTunnelIsReadOnly(t *testing.T) {
	l := start(t, "locally-managed")

	if got := list(t, l); got.GetRemotelyManaged() || len(got.GetPublicHostnames()) != 3 {
		t.Errorf("ListPublicHostnames = %v, want the rules, locally managed", got)
	}
	p := l.plan(t, remove("ha.home.example"))
	if len(p.GetBlocked()) != 1 || !strings.Contains(p.GetBlocked()[0], "locally managed") {
		t.Errorf("Plan = %v, want blocked: locally managed", p)
	}
}

func TestAddCreatesTheIngressRuleThenItsProxiedCNAME(t *testing.T) {
	l := start(t, "baseline")
	a := add("Grafana.home.example", "http://198.18.1.204:3000")

	p := l.plan(t, a)
	want := &pb.Plan{Steps: []*pb.PlanStep{
		{System: pb.System_SYSTEM_CLOUDFLARE_TUNNEL, Title: "Add the ingress rule for grafana.home.example", Values: []*pb.PlanValue{
			{Label: "Hostname", After: "grafana.home.example"}, {Label: "Service", After: "http://198.18.1.204:3000"},
		}},
		{System: pb.System_SYSTEM_CLOUDFLARE_DNS, Title: "Create the CNAME grafana.home.example", Values: []*pb.PlanValue{
			{Label: "Target", After: cloudflarefake.Target}, {Label: "Proxied", After: "yes"},
		}},
	}}
	if diff := cmp.Diff(want, p, protocmp.Transform()); diff != "" {
		t.Fatalf("Plan (-want +got):\n%s", diff)
	}

	msgs, err := l.run(t, a, p)
	if err != nil {
		t.Fatal(err)
	}

	var states [][]pb.StepState
	for _, m := range msgs {
		states = append(states, m.GetSteps())
	}
	run, done, pending := pb.StepState_STEP_STATE_RUNNING, pb.StepState_STEP_STATE_DONE, pb.StepState_STEP_STATE_PENDING
	if diff := cmp.Diff([][]pb.StepState{{run, pending}, {done, pending}, {done, run}, {done, done}, {done, done}}, states); diff != "" {
		t.Errorf("streamed states (-want +got):\n%s", diff)
	}
	if msgs[len(msgs)-1].GetDone() != 2 {
		t.Errorf("last message %v, want done 2", msgs[len(msgs)-1])
	}
	if got := rules(l); got[3] != "grafana.home.example → http://198.18.1.204:3000" || got[4] != "* → http_status:404" {
		t.Errorf("ingress %v, want grafana's rule before the catch-all", got)
	}
	if got := record(l, "grafana.home.example"); got != "CNAME "+cloudflarefake.Target+" proxied" {
		t.Errorf("grafana's record = %q, want a proxied CNAME to the Tunnel", got)
	}
	for _, r := range l.fake.Records(cloudflarefake.ZoneID) {
		if r["name"] == "grafana.home.example" && r["comment"] != cloudflaretunnel.CNAMEComment {
			t.Errorf("comment = %v, want ddash's", r["comment"])
		}
	}
}

func TestAddIsBlockedOnATakenOrForeignName(t *testing.T) {
	l := start(t, "baseline")
	for _, tc := range []struct {
		action *pb.TunnelAction
		want   string
	}{
		{add("ha.home.example", "http://198.18.1.101:8124"), "ha.home.example is a Public Hostname already."},
		{add("vpn.home.example", "http://198.18.1.40:80"), "vpn.home.example has a DNS record already (A 203.0.113.7): remove it in Cloudflare first."},
		{add("app.elsewhere.example", "http://198.18.1.40:80"), "app.elsewhere.example is in no zone of this Cloudflare account (home.example)."},
		{add("not a name", "http://198.18.1.40:80"), `"not a name" is not a hostname: use a name like app.home.example.`},
		{add("app.home.example", "198.18.1.40:80"), `"198.18.1.40:80" is not a service cloudflared forwards to: use a URL like http://198.18.1.20:8080, or http_status:404.`},
	} {
		p := l.plan(t, tc.action)
		if !slicesContain(p.GetBlocked(), tc.want) {
			t.Errorf("Plan(%v) blocked %q, want %q", tc.action, p.GetBlocked(), tc.want)
		}
		if _, err := l.run(t, tc.action, p); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("RunAction(%v) = %v, want FAILED_PRECONDITION", tc.action, err)
		}
	}
	if w := l.fake.Writes(); len(w) != 0 {
		t.Errorf("writes %v, want none", w)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestEditingTheServiceChangesTheRuleInPlaceKeepingItsSettings(t *testing.T) {
	l := start(t, "baseline")

	p := l.planRun(t, edit("video.home.example", &pb.PublicHostnameFields{Hostname: "video.home.example", Service: "http://198.18.1.212:8097"}))

	if diff := cmp.Diff([]string{"Change the ingress rule for video.home.example"}, titles(p)); diff != "" {
		t.Errorf("steps (-want +got):\n%s", diff)
	}
	if got := rules(l)[1]; got != "video.home.example → http://198.18.1.212:8097" {
		t.Errorf("video's rule = %q", got)
	}
	if o, _ := json.Marshal(l.fake.Ingress()[1]["originRequest"]); string(o) != `{"connectTimeout":30,"keepAliveTimeout":90}` {
		t.Errorf("originRequest = %s, want it kept", o)
	}
}

func TestRenamingMovesTheCNAME(t *testing.T) {
	l := start(t, "baseline")

	p := l.planRun(t, edit("video.home.example", &pb.PublicHostnameFields{Hostname: "films.home.example", Service: "http://198.18.1.212:8096"}))

	want := []string{"Change the ingress rule for video.home.example", "Create the CNAME films.home.example", "Delete the CNAME video.home.example"}
	if diff := cmp.Diff(want, titles(p)); diff != "" {
		t.Errorf("steps (-want +got):\n%s", diff)
	}
	if record(l, "films.home.example") == "" || record(l, "video.home.example") != "" {
		t.Errorf("records: films %q, video %q; want the CNAME moved", record(l, "films.home.example"), record(l, "video.home.example"))
	}
}

func TestRemoveDeletesTheCNAMEThenTheRule(t *testing.T) {
	l := start(t, "baseline")

	p := l.planRun(t, remove("request.home.example"))

	if diff := cmp.Diff([]string{"Delete the CNAME request.home.example", "Remove the ingress rule for request.home.example"}, titles(p)); diff != "" {
		t.Errorf("steps (-want +got):\n%s", diff)
	}
	if record(l, "request.home.example") != "" || len(rules(l)) != 3 {
		t.Errorf("record %q, ingress %v; want both gone", record(l, "request.home.example"), rules(l))
	}
}

func TestDisableKeepsTheSettingsAndEnableRestoresThem(t *testing.T) {
	l := start(t, "baseline")

	p := l.planRun(t, disable("video.home.example"))

	if diff := cmp.Diff([]string{"Keep the settings of video.home.example", "Delete the CNAME video.home.example", "Remove the ingress rule for video.home.example"}, titles(p)); diff != "" {
		t.Errorf("disable's steps (-want +got):\n%s", diff)
	}
	if record(l, "video.home.example") != "" || len(rules(l)) != 3 {
		t.Errorf("after disable: record %q, ingress %v; want both gone", record(l, "video.home.example"), rules(l))
	}
	got := list(t, l).GetPublicHostnames()
	if last := got[len(got)-1]; last.GetEnabled() || last.GetFields().GetHostname() != "video.home.example" || last.GetFields().GetService() != "http://198.18.1.212:8096" || last.GetZone() != "home.example" {
		t.Errorf("listed last %v, want video disabled with its service", last)
	}

	p = l.planRun(t, enable("video.home.example"))

	if diff := cmp.Diff([]string{"Add the ingress rule for video.home.example", "Create the CNAME video.home.example", "Forget the kept settings of video.home.example"}, titles(p)); diff != "" {
		t.Errorf("enable's steps (-want +got):\n%s", diff)
	}
	if record(l, "video.home.example") != "CNAME "+cloudflarefake.Target+" proxied" {
		t.Errorf("video's record = %q, want its CNAME back", record(l, "video.home.example"))
	}
	if o, _ := json.Marshal(l.fake.Ingress()[2]["originRequest"]); string(o) != `{"connectTimeout":30,"keepAliveTimeout":90}` {
		t.Errorf("restored originRequest = %s, want the kept one", o)
	}
	for _, ph := range list(t, l).GetPublicHostnames() {
		if !ph.GetEnabled() {
			t.Errorf("%v still disabled", ph)
		}
	}
}

func TestADisabledOneIsEditedAndRemovedInDdashAlone(t *testing.T) {
	l := start(t, "baseline")
	l.planRun(t, disable("ha.home.example"))
	writes := len(l.fake.Writes())

	p := l.planRun(t, edit("ha.home.example", &pb.PublicHostnameFields{Hostname: "ha.home.example", Service: "http://198.18.1.101:8124"}))
	if len(p.GetSteps()) != 1 || p.GetSteps()[0].GetSystem() != pb.System_SYSTEM_DDASH {
		t.Errorf("edit's Plan = %v, want one ddash step", p)
	}
	got := list(t, l).GetPublicHostnames()
	if last := got[len(got)-1]; last.GetFields().GetService() != "http://198.18.1.101:8124" {
		t.Errorf("kept %v, want the new service", last)
	}

	l.planRun(t, remove("ha.home.example"))
	if n := len(list(t, l).GetPublicHostnames()); n != 2 {
		t.Errorf("%d Public Hostnames, want the 2 enabled ones", n)
	}
	if w := l.fake.Writes(); len(w) != writes {
		t.Errorf("writes to Cloudflare %v, want none since the disable", w[writes:])
	}
}

func TestAPlanThatNoLongerHoldsIsRefusedBeforeAnyWrite(t *testing.T) {
	l := start(t, "baseline")
	a := remove("ha.home.example")
	p := l.plan(t, a)
	l.fake.EditIngress(func(r []map[string]any) []map[string]any {
		r[2]["service"] = "http://198.18.1.101:9123"
		return r
	})

	_, err := l.run(t, a, p)

	if status.Code(err) != codes.Aborted || !strings.HasPrefix(status.Convert(err).Message(), "changed since you looked") {
		t.Fatalf("RunAction = %v, want ABORTED changed since you looked", err)
	}
	var newPlan *pb.Plan
	for _, d := range status.Convert(err).Details() {
		if np, ok := d.(*pb.Plan); ok {
			newPlan = np
		}
	}
	if newPlan == nil || newPlan.GetSteps()[1].GetValues()[0].GetBefore() != "http://198.18.1.101:9123" {
		t.Errorf("new Plan = %v, want the changed service", newPlan)
	}
	if w := l.fake.Writes(); len(w) != 0 {
		t.Errorf("writes %v, want none", w)
	}
}

func TestARunStopsAtTheFailedStepNamingIt(t *testing.T) {
	l := start(t, "baseline")
	a := add("grafana.home.example", "http://198.18.1.204:3000")
	p := l.plan(t, a)
	l.fake.FailNext(http.MethodPost+" /zones/"+cloudflarefake.ZoneID+"/dns_records", http.StatusInternalServerError, "Internal server error")

	msgs, err := l.run(t, a, p)

	want := "Stopped at step 2 of 2, Create the CNAME grafana.home.example: Cloudflare answered 500: Internal server error (1000). Done: Add the ingress rule for grafana.home.example. Nothing was rolled back."
	if status.Convert(err).Message() != want {
		t.Errorf("RunAction = %v, want %s", err, want)
	}
	var detail *pb.RunActionResponse
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pb.RunActionResponse); ok {
			detail = r
		}
	}
	if detail.GetDone() != 1 {
		t.Errorf("details %v, want done 1", detail)
	}
	if last := msgs[len(msgs)-1].GetSteps(); last[0] != pb.StepState_STEP_STATE_DONE || last[1] != pb.StepState_STEP_STATE_FAILED {
		t.Errorf("last streamed states %v, want done, failed", last)
	}
	if got := rules(l); len(got) != 5 {
		t.Errorf("ingress %v, want the rule kept: nothing is rolled back", got)
	}
}
