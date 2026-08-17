// Package agenttraceprocessor classifies and persists Durable Agent Trace
// messages before their Kafka offsets may advance.
package agenttraceprocessor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

const CodeInvalidEnvelope = "invalid_envelope"

type Disposition string

const (
	Commit Disposition = "commit"
	Retry  Disposition = "retry"
)

// Message is the Kafka-client-neutral source record processed by this package.
type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
}

type Ingestor interface {
	Ingest(context.Context, collector.Batch) (collector.BatchResult, error)
}

type QuarantineWriter interface {
	Write(context.Context, QuarantineEnvelope) error
}

// QuarantineEnvelope retains the immutable source coordinates and bytes needed
// to diagnose or replay a permanent failure.
type QuarantineEnvelope struct {
	SchemaVersion   int       `json:"schema_version"`
	SourceTopic     string    `json:"source_topic"`
	SourcePartition int32     `json:"source_partition"`
	SourceOffset    int64     `json:"source_offset"`
	SourceKey       []byte    `json:"source_key"`
	SourceValue     []byte    `json:"source_value"`
	Code            string    `json:"code"`
	Detail          string    `json:"detail,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
}

type Config struct {
	Topic      string
	Ingestor   Ingestor
	Quarantine QuarantineWriter
	Now        func() time.Time
}

type Processor struct {
	topic      string
	ingestor   Ingestor
	quarantine QuarantineWriter
	now        func() time.Time
}

func New(config Config) (*Processor, error) {
	config.Topic = strings.TrimSpace(config.Topic)
	if config.Topic == "" || config.Ingestor == nil || config.Quarantine == nil {
		return nil, errors.New("Agent Trace Processor configuration is incomplete")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Processor{topic: config.Topic, ingestor: config.Ingestor, quarantine: config.Quarantine, now: config.Now}, nil
}

// Process returns Commit only after retained storage accepted the message or a
// permanent failure was durably quarantined. Retry always leaves the source
// offset uncommitted.
func (p *Processor) Process(ctx context.Context, message Message) (Disposition, error) {
	if p == nil || p.ingestor == nil || p.quarantine == nil {
		return Retry, errors.New("nil Agent Trace Processor")
	}
	if strings.TrimSpace(message.Topic) != p.topic {
		return p.quarantineMessage(ctx, message, CodeInvalidEnvelope, "unexpected source topic")
	}
	envelope, err := agentbatch.DecodeKafkaTraceEnvelope(message.Value)
	if err != nil {
		return p.quarantineMessage(ctx, message, CodeInvalidEnvelope, err.Error())
	}
	if string(message.Key) != string(envelope.Chunk.Trace.TraceID) {
		return p.quarantineMessage(ctx, message, CodeInvalidEnvelope, "Kafka key does not match trace_id")
	}

	batch := collector.Batch{
		ProtocolVersion: collector.DirectProtocolVersion,
		BatchID:         envelope.BatchID,
		ProducerID:      envelope.ProducerID,
		CreatedAt:       envelope.CreatedAt,
		Chunks:          []collector.TraceChunk{envelope.Chunk},
	}
	ctx = collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: message.Topic, Partition: message.Partition, Offset: message.Offset,
	})
	result, err := p.ingestor.Ingest(ctx, batch)
	if err != nil {
		if errors.Is(err, collector.ErrInvalidBatch) {
			return p.quarantineMessage(ctx, message, collector.CodeInvalidChunk, err.Error())
		}
		return Retry, fmt.Errorf("persist Agent Trace message: %w", err)
	}
	if result.BatchID != batch.BatchID || len(result.Chunks) != 1 || result.Chunks[0].TraceID != envelope.Chunk.Trace.TraceID {
		return Retry, errors.New("Agent Trace storage returned an invalid acknowledgement")
	}
	chunk := result.Chunks[0]
	switch chunk.Status {
	case collector.ChunkCommitted:
		return Commit, nil
	case collector.ChunkRetryable:
		return Retry, fmt.Errorf("Agent Trace storage requested retry: %s", chunk.Code)
	case collector.ChunkRejected:
		if chunk.Code == collector.CodeTombstoned {
			return Commit, nil
		}
		return p.quarantineMessage(ctx, message, chunk.Code, "Agent Trace storage permanently rejected message")
	default:
		return Retry, fmt.Errorf("Agent Trace storage returned unknown status %q", chunk.Status)
	}
}

func (p *Processor) quarantineMessage(ctx context.Context, message Message, code, detail string) (Disposition, error) {
	entry := QuarantineEnvelope{
		SchemaVersion: 1, SourceTopic: message.Topic, SourcePartition: message.Partition, SourceOffset: message.Offset,
		SourceKey: append([]byte(nil), message.Key...), SourceValue: append([]byte(nil), message.Value...),
		Code: code, Detail: detail, ObservedAt: p.now().UTC(),
	}
	if err := p.quarantine.Write(ctx, entry); err != nil {
		return Retry, fmt.Errorf("quarantine Agent Trace message: %w", err)
	}
	return Commit, nil
}
