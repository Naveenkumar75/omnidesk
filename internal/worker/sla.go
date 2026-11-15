// Package worker runs background jobs inside the API process.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/store"
)

type Sweeper interface {
	SweepSLABreaches(ctx context.Context, batch int) (store.SweepResult, error)
}

// RunSLA marks SLA breaches every interval until ctx is cancelled. Each tick
// drains all overdue tickets in batches. Errors are logged and retried on the
// next tick rather than stopping the worker: the sweep is idempotent, so a
// missed or repeated tick is harmless.
func RunSLA(ctx context.Context, s Sweeper, interval time.Duration, batch int, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		SweepOnce(ctx, s, batch, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce drains overdue tickets and returns the total breaches recorded.
func SweepOnce(ctx context.Context, s Sweeper, batch int, log *slog.Logger) store.SweepResult {
	var total store.SweepResult
	for ctx.Err() == nil {
		r, err := s.SweepSLABreaches(ctx, batch)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("sla sweep failed", "err", err)
			}
			break
		}
		total.FirstResponse += r.FirstResponse
		total.Resolution += r.Resolution
		if r.FirstResponse < batch && r.Resolution < batch {
			break
		}
	}
	if total.FirstResponse+total.Resolution > 0 {
		log.Info("sla breaches recorded", "first_response", total.FirstResponse, "resolution", total.Resolution)
	}
	return total
}
