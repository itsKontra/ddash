package sdk

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// TTLs for caching upstream reads, kept below the UI's refresh tiers (FAST
// getters refresh every 15 s, SLOW every 60 s), so each refresh finds the
// value stale and fetches the next one behind its answer (Cache).
const (
	TTLFast = 10 * time.Second
	TTLSlow = 45 * time.Second
)

// fetchTimeout bounds one fetch, which runs under no caller's deadline.
const fetchTimeout = 30 * time.Second

// Cache keeps the last successful result of a fetch in memory. For a TTL
// it is fresh. For one more TTL it is stale: Get still answers with it at
// once, and fetches a new one behind that answer, so a caller who comes
// every refresh tier never waits for the upstream. Older than that, or
// never fetched, Get waits for the fetch. Concurrent callers share one
// fetch, which runs to its end whichever of them leaves. Errors aren't
// kept. Caches are never persisted.
type Cache[T any] struct {
	ttl   time.Duration
	fetch func(context.Context) (T, error)
	group singleflight.Group

	mu      sync.Mutex
	value   T
	expires time.Time // the zero time while there is no value
	gen     uint64    // bumped by Invalidate, so an in-flight fetch isn't stored
}

// Cached caches fetch for ttl, usually TTLFast or TTLSlow.
func Cached[T any](ttl time.Duration, fetch func(context.Context) (T, error)) *Cache[T] {
	return &Cache[T]{ttl: ttl, fetch: fetch}
}

// Get returns the cached value: a fresh one as it is, a stale one while a
// new one is fetched behind it, and otherwise the one it waits to fetch.
func (c *Cache[T]) Get(ctx context.Context) (T, error) {
	c.mu.Lock()
	now, v, gen := time.Now(), c.value, c.gen
	fresh, usable := now.Before(c.expires), now.Before(c.expires.Add(c.ttl))
	c.mu.Unlock()
	if fresh {
		return v, nil
	}
	fetched := c.refresh(ctx, gen)
	if usable {
		return v, nil
	}
	select {
	case r := <-fetched:
		v, _ := r.Val.(T) // nil when T is an interface and fetch returned nil
		return v, r.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// refresh starts a fetch, or joins the one running, and stores its value
// unless Invalidate came meanwhile. The fetch keeps ctx's values but not
// its end: the callers sharing it, and the next one, want it too.
func (c *Cache[T]) refresh(ctx context.Context, gen uint64) <-chan singleflight.Result {
	ctx = context.WithoutCancel(ctx)
	return c.group.DoChan("", func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		v, err := c.fetch(ctx)
		if err != nil {
			return v, err
		}
		c.mu.Lock()
		if c.gen == gen {
			c.value, c.expires = v, time.Now().Add(c.ttl)
		}
		c.mu.Unlock()
		return v, nil
	})
}

// Invalidate drops the cached value. An Action that changes what fetch
// reads calls it, so the next Get sees the change.
func (c *Cache[T]) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero T
	c.value, c.expires = zero, time.Time{}
	c.gen++
	c.group.Forget("")
}
