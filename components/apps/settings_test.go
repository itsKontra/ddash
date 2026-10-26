package apps_test

import (
	"slices"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// The settings Action sets an App's name, icon, group, Hidden and Workload
// override at once; a Hidden App's Check isn't probed.
func TestTheSettingsActionSetsAnAppsSettings(t *testing.T) {
	l := newLab(t, labOptions{})
	override := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_EXTERNAL, Host: "198.18.1.50"}
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{
		Name: " Jellyfin ", Icon: "Jellyfin", Group: "Media", Hidden: true, Workload: override,
	}}, nil)

	got := l.app("proxy:video.home.example")
	if got.GetName() != "Jellyfin" || got.GetIcon() != "jellyfin" || got.GetGroup() != "Media" || !got.GetHidden() || !got.GetTouched() {
		t.Errorf("video = %v, want its settings, touched", got)
	}
	want := &appspb.Workload{Ref: override, Name: "198.18.1.50", Override: true}
	if d := diff(want, got.GetWorkload()); d != "" {
		t.Errorf("Workload (-want +got):\n%s", d)
	}
	if c := l.appChecks()["proxy:video.home.example"]; c.GetSpec().GetName() != "Jellyfin" || !c.GetDisabled() {
		t.Errorf("its Check = %v, want named Jellyfin and not probed", c)
	}

	// Cleared, the defaults are back; the App stays touched.
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{}}, nil)
	got = l.app("proxy:video.home.example")
	if got.GetName() != "Video" || got.GetHidden() || got.GetWorkload().GetOverride() || !got.GetTouched() {
		t.Errorf("video = %v, want its defaults back, still touched", got)
	}
}

// Empty settings don't touch an App: it still disappears with its
// sources.
func TestEmptySettingsDontTouchAnApp(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{}}, nil)
	l.npm.DeleteProxyHost(34)
	l.unregister("video.home.example")
	l.refresh()
	if l.has("proxy:video.home.example") {
		t.Error("video is still listed")
	}
}

func TestBadSettingsAreRefused(t *testing.T) {
	l := newLab(t, labOptions{})
	for _, tc := range []struct {
		req  *appspb.SetSettingsRequest
		code string
	}{
		{&appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{Icon: "not an icon"}}, "invalid_argument"},
		{&appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{Workload: &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Id: "media"}}}, "invalid_argument"},
		{&appspb.SetSettingsRequest{Id: "proxy:nowhere", Settings: &appspb.AppSettings{Name: "Nowhere"}}, "not_found"},
	} {
		if err := l.try("SetSettings", tc.req, nil); err == nil || err.code != tc.code {
			t.Errorf("SetSettings %v: %v, want %s", tc.req, err, tc.code)
		}
	}
}

// Link-only Apps are added and deleted by hand; an App with a source
// can't be deleted that way.
func TestLinkOnlyAppsAreAddedAndDeleted(t *testing.T) {
	l := newLab(t, labOptions{})
	for _, bad := range []*appspb.AddApp{
		{Name: "pfSense", Url: "198.18.0.1"},
		{Name: "pfSense", Url: "ftp://198.18.0.1"},
		{Name: "pfSense", Url: "https://198.18.0.1", Icon: "pf sense"},
	} {
		bad.Reach = appspb.Reach_REACH_LINK_ONLY
		action := addApp(bad)
		p := l.plan(action)
		if len(p.GetBlocked()) != 1 {
			t.Errorf("Plan for %v = %v, want it blocked, saying why", bad, p)
		}
		if _, err := l.tryRun(action, p); err == nil || err.code != "failed_precondition" || err.message != p.GetBlocked()[0] {
			t.Errorf("RunAction %v: %v, want failed_precondition saying why", bad, err)
		}
	}

	a := l.app(l.addLinkOnly("pfSense", "https://198.18.0.1"))
	if a.GetName() != "pfSense" || a.GetReach() != appspb.Reach_REACH_LINK_ONLY {
		t.Errorf("added %v, want a Link-only App", a)
	}

	if err := l.try("DeleteApp", &appspb.DeleteAppRequest{Id: "proxy:video.home.example"}, nil); err == nil || err.code != "failed_precondition" {
		t.Errorf("DeleteApp of a proxied App: %v, want failed_precondition", err)
	}
	l.call("DeleteApp", &appspb.DeleteAppRequest{Id: a.GetId()}, nil)
	if l.has(a.GetId()) {
		t.Error("pfSense is still listed after its delete")
	}
	if l.appChecks()[a.GetId()] != nil {
		t.Error("pfSense still has a Check")
	}
}

// Every App Action is an audited Action of the Operator's.
func TestAppActionsAreAudited(t *testing.T) {
	l := newLab(t, labOptions{})
	l.call("SetSettings", &appspb.SetSettingsRequest{Id: "proxy:video.home.example", Settings: &appspb.AppSettings{Group: "Media"}}, nil)
	l.call("IgnoreWarning", &appspb.IgnoreWarningRequest{Id: "proxy:video.home.example", Kind: noTLS}, nil)
	l.refresh()

	var audited []string
	for _, e := range l.h.AuditLog() {
		if e.Target == "apps" {
			audited = append(audited, e.Method+" by "+e.ActorKind+":"+e.Username+": "+e.Outcome)
		}
	}
	slices.Reverse(audited)
	want := []string{"SetSettings by operator:test: ok", "IgnoreWarning by operator:test: ok", "Refresh by operator:test: ok"}
	if d := diff(want, audited); d != "" {
		t.Errorf("Audit Log entries for apps (-want +got):\n%s", d)
	}
}

// Refresh returns once the Apps are current: a Proxy Host added in Nginx
// Proxy Manager since its last read is an App when it returns.
func TestRefreshReturnsOnceTheAppsAreCurrent(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.AddProxyHost(proxyHost("http://198.18.2.11:3000", "wiki.home.example"))
	if l.has("proxy:wiki.home.example") {
		t.Fatal("the new Proxy Host is an App before any sync")
	}
	l.refresh()
	if !l.has("proxy:wiki.home.example") {
		t.Error("the new Proxy Host is no App after Refresh")
	}
}
