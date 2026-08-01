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
