package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Naveenkumar75/omnidesk/internal/store"
)

type fakeSweeper struct {
	results []store.SweepResult
	err     error
	calls   int
}

func (f *fakeSweeper) SweepSLABreaches(context.Context, int) (store.SweepResult, error) {
	f.calls++
	if f.err != nil {
		return store.SweepResult{}, f.err
	}
	if len(f.results) == 0 {
		return store.SweepResult{}, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSweepOnceDrainsFullBatches(t *testing.T) {
	f := &fakeSweeper{results: []store.SweepResult{{FirstResponse: 10}, {FirstResponse: 10, Resolution: 3}, {FirstResponse: 2}}}
	got := SweepOnce(context.Background(), f, 10, quiet)
	if got.FirstResponse != 22 || got.Resolution != 3 || f.calls != 3 {
		t.Fatalf("got %+v after %d calls, want 22/3 after 3", got, f.calls)
	}
}

func TestSweepOnceStopsOnError(t *testing.T) {
	f := &fakeSweeper{err: errors.New("db down")}
	SweepOnce(context.Background(), f, 10, quiet)
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no tight retry loop on error)", f.calls)
	}
}
