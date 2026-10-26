package checks_test

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// untilState lets time pass, a second at a time, until Check id is in
// state.
func untilState(t *testing.T, c checkspb.ChecksServiceClient, id int64, state checkspb.State) {
	t.Helper()
	for range 10 * Interval11() / time.Second {
		if find(t, c, id).GetState() == state {
			return
		}
		after(time.Second)
	}
	t.Fatalf("Check %d not %v within %v", id, state, 10*Interval11())
}

func TestANotificationIsHeldForTheGroupingWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		l.set("nas:22", "refuse")
		untilState(t, c, id, checkspb.State_STATE_DOWN)
		after(checks.GroupWindow - 5*time.Second)
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Fatalf("sent %v %v after the down, want it held for %v", titles(got), checks.GroupWindow-5*time.Second, checks.GroupWindow)
		}
		after(5*time.Second + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down"}) {
			t.Errorf("sent %v once the window closed, want the single down", got)
		}
	})
}

// report reports the Nginx Proxy Manager item down with reason, or up when
// reason is empty.
func report(t *testing.T, c checkspb.ChecksServiceClient, reason string) {
	t.Helper()
	reportItem(t, c, "nginx-proxy-manager-api", "Nginx Proxy Manager", reason)
}

func reportItem(t *testing.T, c checkspb.ChecksServiceClient, item, name, reason string) {
	t.Helper()
	req := &checkspb.ReportStateRequest{Item: item, Name: name, State: checkspb.State_STATE_UP, Path: "/reverse-proxy"}
	if reason != "" {
		req.State, req.Reason = checkspb.State_STATE_DOWN, reason
	}
	if _, err := c.ReportState(t.Context(), req); err != nil {
		t.Fatalf("ReportState: %v", err)
	}
}

const emerg = `nginx: [emerg] unknown directive "proxy_passs"`

func TestAReportedItemIsNotifiedWhenItGoesDownAndWhenItRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)

		report(t, c, emerg)
		after(checks.GroupWindow + tick11())
		click := sdktest.PublicURL + "/reverse-proxy"
		want := []*ntfyapipb.Notification{{Title: "Nginx Proxy Manager down", Message: emerg, Priority: 4, Tags: []string{"red_circle"}, Click: click}}
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Fatalf("after the read failed (-want +got):\n%s", diff)
		}

		after(4 * time.Minute)
		report(t, c, "")
		after(checks.GroupWindow + tick11())
		want = append(want, &ntfyapipb.Notification{Title: "Nginx Proxy Manager up again", Message: "Down for 5 min (" + emerg + ")", Priority: 3, Tags: []string{"green_circle"}, Click: click})
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Errorf("after it recovered (-want +got):\n%s", diff)
		}
	})
}

func TestAReportedItemFirstReportedUpSendsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		report(t, c, "")
		after(checks.GroupWindow + tick11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v, want nothing for an item that was never down", titles(got))
		}
	})
}

func TestAReportedItemsStateSurvivesARestartOfChecks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		h, c := startIn(t, l)
		l.ntfy.setDown(true)
		report(t, c, emerg)
		synctest.Wait()

		h.Stop()
		l.ntfy.setDown(false)
		startIn(t, l, sdktest.WithDB(h.DBPath))
		after(checks.GroupWindow + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"Nginx Proxy Manager down"}) {
			t.Errorf("sent %v after the restart, want the down that wasn't delivered before it", got)
		}
	})
}

func TestReportStateRefusesABadReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, c := startIn(t, newLab())
		for _, req := range []*checkspb.ReportStateRequest{
			{Name: "Nginx Proxy Manager", State: checkspb.State_STATE_DOWN},
			{Item: "nginx-proxy-manager-api", State: checkspb.State_STATE_DOWN},
			{Item: "nginx-proxy-manager-api", Name: "Nginx Proxy Manager", State: checkspb.State_STATE_PENDING},
			{Item: "nginx-proxy-manager-api", Name: "Nginx Proxy Manager", State: checkspb.State_STATE_UP, Path: "https://elsewhere.example/"},
		} {
			if _, err := c.ReportState(t.Context(), req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("ReportState(%v): %v, want InvalidArgument", req, err)
			}
		}
	})
}

func TestAGroupsTitleCountsByKindAndItsPriorityIsTheHighest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		reportItem(t, c, "a", "Plex", "timeout")
		after(checks.GroupWindow + tick11())
		l.ntfy.sent = nil

		reportItem(t, c, "b", "Grafana", "HTTP 502")
		reportItem(t, c, "a", "Plex", "")
		reportItem(t, c, "c", "Jellyfin", "connection refused")
		reportItem(t, c, "d", "Sonarr", "HTTP 503")
		after(checks.GroupWindow + tick11())

		want := []*ntfyapipb.Notification{{
			Title:    "3 down, 1 up again",
			Message:  "Grafana down: HTTP 502\nJellyfin down: connection refused\nSonarr down: HTTP 503\nPlex up again: down for 1 min (timeout)",
			Priority: 4,
			Tags:     []string{"red_circle"},
			Click:    sdktest.PublicURL + "/checks",
		}}
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	})
}

func TestAnItemThatChangesTwiceSendsOnlyItsLatestState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		report(t, c, emerg)
		after(10 * time.Second)
		report(t, c, "")
		after(10 * time.Second)
		report(t, c, "nginx: [emerg] host not found in upstream")
		after(checks.GroupWindow + tick11())

		if got := l.ntfy.notifications(); len(got) != 1 || got[0].GetMessage() != "nginx: [emerg] host not found in upstream" {
			t.Errorf("sent %v, want the one down in its latest state", got)
		}
	})
}

func TestAnItemBackWhereItWasLastNotifiedSendsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		report(t, c, emerg)
		after(20 * time.Second)
		report(t, c, "")
		after(checks.GroupWindow + tick11())

		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v, want nothing: it ended up as last notified", titles(got))
		}
	})
}

// bounce takes Check id on addr down, waits for its Notification, then
// up again.
func bounce(t *testing.T, l *lab, c checkspb.ChecksServiceClient, id int64, addr string) {
	t.Helper()
	l.set(addr, "refuse")
	untilState(t, c, id, checkspb.State_STATE_DOWN)
	after(checks.GroupWindow + tick11())
	l.set(addr, "up")
	untilState(t, c, id, checkspb.State_STATE_UP)
	after(checks.GroupWindow + tick11())
}

func TestTheThirdDownWithinAnHourMakesACheckFlappingWithOneNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		bounce(t, l, c, id, "nas:22")
		bounce(t, l, c, id, "nas:22")
		if find(t, c, id).GetFlapping() {
			t.Fatal("Flapping after 2 downs, want it only on the 3rd")
		}
		l.set("nas:22", "refuse")
		untilState(t, c, id, checkspb.State_STATE_DOWN)
		if ch := find(t, c, id); !ch.GetFlapping() || ch.GetState() != checkspb.State_STATE_DOWN {
			t.Fatalf("after the 3rd down: flapping %v, state %v; want Flapping and still down", ch.GetFlapping(), ch.GetState())
		}
		after(checks.GroupWindow + tick11())

		got := l.ntfy.notifications()
		if want := []string{"NAS down", "NAS up again", "NAS down", "NAS up again", "NAS flapping"}; !cmp.Equal(titles(got), want) {
			t.Fatalf("sent %v, want %v", titles(got), want)
		}
		want := &ntfyapipb.Notification{
			Title:    "NAS flapping",
			Message:  "Down 3 times within 1 h. Next Notification once it has been up or down for 30 min.",
			Priority: 4,
			Tags:     []string{"warning"},
			Click:    fmt.Sprintf("%s/checks/%d", sdktest.PublicURL, id),
		}
		if diff := cmp.Diff(want, got[4], protocmp.Transform()); diff != "" {
			t.Errorf("the flapping Notification (-want +got):\n%s", diff)
		}
	})
}

func TestAFlappingChecksNotificationsAreHeldUntilItStaysInOneStateFor30Min(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()
		bounce(t, l, c, id, "nas:22")
		bounce(t, l, c, id, "nas:22")
		bounce(t, l, c, id, "nas:22") // Flapping from its 3rd down
		sent := len(l.ntfy.notifications())

		// More bouncing sends nothing.
		bounce(t, l, c, id, "nas:22")
		l.set("nas:22", "refuse")
		untilState(t, c, id, checkspb.State_STATE_DOWN)
		after(checks.GroupWindow + tick11())
		if got := l.ntfy.notifications()[sent:]; len(got) != 0 {
			t.Fatalf("sent %v while Flapping, want nothing", titles(got))
		}

		l.set("nas:22", "up")
		untilState(t, c, id, checkspb.State_STATE_UP)
		after(checks.FlapHold - time.Minute)
		if got := l.ntfy.notifications()[sent:]; len(got) != 0 || !find(t, c, id).GetFlapping() {
			t.Fatalf("up for 29 min: sent %v, flapping %v; want nothing yet, still Flapping", titles(got), find(t, c, id).GetFlapping())
		}
		after(time.Minute + checks.GroupWindow + 2*tick11())
		got := l.ntfy.notifications()[sent:]
		if len(got) != 1 || got[0].GetTitle() != "NAS up again" || got[0].GetMessage() != "Up for 31 min after Flapping" {
			t.Errorf("sent %v once up for 30 min, want one up again", got)
		}
		if ch := find(t, c, id); ch.GetFlapping() {
			t.Error("still Flapping after 30 min in one state")
		}
	})
}

func TestFlappingLeavesStateAndHistoryUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()
		for range 3 {
			bounce(t, l, c, id, "nas:22")
		}
		if ch := find(t, c, id); !ch.GetFlapping() || ch.GetState() != checkspb.State_STATE_UP {
			t.Fatalf("flapping %v, state %v; want Flapping and up", ch.GetFlapping(), ch.GetState())
		}
		var changes []string
		for _, b := range beats(t, c, id) {
			if len(changes) == 0 || changes[len(changes)-1] != b.state {
				changes = append(changes, b.state)
			}
		}
		want := []string{"up", "pending", "down", "up", "pending", "down", "up", "pending", "down", "up"}
		if !cmp.Equal(changes, want) {
			t.Errorf("states over the beats %v, want %v", changes, want)
		}
	})
}

func TestAReportedItemFlapsLikeACheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		for range 3 {
			report(t, c, emerg)
			after(checks.GroupWindow + tick11())
			report(t, c, "")
			after(checks.GroupWindow + tick11())
		}
		want := []string{"Nginx Proxy Manager down", "Nginx Proxy Manager up again", "Nginx Proxy Manager down", "Nginx Proxy Manager up again", "Nginx Proxy Manager flapping"}
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, want) {
			t.Fatalf("sent %v, want %v", got, want)
		}

		after(checks.FlapHold + checks.GroupWindow + tick11())
		want = append(want, "Nginx Proxy Manager up again")
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, want) {
			t.Errorf("sent %v once up for 30 min, want %v", got, want)
		}
	})
}

func endMaintenance(t *testing.T, c checkspb.ChecksServiceClient, scope *checkspb.MaintenanceScope) {
	t.Helper()
	if _, err := c.EndMaintenance(t.Context(), &checkspb.EndMaintenanceRequest{Scope: scope}); err != nil {
		t.Fatalf("EndMaintenance: %v", err)
	}
}

func TestNothingIsSentInMaintenanceAndItsEndNotifiesWhatChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		maintain(t, c, checkScope(id), 0)
		goDown(l, "nas:22")
		after(checks.GroupWindow + tick11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Fatalf("sent %v in Maintenance, want nothing", titles(got))
		}

		endMaintenance(t, c, checkScope(id))
		after(checks.GroupWindow - 5*time.Second)
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Fatalf("sent %v right as Maintenance ended, want it grouped", titles(got))
		}
		after(5*time.Second + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down"}) {
			t.Errorf("sent %v after Maintenance, want the down it kept quiet", got)
		}
	})
}

func TestTheEndOfMaintenanceSendsNothingWhenTheStateIsAsLastNotified(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		maintain(t, c, everything(), 30*time.Minute)
		goDown(l, "nas:22")
		l.set("nas:22", "up")
		after(30 * time.Minute)
		if ch := find(t, c, id); ch.GetMaintenance() || ch.GetState() != checkspb.State_STATE_UP {
			t.Fatalf("after 30 min: maintenance %v, state %v; want it ended and up", ch.GetMaintenance(), ch.GetState())
		}
		after(checks.GroupWindow + tick11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v, want nothing: up, as last notified", titles(got))
		}
	})
}

func TestMaintenanceOnEverythingSilencesReportedItems(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		maintain(t, c, checkScope(id), 0)
		report(t, c, emerg)
		after(checks.GroupWindow + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"Nginx Proxy Manager down"}) {
			t.Fatalf("sent %v with only a Check in Maintenance, want the reported down", got)
		}

		maintain(t, c, everything(), 0)
		report(t, c, "")
		after(checks.GroupWindow + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"Nginx Proxy Manager down"}) {
			t.Fatalf("sent %v in Maintenance on everything, want nothing more", got)
		}
		endMaintenance(t, c, everything())
		after(checks.GroupWindow + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"Nginx Proxy Manager down", "Nginx Proxy Manager up again"}) {
			t.Errorf("sent %v after Maintenance, want the recovery", got)
		}
	})
}

func TestDownsInMaintenanceDontMakeACheckFlapping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		maintain(t, c, checkScope(id), 0)
		for range 3 {
			bounce(t, l, c, id, "nas:22")
		}
		endMaintenance(t, c, checkScope(id))
		bounce(t, l, c, id, "nas:22")
		if ch := find(t, c, id); ch.GetFlapping() {
			t.Error("Flapping from downs in Maintenance, want only downs that count")
		}
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down", "NAS up again"}) {
			t.Errorf("sent %v, want the down and up after Maintenance", got)
		}
	})
}

// tlsLab is a lab whose Jellyfin answers HTTPS with a certificate
// expiring left after each dial.
func tlsLab(t *testing.T, left time.Duration) *lab {
	l := newLab()
	l.ca = newCA(t)
	l.set("jellyfin.home.example:443", "tls "+left.String())
	return l
}

// secure is Jellyfin's App Check, over HTTPS.
func secure() *checkspb.AppCheck {
	return app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example")
}

func TestOneNotificationWhenACertificateExpiresWithin14Days(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := tlsLab(t, 10*24*time.Hour)
		h, c := startIn(t, l)
		setApps(t, c, secure())
		after(checks.GroupWindow + tick11())

		id := appCheck(t, c, secure().GetAppId()).GetId()
		want := []*ntfyapipb.Notification{{
			Title:    "Jellyfin certificate expiring",
			Message:  "Expires in 10 days",
			Priority: 3,
			Tags:     []string{"warning"},
			Click:    fmt.Sprintf("%s/checks/%d", sdktest.PublicURL, id),
		}}
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Fatalf("(-want +got):\n%s", diff)
		}

		// The same certificate is never notified again, a restart included.
		after(10 * Interval11())
		h.Stop()
		startIn(t, l, sdktest.WithDB(h.DBPath))
		after(10 * Interval11())
		if n := len(l.ntfy.notifications()); n != 1 {
			t.Errorf("%d Notifications, want the one", n)
		}
	})
}

func TestACertificateFarFromExpiryIsntNotified(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := tlsLab(t, 30*24*time.Hour)
		_, c := startIn(t, l)
		setApps(t, c, secure())
		after(3 * Interval11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v, want nothing 30 days before expiry", titles(got))
		}
	})
}

func TestMuteSilencesEveryNotificationOfItsCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := tlsLab(t, 10*24*time.Hour)
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		setApps(t, c, secure())
		jf := appCheck(t, c, secure().GetAppId()).GetId()
		nas := create(t, c, named("NAS", "nas:22")).GetId()
		for _, id := range []int64{jf, nas} {
			if _, err := c.SetMuted(t.Context(), &checkspb.SetMutedRequest{Id: id, Muted: true}); err != nil {
				t.Fatal(err)
			}
		}
		for range 3 {
			bounce(t, l, c, nas, "nas:22") // down, up, then flapping
		}
		after(time.Hour)
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v for muted Checks, want nothing", titles(got))
		}
	})
}

func TestNotificationsWithinTheWindowAreSentAsOneGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("store:22", "up")
		_, c := startIn(t, l)
		// Created together on one target, they go down within seconds.
		create(t, c, named("NAS", "store:22"))
		create(t, c, named("NVR", "store:22"))
		synctest.Wait()

		goDown(l, "store:22")
		after(checks.GroupWindow + tick11())

		want := []*ntfyapipb.Notification{{
			Title:    "2 down",
			Message:  "NAS down: connection refused\nNVR down: connection refused",
			Priority: 4,
			Tags:     []string{"red_circle"},
			Click:    sdktest.PublicURL + "/checks",
		}}
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Errorf("(-want +got):\n%s", diff)
		}
	})
}
