package agenttraceprocessor_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentoutbox"
	"github.com/huangxinxinyu/nano-notebook/internal/agenttraceprocessor"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

const traceTopic = "nano.observability.agent-trace.v1"

func TestProcessorCommitsOnlyAfterStorageAcceptsMessage(t *testing.T) {
	ingestor := &fakeIngestor{result: collector.BatchResult{BatchID: "batch-1", Chunks: []collector.ChunkResult{{
		TraceID: "trace-1", Status: collector.ChunkCommitted, CommittedThrough: 1,
	}}}}
	quarantine := &fakeQuarantine{}
	processor := newProcessor(t, ingestor, quarantine)

	disposition, err := processor.Process(context.Background(), validMessage(t))
	if err != nil {
		t.Fatal(err)
	}
	if disposition != agenttraceprocessor.Commit || ingestor.calls != 1 || len(quarantine.entries) != 0 {
		t.Fatalf("disposition=%q calls=%d quarantine=%d", disposition, ingestor.calls, len(quarantine.entries))
	}
	if ingestor.batch.ProtocolVersion != collector.DirectProtocolVersion || len(ingestor.batch.Chunks) != 1 {
		t.Fatalf("ingested batch=%#v", ingestor.batch)
	}
	source, ok := collector.KafkaSourcePositionFromContext(ingestor.ctx)
	if !ok || source.Topic != traceTopic || source.Partition != 3 || source.Offset != 41 {
		t.Fatalf("Kafka source position=%#v found=%t", source, ok)
	}
}

func TestProcessorDoesNotCommitTransientStorageFailure(t *testing.T) {
	ingestor := &fakeIngestor{err: errors.New("postgres unavailable")}
	processor := newProcessor(t, ingestor, &fakeQuarantine{})

	disposition, err := processor.Process(context.Background(), validMessage(t))
	if err == nil || disposition != agenttraceprocessor.Retry {
		t.Fatalf("disposition=%q error=%v", disposition, err)
	}
}

func TestProcessorQuarantinesPermanentFailureBeforeCommit(t *testing.T) {
	ingestor := &fakeIngestor{result: collector.BatchResult{BatchID: "batch-1", Chunks: []collector.ChunkResult{{
		TraceID: "trace-1", Status: collector.ChunkRejected, Code: collector.CodeIdentityConflict,
	}}}}
	quarantine := &fakeQuarantine{}
	processor := newProcessor(t, ingestor, quarantine)
	message := validMessage(t)

	disposition, err := processor.Process(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if disposition != agenttraceprocessor.Commit || len(quarantine.entries) != 1 {
		t.Fatalf("disposition=%q quarantine=%#v", disposition, quarantine.entries)
	}
	entry := quarantine.entries[0]
	if entry.SourceTopic != message.Topic || entry.SourcePartition != message.Partition || entry.SourceOffset != message.Offset || entry.Code != collector.CodeIdentityConflict {
		t.Fatalf("quarantine entry=%#v", entry)
	}
}

func TestProcessorRetriesWhenQuarantineWriteFails(t *testing.T) {
	ingestor := &fakeIngestor{result: collector.BatchResult{BatchID: "batch-1", Chunks: []collector.ChunkResult{{
		TraceID: "trace-1", Status: collector.ChunkRejected, Code: collector.CodeUnsupportedSchema,
	}}}}
	quarantine := &fakeQuarantine{err: errors.New("Kafka unavailable")}
	processor := newProcessor(t, ingestor, quarantine)

	disposition, err := processor.Process(context.Background(), validMessage(t))
	if err == nil || disposition != agenttraceprocessor.Retry {
		t.Fatalf("disposition=%q error=%v", disposition, err)
	}
}

func TestProcessorQuarantinesInvalidEnvelopeWithoutCallingStorage(t *testing.T) {
	ingestor := &fakeIngestor{}
	quarantine := &fakeQuarantine{}
	processor := newProcessor(t, ingestor, quarantine)
	message := validMessage(t)
	message.Value = []byte(`{"schema_version":99}`)

	disposition, err := processor.Process(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if disposition != agenttraceprocessor.Commit || ingestor.calls != 0 || len(quarantine.entries) != 1 {
		t.Fatalf("disposition=%q calls=%d quarantine=%d", disposition, ingestor.calls, len(quarantine.entries))
	}
}

func TestProcessorCommitsPurgeOnlyAfterClickHousePurgerCompletes(t *testing.T) {
	purger := &fakePurger{result: collector.PurgeBatchResult{BatchID: "purge-batch-1", Commands: []collector.PurgeCommandResult{{
		TraceID: "trace-purge-1", Status: collector.PurgeAcknowledged,
	}}}}
	processor, err := agenttraceprocessor.New(agenttraceprocessor.Config{
		Topic: traceTopic, Ingestor: &fakeIngestor{}, PurgeTopic: "nano.observability.agent-trace-purge.v1",
		Purger: purger, Quarantine: &fakeQuarantine{},
	})
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := processor.Process(context.Background(), validPurgeMessage(t))
	if err != nil || disposition != agenttraceprocessor.Commit || purger.calls != 1 {
		t.Fatalf("purge disposition=%q calls=%d err=%v", disposition, purger.calls, err)
	}
	source, ok := collector.KafkaSourcePositionFromContext(purger.ctx)
	if !ok || source.Topic != "nano.observability.agent-trace-purge.v1" || source.Offset != 52 {
		t.Fatalf("purge source=%#v found=%t", source, ok)
	}
}

func TestProcessorRetriesPurgeWhenPhysicalDeletionIsIncomplete(t *testing.T) {
	wantErr := errors.New("Replay object unavailable")
	purger := &fakePurger{err: wantErr}
	processor, err := agenttraceprocessor.New(agenttraceprocessor.Config{
		Topic: traceTopic, Ingestor: &fakeIngestor{}, PurgeTopic: "nano.observability.agent-trace-purge.v1",
		Purger: purger, Quarantine: &fakeQuarantine{},
	})
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := processor.Process(context.Background(), validPurgeMessage(t))
	if !errors.Is(err, wantErr) || disposition != agenttraceprocessor.Retry {
		t.Fatalf("purge disposition=%q err=%v", disposition, err)
	}
}

func TestKafkaQuarantineWriterKeysEnvelopeBySourceCoordinate(t *testing.T) {
	producer := &fakeKafkaProducer{}
	writer, err := agenttraceprocessor.NewKafkaQuarantineWriter(agenttraceprocessor.KafkaQuarantineConfig{
		Topic: "nano.observability.agent-trace-quarantine.v1", Producer: producer,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := agenttraceprocessor.QuarantineEnvelope{
		SchemaVersion: 1, SourceTopic: traceTopic, SourcePartition: 3, SourceOffset: 41,
		SourceKey: []byte("trace-1"), SourceValue: []byte("broken"), Code: agenttraceprocessor.CodeInvalidEnvelope,
		ObservedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if len(producer.messages) != 1 || string(producer.messages[0].Key) != traceTopic+"/3/41" {
		t.Fatalf("messages=%#v", producer.messages)
	}
	var decoded agenttraceprocessor.QuarantineEnvelope
	if err := json.Unmarshal(producer.messages[0].Value, &decoded); err != nil || decoded.Code != entry.Code {
		t.Fatalf("decoded=%#v error=%v", decoded, err)
	}
}

func newProcessor(t *testing.T, ingestor agenttraceprocessor.Ingestor, quarantine agenttraceprocessor.QuarantineWriter) *agenttraceprocessor.Processor {
	t.Helper()
	processor, err := agenttraceprocessor.New(agenttraceprocessor.Config{
		Topic: traceTopic, Ingestor: ingestor, Quarantine: quarantine,
	})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

type fakeIngestor struct {
	result collector.BatchResult
	err    error
	calls  int
	batch  collector.Batch
	ctx    context.Context
}

func (f *fakeIngestor) Ingest(ctx context.Context, batch collector.Batch) (collector.BatchResult, error) {
	f.calls++
	f.batch = batch
	f.ctx = ctx
	return f.result, f.err
}

type fakeQuarantine struct {
	entries []agenttraceprocessor.QuarantineEnvelope
	err     error
}

type fakePurger struct {
	result collector.PurgeBatchResult
	err    error
	calls  int
	ctx    context.Context
}

func (p *fakePurger) Purge(ctx context.Context, _ collector.PurgeBatch) (collector.PurgeBatchResult, error) {
	p.calls++
	p.ctx = ctx
	return p.result, p.err
}

type fakeKafkaProducer struct {
	messages []agentbatch.KafkaMessage
	errors   []error
}

func (f *fakeKafkaProducer) ProduceSync(_ context.Context, messages []agentbatch.KafkaMessage) []error {
	f.messages = append(f.messages, messages...)
	return f.errors
}

func (f *fakeQuarantine) Write(_ context.Context, envelope agenttraceprocessor.QuarantineEnvelope) error {
	f.entries = append(f.entries, envelope)
	return f.err
}

func validMessage(t *testing.T) agenttraceprocessor.Message {
	t.Helper()
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	record := agentobs.Record{
		SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
		IdentityKey: "run/run-1/root/start", Kind: agentobs.RecordSpanStarted,
		TraceID: "trace-1", SpanID: "span-root-1", Name: "agent.execution", OccurredAt: createdAt,
	}
	hash, err := record.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	envelope := agentbatch.KafkaTraceEnvelope{
		SchemaVersion: 1, BatchID: "batch-1", ProducerID: "nano-worker/bench", CreatedAt: createdAt,
		Chunk: collector.TraceChunk{
			Trace: collector.TraceDescriptor{
				TraceID: "trace-1", WorkloadKind: collector.WorkloadAgentRun, WorkloadID: "run-1",
				RunID: "run-1", ChatID: "chat-1", NotebookID: "notebook-1", RootSpanID: "span-root-1",
				AgentName: "nano-default-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
			},
			SequenceAuthority: collector.SequenceAuthorityCollector,
			Records:           []collector.SequencedRecord{{Record: record, CanonicalSHA256: hex.EncodeToString(hash[:])}},
		},
	}
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return agenttraceprocessor.Message{Topic: traceTopic, Partition: 3, Offset: 41, Key: []byte("trace-1"), Value: value}
}

func validPurgeMessage(t *testing.T) agenttraceprocessor.Message {
	t.Helper()
	envelope := agentoutbox.KafkaPurgeEnvelope{
		SchemaVersion: 1, BatchID: "purge-batch-1", ProducerID: "nano-worker", CreatedAt: time.Now().UTC(),
		Command: collector.PurgeCommand{
			CommandID: "purge/trace-purge-1", CommandVersion: 1, Kind: collector.CommandPurgeTrace,
			TraceID: "trace-purge-1", RunID: "run-purge-1", RequestedAt: time.Now().UTC(),
		},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return agenttraceprocessor.Message{
		Topic: "nano.observability.agent-trace-purge.v1", Partition: 3, Offset: 52,
		Key: []byte("trace-purge-1"), Value: encoded,
	}
}
