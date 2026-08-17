package agentbatch_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

func TestKafkaSenderPublishesOneTraceKeyedMessagePerChunk(t *testing.T) {
	batch := kafkaBatchFixture(t)
	producer := &recordingKafkaProducer{}
	sender, err := agentbatch.NewKafkaSender(agentbatch.KafkaSenderConfig{
		Topic: "nano.observability.agent-trace.v1", Producer: producer,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sender.Send(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(producer.messages) != 2 || string(producer.messages[0].Key) != "trace-kafka-1" || string(producer.messages[1].Key) != "trace-kafka-2" {
		t.Fatalf("messages=%#v", producer.messages)
	}
	for index, message := range producer.messages {
		envelope, err := agentbatch.DecodeKafkaTraceEnvelope(message.Value)
		if err != nil {
			t.Fatalf("decode message %d: %v", index, err)
		}
		if envelope.SchemaVersion != 1 || envelope.BatchID != batch.BatchID || envelope.ProducerID != batch.ProducerID ||
			envelope.Chunk.Trace.TraceID != batch.Chunks[index].Trace.TraceID {
			t.Fatalf("envelope %d=%#v", index, envelope)
		}
	}
	if result.BatchID != batch.BatchID || len(result.Chunks) != 2 || result.Chunks[0].Status != collector.ChunkCommitted || result.Chunks[1].Status != collector.ChunkCommitted {
		t.Fatalf("result=%#v", result)
	}
}

func TestKafkaSenderTreatsAnyUnacknowledgedChunkAsRetryable(t *testing.T) {
	producer := &recordingKafkaProducer{errors: []error{nil, errors.New("broker unavailable")}}
	sender, err := agentbatch.NewKafkaSender(agentbatch.KafkaSenderConfig{Topic: "agent-trace", Producer: producer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Send(context.Background(), kafkaBatchFixture(t)); err == nil || !agentbatch.Retryable(err) {
		t.Fatalf("partial acknowledgement error=%v retryable=%t", err, agentbatch.Retryable(err))
	}
}

func TestNewFranzKafkaProducerValidatesBoundedConfiguration(t *testing.T) {
	if _, err := agentbatch.NewFranzKafkaProducer(agentbatch.FranzKafkaConfig{}); err == nil {
		t.Fatal("empty Kafka configuration was accepted")
	}
	producer, err := agentbatch.NewFranzKafkaProducer(agentbatch.FranzKafkaConfig{
		Brokers: []string{"127.0.0.1:9092"}, ClientID: "nano-test-producer",
		MaxBufferedRecords: 1_000, MaxBufferedBytes: 4 * 1024 * 1024,
		DeliveryTimeout: 5 * time.Second, Linger: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	producer.Close()
}

type recordingKafkaProducer struct {
	messages []agentbatch.KafkaMessage
	errors   []error
}

func (p *recordingKafkaProducer) ProduceSync(_ context.Context, messages []agentbatch.KafkaMessage) []error {
	p.messages = append([]agentbatch.KafkaMessage(nil), messages...)
	return append([]error(nil), p.errors...)
}

func kafkaBatchFixture(t *testing.T) collector.Batch {
	t.Helper()
	batch := collector.Batch{
		ProtocolVersion: collector.DirectProtocolVersion, BatchID: "batch-kafka-1", ProducerID: "nano-worker/bench",
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	for index := 1; index <= 2; index++ {
		traceID := agentobs.TraceID("trace-kafka-" + string(rune('0'+index)))
		rootID := agentobs.SpanID("root-kafka-" + string(rune('0'+index)))
		record := agentobs.Record{
			SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
			IdentityKey: "run/run-kafka/root/start", Kind: agentobs.RecordSpanStarted,
			TraceID: traceID, SpanID: rootID, Name: "agent.execution", OccurredAt: batch.CreatedAt,
		}
		hash, err := record.CanonicalHash()
		if err != nil {
			t.Fatal(err)
		}
		batch.Chunks = append(batch.Chunks, collector.TraceChunk{
			Trace: collector.TraceDescriptor{
				TraceID: traceID, WorkloadKind: collector.WorkloadAgentRun, WorkloadID: "run-kafka",
				RunID: "run-kafka", ChatID: "chat-kafka", NotebookID: "notebook-kafka", RootSpanID: rootID,
				AgentName: "nano-default-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
			},
			SequenceAuthority: collector.SequenceAuthorityCollector,
			Records:           []collector.SequencedRecord{{Record: record, CanonicalSHA256: hex.EncodeToString(hash[:])}},
		})
	}
	return batch
}
