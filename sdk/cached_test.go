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
		time.Sleep(time.Second)
		expired, _ := c.Get(t.Context())

		if first != 1 || again != 1 || expired != 2 {
			t.Errorf("values = %d, %d, %d; want 1, 1 (cached), 2 (after TTL)", first, again, expired)
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
