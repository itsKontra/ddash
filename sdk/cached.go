package sdk

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// TTLs for caching upstream reads, kept below the UI's refresh tiers so
// each refresh sees fresh data: FAST getters refresh every 15 s, SLOW every 60 s.
const (
	TTLFast = 10 * time.Second
	TTLSlow = 45 * time.Second
)

// Cache keeps the last successful result of a fetch in memory for a TTL.
// Concurrent callers during a fetch share it. Errors aren't kept. Caches
// are never persisted.
type Cache[T any] struct {
	ttl   time.Duration
	fetch func(context.Context) (T, error)
	group singleflight.Group

	mu      sync.Mutex
	value   T
	expires time.Time
	gen     uint64 // bumped by Invalidate, so an in-flight fetch isn't stored
}

// Cached caches fetch for ttl, usually TTLFast or TTLSlow.
func Cached[T any](ttl time.Duration, fetch func(context.Context) (T, error)) *Cache[T] {
	return &Cache[T]{ttl: ttl, fetch: fetch}
}

// Get returns the cached value, fetching it if it expired.
func (c *Cache[T]) Get(ctx context.Context) (T, error) {
	c.mu.Lock()
	if time.Now().Before(c.expires) {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	gen := c.gen
	c.mu.Unlock()

	r, err, _ := c.group.Do("", func() (any, error) {
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
	v, _ := r.(T) // r is nil when T is an interface and fetch returned nil
	return v, err
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
