// Package ratelimit provides an in-memory, per-key token-bucket limiter.
//
// It is deliberately process-local: OmniDesk runs as a single API instance in
// v1. With several replicas each would enforce its own budget, so the
// effective limit becomes N times larger; moving the buckets to Redis or
// Postgres would be the fix (see docs/architecture.md).
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

type Limiter struct {
	mu      sync.Mutex
	keys    map[string]*entry
	every   rate.Limit
	burst   int
	idleTTL time.Duration
	now     func() time.Time
}

// New allows `burst` events at once per key, refilling one event every `per`.
func New(per time.Duration, burst int) *Limiter {
	return &Limiter{
		keys:    map[string]*entry{},
		every:   rate.Every(per),
		burst:   burst,
		idleTTL: 10 * time.Minute,
		now:     time.Now,
	}
}

// Allow reports whether one more event for key is allowed now. When it is
// not, it also returns how long until the next event would be allowed.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.keys[key]
	if !ok {
		if len(l.keys) > 10_000 {
			l.sweep(now)
		}
		e = &entry{lim: rate.NewLimiter(l.every, l.burst)}
		l.keys[key] = e
	}
	e.seen = now
	r := e.lim.ReserveN(now, 1)
	if delay := r.DelayFrom(now); delay > 0 {
		r.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// sweep drops keys that have been idle long enough to have refilled, which
// bounds memory under a flood of distinct keys. Caller holds mu.
func (l *Limiter) sweep(now time.Time) {
	for k, e := range l.keys {
		if now.Sub(e.seen) > l.idleTTL {
			delete(l.keys, k)
		}
	}
}
