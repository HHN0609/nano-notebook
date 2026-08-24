package agentbatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

// KafkaMessage is the client-neutral message passed to the Kafka adapter.
type KafkaMessage struct {
	Topic string
	Key   []byte
	Value []byte
}

// KafkaProducer isolates transport semantics from a concrete Kafka client.
// A nil or empty error slice means all messages were acknowledged. Otherwise
// it must contain one entry per input message.
type KafkaProducer interface {
	ProduceSync(context.Context, []KafkaMessage) []error
}

// KafkaTraceEnvelope is the strongly versioned Kafka value contract.
type KafkaTraceEnvelope struct {
	SchemaVersion int                  `json:"schema_version"`
	BatchID       string               `json:"batch_id"`
	ProducerID    string               `json:"producer_id"`
	CreatedAt     time.Time            `json:"created_at"`
	Chunk         collector.TraceChunk `json:"chunk"`
}

// DecodeKafkaTraceEnvelope strictly decodes one Durable Agent Trace message.
func DecodeKafkaTraceEnvelope(encoded []byte) (KafkaTraceEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var envelope KafkaTraceEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return KafkaTraceEnvelope{}, fmt.Errorf("decode Agent Trace Kafka envelope: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return KafkaTraceEnvelope{}, errors.New("Agent Trace Kafka envelope has trailing data")
	}
	if envelope.SchemaVersion != 1 || strings.TrimSpace(envelope.BatchID) == "" || strings.TrimSpace(envelope.ProducerID) == "" ||
		envelope.CreatedAt.IsZero() || strings.TrimSpace(string(envelope.Chunk.Trace.TraceID)) == "" || len(envelope.Chunk.Records) == 0 {
		return KafkaTraceEnvelope{}, errors.New("Agent Trace Kafka envelope is incomplete")
	}
	return envelope, nil
}
