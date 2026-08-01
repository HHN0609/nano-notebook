package metrics

import (
	"context"
	"time"
)

// PoolStat is the subset of pgxpool.Stat this package needs, expressed
// without a pgx dependency so this leaf package stays free of it.
type PoolStat struct {
	AcquiredConns int32
	IdleConns     int32
	TotalConns    int32
	MaxConns      int32
}

// ObservePoolStats polls stat every interval and republishes it as
// nano_db_pool_connections{pool,state} (PRD criterion 57) until ctx is
// done. Pull-based scraping isn't available here because pgxpool.Pool
// exposes its stats as a snapshot method, not a Prometheus Collector; a
// small ticker is the standard client_golang pattern for this case.
func ObservePoolStats(ctx context.Context, catalog *Catalog, poolName string, interval time.Duration, stat func() PoolStat) {
	if catalog == nil || stat == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	publish := func() {
		s := stat()
		catalog.DBPoolConnections.WithLabelValues(poolName, "acquired").Set(float64(s.AcquiredConns))
		catalog.DBPoolConnections.WithLabelValues(poolName, "idle").Set(float64(s.IdleConns))
		catalog.DBPoolConnections.WithLabelValues(poolName, "total").Set(float64(s.TotalConns))
		catalog.DBPoolConnections.WithLabelValues(poolName, "max").Set(float64(s.MaxConns))
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
