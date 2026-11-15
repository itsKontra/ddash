package sdk_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/sdk"
)

func TestTTLsStayBelowTheirRefreshTiers(t *testing.T) {
	if sdk.TTLFast != 10*time.Second || sdk.TTLSlow != 45*time.Second {
		t.Errorf("TTLFast, TTLSlow = %v, %v; want 10s, 45s", sdk.TTLFast, sdk.TTLSlow)
	}
}

// counter returns a fetch that counts its calls and returns the count.
func counter(calls *atomic.Int32) func(context.Context) (int32, error) {
	return func(context.Context) (int32, error) { return calls.Add(1), nil }
}

func TestCachedServesFromMemoryUntilTheTTLEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := sdk.Cached(sdk.TTLFast, counter(&calls))

		first, _ := c.Get(t.Context())
		time.Sleep(sdk.TTLFast - time.Second)
		again, _ := c.Get(t.Context())
		synctest.Wait()

		if first != 1 || again != 1 || calls.Load() != 1 {
			t.Errorf("values = %d, %d after %d fetches; want 1, 1 after 1", first, again, calls.Load())
		}
	})
}

// A caller who comes every refresh tier finds the value just past its TTL:
// it gets that value at once, and the next caller gets the one fetched
// behind it.
func TestCachedAnswersWithAStaleValueWhileItFetchesTheNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := sdk.Cached(sdk.TTLFast, func(context.Context) (int32, error) {
			if calls.Load() > 0 {
				time.Sleep(2 * time.Second) // a slow upstream
			}
			return calls.Add(1), nil
		})
		c.Get(t.Context())

		time.Sleep(sdk.TTLFast + time.Second)
		asked := time.Now()
		stale, err := c.Get(t.Context())
		if err != nil || stale != 1 || time.Since(asked) != 0 {
			t.Errorf("past the TTL: %d, %v after %v; want the stale 1 at once", stale, err, time.Since(asked))
		}

		time.Sleep(3 * time.Second)
		next, _ := c.Get(t.Context())
		if next != 2 || calls.Load() != 2 {
			t.Errorf("after the fetch behind it: %d after %d fetches; want 2 after 2", next, calls.Load())
		}
	})
}

func TestCachedWaitsForAFetchOnceTheValueIsTwoTTLsOld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := sdk.Cached(sdk.TTLFast, counter(&calls))
		c.Get(t.Context())

		time.Sleep(2 * sdk.TTLFast)
		got, _ := c.Get(t.Context())

		if got != 2 {
			t.Errorf("two TTLs later got %d, want a fetched 2", got)
		}
	})
}

func TestCachedKeepsAStaleValueWhenTheFetchBehindItFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fail := false
		c := sdk.Cached(sdk.TTLFast, func(context.Context) (string, error) {
			if fail {
				return "", errors.New("connection refused")
			}
			return "up", nil
		})
		c.Get(t.Context())
		fail = true

		time.Sleep(sdk.TTLFast + time.Second)
		stale, err := c.Get(t.Context())
		synctest.Wait()
		if stale != "up" || err != nil {
			t.Errorf("past the TTL: %q, %v; want the stale value", stale, err)
		}

		time.Sleep(sdk.TTLFast)
		if _, err := c.Get(t.Context()); err == nil {
			t.Error("two TTLs after the last good fetch: want the fetch error")
		}
	})
}

// The getter of a browser that left is cancelled; the fetch it started
// goes on for the callers sharing it.
func TestCachedFetchOutlivesTheCallerWhoStartedIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := sdk.Cached(sdk.TTLFast, func(ctx context.Context) (string, error) {
			select {
			case <-time.After(time.Second):
				return "up", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
		leaving, leave := context.WithCancel(t.Context())
		var first, second error
		var got string
		var wg sync.WaitGroup
		wg.Go(func() { _, first = c.Get(leaving) })
		synctest.Wait()
		wg.Go(func() { got, second = c.Get(t.Context()) })
		synctest.Wait()

		leave()
		wg.Wait()

		if !errors.Is(first, context.Canceled) {
			t.Errorf("the caller who left got %v, want context.Canceled", first)
		}
		if second != nil || got != "up" {
			t.Errorf("the caller who stayed got %q, %v; want the value", got, second)
		}
	})
}

func TestCachedSharesOneFetchBetweenConcurrentCallers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := sdk.Cached(sdk.TTLFast, func(ctx context.Context) (int32, error) {
			time.Sleep(time.Second) // a slow upstream
			return calls.Add(1), nil
		})

		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() { c.Get(t.Context()) })
		}
		wg.Wait()

		if calls.Load() != 1 {
			t.Errorf("%d fetches for 5 concurrent callers, want 1", calls.Load())
		}
	})
}

func TestInvalidateMakesTheNextGetFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := sdk.Cached(sdk.TTLSlow, counter(&calls))
		c.Get(t.Context())

		c.Invalidate()
		got, _ := c.Get(t.Context())

		if got != 2 {
			t.Errorf("after Invalidate got %d, want a fresh fetch (2)", got)
		}
	})
}

func TestCachedDoesNotKeepErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fail := true
		c := sdk.Cached(sdk.TTLSlow, func(context.Context) (string, error) {
			if fail {
				return "", errors.New("connection refused")
			}
			return "up", nil
		})
		if _, err := c.Get(t.Context()); err == nil {
			t.Fatal("want the fetch error")
		}

		fail = false
		got, err := c.Get(t.Context())

		if err != nil || got != "up" {
			t.Errorf("Get after a failed fetch = %q, %v; want a retry", got, err)
		}
	})
}
