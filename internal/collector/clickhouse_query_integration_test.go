package collector_test

import (
	"context"
	"fmt"
	"testing"
	"time"

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
