package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

// TestSteadyStateGaugesStartAtZero verifies the PRD criterion 59 invariant
// at the one point this leaf package can check it directly: a freshly
// constructed Catalog, before any Task, HTTP request, or SSE connection has
// touched it, must already read zero on every leak-detection gauge. A
// service-level soak test (admit Tasks, drain, assert return-to-zero) lives
// in internal/app, since only that layer can admit real Tasks; this test is
// the fast, dependency-free half of the invariant that runs on every build.
func TestSteadyStateGaugesStartAtZero(t *testing.T) {
	reg := NewRegistry()
	catalog := NewCatalog(reg)

	gauges := map[string]interface{ Write(*dto.Metric) error }{
		"nano_runhub_subscribers":             catalog.RunHubSubscribers,
		"nano_runhub_runs_tracked":            catalog.RunHubRunsTracked,
		"nano_http_inflight_requests":         catalog.HTTPInflightRequests,
		"nano_worker_inflight_attempts":       catalog.WorkerInflightAttempts,
		"nano_worker_heartbeat_goroutines":    catalog.WorkerHeartbeatGoroutines,
		"nano_collector_memory_store_records": catalog.CollectorMemoryStoreRecords,
	}
	for name, gauge := range gauges {
		metric := &dto.Metric{}
		if err := gauge.Write(metric); err != nil {
			t.Fatalf("%s: write failed: %v", name, err)
		}
		if got := metric.GetGauge().GetValue(); got != 0 {
			t.Errorf("%s: expected steady-state 0, got %v", name, got)
		}
	}
}
