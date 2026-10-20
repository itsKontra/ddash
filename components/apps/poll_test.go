package apps_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/sdk"
)

// afterPoll lets fake time pass the poll interval, its jitter included.
func afterPoll() {
	time.Sleep(apps.PollInterval + apps.PollInterval/10 + time.Second)
	synctest.Wait()
}

func names(s started, t *testing.T) []string {
	t.Helper()
	var out []string
	for _, a := range s.apps(t) {
		out = append(out, a.GetServerName())
	}
	return out
}

func TestANewProxyHostReachesAppsWithinAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl"))
		s := start(t, n)

		n.set(
			proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl"),
			proxy([]string{"paperless.home.example"}, "http://198.18.2.30:8000", "443 ssl"),
		)
		if got := names(s, t); len(got) != 1 {
			t.Fatalf("before the next poll: %v, want only grafana", got)
		}

		afterPoll()
		if d := diff([]string{"grafana.home.example", "paperless.home.example"}, names(s, t)); d != "" {
			t.Errorf("after a poll (-want +got):\n%s", d)
		}
	})
}

func TestWhileNginxConfigIsUnavailableAppsKeepsTheLastAppsAndIsDegraded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl"))
		s := start(t, n)
		if got := s.h.Health(); got.State != sdk.OK {
			t.Fatalf("health = %v, want ok", got)
		}

		n.fail(status.Error(codes.Unavailable, "nginx-config is down"))
		afterPoll()

		if d := diff([]string{"grafana.home.example"}, names(s, t)); d != "" {
			t.Errorf("Apps (-want +got):\n%s", d)
		}
		if got := s.list(t).GetDiscoveryError(); got != "nginx-config is down" {
			t.Errorf("discovery_error = %q, want nginx-config's error", got)
		}
		want := sdk.Health{State: sdk.Degraded, Message: "nginx-config unavailable: nginx-config is down"}
		if got := s.h.Health(); got != want {
			t.Errorf("health = %v, want %v", got, want)
		}

		n.fail(nil)
		afterPoll()
		if got := s.h.Health(); got.State != sdk.OK {
			t.Errorf("health after recovery = %v, want ok", got)
		}
	})
}

func TestAppsStartsEvenWhenNginxConfigIsUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx()
		n.fail(errors.New("not configured"))
		s := start(t, n)

		if got := s.apps(t); len(got) != 0 {
			t.Errorf("Apps = %v, want none", got)
		}
		if got := s.h.Health(); got.State != sdk.Degraded {
			t.Errorf("health = %v, want degraded", got)
		}

		n.fail(nil)
		n.set(proxy([]string{"grafana.home.example"}, "http://grafana", "443 ssl"))
		afterPoll()
		if d := diff([]string{"grafana.home.example"}, names(s, t)); d != "" {
			t.Errorf("Apps once nginx-config answers (-want +got):\n%s", d)
		}
	})
}
