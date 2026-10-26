package apps_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

// addWiki is Add App for wiki.home.example behind the Reverse Proxy.
var addWiki = addApp(&appspb.AddApp{Url: "https://wiki.home.example", Name: "Wiki", Reach: appspb.Reach_REACH_PROXIED, ProxyHost: wiki})

// wikiHosts are the Proxy Hosts Nginx Proxy Manager has for
// wiki.home.example.
func (l *lab) wikiHosts() int {
	n := 0
	for _, h := range l.npm.ProxyHosts() {
		if slices.Contains(strings.Fields(strings.Trim(fmt.Sprint(h["domain_names"]), "[]")), "wiki.home.example") {
			n++
		}
	}
	return n
}

// When the systems changed since the Operator looked, so that the Plan
// built again from a fresh read differs from the one confirmed, the
// Action refuses before any write, returning the new Plan.
func TestAChangeSinceThePlanRefusesBeforeAnyWrite(t *testing.T) {
	l := newLab(t, labOptions{})
	confirmed := l.plan(addWiki)

	// Meanwhile, in Nginx Proxy Manager's UI, and not read yet.
	l.npm.AddProxyHost(proxyHost("http://198.18.2.12:80", "wiki.home.example"))
	npmLog, fwLog := len(l.npm.Log()), len(l.fw.Log())

	_, err := l.tryRun(addWiki, confirmed)
	if err == nil || err.code != "aborted" || !strings.HasPrefix(err.message, "changed since you looked") {
		t.Fatalf("RunAction = %v, want aborted: changed since you looked", err)
	}
	var fresh appspb.Plan
	if !err.detail(&fresh) {
		t.Fatal("the refusal carries no Plan")
	}
	if d := diff(l.plan(addWiki), &fresh); d != "" {
		t.Errorf("the refusal's Plan (-want the Plan now +got):\n%s", d)
	}
	if n := fresh.GetNames(); len(n) != 1 || n[0].GetKind() != appspb.NameNoteKind_NAME_NOTE_KIND_APP {
		t.Errorf("the new Plan's notes = %v, want wiki.home.example taken by the new Proxy Host's App", n)
	}
	if w := append(writes(l.npm.Log(), npmLog), writes(l.fw.Log(), fwLog)...); len(w) != 0 {
		t.Errorf("writes = %q, want none", w)
	}
}

// A change to what a step expects, here the Reverse Proxy's Host Override
// moving, refuses the Plan too.
func TestAChangedBeforeValueRefusesThePlan(t *testing.T) {
	l := newLab(t, labOptions{})
	confirmed := l.plan(addWiki)
	l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		for i := range all {
			if all[i].Host == "nginx" {
				all[i].Domain = "lan.home.example"
			}
		}
		return all
	})

	_, err := l.tryRun(addWiki, confirmed)
	var fresh appspb.Plan
	if err == nil || err.code != "aborted" || !err.detail(&fresh) {
		t.Fatalf("RunAction = %v, want aborted with the new Plan", err)
	}
	if got := titles(&fresh); !slices.Contains(got, "SYSTEM_PFSENSE: Add alias wiki.home.example to nginx.lan.home.example") {
		t.Errorf("the new Plan's steps = %q, want the alias added to nginx.lan.home.example", got)
	}
	if l.wikiHosts() != 0 {
		t.Error("a Proxy Host was created")
	}
}

// App Actions take turns: of two Adds of the same name at once, one adds
// the App and the other, finding the name taken, is refused.
func TestTwoConcurrentAddsOfOneNameAddOneApp(t *testing.T) {
	l := newLab(t, labOptions{})
	confirmed := l.plan(addWiki)

	errs := make([]*callError, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = l.tryRun(addWiki, confirmed) })
	}
	wg.Wait()

	ok, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case err.code == "aborted":
			refused++
		default:
			t.Errorf("RunAction = %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Errorf("%d added and %d refused, want one each", ok, refused)
	}
	if n := l.wikiHosts(); n != 1 {
		t.Errorf("%d Proxy Hosts for wiki.home.example, want 1", n)
	}
}

// failedRun runs addWiki, which must stop at a failed step: it returns the
// error and its RunActionResponse.
func (l *lab) failedRun() (*callError, *appspb.RunActionResponse) {
	l.t.Helper()
	_, err := l.tryRun(addWiki, l.plan(addWiki))
	if err == nil {
		l.t.Fatal("RunAction succeeded, want it stopped")
	}
	var resp appspb.RunActionResponse
	if !err.detail(&resp) {
		l.t.Fatalf("RunAction = %v, without a RunActionResponse", err)
	}
	return err, &resp
}

// Nginx Proxy Manager refusing the Proxy Host stops the Action at its first
// step, before anything is written; a name a Redirection or 404 Host holds
// reads as such.
func TestAnAddStopsWhenNginxProxyManagerRefusesTheProxyHost(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.AddRedirectionHost("wiki.home.example")
	fwLog := len(l.fw.Log())

	err, resp := l.failedRun()
	want := "Stopped at step 1 of 3, Create Proxy Host wiki.home.example: Nginx Proxy Manager refused it: wiki.home.example is already in use (a Redirection or 404 Host). Nothing was rolled back."
	if err.code != "already_exists" || err.message != want || resp.GetDone() != 0 {
		t.Errorf("RunAction = %v (%v), want already_exists: %q, nothing done", err, resp, want)
	}
	if w := writes(l.fw.Log(), fwLog); len(w) != 0 {
		t.Errorf("pfSense writes = %q, want none", w)
	}
	if appsNamed(l.list().GetApps(), "wiki.home.example") != nil {
		t.Error("an App was added")
	}
}

// A Proxy Host nginx rejects is saved Offline all the same: the Action
// stops there, and the sync ending it shows the App with its Warning.
func TestAnAddStopsWhenNginxRejectsTheProxyHost(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.RejectNextSave("nginx: [emerg] host not found in upstream")
	fwLog := len(l.fw.Log())

	err, resp := l.failedRun()
	if err.code != "aborted" || !strings.HasPrefix(err.message, "Stopped at step 1 of 3, Create Proxy Host wiki.home.example: ") ||
		!strings.Contains(err.message, "host not found in upstream") || !strings.HasSuffix(err.message, "Nothing was rolled back.") || resp.GetDone() != 0 {
		t.Errorf("RunAction = %v (%v), want it stopped at step 1 with nginx's error", err, resp)
	}
	if w := writes(l.fw.Log(), fwLog); len(w) != 0 {
		t.Errorf("pfSense writes = %q, want none", w)
	}
	a := l.byURL("https://wiki.home.example")
	if !slices.Contains(kinds(a), appspb.WarningKind_WARNING_KIND_PROXY_HOST_OFFLINE) {
		t.Errorf("Wiki's Warnings = %v, want Proxy Host offline", kinds(a))
	}
}

// A failed pfSense step stops the Action after the Proxy Host was created:
// the error says so, and the sync ending it shows the App Not registered.
func TestAnAddStopsAtAFailedPfSenseStep(t *testing.T) {
	l := newLab(t, labOptions{})
	l.fw.FailDNSWrite(1)

	err, resp := l.failedRun()
	if !strings.HasPrefix(err.message, "Stopped at step 2 of 3, Add alias wiki.home.example to nginx.home.example: ") ||
		!strings.HasSuffix(err.message, "Done: Create Proxy Host wiki.home.example. Nothing was rolled back.") || resp.GetDone() != 1 {
		t.Errorf("RunAction = %v (%v), want it stopped at step 2, the Proxy Host created", err, resp)
	}
	if l.wikiHosts() != 1 {
		t.Error("the Proxy Host wasn't created")
	}
	a := l.byURL("https://wiki.home.example")
	if a.GetSettings().GetName() != "" || !slices.Contains(kinds(a), appspb.WarningKind_WARNING_KIND_NOT_REGISTERED) {
		t.Errorf("Wiki = %v, want the Proxy Host's App, untouched, Not registered", a)
	}
}

// A sync ending the Action that can't re-read in its time still reports
// what was written, plus that it couldn't re-read; the App shows as added.
func TestAFinalSyncTimingOutStillReportsSuccess(t *testing.T) {
	l := newLab(t, labOptions{resync: 200 * time.Millisecond})
	l.stallPfSense.Store(true)

	resp := l.run(addWiki)
	if resp.GetDone() != 3 || !strings.HasPrefix(resp.GetReread(), "couldn't re-read pfsense-api") {
		t.Errorf("RunAction = %v, want 3 steps done and that it couldn't re-read pfSense", resp)
	}
	l.stallPfSense.Store(false)
	a := l.app(resp.GetAppId())
	if a.GetReach() != appspb.Reach_REACH_PROXIED || a.GetOrphaned() || a.GetName() != "Wiki" || len(a.GetDns()) != 1 {
		t.Errorf("Wiki = %v, want it proxied and registered as written", a)
	}
}

// The Action's writes to Nginx Proxy Manager and pfSense are recorded in
// the Audit Log on its chain.
func TestAnAddsWritesAreOnItsChainInTheAuditLog(t *testing.T) {
	l := newLab(t, labOptions{})
	l.run(addWiki)

	var chain string
	byChain := map[string][]string{}
	for _, e := range l.h.AuditLog() {
		if e.Method == "RunAction" {
			chain = e.Chain
		}
		byChain[e.Chain] = append(byChain[e.Chain], e.Target+"."+e.Method+": "+e.Outcome)
	}
	got := byChain[chain]
	for _, want := range []string{"apps.RunAction: ok", "nginx-proxy-manager-api.CreateProxyHost: ok", "pfsense-api.ChangeHostOverrides: ok"} {
		if !slices.Contains(got, want) {
			t.Errorf("RunAction's chain %q lacks %q", got, want)
		}
	}
}

// pfSense failing to apply what every step wrote stops the Action after
// its last pfSense step, before the App record is written.
func TestAnAddStopsWhenPfSenseFailsToApply(t *testing.T) {
	l := newLab(t, labOptions{})
	l.fw.FailDNSApply()

	err, resp := l.failedRun()
	if !strings.HasPrefix(err.message, "Stopped after step 2 of 3, applying the Host Override changes in pfSense: ") ||
		!strings.HasSuffix(err.message, "Done: Create Proxy Host wiki.home.example; Add alias wiki.home.example to nginx.home.example. Nothing was rolled back.") ||
		resp.GetDone() != 2 {
		t.Errorf("RunAction = %v (%v), want it stopped applying, after step 2", err, resp)
	}
	if a := l.byURL("https://wiki.home.example"); a.GetSettings().GetName() != "" {
		t.Errorf("Wiki = %v, want the App record unwritten", a)
	}
}

// An Action whose last step is a pfSense step names the failed apply too.
func TestAFixStopsWhenPfSenseFailsToApply(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.AddProxyHost(proxyHost("http://198.18.2.11:3000", "wiki.home.example"))
	l.refresh()
	const id = "proxy:wiki.home.example"
	w := warningOf(l.app(id), notRegisteredKind)
	i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetKind() == appspb.FixKind_FIX_KIND_REGISTER })
	action := fixAction(id, w.GetFixes()[i])
	l.fw.FailDNSApply()

	_, err := l.tryRun(action, l.plan(action))
	want := "Stopped after step 1 of 1, applying the Host Override changes in pfSense: "
	if err == nil || !strings.HasPrefix(err.message, want) || !strings.HasSuffix(err.message, "Done: Add alias wiki.home.example to nginx.home.example. Nothing was rolled back.") {
		t.Errorf("RunAction = %v, want %q…", err, want)
	}
}
