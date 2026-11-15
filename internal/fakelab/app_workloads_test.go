package fakelab_test

import (
	"testing"

	"github.com/itsKontra/ddash/internal/fakelab"
)

// paperlessState is the state of paperless's webserver on the lab's Docker
// Agent, serving scenarios.
func paperlessState(t *testing.T, scenarios ...string) string {
	t.Helper()
	lab, err := fakelab.Start(repoRoot, "127.0.0.1", scenarios...)
	if err != nil {
		t.Fatal(err)
	}
	defer lab.Close()
	return lab.DockerAgents[0].Fake.State("paperless-webserver-1")
}

func TestTheAppWorkloadsDownScenarioHasPaperlessExited(t *testing.T) {
	if got := paperlessState(t); got != "running" {
		t.Errorf("baseline: paperless's webserver is %q, want running", got)
	}
	if got := paperlessState(t, "app-workloads-down"); got != "exited" {
		t.Errorf("app-workloads-down: paperless's webserver is %q, want exited", got)
	}
}
