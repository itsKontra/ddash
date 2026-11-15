package agent

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func lines(t *testing.T, script string) []string {
	t.Helper()
	var got []string
	done := make(chan error, 1)
	go func() {
		done <- RunLines(exec.CommandContext(context.Background(), "sh", "-c", script), func(l string) { got = append(got, l) })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("RunLines never returned")
	}
	return got
}

func TestRunLinesPassesEachLineOfBothStreams(t *testing.T) {
	got := lines(t, `echo one; echo two >&2; printf 'three\r\n'; printf last`)
	slices.Sort(got)
	if want := []string{"last", "one", "three", "two"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// A job's command once hung for good on a line past the scanner's limit:
// nothing read the pipe any more, so the command never finished writing.
func TestRunLinesSurvivesALineLongerThanItKeeps(t *testing.T) {
	got := lines(t, `head -c 3000000 /dev/zero | tr '\0' a; echo; echo after`)

	if len(got) != 2 || len(got[0]) != maxLine || strings.Trim(got[0], "a") != "" || got[1] != "after" {
		t.Errorf("got %d lines, the first %d long, the last %q; want the long line cut to %d, then after", len(got), len(got[0]), got[len(got)-1], maxLine)
	}
}

// A package's install script may start a daemon that keeps the output
// open; the command itself has ended.
func TestRunLinesReturnsThoughSomethingItStartedHoldsTheOutput(t *testing.T) {
	waitDelay = 200 * time.Millisecond
	t.Cleanup(func() { waitDelay = 10 * time.Second })
	started := time.Now()

	got := lines(t, `sleep 15 & echo started`)

	if !slices.Equal(got, []string{"started"}) || time.Since(started) > 5*time.Second {
		t.Errorf("lines = %q after %v; want started, at once", got, time.Since(started))
	}
}
