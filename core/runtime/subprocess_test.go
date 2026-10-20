package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/release"
	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
)

// launchSample launches the sample Component binary as a subprocess and
// starts it with target.
func launchSample(t *testing.T, target string) runtime.Process {
	t.Helper()
	dir := t.TempDir()
	if err := release.Binary(t.Context(), "../..", "./core/internal/sample/cmd/sample", filepath.Join(dir, "greeter"), "dev"); err != nil {
		t.Fatal(err)
	}
	if err := release.Sums(dir); err != nil {
		t.Fatal(err)
	}
	proc, err := runtime.Subprocess(dir, t.Output()).Launch(t.Context(), runtime.LaunchSpec{
		Name:   "greeter",
		DBPath: filepath.Join(t.TempDir(), "greeter.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Stop(context.Background()) })
	cfg, _ := proto.Marshal(&samplepb.SampleConfig{Target: target})
	if _, err := componentpb.NewLifecycleServiceClient(proc.Conn()).Start(t.Context(), &componentpb.StartRequest{Config: cfg}); err != nil {
		t.Fatal(err)
	}
	return proc
}

// stop stops proc with timeout and says how long that took.
func stop(t *testing.T, proc runtime.Process, timeout time.Duration) time.Duration {
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	begin := time.Now()
	proc.Stop(ctx)
	took := time.Since(begin)
	select {
	case <-proc.Done():
	default:
		t.Error("the process still runs after Stop")
	}
	return took
}

func TestStopCancelsTheComponentsCtxAndWaitsForItsWork(t *testing.T) {
	proc := launchSample(t, "slow-stop") // finishes 1 s after its ctx ends

	took := stop(t, proc, 10*time.Second)

	if took < time.Second || took > 5*time.Second {
		t.Errorf("Stop took %s, want the second its work takes to finish", took)
	}
}

func TestStopKillsAComponentStillBusyWhenTheTimeoutEnds(t *testing.T) {
	proc := launchSample(t, "ignore-stop") // never finishes its work

	took := stop(t, proc, time.Second)

	// Left alone, the SDK would give up waiting after 8 s.
	if took < time.Second || took > 4*time.Second {
		t.Errorf("Stop took %s, want it killed when the 1 s timeout ended", took)
	}
}
