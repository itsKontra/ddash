package adguardapi_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
)

const setProtection = "POST /control/protection"

// clock is a fake's time, moved by hand.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func adguardOn(t *testing.T, scenario string) (*fakeAdGuard, *clock) {
	at := &clock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	return serve(t, scenario, adguardfake.Options{Now: at.Now}), at
}

func pause(t *testing.T, c adguardapipb.AdguardApiServiceClient, d time.Duration) {
	t.Helper()
	if _, err := c.PauseProtection(t.Context(), &adguardapipb.PauseProtectionRequest{Duration: durationpb.New(d)}); err != nil {
		t.Fatal(err)
	}
}

func resume(t *testing.T, c adguardapipb.AdguardApiServiceClient) {
	t.Helper()
	if _, err := c.ResumeProtection(t.Context(), &adguardapipb.ResumeProtectionRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestPausingTurnsProtectionOffForTheDurationAndTheStatusSaysSoAtOnce(t *testing.T) {
	srv, _ := adguardOn(t, "baseline")
	_, c := start(t, srv.URL)
	getStatus(t, c) // cached: the pause must not wait out the cache

	pause(t, c, 10*time.Minute)

	want := &adguardapipb.GetStatusResponse{Running: true, Version: "v0.107.79", ProtectionPausedFor: durationpb.New(10 * time.Minute)}
	if diff := cmp.Diff(want, getStatus(t, c), protocmp.Transform()); diff != "" {
		t.Errorf("GetStatus after a pause (-want +got):\n%s", diff)
	}
}

func TestAPauseEndsInAdGuardWithoutDdash(t *testing.T) {
	srv, at := adguardOn(t, "baseline")
	_, c := start(t, srv.URL)
	pause(t, c, time.Hour)

	at.now = at.now.Add(59 * time.Minute)
	if on, _ := srv.Protection(); on {
		t.Fatalf("protection on a minute before the pause is up")
	}
	at.now = at.now.Add(time.Minute)
	if on, until := srv.Protection(); !on || !until.IsZero() {
		t.Errorf("protection = %v until %v once the pause is up, want back on", on, until)
	}
	// One write: AdGuard ends the pause, not a second call from ddash.
	if got := slices.DeleteFunc(srv.Log(), func(e string) bool { return e != setProtection }); len(got) != 1 {
		t.Errorf("protection writes = %v, want the pause alone", got)
	}
}

func TestResumingTurnsProtectionBackOnWhetherPausedOrOff(t *testing.T) {
	for _, scenario := range []string{"paused", "protection-off"} {
		t.Run(scenario, func(t *testing.T) {
			srv, _ := adguardOn(t, scenario)
			_, c := start(t, srv.URL)
			getStatus(t, c)

			resume(t, c)

			if got := getStatus(t, c); !got.GetProtectionEnabled() || got.GetProtectionPausedFor() != nil {
				t.Errorf("GetStatus after resume = %v, want protection on", got)
			}
		})
	}
}

func TestPausingWhilePausedStartsThePauseAnew(t *testing.T) {
	srv, at := adguardOn(t, "paused")
	_, c := start(t, srv.URL)
	at.now = at.now.Add(8 * time.Minute)

	pause(t, c, time.Hour)

	if got := getStatus(t, c).GetProtectionPausedFor().AsDuration(); got != time.Hour {
		t.Errorf("paused for %v, want the new hour", got)
	}
}

func TestAPauseOfNothingOrOverADayIsRefusedAndNotSent(t *testing.T) {
	srv, _ := adguardOn(t, "baseline")
	_, c := start(t, srv.URL)

	for _, d := range []*durationpb.Duration{nil, durationpb.New(0), durationpb.New(-time.Minute), durationpb.New(25 * time.Hour)} {
		_, err := c.PauseProtection(t.Context(), &adguardapipb.PauseProtectionRequest{Duration: d})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("PauseProtection(%v) error = %v, want InvalidArgument", d, err)
		}
	}
	if slices.Contains(srv.Log(), setProtection) {
		t.Errorf("a refused pause was sent: %v", srv.Log())
	}
}
