package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(time.Minute, 3)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("event %d within burst was refused", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("4th event: ok=%v wait=%v, want refused with wait in (0, 1m]", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys must have independent budgets")
	}

	now = now.Add(time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("one event should be allowed after one refill period")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestLimiterSweepsIdleKeys(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(time.Second, 1)
	l.now = func() time.Time { return now }
	l.Allow("old")
	now = now.Add(time.Hour)
	l.sweep(now)
	if _, ok := l.keys["old"]; ok {
		t.Fatal("idle key was not swept")
	}
}
