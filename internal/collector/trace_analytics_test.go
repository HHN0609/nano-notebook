package collector

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	dto "github.com/prometheus/client_model/go"
)

func TestClickHouseStoreTracksRawSummaryWatermarkGap(t *testing.T) {
	catalog := metrics.NewCatalog(metrics.NewRegistry())
	store := &ClickHouseStore{metrics: catalog}
	base := time.Unix(100, 0).UnixNano()

	store.observeRawWatermark(base + int64(7*time.Second))
	store.observeSummaryWatermark(base + int64(2*time.Second))
	if got := gaugeValue(t, catalog.AgentTraceRawSummaryWatermarkGap); got != 5 {
		t.Fatalf("watermark gap=%v want=5", got)
	}
	store.observeSummaryWatermark(base + int64(7*time.Second))
	if got := gaugeValue(t, catalog.AgentTraceRawSummaryWatermarkGap); got != 0 {
		t.Fatalf("converged watermark gap=%v want=0", got)
	}
}

func gaugeValue(t *testing.T, gauge interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	value := &dto.Metric{}
	if err := gauge.Write(value); err != nil {
		t.Fatal(err)
	}
	return value.GetGauge().GetValue()
}

func TestNormalizeTraceAnalyticsQueryDefaultsToBoundedUTCWindow(t *testing.T) {
	now := time.Date(2026, 8, 17, 15, 4, 5, 123, time.FixedZone("CST", 8*60*60))

	query, err := NormalizeTraceAnalyticsQuery(now, TraceAnalyticsOverview, TraceAnalyticsQuery{})
	if err != nil {
		t.Fatal(err)
	}

	wantBefore := now.UTC().UnixNano()
	wantAfter := now.UTC().Add(-24 * time.Hour).UnixNano()
	if query.StartedAfterUnixNano != wantAfter || query.StartedBeforeUnixNano != wantBefore {
		t.Fatalf("normalized range = [%d,%d), want [%d,%d)", query.StartedAfterUnixNano, query.StartedBeforeUnixNano, wantAfter, wantBefore)
	}
	if query.Bucket != AnalyticsBucketHour || query.WorkloadKind != WorkloadAgentRun || query.Limit != 10 {
		t.Fatalf("normalized defaults = %#v", query)
	}
}

func TestNormalizeTraceAnalyticsQueryRejectsUnboundedOrExplosiveQueries(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	after31Days := now.Add(-31 * 24 * time.Hour).UnixNano()
	before := now.UnixNano()
	tests := []struct {
		name  string
		kind  TraceAnalyticsQueryKind
		query TraceAnalyticsQuery
	}{
		{name: "more than thirty days", kind: TraceAnalyticsOverview, query: TraceAnalyticsQuery{StartedAfterUnixNano: after31Days, StartedBeforeUnixNano: before}},
		{name: "reverse range", kind: TraceAnalyticsOverview, query: TraceAnalyticsQuery{StartedAfterUnixNano: before, StartedBeforeUnixNano: after31Days}},
		{name: "too many points", kind: TraceAnalyticsTimeseries, query: TraceAnalyticsQuery{StartedAfterUnixNano: now.Add(-30 * 24 * time.Hour).UnixNano(), StartedBeforeUnixNano: before, Bucket: AnalyticsBucketFiveMinutes}},
		{name: "overview group by", kind: TraceAnalyticsOverview, query: TraceAnalyticsQuery{GroupBy: AnalyticsDimensionAgent}},
		{name: "latency invalid group by", kind: TraceAnalyticsLatency, query: TraceAnalyticsQuery{GroupBy: AnalyticsDimensionErrorCode}},
		{name: "breakdown invalid group by", kind: TraceAnalyticsBreakdowns, query: TraceAnalyticsQuery{GroupBy: AnalyticsDimensionTool}},
		{name: "tools invalid group by", kind: TraceAnalyticsTools, query: TraceAnalyticsQuery{GroupBy: AnalyticsDimensionModel}},
		{name: "oversized top n", kind: TraceAnalyticsBreakdowns, query: TraceAnalyticsQuery{GroupBy: AnalyticsDimensionAgent, Limit: 51}},
		{name: "unknown workload", kind: TraceAnalyticsOverview, query: TraceAnalyticsQuery{WorkloadKind: WorkloadKind("unknown")}},
		{name: "oversized authorization scope", kind: TraceAnalyticsOverview, query: TraceAnalyticsQuery{NotebookIDs: make([]string, 501)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeTraceAnalyticsQuery(now, tt.kind, tt.query); err == nil {
				t.Fatalf("NormalizeTraceAnalyticsQuery(%#v) succeeded", tt.query)
			}
		})
	}
}

func TestNormalizeTraceAnalyticsQueryAcceptsOnlyEndpointDimensions(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	valid := []struct {
		kind      TraceAnalyticsQueryKind
		dimension AnalyticsDimension
	}{
		{TraceAnalyticsLatency, AnalyticsDimensionAgent},
		{TraceAnalyticsLatency, AnalyticsDimensionModel},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionAgent},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionModel},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionStatus},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionErrorCode},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionProvider},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionStopReason},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionDefinition},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionPrompt},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionConfiguration},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionDelegationTarget},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionDelegationOutcome},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionRAGStage},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionRAGDegradation},
		{TraceAnalyticsBreakdowns, AnalyticsDimensionCitationOutcome},
		{TraceAnalyticsTools, AnalyticsDimensionTool},
		{TraceAnalyticsTools, AnalyticsDimensionErrorCode},
	}
	for _, tt := range valid {
		if _, err := NormalizeTraceAnalyticsQuery(now, tt.kind, TraceAnalyticsQuery{GroupBy: tt.dimension}); err != nil {
			t.Fatalf("kind %q dimension %q rejected: %v", tt.kind, tt.dimension, err)
		}
	}
}
