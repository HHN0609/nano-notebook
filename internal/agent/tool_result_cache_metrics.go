package agent

import (
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

// ToolResultCacheMetrics is the cache-specific, bounded-label view of the
// process metric catalog. A nil recorder is a safe no-op.
type ToolResultCacheMetrics struct {
	catalog *metrics.Catalog
}

func NewToolResultCacheMetrics(catalog *metrics.Catalog) *ToolResultCacheMetrics {
	if catalog == nil {
		return nil
	}
	return &ToolResultCacheMetrics{catalog: catalog}
}

func (m *ToolResultCacheMetrics) RecordOperation(operation, outcome string, bodyBytes int, latency time.Duration) {
	if m == nil || m.catalog == nil {
		return
	}
	m.catalog.ToolResultCacheOperations.WithLabelValues(operation, outcome).Inc()
	m.catalog.ToolResultCacheDuration.WithLabelValues(operation, outcome).Observe(latency.Seconds())
	if bodyBytes > 0 {
		direction := "stored"
		if operation == "read" {
			direction = "served"
		}
		m.catalog.ToolResultCacheBytes.WithLabelValues(direction).Add(float64(bodyBytes))
	}
}

func (m *ToolResultCacheMetrics) SetRedisEvictedKeys(count float64) {
	if m == nil || m.catalog == nil || count < 0 {
		return
	}
	m.catalog.ToolResultCacheRedisEvictedKeys.Set(count)
}
