package collector_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs/semconv"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

func TestClickHouseStorePersistsAndLoadsTrace(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("clickhouse-%d", time.Now().UnixNano())
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
		Topic: "nano.observability.agent-trace.v1", Partition: 3, Offset: 41,
	})
	result, err := ingestor.Ingest(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Chunks[0]; got.Status != collector.ChunkCommitted || got.CommittedThrough != 2 {
		t.Fatalf("chunk result=%#v", got)
	}
	stored, err := store.LoadTrace(ctx, batch.Chunks[0].Trace.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	expectedTrace, err := collector.CanonicalTraceDescriptor(batch.Chunks[0].Trace)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Trace != expectedTrace || stored.CommittedThrough != 2 || len(stored.Records) != 2 {
		t.Fatalf("stored Trace=%#v", stored)
	}
	for index, want := range batch.Chunks[0].Records {
		got := stored.Records[index]
		if got.Sequence != want.Sequence || got.CanonicalSHA256 != want.CanonicalSHA256 ||
			got.Record.IdentityKey != want.Record.IdentityKey || !got.Record.OccurredAt.Equal(want.Record.OccurredAt) {
			t.Fatalf("stored record %d=%#v want=%#v", index, got, want)
		}
	}
	expectedProjection, err := collector.BuildTraceProjection(stored)
	if err != nil {
		t.Fatal(err)
	}
	var projectedSequence uint32
	var projectedStatus string
	var active bool
	if err := connection.QueryRow(ctx, `
		SELECT projected_sequence, status, active
		FROM obs_trace_summaries FINAL WHERE trace_id = ?
	`, string(batch.Chunks[0].Trace.TraceID)).Scan(&projectedSequence, &projectedStatus, &active); err != nil {
		t.Fatal(err)
	}
	if projectedSequence != 2 || projectedStatus != string(expectedProjection.Summary.Status) || active != expectedProjection.Summary.Active {
		t.Fatalf("projected sequence=%d status=%q active=%v", projectedSequence, projectedStatus, active)
	}
}

func TestClickHouseStoreReconcilesReplayAndRejectsCanonicalConflict(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("clickhouse-replay-%d", time.Now().UnixNano())
	batch := collectorBatchFor(t, suffix)
	occurredAt := time.Now().UTC()
	for index := range batch.Chunks[0].Records {
		batch.Chunks[0].Records[index].Record.OccurredAt = occurredAt.Add(time.Duration(index) * time.Nanosecond)
		batch.Chunks[0].Records[index] = collectorEnvelope(t, index+1, batch.Chunks[0].Records[index].Record)
	}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	firstContext := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 4, Offset: 52,
	})
	if _, err := ingestor.Ingest(firstContext, batch); err != nil {
		t.Fatal(err)
	}
	replayed, err := ingestor.Ingest(firstContext, batch)
	if err != nil {
		t.Fatal(err)
	}
	if got := replayed.Chunks[0]; got.Status != collector.ChunkCommitted || got.CommittedThrough != 2 {
		t.Fatalf("replay result=%#v", got)
	}

	conflict := collectorBatchFor(t, suffix)
	for index := range conflict.Chunks[0].Records {
		conflict.Chunks[0].Records[index].Record.OccurredAt = occurredAt.Add(time.Duration(index) * time.Nanosecond)
	}
	conflict.Chunks[0].Records[1].Record.Name = "nano.run.changed"
	for index := range conflict.Chunks[0].Records {
		conflict.Chunks[0].Records[index] = collectorEnvelope(t, index+1, conflict.Chunks[0].Records[index].Record)
	}
	conflictContext := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 4, Offset: 53,
	})
	rejected, err := ingestor.Ingest(conflictContext, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if got := rejected.Chunks[0]; got.Status != collector.ChunkRejected || got.Code != collector.CodeIdentityConflict || got.CommittedThrough != 2 {
		t.Fatalf("conflict result=%#v", got)
	}

	var physicalRows uint64
	if err := connection.QueryRow(ctx, "SELECT count() FROM obs_trace_records_raw WHERE trace_id = ?", string(batch.Chunks[0].Trace.TraceID)).Scan(&physicalRows); err != nil {
		t.Fatal(err)
	}
	if physicalRows != 2 {
		t.Fatalf("physical rows=%d want=2", physicalRows)
	}
}

func TestClickHouseStoreWritesTypedTraceAndSpanAnalytics(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("typed-analytics-%d", time.Now().UnixNano())
	traceID := agentobs.TraceID("trace-" + suffix)
	rootID, modelID, toolID := agentobs.SpanID("root-"+suffix), agentobs.SpanID("model-"+suffix), agentobs.SpanID("tool-"+suffix)
	base := time.Now().UTC()
	record := func(sequence int, kind agentobs.RecordKind, spanID agentobs.SpanID, name string, offset time.Duration, attributes ...agentobs.Attribute) agentobs.Record {
		item := collectorRecord(traceID, spanID, fmt.Sprintf("typed/%d", sequence), kind, name)
		item.OccurredAt, item.Attributes = base.Add(offset), attributes
		return item
	}
	rootStart := record(1, agentobs.RecordSpanStarted, rootID, semconv.AgentExecution, 0,
		agentobs.String("nano.agent.definition", "chat.leader@1"), agentobs.String("nano.run.prompt_version", "prompt@4"),
		agentobs.String("nano.configuration_set.id", "config@2"))
	modelStart := record(2, agentobs.RecordSpanStarted, modelID, semconv.ModelCall, time.Millisecond,
		agentobs.String(semconv.ModelNameKey, "requested-model"))
	modelStart.ParentSpanID = rootID
	modelEnd := record(3, agentobs.RecordSpanEnded, modelID, semconv.ModelCall, 2*time.Millisecond,
		agentobs.String(semconv.ModelProviderKey, "aliyun"), agentobs.String(semconv.ModelNameKey, "selected-model"),
		agentobs.Int64(semconv.TokenCachedKey, 7), agentobs.Int64(semconv.TokenReasoningKey, 3))
	modelEnd.Status = agentobs.StatusOK
	toolStart := record(4, agentobs.RecordSpanStarted, toolID, semconv.AgentAction, 3*time.Millisecond,
		agentobs.String(semconv.ActionNameKey, "current_time"))
	toolStart.ParentSpanID = rootID
	toolEnd := record(5, agentobs.RecordSpanEnded, toolID, semconv.AgentAction, 5*time.Millisecond,
		agentobs.String(semconv.ActionNameKey, "current_time"), agentobs.String(semconv.OperationStatusKey, "domain_error"),
		agentobs.String(semconv.ErrorKindKey, "invalid_time_zone"))
	toolEnd.Status = agentobs.StatusError
	rootEnd := record(6, agentobs.RecordSpanEnded, rootID, semconv.AgentExecution, 6*time.Millisecond,
		agentobs.String("nano.run.status", "failed"), agentobs.String("nano.error.code", "action_budget_exhausted"))
	rootEnd.Status = agentobs.StatusError
	batch := collector.Batch{ProtocolVersion: collector.ProtocolVersion, BatchID: "batch-" + suffix, ProducerID: "nano-worker", CreatedAt: base,
		Chunks: []collector.TraceChunk{{Trace: collector.TraceDescriptor{
			TraceID: traceID, RunID: "run-" + suffix, ChatID: "chat-" + suffix, NotebookID: "notebook-" + suffix,
			RootSpanID: rootID, AgentName: "agent-a", SchemaVersion: 1, SemanticConventionVersion: 1,
		}, FirstSequence: 1, Records: []collector.SequencedRecord{
			collectorEnvelope(t, 1, rootStart), collectorEnvelope(t, 2, modelStart), collectorEnvelope(t, 3, modelEnd),
			collectorEnvelope(t, 4, toolStart), collectorEnvelope(t, 5, toolEnd), collectorEnvelope(t, 6, rootEnd),
		}}}}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx = collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{Topic: "nano.observability.agent-trace.v1", Partition: 8, Offset: time.Now().UnixNano()})
	if _, err := ingestor.Ingest(ctx, batch); err != nil {
		t.Fatal(err)
	}
	var providers, delegationTargets, ragDegradations []string
	var cached, reasoning *int64
	var errorCode, stopReason, definition, prompt, configuration string
	if err := connection.QueryRow(ctx, `
		SELECT providers, cached_tokens, reasoning_tokens, error_code, stop_reason, agent_definition,
			prompt_version, configuration_version, delegation_targets, rag_degradations
		FROM obs_trace_summaries FINAL WHERE trace_id = ?
	`, string(traceID)).Scan(&providers, &cached, &reasoning, &errorCode, &stopReason, &definition, &prompt, &configuration, &delegationTargets, &ragDegradations); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0] != "aliyun" || cached == nil || *cached != 7 || reasoning == nil || *reasoning != 3 ||
		errorCode != "action_budget_exhausted" || stopReason != "action_budget_exhausted" || definition != "chat.leader@1" ||
		prompt != "prompt@4" || configuration != "config@2" || len(delegationTargets) != 0 || len(ragDegradations) != 0 {
		t.Fatalf("typed summary providers=%v cached=%v reasoning=%v error=%q stop=%q definition=%q prompt=%q config=%q", providers, cached, reasoning, errorCode, stopReason, definition, prompt, configuration)
	}
	rows, err := connection.Query(ctx, `
		SELECT span_kind, tool_name, provider, cached_tokens, reasoning_tokens, error_code, outcome
		FROM obs_span_analytics FINAL WHERE trace_id = ? ORDER BY span_kind, span_id
	`, string(traceID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type typedSpan struct {
		kind, tool, provider, errorCode, outcome string
		cached, reasoning                        *int64
	}
	var spans []typedSpan
	for rows.Next() {
		var span typedSpan
		if err := rows.Scan(&span.kind, &span.tool, &span.provider, &span.cached, &span.reasoning, &span.errorCode, &span.outcome); err != nil {
			t.Fatal(err)
		}
		spans = append(spans, span)
	}
	if len(spans) != 2 || spans[0].kind != "model" || spans[0].provider != "aliyun" || spans[1].kind != "tool" ||
		spans[1].tool != "current_time" || spans[1].errorCode != "invalid_time_zone" || spans[1].outcome != "domain_error" {
		t.Fatalf("typed spans=%#v", spans)
	}
}

func TestClickHouseAnalyticsConvergesFromActiveToTerminalAcrossDuplicateDelivery(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("analytics-convergence-%d", time.Now().UnixNano())
	batch := collectorBatchFor(t, suffix)
	base := time.Now().UTC()
	rootEnd := collectorRecord(batch.Chunks[0].Trace.TraceID, batch.Chunks[0].Trace.RootSpanID, "run/"+batch.Chunks[0].Trace.RunID+"/root/end", agentobs.RecordSpanEnded, semconv.AgentExecution)
	rootEnd.Status = agentobs.StatusOK
	batch.Chunks[0].Records[1].Record = rootEnd
	for index := range batch.Chunks[0].Records {
		batch.Chunks[0].Records[index].Record.OccurredAt = base.Add(time.Duration(index) * time.Millisecond)
		batch.Chunks[0].Records[index] = collectorEnvelope(t, index+1, batch.Chunks[0].Records[index].Record)
	}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	firstChunk := batch.Chunks[0]
	firstChunk.Records = append([]collector.SequencedRecord(nil), batch.Chunks[0].Records[:1]...)
	first := collector.Batch{ProtocolVersion: batch.ProtocolVersion, BatchID: "active-" + suffix, ProducerID: batch.ProducerID, CreatedAt: batch.CreatedAt, Chunks: []collector.TraceChunk{firstChunk}}
	firstCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{Topic: "nano.observability.agent-trace.v1", Partition: 9, Offset: time.Now().UnixNano()})
	if _, err := ingestor.Ingest(firstCtx, first); err != nil {
		t.Fatal(err)
	}
	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	query := collector.TraceAnalyticsQuery{
		StartedAfterUnixNano: base.Add(-time.Minute).UnixNano(), StartedBeforeUnixNano: base.Add(time.Minute).UnixNano(),
		NotebookIDs: []string{batch.Chunks[0].Trace.NotebookID},
	}
	active, err := analytics.Overview(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if active.Data.RunCount != 1 || active.Data.CompletedCount != 0 || active.Data.SuccessRate != nil || active.Data.P95DurationNanoseconds != nil {
		t.Fatalf("active overview=%#v", active.Data)
	}

	terminalChunk := batch.Chunks[0]
	terminalChunk.FirstSequence = 2
	terminalChunk.Records = append([]collector.SequencedRecord(nil), batch.Chunks[0].Records[1:]...)
	terminal := collector.Batch{ProtocolVersion: batch.ProtocolVersion, BatchID: "terminal-" + suffix, ProducerID: batch.ProducerID, CreatedAt: batch.CreatedAt, Chunks: []collector.TraceChunk{terminalChunk}}
	terminalCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{Topic: "nano.observability.agent-trace.v1", Partition: 9, Offset: time.Now().UnixNano()})
	if _, err := ingestor.Ingest(terminalCtx, terminal); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.Ingest(terminalCtx, terminal); err != nil {
		t.Fatal(err)
	}
	completed, err := analytics.Overview(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Data.RunCount != 1 || completed.Data.CompletedCount != 1 || completed.Data.SuccessRate == nil || *completed.Data.SuccessRate != 1 {
		t.Fatalf("terminal overview after duplicate=%#v", completed.Data)
	}
}

func openClickHouseTestConnection(t *testing.T, ctx context.Context) driver.Conn {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("NANO_TEST_CLICKHOUSE_ADDR"))
	if address == "" {
		t.Skip("NANO_TEST_CLICKHOUSE_ADDR is not set")
	}
	connection, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{address},
		Auth: clickhouse.Auth{
			Database: "nano_observability",
			Username: "nano_observability",
			Password: "nano-observability",
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Ping(ctx); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}
