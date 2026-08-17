package obsbench_test

import (
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestResultValidationProtectsHeadlineMetricContract(t *testing.T) {
	result := obsbench.Result{
		SchemaVersion:   1,
		Stage:           obsbench.StageDirectPostgres,
		WorkloadVersion: "agent-run-reference-v1",
		ManifestSHA256:  strings.Repeat("a", 64),
		StartedAt:       time.Unix(1_700_000_000, 0).UTC(),
		Level: obsbench.LevelResult{
			OfferedRootRunsPerSecond:  20,
			AchievedRootRunsPerSecond: 19.99,
			RecordsPerSecond:          300,
			MiBPerSecond:              1.25,
			AcceptanceLatency:         obsbench.Latency{P50Milliseconds: 10, P95Milliseconds: 20, P99Milliseconds: 30},
			ProductQueryLatency:       obsbench.Latency{P50Milliseconds: 15, P95Milliseconds: 40, P99Milliseconds: 80},
			ScheduledArrivals:         12_000,
			LateArrivals:              0,
			ValidRecords:              180_000,
			LostRecords:               0,
		},
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("valid Result: %v", err)
	}

	invalid := result
	invalid.Level.ProductQueryLatency.P99Milliseconds = 39
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "p50 <= p95 <= p99") {
		t.Fatalf("invalid latency error=%v", err)
	}

	invalid = result
	invalid.Level.LostRecords = 1
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "lost records") {
		t.Fatalf("lost record error=%v", err)
	}
}
