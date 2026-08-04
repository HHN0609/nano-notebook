package metrics

import (
	"context"
	"time"
)

// ProjectionQueueErrorStat is the subset of
// collector.ProjectionQueueErrorStat this package needs, expressed without
// a collector dependency because this is a leaf package (see registry.go's
// package doc comment).
type ProjectionQueueErrorStat struct {
	ErrorCode        string
	Count            int64
	OldestAgeSeconds float64
}

// ObserveProjectionQueueStats polls stats every interval and republishes it
// as nano_collector_projection_queue_stuck_records{error_code} and
// nano_collector_projection_queue_stuck_oldest_seconds{error_code} until ctx
// is done, so operators can alert on Collector Traces stuck erroring or
// abandoned in the projection queue. Same ticker-poll shape as
// ObservePoolStats, for the same reason: the queue's stuck-row stats are a
// point-in-time snapshot query, not a push-based Prometheus Collector.
func ObserveProjectionQueueStats(ctx context.Context, catalog *Catalog, interval time.Duration, stats func(context.Context) ([]ProjectionQueueErrorStat, error)) {
	if catalog == nil || stats == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	publish := func() {
		current, err := stats(ctx)
		if err != nil {
			return
		}
		catalog.CollectorProjectionQueueStuckRecords.Reset()
		catalog.CollectorProjectionQueueStuckOldestSeconds.Reset()
		for _, stat := range current {
			catalog.CollectorProjectionQueueStuckRecords.WithLabelValues(stat.ErrorCode).Set(float64(stat.Count))
			catalog.CollectorProjectionQueueStuckOldestSeconds.WithLabelValues(stat.ErrorCode).Set(stat.OldestAgeSeconds)
		}
	}
	publish()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}
