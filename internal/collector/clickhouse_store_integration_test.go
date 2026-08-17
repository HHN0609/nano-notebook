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
