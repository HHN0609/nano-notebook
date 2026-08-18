package collector_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
)

func TestClickHousePurgeTombstonesBeforeDeletionResumesAndPreventsResurrection(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	stagingObjects := objectstore.NewMemoryStore()
	replayObjects := &failOnceDeleteStore{MemoryStore: objectstore.NewMemoryStore()}
	ciphertext := []byte("opaque-clickhouse-purge-ciphertext")
	batch := clickHouseReplayBatch(t, fmt.Sprintf("ch-purge-%d", time.Now().UnixNano()), ciphertext, time.Now().UTC().Add(time.Hour))
	attachment := batch.Chunks[0].Attachments[0]
	if err := stagingObjects.Put(ctx, attachment.StagingObjectKey, ciphertext); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStoreWithReplay(connection, stagingObjects, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, _ := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	traceCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 2, Offset: time.Now().UnixNano(),
	})
	if result, err := ingestor.Ingest(traceCtx, batch); err != nil || result.Chunks[0].Status != collector.ChunkCommitted {
		t.Fatalf("initial Trace ingest=%#v err=%v", result, err)
	}
	queries, err := collector.NewClickHouseTraceQueryStoreWithReplay(connection, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	analytics, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	purger, err := collector.NewPurger(collector.PurgerConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	command := collector.PurgeCommand{
		CommandID: uuid.NewString(), CommandVersion: 1, Kind: collector.CommandPurgeTrace,
		TraceID: batch.Chunks[0].Trace.TraceID, RunID: batch.Chunks[0].Trace.RunID, RequestedAt: time.Now().UTC(),
	}
	purgeBatch := collector.PurgeBatch{
		ProtocolVersion: collector.ProtocolVersion, BatchID: uuid.NewString(), ProducerID: "nano-worker",
		CreatedAt: time.Now().UTC(), Commands: []collector.PurgeCommand{command},
	}
	replayObjects.failNextDelete = true
	replayObjects.beforeDelete = func() {
		var tombstones, rawRows uint64
		if err := connection.QueryRow(ctx, "SELECT count() FROM obs_trace_tombstones FINAL WHERE trace_id = ?", string(command.TraceID)).Scan(&tombstones); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRow(ctx, "SELECT count() FROM obs_trace_records_raw WHERE trace_id = ?", string(command.TraceID)).Scan(&rawRows); err != nil {
			t.Fatal(err)
		}
		listed, err := queries.List(ctx, collector.TraceListQuery{IdentityExact: string(command.TraceID), PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		if tombstones != 1 || rawRows == 0 || len(listed.Items) != 0 {
			t.Fatalf("during purge tombstones=%d raw=%d listed=%d", tombstones, rawRows, len(listed.Items))
		}
		overview, err := analytics.Overview(ctx, collector.TraceAnalyticsQuery{
			StartedAfterUnixNano:  time.Now().UTC().Add(-time.Hour).UnixNano(),
			StartedBeforeUnixNano: time.Now().UTC().Add(time.Hour).UnixNano(),
			WorkloadKind:          collector.WorkloadAgentRun, NotebookIDs: []string{batch.Chunks[0].Trace.NotebookID},
		})
		if err != nil || overview.Data.RunCount != 0 {
			t.Fatalf("Tombstoned analytics=%#v err=%v", overview.Data, err)
		}
		if _, err := queries.Detail(ctx, command.TraceID); !errors.Is(err, collector.ErrTraceNotFound) {
			t.Fatalf("Tombstoned detail error=%v", err)
		}
		record := batch.Chunks[0].Records[1].Record
		if _, err := queries.Replay(ctx, command.TraceID, record.SpanID, attachment.AttachmentID); !errors.Is(err, collector.ErrReplayNotFound) {
			t.Fatalf("Tombstoned Replay error=%v", err)
		}
	}
	purgeCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace-purge.v1", Partition: 2, Offset: time.Now().UnixNano(),
	})
	if _, err := purger.Purge(purgeCtx, purgeBatch); err == nil {
		t.Fatal("purge unexpectedly completed after injected Replay delete failure")
	}
	var stage string
	if err := connection.QueryRow(ctx, "SELECT stage FROM obs_trace_purge_state FINAL WHERE command_id = ?", command.CommandID).Scan(&stage); err != nil {
		t.Fatal(err)
	}
	if stage != "tombstoned" {
		t.Fatalf("partial purge stage=%q", stage)
	}
	replayObjects.beforeDelete = nil
	result, err := purger.Purge(purgeCtx, purgeBatch)
	if err != nil || result.Commands[0].Status != collector.PurgeAcknowledged {
		t.Fatalf("resumed purge=%#v err=%v", result, err)
	}
	if err := connection.QueryRow(ctx, "SELECT stage FROM obs_trace_purge_state FINAL WHERE command_id = ?", command.CommandID).Scan(&stage); err != nil {
		t.Fatal(err)
	}
	if stage != "complete" || replayObjects.Len() != 0 {
		t.Fatalf("completed purge stage=%q replay_objects=%d", stage, replayObjects.Len())
	}
	for _, table := range []string{"obs_trace_records_raw", "obs_trace_summaries", "obs_span_analytics", "obs_replay_payload_refs"} {
		var rows uint64
		if err := connection.QueryRow(ctx, "SELECT count() FROM "+table+" WHERE trace_id = ?", string(command.TraceID)).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("purged table %s rows=%d", table, rows)
		}
	}
	lateCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 2, Offset: time.Now().UnixNano() + 1,
	})
	late, err := ingestor.Ingest(lateCtx, batch)
	if err != nil || late.Chunks[0].Status != collector.ChunkRejected || late.Chunks[0].Code != collector.CodeTombstoned {
		t.Fatalf("late Trace after purge=%#v err=%v", late, err)
	}
	duplicate, err := purger.Purge(purgeCtx, purgeBatch)
	if err != nil || duplicate.Commands[0].Status != collector.PurgeAcknowledged {
		t.Fatalf("duplicate purge=%#v err=%v", duplicate, err)
	}
}

type failOnceDeleteStore struct {
	*objectstore.MemoryStore
	failNextDelete bool
	beforeDelete   func()
}

func (s *failOnceDeleteStore) Delete(ctx context.Context, key string) error {
	if s.beforeDelete != nil {
		s.beforeDelete()
	}
	if s.failNextDelete {
		s.failNextDelete = false
		return errors.New("injected Replay object delete failure")
	}
	return s.MemoryStore.Delete(ctx, key)
}
