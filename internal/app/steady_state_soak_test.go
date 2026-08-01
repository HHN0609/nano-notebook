package app

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestRunHubSteadyStateAfterSoak exercises the leak surface docs/sprint/SPRINT-12-PRD.md
// section 4.6 names directly — internal/app/run_hub.go's SSE subscriber map
// — at the volume PRD criterion 60 asks for (at least 200 cycles), then
// asserts the PRD criterion 59 steady-state invariant: with no connected
// clients, nano_runhub_subscribers and nano_runhub_runs_tracked read zero
// and the goroutine count returns within 10% of baseline.
//
// This is the run-hub half of the soak requirement. It does not admit 200
// live Chat Tasks end-to-end (that needs a running Bifrost/model dependency
// unsuited to this test binary); docs/sprint/SPRINT-12-ACCEPTANCE.md records
// that scope reduction explicitly rather than silently narrowing it.
func TestRunHubSteadyStateAfterSoak(t *testing.T) {
	reg := metrics.NewRegistry()
	catalog := metrics.NewCatalog(reg)
	hub := newRunHubWithMetrics(catalog)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	const cycles = 200
	var wg sync.WaitGroup
	for i := 0; i < cycles; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			runID := "run_" + strconv.Itoa(n)
			_, unsubscribe := hub.subscribe(runID)
			hub.notify(runID)
			unsubscribe()
		}(i)
	}
	wg.Wait()

	debug.FreeOSMemory()
	runtime.GC()

	assertGaugeZero(t, catalog.RunHubSubscribers, "nano_runhub_subscribers")
	assertGaugeZero(t, catalog.RunHubRunsTracked, "nano_runhub_runs_tracked")

	afterGoroutines := runtime.NumGoroutine()
	tolerance := baseline / 10
	if tolerance < 2 {
		tolerance = 2
	}
	if afterGoroutines > baseline+tolerance {
		t.Errorf("goroutine count did not return to baseline: baseline=%d after=%d tolerance=%d", baseline, afterGoroutines, tolerance)
	}
}

func assertGaugeZero(t *testing.T, gauge prometheus.Gauge, name string) {
	t.Helper()
	metric := &dto.Metric{}
	if err := gauge.Write(metric); err != nil {
		t.Fatalf("%s: write failed: %v", name, err)
	}
	if got := metric.GetGauge().GetValue(); got != 0 {
		t.Errorf("%s: expected steady-state 0 after soak, got %v", name, got)
	}
}
