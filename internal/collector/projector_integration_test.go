package collector_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

func TestProjectorPersistsDeterministicViewsAndAdvancesWatermark(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	stored := projectionStoredTrace(t, true, true)
	store := collector.NewPostgresStore(pool)
	if _, err := store.CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: stored.Trace, FirstSequence: 1, Records: stored.Records,
	}); err != nil {
		t.Fatalf("CommitTraceChunk: %v", err)
	}

	projector, err := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	projected, err := projector.RunOnce(ctx)
	if err != nil || !projected {
		t.Fatalf("RunOnce projected=%t error=%v", projected, err)
	}
	var projectedSequence, spanCount, eventCount, linkCount, totalTokens int
	var active, costKnown bool
	if err := pool.QueryRow(ctx, `
		select t.projected_sequence,
			(select count(*) from obs_spans where trace_id = t.trace_id),
			(select count(*) from obs_events where trace_id = t.trace_id),
			(select count(*) from obs_links where trace_id = t.trace_id),
			s.active, s.total_tokens, s.cost_known
		from obs_traces t join obs_trace_summaries s using (trace_id)
		where t.trace_id = $1
	`, stored.Trace.TraceID).Scan(&projectedSequence, &spanCount, &eventCount, &linkCount, &active, &totalTokens, &costKnown); err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if projectedSequence != len(stored.Records) || spanCount != 4 || eventCount != 1 || linkCount != 1 || active || totalTokens != 27 || !costKnown {
		t.Fatalf("projection cursor=%d spans=%d events=%d links=%d active=%t tokens=%d costKnown=%t",
			projectedSequence, spanCount, eventCount, linkCount, active, totalTokens, costKnown)
	}

	first, err := collector.LoadProjectedTrace(ctx, pool, stored.Trace.TraceID)
	if err != nil {
		t.Fatalf("LoadProjectedTrace first: %v", err)
	}
	if err := projector.RebuildTrace(ctx, stored.Trace.TraceID); err != nil {
		t.Fatalf("RebuildTrace: %v", err)
	}
	second, err := collector.LoadProjectedTrace(ctx, pool, stored.Trace.TraceID)
	if err != nil {
		t.Fatalf("LoadProjectedTrace second: %v", err)
	}
	if first.CanonicalJSON != second.CanonicalJSON {
		t.Fatalf("projection rebuild changed view\nfirst: %s\nsecond: %s", first.CanonicalJSON, second.CanonicalJSON)
	}
}

func TestProjectorPersistsAndQueriesSourceProcessingWorkloadIdentity(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	traceID := agentobs.TraceID("trace-source-job")
	rootID := agentobs.SpanID("root-source-job")
	started := collectorRecord(traceID, rootID, "source/job-source/attempt-1/root/start", agentobs.RecordSpanStarted, "source.processing")
	ended := collectorRecord(traceID, rootID, "source/job-source/attempt-1/root/end", agentobs.RecordSpanEnded, "source.processing")
	ended.OccurredAt = started.OccurredAt.Add(time.Second)
	ended.Status = agentobs.StatusOK
	descriptor := collector.TraceDescriptor{
		TraceID: traceID, WorkloadKind: collector.WorkloadSourceProcessing, WorkloadID: "job-source/attempt-1",
		NotebookID: "notebook-source", RootSpanID: rootID, AgentName: "nano-source-processor",
		SchemaVersion: 1, SemanticConventionVersion: 1,
	}
	if _, err := collector.NewPostgresStore(pool).CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: descriptor, FirstSequence: 1,
		Records: []collector.SequencedRecord{collectorEnvelope(t, 1, started), collectorEnvelope(t, 2, ended)},
	}); err != nil {
		t.Fatalf("CommitTraceChunk: %v", err)
	}
	projector, _ := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond})
	if projected, err := projector.RunOnce(ctx); err != nil || !projected {
		t.Fatalf("RunOnce projected=%t error=%v", projected, err)
	}
	queries, _ := collector.NewTraceQueryStore(pool, nil)
	result, err := queries.List(ctx, collector.TraceListQuery{IdentityExact: "job-source/attempt-1", PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Summary.WorkloadKind != collector.WorkloadSourceProcessing ||
		result.Items[0].Summary.WorkloadID != "job-source/attempt-1" || result.Items[0].Summary.RunID != "" || result.Items[0].Summary.ChatID != "" {
		t.Fatalf("Source-processing query = %#v", result)
	}
}

func TestProjectorPersistsIncompleteDirectTraceAndConvergesAfterLateRoot(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{
		ProducerIDPrefix: "nano-", Store: collector.NewPostgresStore(pool),
	})
	if err != nil {
		t.Fatalf("NewIngestor: %v", err)
	}
	childBatch := directCollectorBatch(t)
	childBatch.BatchID = "batch-projector-child-first"
	childBatch.ProducerID = "nano-worker/one"
	child := childBatch.Chunks[0].Records[0].Record
	child.IdentityKey = "run/run-1/attempt/1/start"
	child.SpanID = "attempt-projector-late"
	child.ParentSpanID = childBatch.Chunks[0].Trace.RootSpanID
	child.Name = "nano.job.attempt"
	childBatch.Chunks[0].Records = []collector.SequencedRecord{collectorEnvelope(t, 0, child)}
	if result, err := ingestor.Ingest(ctx, childBatch); err != nil || result.Chunks[0].Status != collector.ChunkCommitted {
		t.Fatalf("child Ingest = %#v, %v", result, err)
	}
	projector, _ := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond})
	if projected, err := projector.RunOnce(ctx); err != nil || !projected {
		t.Fatalf("project child projected=%t error=%v", projected, err)
	}
	incomplete, err := collector.LoadProjectedTrace(ctx, pool, childBatch.Chunks[0].Trace.TraceID)
	if err != nil {
		t.Fatalf("LoadProjectedTrace incomplete: %v", err)
	}
	if !incomplete.Projection.Summary.Active || incomplete.ProjectedThrough != 1 || len(incomplete.Projection.Spans) != 1 {
		t.Fatalf("incomplete projection = %#v", incomplete)
	}

	rootBatch := directCollectorBatch(t)
	rootBatch.BatchID = "batch-projector-root-late"
	rootBatch.ProducerID = "nano-control-plane/one"
	rootBatch.Chunks[0].Records = rootBatch.Chunks[0].Records[:1]
	if result, err := ingestor.Ingest(ctx, rootBatch); err != nil || result.Chunks[0].Status != collector.ChunkCommitted || result.Chunks[0].CommittedThrough != 2 {
		t.Fatalf("root Ingest = %#v, %v", result, err)
	}
	if projected, err := projector.RunOnce(ctx); err != nil || !projected {
		t.Fatalf("project root projected=%t error=%v", projected, err)
	}
	converged, err := collector.LoadProjectedTrace(ctx, pool, rootBatch.Chunks[0].Trace.TraceID)
	if err != nil {
		t.Fatalf("LoadProjectedTrace converged: %v", err)
	}
	if converged.ProjectedThrough != 2 || len(converged.Projection.Spans) != 2 {
		t.Fatalf("converged projection = %#v", converged)
	}
}

func TestPostgresStoreSkipsProjectionRequeueOnNoOpResend(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	stored := projectionStoredTrace(t, true, true)
	store := collector.NewPostgresStore(pool)
	chunk := collector.TraceChunk{Trace: stored.Trace, FirstSequence: 1, Records: stored.Records}
	if _, err := store.CommitTraceChunk(ctx, chunk); err != nil {
		t.Fatalf("first CommitTraceChunk: %v", err)
	}
	projector, err := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	if projected, err := projector.RunOnce(ctx); err != nil || !projected {
		t.Fatalf("RunOnce projected=%t error=%v", projected, err)
	}
	assertQueueRowAbsent := func(when string) {
		t.Helper()
		var exists bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from obs_projection_queue where trace_id = $1)`,
			stored.Trace.TraceID).Scan(&exists); err != nil {
			t.Fatalf("query obs_projection_queue %s: %v", when, err)
		}
		if exists {
			t.Fatalf("obs_projection_queue has a row for a fully projected Trace %s", when)
		}
	}
	assertQueueRowAbsent("after first projection")

	// Resend the identical chunk: no new records, committed_sequence does
	// not advance. This must not re-enqueue the already-projected Trace.
	if _, err := store.CommitTraceChunk(ctx, chunk); err != nil {
		t.Fatalf("resend CommitTraceChunk: %v", err)
	}
	assertQueueRowAbsent("after no-op resend")
}

func TestProjectorAbandonsPersistentlyInvalidTraceAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	stored := projectionStoredTrace(t, true, true)
	if _, err := collector.NewPostgresStore(pool).CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: stored.Trace, FirstSequence: 1, Records: stored.Records,
	}); err != nil {
		t.Fatalf("CommitTraceChunk: %v", err)
	}
	malformed := stored.Records[3].Record
	malformed.Attributes = append(malformed.Attributes, collectorInvalidReplayReference())
	malformedEnvelope := collectorEnvelope(t, 4, malformed)
	payload, err := malformed.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `drop trigger obs_trace_records_immutable_update on obs_trace_records`); err != nil {
		t.Fatalf("drop fixture immutability trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update obs_trace_records set canonical_payload = $3, canonical_sha256 = $4
		where trace_id = $1 and sequence = $2
	`, stored.Trace.TraceID, 4, payload, malformedEnvelope.CanonicalSHA256); err != nil {
		t.Fatalf("seed malformed historical record: %v", err)
	}
	if err := collector.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("restore Collector invariants: %v", err)
	}
	projector, _ := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond, MaxAttempts: 3})

	for attempt := 1; attempt <= 2; attempt++ {
		// RetryDelay is Postgres-clock-relative; give it real wall-clock
		// room to elapse so the next claim's `available_at <= now()` check
		// actually finds the row instead of racing it.
		time.Sleep(50 * time.Millisecond)
		projected, err := projector.RunOnce(ctx)
		if err == nil || projected || errors.Is(err, collector.ErrProjectionAbandoned) {
			t.Fatalf("attempt %d: RunOnce projected=%t error=%v, want a non-abandoned failure", attempt, projected, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	projected, err := projector.RunOnce(ctx)
	if err == nil || projected || !errors.Is(err, collector.ErrProjectionAbandoned) {
		t.Fatalf("final RunOnce projected=%t error=%v, want ErrProjectionAbandoned", projected, err)
	}
	var attemptCount int
	var lastErrorCode string
	var availableAt time.Time
	if err := pool.QueryRow(ctx, `
		select attempt_count, last_error_code, available_at from obs_projection_queue where trace_id = $1
	`, stored.Trace.TraceID).Scan(&attemptCount, &lastErrorCode, &availableAt); err != nil {
		t.Fatalf("load queue row: %v", err)
	}
	if attemptCount != 3 || lastErrorCode != "projection_abandoned" || !availableAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("abandoned queue row attempts=%d code=%q available_at=%v", attemptCount, lastErrorCode, availableAt)
	}

	// A genuinely new commit still wakes the abandoned Trace immediately:
	// the queue upsert always resets available_at regardless of last_error_code.
	if _, err := collector.NewPostgresStore(pool).CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: stored.Trace, FirstSequence: len(stored.Records) + 1,
		Records: []collector.SequencedRecord{collectorEnvelope(t, len(stored.Records)+1, agentobs.Record{
			SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
			IdentityKey: "late-wakeup", Kind: agentobs.RecordEvent, TraceID: stored.Trace.TraceID,
			SpanID: stored.Trace.RootSpanID, Name: "nano.run.woken", OccurredAt: time.Now(),
		})},
	}); err != nil {
		t.Fatalf("wakeup CommitTraceChunk: %v", err)
	}
	if err := pool.QueryRow(ctx, `select available_at from obs_projection_queue where trace_id = $1`,
		stored.Trace.TraceID).Scan(&availableAt); err != nil {
		t.Fatalf("load woken queue row: %v", err)
	}
	if availableAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("abandoned Trace was not woken by a genuinely new commit: available_at=%v", availableAt)
	}
}

func TestProjectionQueueStatsGroupsStuckRowsByErrorCode(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	store := collector.NewPostgresStore(pool)

	invalid := projectionStoredTrace(t, true, true)
	if _, err := store.CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: invalid.Trace, FirstSequence: 1, Records: invalid.Records,
	}); err != nil {
		t.Fatalf("CommitTraceChunk invalid: %v", err)
	}
	abandonedTraceID := agentobs.TraceID("trace-projection-queue-stats-abandoned")
	abandonedRoot := agentobs.SpanID("root-projection-queue-stats-abandoned")
	started := collectorRecord(abandonedTraceID, abandonedRoot, "queue-stats/root/start", agentobs.RecordSpanStarted, "agent.execution")
	ended := collectorRecord(abandonedTraceID, abandonedRoot, "queue-stats/root/end", agentobs.RecordSpanEnded, "agent.execution")
	ended.Status = agentobs.StatusOK
	if _, err := store.CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: collector.TraceDescriptor{
			TraceID: abandonedTraceID, RunID: "run-queue-stats", ChatID: "chat-queue-stats", NotebookID: "notebook-queue-stats",
			RootSpanID: abandonedRoot, AgentName: "nano-research-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
		},
		FirstSequence: 1,
		Records:       []collector.SequencedRecord{collectorEnvelope(t, 1, started), collectorEnvelope(t, 2, ended)},
	}); err != nil {
		t.Fatalf("CommitTraceChunk abandoned: %v", err)
	}

	// Seed the queue rows directly with the two error codes RunOnce
	// actually produces, rather than re-driving full failure/abandon
	// cycles through the Projector — this isolates the SQL query itself
	// (the thing this test exists to catch a bug in) from that machinery,
	// which is already covered by TestProjectorFailureLeavesRawCursorAndDiagnostic
	// and TestProjectorAbandonsPersistentlyInvalidTraceAfterMaxAttempts.
	if _, err := pool.Exec(ctx, `
		update obs_projection_queue set last_error_code = 'projection_invalid', updated_at = now()
		where trace_id = $1
	`, invalid.Trace.TraceID); err != nil {
		t.Fatalf("seed projection_invalid queue row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update obs_projection_queue set last_error_code = 'projection_abandoned', updated_at = now()
		where trace_id = $1
	`, abandonedTraceID); err != nil {
		t.Fatalf("seed projection_abandoned queue row: %v", err)
	}
	if _, err := pool.Exec(ctx, `update obs_traces set created_at = now() - interval '1 hour' where trace_id = $1`,
		abandonedTraceID); err != nil {
		t.Fatalf("age the abandoned Trace: %v", err)
	}

	stats, err := collector.ProjectionQueueStats(ctx, pool)
	if err != nil {
		t.Fatalf("ProjectionQueueStats: %v", err)
	}
	byCode := make(map[string]collector.ProjectionQueueErrorStat, len(stats))
	for _, stat := range stats {
		byCode[stat.ErrorCode] = stat
	}
	if len(byCode) != 2 {
		t.Fatalf("ProjectionQueueStats = %#v, want exactly 2 error codes", stats)
	}
	invalidStat, ok := byCode["projection_invalid"]
	if !ok || invalidStat.Count != 1 {
		t.Fatalf("projection_invalid stat = %#v, ok=%t", invalidStat, ok)
	}
	abandonedStat, ok := byCode["projection_abandoned"]
	if !ok || abandonedStat.Count != 1 || abandonedStat.OldestAgeSeconds < 3500 {
		t.Fatalf("projection_abandoned stat = %#v, ok=%t, want age >= ~1h", abandonedStat, ok)
	}
}

func collectorInvalidReplayReference() agentobs.Attribute {
	return agentobs.String(replay.ModelRequestAttachmentKey, "not-an-attachment-id")
}

func TestProjectorFailureLeavesRawCursorAndDiagnostic(t *testing.T) {
	ctx := context.Background()
	pool := openObservabilityTestPool(t, ctx)
	t.Cleanup(pool.Close)
	resetObservabilityTestSchema(t, ctx, pool)
	stored := projectionStoredTrace(t, true, true)
	if _, err := collector.NewPostgresStore(pool).CommitTraceChunk(ctx, collector.TraceChunk{
		Trace: stored.Trace, FirstSequence: 1, Records: stored.Records,
	}); err != nil {
		t.Fatalf("CommitTraceChunk: %v", err)
	}
	malformed := stored.Records[3].Record
	malformed.Attributes = append(malformed.Attributes, collectorInvalidReplayReference())
	malformedEnvelope := collectorEnvelope(t, 4, malformed)
	payload, err := malformed.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `drop trigger obs_trace_records_immutable_update on obs_trace_records`); err != nil {
		t.Fatalf("drop fixture immutability trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update obs_trace_records set canonical_payload = $3, canonical_sha256 = $4
		where trace_id = $1 and sequence = $2
	`, stored.Trace.TraceID, 4, payload, malformedEnvelope.CanonicalSHA256); err != nil {
		t.Fatalf("seed malformed historical record: %v", err)
	}
	if err := collector.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("restore Collector invariants: %v", err)
	}
	projector, _ := collector.NewProjector(pool, collector.ProjectorConfig{RetryDelay: time.Millisecond})
	projected, err := projector.RunOnce(ctx)
	if err == nil || projected {
		t.Fatalf("RunOnce projected=%t error=%v", projected, err)
	}
	var committed, projectedSequence, rawCount int
	var diagnostic string
	if err := pool.QueryRow(ctx, `
		select committed_sequence, projected_sequence,
			(select count(*) from obs_trace_records where trace_id = obs_traces.trace_id),
			(select last_error_code from obs_projection_queue where trace_id = obs_traces.trace_id)
		from obs_traces where trace_id = $1
	`, stored.Trace.TraceID).Scan(&committed, &projectedSequence, &rawCount, &diagnostic); err != nil {
		t.Fatalf("load failure state: %v", err)
	}
	if committed != len(stored.Records) || projectedSequence != 0 || rawCount != len(stored.Records) || diagnostic == "" {
		t.Fatalf("failure state committed=%d projected=%d raw=%d diagnostic=%q", committed, projectedSequence, rawCount, diagnostic)
	}
}
