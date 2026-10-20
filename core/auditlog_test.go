package core_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/sdk"
)

// listAuditLog reads a page of the Audit Log through the core API, as the
// Settings page does.
func listAuditLog(t *testing.T, h *coretest.Harness, request string) *corepb.ListAuditLogResponse {
	t.Helper()
	res := h.CoreCall("ListAuditLog", request)
	if res.Code != http.StatusOK {
		t.Fatalf("ListAuditLog %s: status %d: %s", request, res.Code, res.Body)
	}
	var out corepb.ListAuditLogResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("ListAuditLog: %v: %s", err, res.Body)
	}
	return &out
}

// hops names each entry of a chain as target.Method.
func hops(c *corepb.AuditChain) []string {
	var out []string
	for _, e := range c.GetEntries() {
		out = append(out, e.GetTarget()+"."+e.GetMethod())
	}
	return out
}

// chainHops is hops for every chain on a page.
func chainHops(page *corepb.ListAuditLogResponse) [][]string {
	var out [][]string
	for _, c := range page.GetChains() {
		out = append(out, hops(c))
	}
	return out
}

func TestTheAuditLogListsEachActionAsOneChainOfItsHopsNewestFirst(t *testing.T) {
	h := newRelay(t, relayTo("onward"), sample.Hooks{})
	if res := h.Call("relayer", "relay", "Act", `{"note":"first"}`); res.Code != http.StatusOK {
		t.Fatalf("Act: status %d: %s", res.Code, res.Body)
	}

	page := listAuditLog(t, h, `{}`)

	want := [][]string{{"relay.Act", "greeter.Act"}, {"core.Install"}}
	if got := chainHops(page); !reflect.DeepEqual(got, want) {
		t.Fatalf("chains = %v, want %v", got, want)
	}
	root, hop := page.Chains[0].Entries[0], page.Chains[0].Entries[1]
	if page.Chains[0].Id == "" || page.Chains[0].Id == page.Chains[1].Id {
		t.Errorf("chain ids %q and %q, want two distinct ids", page.Chains[0].Id, page.Chains[1].Id)
	}
	if root.ActorKind != "operator" || root.Username != "test" || !root.Disruptive || root.Outcome != "ok" ||
		root.Request != `{"note":"first"}` || root.Time == nil || root.Duration == nil {
		t.Errorf("root = %v, want operator test's disruptive ok Act with its request, time and duration", root)
	}
	if hop.Request != `{"note":"onward"}` || hop.Time.AsTime().Before(root.Time.AsTime()) {
		t.Errorf("hop = %v, want greeter's Act with note onward, after the root", hop)
	}
	if page.NextPageToken != "" {
		t.Errorf("next page token %q, want none: everything fits one page", page.NextPageToken)
	}
}

// relayWhenAsked is a sample hook whose Act calls Act on the Component it
// requires only for the note "relay", passing on the note "onward". It
// answers ok whatever that call returns.
var relayWhenAsked = sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
	if req.GetNote() == "relay" {
		_, _ = sdk.Require(env, samplepb.NewSampleServiceClient).Act(ctx, &samplepb.ActRequest{Note: "onward"})
	}
	return &samplepb.ActResponse{}, nil
}}

func TestTheAuditLogFiltersByTargetKeepingTheWholeChain(t *testing.T) {
	h := newRelay(t, relayWhenAsked, sample.Hooks{})
	for _, note := range []string{"relay", "stay"} {
		if res := h.Call("relayer", "relay", "Act", `{"note":"`+note+`"}`); res.Code != http.StatusOK {
			t.Fatalf("Act %s: status %d: %s", note, res.Code, res.Body)
		}
	}

	for target, want := range map[string][][]string{
		"greeter": {{"relay.Act", "greeter.Act"}},
		"relay":   {{"relay.Act"}, {"relay.Act", "greeter.Act"}},
		"core":    {{"core.Install"}},
		"nobody":  nil,
	} {
		page := listAuditLog(t, h, `{"target":"`+target+`"}`)

		if got := chainHops(page); !reflect.DeepEqual(got, want) {
			t.Errorf("chains to %s = %v, want %v", target, got, want)
		}
	}
	if got, want := listAuditLog(t, h, `{}`).Targets, []string{"core", "greeter", "relay"}; !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v to filter by", got, want)
	}
}

func TestTheAuditLogFiltersByOutcome(t *testing.T) {
	refuseOnward := sample.Hooks{Act: func(_ context.Context, _ *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		if req.GetNote() == "onward" {
			return nil, status.Error(codes.PermissionDenied, "protected in Proxmox")
		}
		return &samplepb.ActResponse{}, nil
	}}
	h := newRelay(t, relayWhenAsked, refuseOnward)
	for _, note := range []string{"relay", "stay"} {
		if res := h.Call("relayer", "relay", "Act", `{"note":"`+note+`"}`); res.Code != http.StatusOK {
			t.Fatalf("Act %s: status %d: %s", note, res.Code, res.Body)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		r := coretest.ConnectRequest("relay", "WaitAction", `{}`).WithContext(ctx)
		r.Header.Set(coretest.PluginHeader, "relayer")
		h.Do(r)
	}()
	defer func() { cancel(); <-ended }()
	for deadline := time.Now().Add(10 * time.Second); h.AuditLog()[0].Method != "WaitAction"; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("WaitAction never showed as running")
		}
	}

	for outcome, want := range map[string][][]string{
		"ok":          {{"relay.Act"}, {"core.Install"}},
		"failed":      {{"relay.Act", "greeter.Act"}},
		"running":     {{"relay.WaitAction"}},
		"interrupted": nil,
	} {
		page := listAuditLog(t, h, `{"outcome":"`+outcome+`"}`)

		if got := chainHops(page); !reflect.DeepEqual(got, want) {
			t.Errorf("%s chains = %v, want %v", outcome, got, want)
		}
	}
	chains := listAuditLog(t, h, `{"outcome":"failed"}`).Chains
	if len(chains) != 1 || len(chains[0].Entries) != 2 {
		return
	}
	if failed := chains[0].Entries[1]; failed.Outcome != "PermissionDenied" || failed.Message != "protected in Proxmox" {
		t.Errorf("failed hop = %v, want PermissionDenied with its message", failed)
	}
}

func TestTheAuditLogIsReadAPageOfChainsAtATime(t *testing.T) {
	h := newRelay(t, relayWhenAsked, sample.Hooks{})
	for _, note := range []string{"1", "relay", "3", "4", "5"} {
		if res := h.Call("relayer", "relay", "Act", `{"note":"`+note+`"}`); res.Code != http.StatusOK {
			t.Fatalf("Act %s: status %d: %s", note, res.Code, res.Body)
		}
	}

	var pages [][]string
	token := ""
	for range 10 {
		page := listAuditLog(t, h, `{"pageSize":2,"pageToken":"`+token+`"}`)
		var roots []string
		for _, c := range page.Chains {
			root := c.Entries[0]
			roots = append(roots, root.Method+" "+root.Request)
		}
		pages = append(pages, roots)
		if token = page.NextPageToken; token == "" {
			break
		}
	}

	want := [][]string{
		{`Act {"note":"5"}`, `Act {"note":"4"}`},
		{`Act {"note":"3"}`, `Act {"note":"relay"}`},
		{`Act {"note":"1"}`, `Install {"plugin":"relayer"}`},
	}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("pages = %v, want %v", pages, want)
	}
}

func TestTheAuditLogRefusesAnUnknownFilter(t *testing.T) {
	h := newHello(t, configured)

	for _, request := range []string{`{"outcome":"meh"}`, `{"actorKind":"robot"}`, `{"pageToken":"bogus"}`} {
		wantError(t, h.CoreCall("ListAuditLog", request), http.StatusBadRequest, "invalid_argument", "")
	}
}

// actOnItsOwn is a sample hook whose Start has the Component call Act on
// the Component it requires once, on its own, and report the error.
func actOnItsOwn(acted chan<- error) sample.Hooks {
	return sample.Hooks{Start: func(ctx context.Context, env *sdk.Env, _ *samplepb.SampleConfig) error {
		go func() {
			_, err := sdk.Require(env, samplepb.NewSampleServiceClient).Act(ctx, &samplepb.ActRequest{Note: "on its own"})
			acted <- err
		}()
		return nil
	}}
}

func TestTheAuditLogFiltersChainsByTheirRootsActorKind(t *testing.T) {
	acted := make(chan error, 1)
	h := newRelay(t, actOnItsOwn(acted), sample.Hooks{})
	if err := <-acted; err != nil {
		t.Fatalf("relay's own Act: %v", err)
	}
	if res := h.Call("relayer", "relay", "Act", `{}`); res.Code != http.StatusOK {
		t.Fatalf("Act: status %d: %s", res.Code, res.Body)
	}

	for kind, want := range map[string][][]string{
		"operator":  {{"relay.Act"}, {"core.Install"}},
		"component": {{"greeter.Act"}},
		"core":      nil,
	} {
		page := listAuditLog(t, h, `{"actorKind":"`+kind+`"}`)

		if got := chainHops(page); !reflect.DeepEqual(got, want) {
			t.Errorf("%s chains = %v, want %v", kind, got, want)
		}
		for _, c := range page.Chains {
			if c.Entries[0].ActorKind != kind {
				t.Errorf("%s chain rooted in %v", kind, c.Entries[0])
			}
		}
	}
}
