package sdk

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"
)

// Every runs fn at once and then every interval plus up to 10% jitter, until
// ctx ends. Pass the ctx a Component's Start got: Stop then waits for a run
// in progress, and a panic in fn is recovered and makes the Component
// degraded until fn next runs cleanly.
func Every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	l, ok := ctx.Value(loopsKey{}).(*loops)
	if !ok {
		l = &loops{log: slog.New(slog.DiscardHandler)}
	}
	id := l.add()
	go func() {
		defer l.wg.Done()
		for ctx.Err() == nil {
			l.run(ctx, id, fn)
			jitter := time.Duration(0)
			if interval >= 10 {
				jitter = rand.N(interval / 10)
			}
			timer := time.NewTimer(interval + jitter)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}()
}

// loops tracks a Component's background loops.
type loops struct {
	log *slog.Logger
	wg  sync.WaitGroup

	mu     sync.Mutex
	n      int
	panics map[int]string // loop id → why its last run panicked
}

type loopsKey struct{}

func (l *loops) add() int {
	l.wg.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return l.n
}

func (l *loops) run(ctx context.Context, id int, fn func(context.Context)) {
	defer func() {
		r := recover()
		l.mu.Lock()
		defer l.mu.Unlock()
		if r == nil {
			delete(l.panics, id)
			return
		}
		l.log.Error("background loop panicked", "panic", r, "stack", string(debug.Stack()))
		if l.panics == nil {
			l.panics = map[int]string{}
		}
		l.panics[id] = fmt.Sprint(r)
	}()
	fn(ctx)
}

// health is Degraded while any loop's last run panicked.
func (l *loops) health() Health {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id := 1; id <= l.n; id++ {
		if p, ok := l.panics[id]; ok {
			return Health{State: Degraded, Message: "background loop panicked: " + p}
		}
	}
	return Health{State: OK}
}
