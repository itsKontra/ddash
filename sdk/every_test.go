package sdk_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/internal/echo/echopb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// startLoop starts an echo whose Start runs fn every interval.
func startLoop(t *testing.T, interval time.Duration, fn func(context.Context)) *sdktest.Harness {
	h := sdktest.New(t, define(&echo{start: func(ctx context.Context, _ *sdk.Env, _ *echopb.EchoConfig) error {
		sdk.Every(ctx, interval, fn)
		return nil
	}}))
	h.MustStart(&echopb.EchoConfig{})
	return h
}

func TestEveryRunsAtOnceThenEveryIntervalWithJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs []time.Duration
		begin := time.Now()
		startLoop(t, time.Minute, func(context.Context) { runs = append(runs, time.Since(begin)) })

		time.Sleep(10 * time.Minute)
		synctest.Wait()

		// 10 runs fit into 10 min only without jitter; jitter adds up to 10%.
		if len(runs) < 9 || len(runs) > 10 {
			t.Fatalf("%d runs in 10 min, want 9 or 10: %v", len(runs), runs)
		}
		if runs[0] != 0 {
			t.Errorf("first run after %v, want at once", runs[0])
		}
		jittered := false
		for i := 1; i < len(runs); i++ {
			gap := runs[i] - runs[i-1]
			if gap < time.Minute || gap > time.Minute+6*time.Second {
				t.Errorf("gap %d = %v, want 1 min plus up to 10%%", i, gap)
			}
			if gap != time.Minute {
				jittered = true
			}
		}
		if !jittered {
			t.Error("every gap was exactly 1 min: no jitter")
		}
	})
}

func TestAPanicInALoopIsDegradedUntilItsNextCleanRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var run atomic.Int32
		h := startLoop(t, time.Minute, func(context.Context) {
			if run.Add(1) == 1 {
				panic("nil map")
			}
		})
		synctest.Wait()

		if got := h.Health(); got.State != sdk.Degraded || !strings.Contains(got.Message, "nil map") {
			t.Errorf("after a panic: health = %v, want degraded naming the panic", got)
		}

		time.Sleep(2 * time.Minute)
		synctest.Wait()

		if got := h.Health(); got.State != sdk.OK {
			t.Errorf("after a clean run: health = %v, want ok", got)
		}
	})
}

func TestStopEndsLoopsAndWaitsForThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs, finished atomic.Int32
		h := startLoop(t, time.Minute, func(ctx context.Context) {
			runs.Add(1)
			<-ctx.Done()
			time.Sleep(time.Second) // cleanup after cancel
			finished.Add(1)
		})
		synctest.Wait()

		h.Stop()

		if finished.Load() != 1 {
			t.Error("Stop returned before the running loop finished")
		}
		time.Sleep(10 * time.Minute)
		if runs.Load() != 1 {
			t.Errorf("%d runs; the loop kept running after Stop", runs.Load())
		}
	})
}
