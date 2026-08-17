package collector_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

func TestClickHouseTraceQueriesListAndRebuildDetailFromRawFacts(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("clickhouse-query-%d", time.Now().UnixNano())
	batch := collectorBatchFor(t, suffix)
	for index := range batch.Chunks[0].Records {
		batch.Chunks[0].Records[index].Record.OccurredAt = time.Now().UTC().Add(time.Duration(index) * time.Nanosecond)
		batch.Chunks[0].Records[index] = collectorEnvelope(t, index+1, batch.Chunks[0].Records[index].Record)
	}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx = collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 6, Offset: time.Now().UnixNano(),
	})
	if _, err := ingestor.Ingest(ctx, batch); err != nil {
		t.Fatal(err)
	}
	queries, err := collector.NewClickHouseTraceQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := queries.List(ctx, collector.TraceListQuery{IdentityExact: string(batch.Chunks[0].Trace.TraceID), PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Summary.TraceID != batch.Chunks[0].Trace.TraceID ||
		listed.Items[0].CommittedThrough != 2 || listed.Items[0].ProjectionLagged {
		t.Fatalf("listed=%#v", listed)
	}
	detail, err := queries.Detail(ctx, batch.Chunks[0].Trace.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.CommittedThrough != 2 || detail.ProjectedThrough != 2 || detail.Projection.Summary.TraceID != batch.Chunks[0].Trace.TraceID {
		t.Fatalf("detail=%#v", detail)
	}
}

func TestClickHouseTraceAnalyticsOverviewUsesTerminalDenominatorsAndCoverage(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("analytics-overview-%d", time.Now().UnixNano())
	notebookID := "notebook-" + suffix
	base := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "ok-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-a",
		StartedAt: base, LastObserved: base.Add(100 * time.Millisecond), Status: "ok", Duration: int64Pointer(100_000_000),
		Attempts: 1, InputTokens: int64Pointer(20), OutputTokens: int64Pointer(10), TotalTokens: int64Pointer(30),
		CostKnown: true, CostAmount: float64Pointer(0.2), CostCurrency: "USD",
	})
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "error-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-b",
		StartedAt: base.Add(time.Minute), LastObserved: base.Add(time.Minute + 300*time.Millisecond), Status: "error", Duration: int64Pointer(300_000_000), Attempts: 2,
	})
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "active-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-a",
		StartedAt: base.Add(2 * time.Minute), LastObserved: base.Add(2*time.Minute + 50*time.Millisecond), Active: true, Attempts: 1,
	})

	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	result, err := analytics.Overview(ctx, collector.TraceAnalyticsQuery{
		StartedAfterUnixNano: base.Add(-time.Minute).UnixNano(), StartedBeforeUnixNano: base.Add(time.Hour).UnixNano(),
		WorkloadKind: collector.WorkloadAgentRun, NotebookIDs: []string{notebookID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.RunCount != 3 || result.Data.CompletedCount != 2 || result.Data.SuccessRate == nil || *result.Data.SuccessRate != 0.5 ||
		result.Data.ErrorRate == nil || *result.Data.ErrorRate != 0.5 || result.Data.RetryRate == nil || *result.Data.RetryRate != 0.5 {
		t.Fatalf("overview rates = %#v", result.Data)
	}
	if result.Data.P95DurationNanoseconds == nil || *result.Data.P95DurationNanoseconds != 300_000_000 {
		t.Fatalf("overview p95 = %#v", result.Data.P95DurationNanoseconds)
	}
	if result.Data.TotalTokens == nil || *result.Data.TotalTokens != 30 || result.Coverage.TokenSamples != 1 || result.Coverage.TotalSamples != 3 {
		t.Fatalf("overview token data=%#v coverage=%#v", result.Data, result.Coverage)
	}
	if result.Coverage.CostSamples != 1 || len(result.Data.Costs) != 1 || result.Data.Costs[0].Currency != "USD" || result.Data.Costs[0].Amount != 0.2 {
		t.Fatalf("overview costs=%#v coverage=%#v", result.Data.Costs, result.Coverage)
	}
	if result.SchemaVersion != collector.TraceAnalyticsSchemaVersion || result.GeneratedAt.IsZero() || result.FreshThrough == nil ||
		!result.FreshThrough.Equal(base.Add(2*time.Minute+50*time.Millisecond)) {
		t.Fatalf("overview metadata = %#v", result.TraceAnalyticsMetadata)
	}
}

func TestClickHouseTraceAnalyticsTimeseriesAndLatencyKeepActiveRunsOutOfPercentiles(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("analytics-series-%d", time.Now().UnixNano())
	notebookID := "notebook-" + suffix
	base := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "ok-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-a",
		StartedAt: base.Add(5 * time.Minute), LastObserved: base.Add(5*time.Minute + 100*time.Millisecond), Status: "ok", Duration: int64Pointer(100_000_000),
		Attempts: 1, TotalTokens: int64Pointer(30),
	})
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "error-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-b",
		StartedAt: base.Add(65 * time.Minute), LastObserved: base.Add(65*time.Minute + 300*time.Millisecond), Status: "error", Duration: int64Pointer(300_000_000), Attempts: 2,
	})
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "active-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-a",
		StartedAt: base.Add(70 * time.Minute), LastObserved: base.Add(70*time.Minute + 50*time.Millisecond), Active: true, Attempts: 1,
	})
	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	query := collector.TraceAnalyticsQuery{
		StartedAfterUnixNano: base.UnixNano(), StartedBeforeUnixNano: base.Add(3 * time.Hour).UnixNano(),
		Bucket: collector.AnalyticsBucketHour, NotebookIDs: []string{notebookID},
	}
	series, err := analytics.Timeseries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Data) != 2 || series.Data[0].RunCount != 1 || series.Data[0].P95DurationNanoseconds == nil ||
		*series.Data[0].P95DurationNanoseconds != 100_000_000 || series.Data[1].RunCount != 2 || series.Data[1].CompletedCount != 1 ||
		series.Data[1].ActiveCount != 1 || series.Data[1].P95DurationNanoseconds == nil || *series.Data[1].P95DurationNanoseconds != 300_000_000 {
		t.Fatalf("timeseries = %#v", series.Data)
	}
	latencyQuery := query
	latencyQuery.GroupBy = collector.AnalyticsDimensionModel
	latency, err := analytics.Latency(ctx, latencyQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(latency.Data) != 2 || latency.Data[0].Value != "model-a" || latency.Data[0].SampleCount != 1 ||
		latency.Data[1].Value != "model-b" || latency.Data[1].SampleCount != 1 ||
		latency.Data[1].P99DurationNanoseconds != 300_000_000 {
		t.Fatalf("latency = %#v", latency.Data)
	}
}

func TestClickHouseTraceAnalyticsBreakdownsAndToolsUseTypedDimensions(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("analytics-breakdown-%d", time.Now().UnixNano())
	notebookID := "notebook-" + suffix
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "ok-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-a", Provider: "aliyun",
		StartedAt: base, LastObserved: base.Add(time.Second), Status: "ok", Duration: int64Pointer(100_000_000), Attempts: 1,
		Definition: "chat.leader@1", Prompt: "prompt@4", Config: "config@2",
	})
	seedClickHouseAnalyticsSummary(t, ctx, connection, analyticsSummarySeed{
		TraceID: "error-" + suffix, NotebookID: notebookID, Agent: "agent-a", Model: "model-b", Provider: "openai",
		StartedAt: base.Add(time.Minute), LastObserved: base.Add(time.Minute + time.Second), Status: "error", Duration: int64Pointer(300_000_000), Attempts: 2,
		ErrorCode: "tool_execution_failed", StopReason: "tool_execution_failed", Definition: "chat.leader@1", Prompt: "prompt@4", Config: "config@2",
	})
	seedClickHouseSpanAnalytics(t, ctx, connection, spanAnalyticsSeed{TraceID: "ok-" + suffix, NotebookID: notebookID, Agent: "agent-a", StartedAt: base, SpanID: "tool-ok", Tool: "current_time", Status: "ok", Outcome: "succeeded", Duration: 100_000_000})
	seedClickHouseSpanAnalytics(t, ctx, connection, spanAnalyticsSeed{TraceID: "error-" + suffix, NotebookID: notebookID, Agent: "agent-a", StartedAt: base.Add(time.Minute), SpanID: "tool-error", Tool: "current_time", Status: "error", Outcome: "domain_error", ErrorCode: "invalid_time_zone", Duration: 300_000_000})
	seedClickHouseSpanAnalytics(t, ctx, connection, spanAnalyticsSeed{TraceID: "ok-" + suffix, NotebookID: notebookID, Agent: "agent-a", StartedAt: base, SpanID: "tool-search", Tool: "search_evidence", Status: "ok", Outcome: "succeeded", Duration: 200_000_000})
	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	query := collector.TraceAnalyticsQuery{StartedAfterUnixNano: base.Add(-time.Minute).UnixNano(), StartedBeforeUnixNano: base.Add(time.Hour).UnixNano(), NotebookIDs: []string{notebookID}, Limit: 10}
	query.GroupBy = collector.AnalyticsDimensionErrorCode
	breakdown, err := analytics.Breakdowns(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown.Data) != 2 || breakdown.Data[0].Value != "tool_execution_failed" || breakdown.Data[0].RunCount != 1 ||
		breakdown.Data[1].Value != "unknown" || breakdown.Data[1].RunCount != 1 {
		t.Fatalf("error breakdown=%#v", breakdown.Data)
	}
	query.GroupBy = collector.AnalyticsDimensionTool
	tools, err := analytics.Tools(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Data) != 2 || tools.Data[0].Value != "current_time" || tools.Data[0].CallCount != 2 ||
		tools.Data[0].SuccessRate == nil || *tools.Data[0].SuccessRate != 0.5 || tools.Data[0].P95DurationNanoseconds == nil ||
		*tools.Data[0].P95DurationNanoseconds != 300_000_000 || tools.Data[1].Value != "search_evidence" || tools.Data[1].CallCount != 1 {
		t.Fatalf("tool analytics=%#v", tools.Data)
	}
}

func TestClickHouseTraceAnalyticsFrozenDatasetMeetsInteractiveP95Targets(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("analytics-performance-%d", time.Now().UnixNano())
	notebookID := "notebook-" + suffix
	base := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := connection.Exec(ctx, `
		INSERT INTO obs_trace_summaries (
			trace_id, workload_kind, workload_id, run_id, chat_id, notebook_id, root_span_id, agent_name,
			started_at_unix_nano, last_observed_unix_nano, status, active, models, duration_nanoseconds,
			attempt_count, total_tokens, cost_known, cost_amount, cost_currency,
			committed_sequence, projected_sequence, ingest_version
		)
		SELECT concat(?, toString(number)), 'agent_run', concat('run-', toString(number)), concat('run-', toString(number)),
			concat('chat-', toString(number)), ?, concat('root-', toString(number)), concat('agent-', toString(number % 8)),
			? + toInt64(number) * ?, ? + toInt64(number) * ? + 100000000,
			if(number % 10 = 0, 'error', 'ok'), false, [concat('model-', toString(number % 4))], toNullable(toInt64(100000000 + number % 10000000)),
			toUInt32(1 + number % 3), toNullable(toInt64(100 + number % 50)), true, toNullable(toFloat64(number % 100) / 1000), if(number % 2 = 0, 'USD', 'CNY'),
			2, 2, 2
		FROM numbers(2000)
	`, "perf-"+suffix+"-", notebookID, base.UnixNano(), int64((30*24*time.Hour)/2000), base.UnixNano(), int64((30*24*time.Hour)/2000)); err != nil {
		t.Fatal(err)
	}
	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cases := []struct {
		name  string
		query collector.TraceAnalyticsQuery
		limit time.Duration
	}{
		{"24h", collector.TraceAnalyticsQuery{StartedAfterUnixNano: now.Add(-24 * time.Hour).UnixNano(), StartedBeforeUnixNano: now.UnixNano(), Bucket: collector.AnalyticsBucketHour, NotebookIDs: []string{notebookID}}, 500 * time.Millisecond},
		{"30d", collector.TraceAnalyticsQuery{StartedAfterUnixNano: now.Add(-30 * 24 * time.Hour).UnixNano(), StartedBeforeUnixNano: now.UnixNano(), Bucket: collector.AnalyticsBucketDay, NotebookIDs: []string{notebookID}}, 2 * time.Second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			latencies := make([]time.Duration, 0, 60)
			for range 20 {
				for _, run := range []func() error{
					func() error { _, err := analytics.Overview(ctx, testCase.query); return err },
					func() error { _, err := analytics.Timeseries(ctx, testCase.query); return err },
					func() error {
						query := testCase.query
						query.GroupBy = collector.AnalyticsDimensionAgent
						_, err := analytics.Latency(ctx, query)
						return err
					},
				} {
					started := time.Now()
					if err := run(); err != nil {
						t.Fatal(err)
					}
					latencies = append(latencies, time.Since(started))
				}
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			p95 := latencies[(len(latencies)*95+99)/100-1]
			if p95 > testCase.limit {
				t.Fatalf("frozen dataset p95=%s limit=%s samples=%d", p95, testCase.limit, len(latencies))
			}
			t.Logf("frozen dataset p95=%s limit=%s samples=%d", p95, testCase.limit, len(latencies))
		})
	}
}

type analyticsSummarySeed struct {
	TraceID, NotebookID, Agent, Model, Status, CostCurrency     string
	Provider, ErrorCode, StopReason, Definition, Prompt, Config string
	StartedAt, LastObserved                                     time.Time
	Active                                                      bool
	Duration, InputTokens, OutputTokens, TotalTokens            *int64
	CachedTokens, ReasoningTokens                               *int64
	Attempts                                                    uint32
	CostKnown                                                   bool
	CostAmount                                                  *float64
}

func seedClickHouseAnalyticsSummary(t *testing.T, ctx context.Context, connection driver.Conn, seed analyticsSummarySeed) {
	t.Helper()
	providers := []string{}
	if seed.Provider != "" {
		providers = append(providers, seed.Provider)
	}
	var endedAt *int64
	if !seed.Active {
		value := seed.LastObserved.UnixNano()
		endedAt = &value
	}
	if err := connection.Exec(ctx, `
		INSERT INTO obs_trace_summaries (
			trace_id, workload_kind, workload_id, run_id, chat_id, notebook_id, root_span_id, agent_name,
			started_at, started_at_unix_nano, last_observed_unix_nano, ended_at_unix_nano, duration_nanoseconds,
			status, active, models, input_tokens, output_tokens, total_tokens, cost_known, cost_amount,
			cost_currency, cost_source, attempt_count, providers, cached_tokens, reasoning_tokens,
			error_code, stop_reason, agent_definition, prompt_version, configuration_version,
			committed_sequence, projected_sequence, ingest_version
		) VALUES (?, 'agent_run', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'gateway', ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 1)
	`, seed.TraceID, "workload-"+seed.TraceID, "run-"+seed.TraceID, "chat-"+seed.TraceID, seed.NotebookID,
		"root-"+seed.TraceID, seed.Agent, seed.StartedAt, seed.StartedAt.UnixNano(), seed.LastObserved.UnixNano(), endedAt,
		seed.Duration, seed.Status, seed.Active, []string{seed.Model}, seed.InputTokens, seed.OutputTokens, seed.TotalTokens,
		seed.CostKnown, seed.CostAmount, seed.CostCurrency, seed.Attempts, providers, seed.CachedTokens, seed.ReasoningTokens,
		seed.ErrorCode, seed.StopReason, seed.Definition, seed.Prompt, seed.Config); err != nil {
		t.Fatal(err)
	}
}

type spanAnalyticsSeed struct {
	TraceID, NotebookID, Agent, SpanID, Tool, Status, Outcome, ErrorCode string
	StartedAt                                                            time.Time
	Duration                                                             int64
}

func seedClickHouseSpanAnalytics(t *testing.T, ctx context.Context, connection driver.Conn, seed spanAnalyticsSeed) {
	t.Helper()
	if err := connection.Exec(ctx, `
		INSERT INTO obs_span_analytics (
			trace_id, notebook_id, agent_name, started_at, span_id, span_kind, name, tool_name,
			status, outcome, duration_nanoseconds, provider, requested_model, selected_model,
			cached_tokens, reasoning_tokens, error_code, retryable, ingest_version
		) VALUES (?, ?, ?, ?, ?, 'tool', 'agent.action', ?, ?, ?, ?, '', '', '', NULL, NULL, ?, NULL, 1)
	`, seed.TraceID, seed.NotebookID, seed.Agent, seed.StartedAt, seed.SpanID, seed.Tool, seed.Status, seed.Outcome, seed.Duration, seed.ErrorCode); err != nil {
		t.Fatal(err)
	}
}

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }
