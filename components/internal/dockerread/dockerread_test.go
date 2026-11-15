package dockerread_test

import (
	"testing"

	"github.com/itsKontra/ddash/components/internal/dockerread"
	containerpb "github.com/itsKontra/ddash/proto/ddash/container"
)

func TestHealthIsReadFromDockersStatus(t *testing.T) {
	for status, want := range map[string]containerpb.Health{
		"Up 3 hours (healthy)":            containerpb.Health_HEALTH_HEALTHY,
		"Up 2 minutes (unhealthy)":        containerpb.Health_HEALTH_UNHEALTHY,
		"Up 5 seconds (health: starting)": containerpb.Health_HEALTH_STARTING,
		"Up 3 hours":                      containerpb.Health_HEALTH_UNSPECIFIED,
		"Exited (0) 2 days ago":           containerpb.Health_HEALTH_UNSPECIFIED,
		"Restarting (1) 4 seconds ago":    containerpb.Health_HEALTH_UNSPECIFIED,
	} {
		if got := dockerread.Health(status); got != want {
			t.Errorf("Health(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestEveryStateDockerWritesHasItsEnumAndItsWordBack(t *testing.T) {
	for _, word := range []string{"created", "running", "paused", "restarting", "removing", "exited", "dead"} {
		s := dockerread.State(word)
		if s == containerpb.State_STATE_UNSPECIFIED || dockerread.StateWord(s) != word {
			t.Errorf("State(%q) = %v, and back %q", word, s, dockerread.StateWord(s))
		}
	}
	if got := dockerread.State("hibernating"); got != containerpb.State_STATE_UNSPECIFIED || dockerread.StateWord(got) != "unknown" {
		t.Errorf("a state this Release doesn't know: %v, %q", got, dockerread.StateWord(got))
	}
}

func TestOnlyTheLabelSetToTrueProtects(t *testing.T) {
	if !dockerread.Protected(map[string]string{containerpb.LabelProtected: "true"}) {
		t.Error("ddash.protected=true doesn't protect")
	}
	for _, labels := range []map[string]string{nil, {containerpb.LabelProtected: "false"}, {"other": "true"}} {
		if dockerread.Protected(labels) {
			t.Errorf("%v protects", labels)
		}
	}
}
