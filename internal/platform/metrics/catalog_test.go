package metrics

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestCatalogDoesNotRegisterReservedUnimplementedMetrics(t *testing.T) {
	reg := NewRegistry()
	NewCatalog(reg)
	mfs, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	registered := map[string]bool{}
	for _, mf := range mfs {
		registered[mf.GetName()] = true
	}
	for _, name := range ReservedUnimplementedMetrics {
		if registered[name] {
			t.Errorf("%q is reserved (PRD criterion 34) and must not be registered until streaming model support exists", name)
		}
	}
}

func TestCatalogEveryMetricNameIsValid(t *testing.T) {
	reg := NewRegistry()
	NewCatalog(reg)
	mfs, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	for _, mf := range mfs {
		if err := ValidateMetricName(mf.GetName()); err != nil {
			t.Errorf("metric %q fails naming convention: %v", mf.GetName(), err)
		}
	}
}

func TestTraceAnalyticsPipelineMetricsUseOnlyBoundedOperationalLabels(t *testing.T) {
	reg := NewRegistry()
	catalog := NewCatalog(reg)
	catalog.AgentTraceProcessorMessages.WithLabelValues("persisted").Inc()
	catalog.AgentTraceProcessorBatchRecords.Observe(1)
	catalog.AgentTraceProcessorBatchBytes.Observe(1)
	catalog.AgentTraceProcessorDuration.WithLabelValues("persisted").Observe(0.01)
	catalog.AgentTraceOffsetCommitFailures.Inc()
	catalog.AgentTraceConsumerRebalances.WithLabelValues("assigned").Inc()
	catalog.AgentTraceConsumerLag.WithLabelValues("0").Set(1)
	catalog.AgentTraceOldestMessageAge.WithLabelValues("0").Set(1)
	catalog.AgentTraceSearchableFreshness.Set(1)
	catalog.AgentTraceRawSummaryWatermarkGap.Set(1)
	catalog.ClickHouseRequests.WithLabelValues("analytics_overview", "success").Inc()
	catalog.ClickHouseRequestDuration.WithLabelValues("analytics_overview", "success").Observe(0.01)
	want := map[string][]string{
		"nano_agent_trace_processor_messages_total":          {"result"},
		"nano_agent_trace_processor_batch_records":           nil,
		"nano_agent_trace_processor_batch_bytes":             nil,
		"nano_agent_trace_processor_duration_seconds":        {"result"},
		"nano_agent_trace_offset_commit_failures_total":      nil,
		"nano_agent_trace_consumer_rebalances_total":         {"event"},
		"nano_agent_trace_consumer_lag_records":              {"partition"},
		"nano_agent_trace_oldest_message_age_seconds":        {"partition"},
		"nano_agent_trace_searchable_freshness_seconds":      nil,
		"nano_agent_trace_raw_summary_watermark_gap_seconds": nil,
		"nano_clickhouse_requests_total":                     {"operation", "outcome"},
		"nano_clickhouse_request_duration_seconds":           {"operation", "outcome"},
	}
	families, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string][]string)
	for _, family := range families {
		if len(family.Metric) == 0 {
			continue
		}
		actual[family.GetName()] = []string{}
		for _, pair := range family.Metric[0].Label {
			actual[family.GetName()] = append(actual[family.GetName()], pair.GetName())
		}
	}
	for name, labels := range want {
		got, ok := actual[name]
		if !ok {
			t.Fatalf("metric %q is not registered; gathered=%v", name, actual)
		}
		if len(got) != len(labels) {
			t.Fatalf("metric %q labels=%v want=%v", name, got, labels)
		}
		for _, forbidden := range []string{"trace_id", "run_id", "chat_id", "user_id", "notebook_id", "error"} {
			for _, label := range got {
				if label == forbidden {
					t.Fatalf("metric %q exposes high-cardinality label %q", name, label)
				}
			}
		}
	}
}

// TestCatalogCardinalityBudget materializes one series per combination in
// each metric's declared label domain (allowlists.go) and asserts the
// total exposed series stays under the 15,000-per-instance budget from PRD
// criterion 15. Histogram series are counted the way a Prometheus scrape
// actually counts them: one series per configured bucket, plus +Inf, plus
// _sum and _count.
func TestCatalogCardinalityBudget(t *testing.T) {
	const budget = 15_000

	reg := NewRegistry()
	c := NewCatalog(reg)

	other := func(values []string) []string { return append(append([]string{}, values...), OtherLabel) }

	for _, outcome := range other(OutcomeValues) {
		for _, variant := range other(AgentRunVariantValues) {
			c.TaskTerminal.WithLabelValues("agent_run", variant, outcome).Inc()
		}
		for _, variant := range other(StudioOutputVariantValues) {
			c.TaskTerminal.WithLabelValues("studio_output", variant, outcome).Inc()
		}
		for _, variant := range other(SourceProcessingVariantValues) {
			c.TaskTerminal.WithLabelValues("source_processing", variant, outcome).Inc()
		}
	}
	for _, variant := range other(AgentRunVariantValues) {
		for _, disposition := range AttemptDispositionValues {
			c.AgentAttempt.WithLabelValues(variant, disposition).Inc()
		}
		c.AgentRunAttempts.WithLabelValues(variant).Observe(1)
	}
	for _, degradation := range other(RetrievalDegradationValues) {
		c.AgentRunDegraded.WithLabelValues(degradation).Inc()
		c.RetrievalDegraded.WithLabelValues(degradation).Inc()
	}
	for _, kind := range TaskKindValues {
		for _, variant := range other(AgentRunVariantValues) {
			c.TaskQueueWait.WithLabelValues(kind, variant).Observe(1)
			for _, outcome := range other(OutcomeValues) {
				c.TaskEndToEnd.WithLabelValues(kind, variant, outcome).Observe(1)
			}
		}
	}
	for _, variant := range other(AgentRunVariantValues) {
		c.ChatFirstProgress.WithLabelValues(variant).Observe(1)
	}
	for _, outcome := range []string{"completed", "failed"} {
		c.ModelCall.WithLabelValues("model", "final_draft", outcome).Observe(1)
		c.RetrievalSearch.WithLabelValues(outcome).Observe(1)
		for _, stage := range RetrievalStageValues {
			c.RetrievalStage.WithLabelValues(stage, outcome).Observe(1)
		}
	}
	c.ToolExecution.WithLabelValues("search_evidence", "completed").Observe(1)
	for _, kind := range TaskKindValues {
		for _, layer := range ErrorLayerValues {
			for _, code := range other(ErrorCodeValues) {
				c.ErrorTotal.WithLabelValues(kind, layer, code).Inc()
			}
		}
	}
	c.HTTPRequests.WithLabelValues("/api/v1/notebooks", "GET", "200").Inc()
	c.HTTPRequestDuration.WithLabelValues("/api/v1/notebooks", "GET", "200").Observe(1)
	for _, stream := range SSEStreamValues {
		for _, reason := range SSECloseReasonValues {
			c.SSEConnectionDuration.WithLabelValues(stream, reason).Observe(1)
		}
		c.SSEEventsSent.WithLabelValues(stream, "run").Inc()
		c.SSEConnectionsActive.WithLabelValues(stream).Set(1)
	}
	c.DBPoolConnections.WithLabelValues("control_plane", "acquired").Set(1)

	mfs, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	total := 0
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			total += seriesPerMetric(mf.GetType(), m)
		}
	}
	if total >= budget {
		t.Fatalf("catalog exceeds cardinality budget: %d series (budget %d)", total, budget)
	}
	t.Logf("catalog materializes %d series against a %d budget", total, budget)
}

func seriesPerMetric(kind dto.MetricType, m *dto.Metric) int {
	if kind != dto.MetricType_HISTOGRAM {
		return 1
	}
	// buckets + implicit +Inf bucket + _sum + _count, matching how a
	// Prometheus scrape counts a histogram's exposed series.
	return len(m.GetHistogram().GetBucket()) + 3
}
