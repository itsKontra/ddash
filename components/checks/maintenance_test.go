package checks_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

func checkScope(id int64) *checkspb.MaintenanceScope {
	return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_CheckId{CheckId: id}}
}

// maintain starts Maintenance on scope for d (0: until ended).
func maintain(t *testing.T, c checkspb.ChecksServiceClient, scope *checkspb.MaintenanceScope, d time.Duration) *checkspb.Maintenance {
	t.Helper()
	req := &checkspb.StartMaintenanceRequest{Scope: scope}
	if d > 0 {
		req.Duration = durationpb.New(d)
	}
	resp, err := c.StartMaintenance(t.Context(), req)
	if err != nil {
		t.Fatalf("StartMaintenance: %v", err)
	}
	return resp.GetMaintenance()
}

// maintBeats is whether each of Check id's kept beats was in Maintenance.
func maintBeats(t *testing.T, c checkspb.ChecksServiceClient, id int64) []bool {
	t.Helper()
	resp, err := c.ListBeats(t.Context(), &checkspb.ListBeatsRequest{Id: id})
	if err != nil {
		t.Fatalf("ListBeats: %v", err)
	}
	var out []bool
	for _, b := range resp.GetBeats() {
		out = append(out, b.GetMaintenance())
	}
	return out
}

func TestBeatsInMaintenanceAreRecordedAsMaintenanceAndLeftOutOfUptime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()

		maintain(t, c, checkScope(id), 0)
		if ch := find(t, c, id); !ch.GetMaintenance() || ch.GetMaintenanceEnds() != nil {
			t.Fatalf("in Maintenance until ended: maintenance %v, ends %v", ch.GetMaintenance(), ch.GetMaintenanceEnds())
		}
		l.set("nas:22", "refuse")
		for range 3 {
			next(t, c, id)
		}

		if got, want := maintBeats(t, c, id), []bool{false, true, true, true}; !slices.Equal(got, want) {
			t.Errorf("beats in Maintenance %v, want %v", got, want)
		}
		h := history(t, c, id, checkspb.Window_WINDOW_24H)
		if got, want := uptimes(h), []string{"WINDOW_24H 1/0 1", "WINDOW_30D 1/0 1", "WINDOW_365D 1/0 1"}; !slices.Equal(got, want) {
			t.Errorf("uptime %v, want %v: the failed beats were in Maintenance", got, want)
		}
		if n := h.GetUptime()[0].GetMaintenance(); n != 3 {
			t.Errorf("%d beats counted in Maintenance, want 3", n)
		}
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_DOWN {
			t.Errorf("state %v, want down: Maintenance doesn't stop the state from following the beats", got)
		}
	})
}

func TestMaintenanceEndsByItselfWhenItsTimeIsUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()

		started := time.Now()
		m := maintain(t, c, checkScope(id), 30*time.Minute)
		if !m.GetEnds().AsTime().Equal(started.Add(30 * time.Minute)) {
			t.Errorf("ends %v, want 30 min after it started (%v)", m.GetEnds().AsTime(), started)
		}
		if ends := find(t, c, id).GetMaintenanceEnds(); !ends.AsTime().Equal(m.GetEnds().AsTime()) {
			t.Errorf("the Check's Maintenance ends %v, want %v", ends.AsTime(), m.GetEnds().AsTime())
		}

		after(30*time.Minute - time.Millisecond)
		if !find(t, c, id).GetMaintenance() || len(listMaintenance(t, c)) != 1 {
			t.Fatal("Maintenance ended before its time")
		}
		after(time.Millisecond)
		if ch := find(t, c, id); ch.GetMaintenance() || ch.GetMaintenanceEnds() != nil {
			t.Errorf("after 30 min: maintenance %v, ends %v; want ended", ch.GetMaintenance(), ch.GetMaintenanceEnds())
		}
		if n := len(listMaintenance(t, c)); n != 0 {
			t.Errorf("%d Maintenance listed after it ended, want none", n)
		}
		next(t, c, id)
		bs := maintBeats(t, c, id)
		if bs[len(bs)-1] || !bs[len(bs)-2] {
			t.Errorf("beats in Maintenance %v: want the last before the end in it, the one after not", bs)
		}
	})
}

func listMaintenance(t *testing.T, c checkspb.ChecksServiceClient) []*checkspb.Maintenance {
	t.Helper()
	resp, err := c.ListMaintenance(t.Context(), &checkspb.ListMaintenanceRequest{})
	if err != nil {
		t.Fatalf("ListMaintenance: %v", err)
	}
	return resp.GetMaintenance()
}

func TestMaintenanceCanBeEndedEarly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		maintain(t, c, checkScope(id), 4*time.Hour)
		next(t, c, id)

		if _, err := c.EndMaintenance(t.Context(), &checkspb.EndMaintenanceRequest{Scope: checkScope(id)}); err != nil {
			t.Fatalf("EndMaintenance: %v", err)
		}
		if find(t, c, id).GetMaintenance() || len(listMaintenance(t, c)) != 0 {
			t.Fatal("still in Maintenance after it was ended")
		}
		next(t, c, id)
		if got, want := maintBeats(t, c, id), []bool{false, true, false}; !slices.Equal(got, want) {
			t.Errorf("beats in Maintenance %v, want %v", got, want)
		}
		if _, err := c.EndMaintenance(t.Context(), &checkspb.EndMaintenanceRequest{Scope: checkScope(id)}); err != nil {
			t.Errorf("ending it again: %v, want nothing to do", err)
		}
	})
}

func workloadScope(kind workloadpb.Kind, host, id string) *checkspb.MaintenanceScope {
	return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: &workloadpb.WorkloadRef{Kind: kind, Host: host, Id: id}}}
}

func everything() *checkspb.MaintenanceScope {
	return &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Everything{Everything: true}}
}

// on is a, its App running on w.
func on(a *checkspb.AppCheck, w *workloadpb.WorkloadRef) *checkspb.AppCheck {
	a.Workload = w
	return a
}

// inMaintenance is the names of the listed Checks in Maintenance.
func inMaintenance(t *testing.T, c checkspb.ChecksServiceClient) []string {
	t.Helper()
	var out []string
	for _, ch := range list(t, c) {
		if ch.GetMaintenance() {
			out = append(out, ch.GetSpec().GetName())
		}
	}
	return out
}

func TestAWorkloadsMaintenanceCoversTheAppChecksOfAppsRunningOnIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		media := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "110"}
		setApps(t, c,
			on(app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example"), media),
			on(app("proxy:sonarr.home.example", "Sonarr", "https://sonarr.home.example"), media),
			on(app("proxy:grafana.home.example", "Grafana", "https://grafana.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "120"}),
			on(app("proxy:vault.home.example", "Vault", "https://vault.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_VM, Host: "pve1", Id: "110"}),
			app("proxy:printer.home.example", "Printer", "http://printer.home.example"),
		)
		create(t, c, named("media ssh", "198.18.2.10:22"))

		// LXC 110 has since migrated to pve2: VMID 110 is still media.
		maintain(t, c, workloadScope(workloadpb.Kind_KIND_LXC, "pve2", "110"), time.Hour)
		if got, want := inMaintenance(t, c), []string{"Jellyfin", "Sonarr"}; !slices.Equal(got, want) {
			t.Errorf("in Maintenance %v, want %v: the App Checks on LXC 110 only", got, want)
		}

		maintain(t, c, everything(), 0)
		if got := inMaintenance(t, c); len(got) != 6 {
			t.Errorf("in Maintenance %v, want every Check", got)
		}
		got := listMaintenance(t, c)
		if len(got) != 2 || !got[0].GetScope().GetEverything() || got[1].GetScope().GetWorkload().GetId() != "110" {
			t.Errorf("Maintenance listed %v, want everything then LXC 110", got)
		}
	})
}

func TestADockerWorkloadIsItsHostAndContainer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		setApps(t, c,
			on(app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: "media", Id: "jellyfin"}),
			on(app("proxy:other.home.example", "Other", "https://other.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: "nas", Id: "jellyfin"}),
		)
		maintain(t, c, workloadScope(workloadpb.Kind_KIND_DOCKER, "media", "jellyfin"), 0)
		if got, want := inMaintenance(t, c), []string{"Jellyfin"}; !slices.Equal(got, want) {
			t.Errorf("in Maintenance %v, want %v", got, want)
		}
	})
}

func TestAWorkloadsMaintenanceCoversAnAppLinkedToItLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		setApps(t, c, app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example"))
		maintain(t, c, workloadScope(workloadpb.Kind_KIND_VM, "pve1", "101"), 0)
		if got := inMaintenance(t, c); len(got) != 0 {
			t.Fatalf("in Maintenance %v before its App runs on VM 101", got)
		}
		setApps(t, c, on(app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_VM, Host: "pve1", Id: "101"}))
		if got, want := inMaintenance(t, c), []string{"Jellyfin"}; !slices.Equal(got, want) {
			t.Errorf("in Maintenance %v, want %v", got, want)
		}
	})
}

func TestMaintenanceSurvivesARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		l.set("nvr:554", "up")
		h, c := startIn(t, l)
		nas := create(t, c, tcp("nas:22")).GetId()
		nvr := create(t, c, tcp("nvr:554")).GetId()
		synctest.Wait()
		maintain(t, c, checkScope(nas), time.Hour)
		maintain(t, c, checkScope(nvr), 10*time.Minute)
		ends := find(t, c, nas).GetMaintenanceEnds().AsTime()

		h.Stop()
		after(20 * time.Minute)
		_, c2 := startIn(t, l, sdktest.WithDB(h.DBPath))
		synctest.Wait()

		if ch := find(t, c2, nas); !ch.GetMaintenance() || !ch.GetMaintenanceEnds().AsTime().Equal(ends) {
			t.Errorf("nas after the restart: maintenance %v until %v, want kept until %v", ch.GetMaintenance(), ch.GetMaintenanceEnds().AsTime(), ends)
		}
		if find(t, c2, nvr).GetMaintenance() {
			t.Error("nvr's Maintenance ran out while ddash was down, but it is still in Maintenance")
		}
		if got := maintBeats(t, c2, nas); !got[len(got)-1] {
			t.Errorf("nas's first beat after the restart wasn't in Maintenance: %v", got)
		}
		if n := len(listMaintenance(t, c2)); n != 1 {
			t.Errorf("%d Maintenance listed, want nas's", n)
		}

		after(40 * time.Minute)
		if find(t, c2, nas).GetMaintenance() {
			t.Error("nas still in Maintenance after its hour")
		}
	})
}

func TestUntilUpMaintenanceEndsOnceEveryCheckItCoversIsUpAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("jellyfin.home.example:80", "refuse")
		l.set("sonarr.home.example:80", "refuse")
		_, c := startIn(t, l)
		media := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "110"}
		setApps(t, c,
			on(app("proxy:jellyfin.home.example", "Jellyfin", "http://jellyfin.home.example"), media),
			on(app("proxy:sonarr.home.example", "Sonarr", "http://sonarr.home.example"), media),
		)
		synctest.Wait()
		// ddash restarted LXC 110: Maintenance until its Checks are up, at most 5 min.
		if _, err := c.StartMaintenance(t.Context(), &checkspb.StartMaintenanceRequest{
			Scope: workloadScope(workloadpb.Kind_KIND_LXC, "pve1", "110"), Duration: durationpb.New(5 * time.Minute), UntilUp: true,
		}); err != nil {
			t.Fatal(err)
		}

		l.set("jellyfin.home.example:80", "http 200")
		after(Interval11())
		if got := inMaintenance(t, c); len(got) != 2 {
			t.Fatalf("in Maintenance %v with Sonarr still failing, want both", got)
		}
		l.set("sonarr.home.example:80", "http 200")
		after(Interval11())
		if got := inMaintenance(t, c); len(got) != 0 {
			t.Errorf("in Maintenance %v once both are up, want ended", got)
		}
		if n := len(listMaintenance(t, c)); n != 0 {
			t.Errorf("%d Maintenance listed, want none", n)
		}
	})
}

func TestUntilUpMaintenanceIsNotEndedByABeatFromBeforeIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		l.set("nvr:554", "up")
		_, c := startIn(t, l)
		nas := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		after(30 * time.Second)
		nvr := create(t, c, tcp("nvr:554")).GetId()
		synctest.Wait()
		if _, err := c.StartMaintenance(t.Context(), &checkspb.StartMaintenanceRequest{Scope: everything(), UntilUp: true}); err != nil {
			t.Fatal(err)
		}
		next(t, c, nas)
		if !find(t, c, nvr).GetMaintenance() {
			t.Fatal("ended by nas's up beat while nvr's last up beat was from before it started")
		}
		next(t, c, nvr)
		if got := inMaintenance(t, c); len(got) != 0 {
			t.Errorf("in Maintenance %v once both had an up beat, want ended", got)
		}
	})
}

func TestStartMaintenanceRefusesABadScopeOrDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		for _, tc := range []struct {
			name string
			req  *checkspb.StartMaintenanceRequest
			code codes.Code
		}{
			{"no scope", &checkspb.StartMaintenanceRequest{}, codes.InvalidArgument},
			{"everything false", &checkspb.StartMaintenanceRequest{Scope: &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Everything{}}}, codes.InvalidArgument},
			{"unknown Check", &checkspb.StartMaintenanceRequest{Scope: checkScope(id + 1)}, codes.NotFound},
			{"LXC without VMID", &checkspb.StartMaintenanceRequest{Scope: workloadScope(workloadpb.Kind_KIND_LXC, "pve1", "")}, codes.InvalidArgument},
			{"Docker without host", &checkspb.StartMaintenanceRequest{Scope: workloadScope(workloadpb.Kind_KIND_DOCKER, "", "jellyfin")}, codes.InvalidArgument},
			{"Workload without kind", &checkspb.StartMaintenanceRequest{Scope: workloadScope(workloadpb.Kind_KIND_UNSPECIFIED, "pve1", "110")}, codes.InvalidArgument},
			{"zero duration", &checkspb.StartMaintenanceRequest{Scope: everything(), Duration: durationpb.New(0)}, codes.InvalidArgument},
			{"over a day", &checkspb.StartMaintenanceRequest{Scope: everything(), Duration: durationpb.New(25 * time.Hour)}, codes.InvalidArgument},
		} {
			if _, err := c.StartMaintenance(t.Context(), tc.req); status.Code(err) != tc.code {
				t.Errorf("%s: %v, want %v", tc.name, err, tc.code)
			}
		}
		if n := len(listMaintenance(t, c)); n != 0 {
			t.Errorf("%d Maintenance started, want none", n)
		}
	})
}

func TestStartingMaintenanceAgainReplacesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		maintain(t, c, checkScope(id), 4*time.Hour)
		maintain(t, c, checkScope(id), 30*time.Minute)
		got := listMaintenance(t, c)
		if len(got) != 1 || got[0].GetEnds().AsTime().Sub(got[0].GetStarted().AsTime()) != 30*time.Minute {
			t.Errorf("Maintenance %v, want one of 30 min", got)
		}
	})
}

func TestDeletingACheckEndsItsMaintenance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		h, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		maintain(t, c, checkScope(id), 0)
		if _, err := c.DeleteCheck(t.Context(), &checkspb.DeleteCheckRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		if n := len(listMaintenance(t, c)); n != 0 {
			t.Errorf("%d Maintenance listed, want the deleted Check's gone", n)
		}
		h.Stop()
		_, c2 := startIn(t, l, sdktest.WithDB(h.DBPath))
		if n := len(listMaintenance(t, c2)); n != 0 {
			t.Errorf("%d Maintenance after a restart, want the deleted Check's gone", n)
		}
	})
}

func TestListMaintenanceIsAFastGetter(t *testing.T) {
	m := checkspb.File_checks_checks_proto.Services().ByName("ChecksService").Methods().ByName("ListMaintenance")
	g, _ := proto.GetExtension(m.Options(), ddashpb.E_Getter).(*ddashpb.Getter)
	if g.GetRefresh() != ddashpb.Getter_FAST {
		t.Errorf("refresh = %v, want FAST", g.GetRefresh())
	}
}
