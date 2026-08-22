package agentbatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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

// DefaultKafkaMaxRetries allows three retries after the initial Batch send.
const DefaultKafkaMaxRetries = 3

// KafkaSenderConfig configures Durable Agent Trace Kafka acceptance.
type KafkaSenderConfig struct {
	Topic      string
	Producer   KafkaProducer
	MaxRetries int
}

// KafkaSender publishes one trace-keyed Kafka message per Collector Trace chunk.
type KafkaSender struct {
	topic      string
	producer   KafkaProducer
	maxRetries int
	retryMu    sync.Mutex
	failures   map[string]int
}

// KafkaTraceEnvelope is the strongly versioned Kafka value contract.
type KafkaTraceEnvelope struct {
	SchemaVersion int                  `json:"schema_version"`
	BatchID       string               `json:"batch_id"`
	ProducerID    string               `json:"producer_id"`
	CreatedAt     time.Time            `json:"created_at"`
	Chunk         collector.TraceChunk `json:"chunk"`
}

func NewKafkaSender(config KafkaSenderConfig) (*KafkaSender, error) {
	config.Topic = strings.TrimSpace(config.Topic)
	if config.Topic == "" || config.Producer == nil || config.MaxRetries < 0 {
		return nil, errors.New("Agent Trace Kafka Sender configuration is incomplete")
	}
	return &KafkaSender{
		topic: config.Topic, producer: config.Producer, maxRetries: config.MaxRetries,
		failures: make(map[string]int),
	}, nil
}

func (s *KafkaSender) Send(ctx context.Context, batch collector.Batch) (collector.BatchResult, error) {
	if s == nil || s.producer == nil {
		return collector.BatchResult{}, errors.New("nil Agent Trace Kafka Sender")
	}
	if batch.ProtocolVersion != collector.DirectProtocolVersion || strings.TrimSpace(batch.BatchID) == "" ||
		strings.TrimSpace(batch.ProducerID) == "" || batch.CreatedAt.IsZero() || len(batch.Chunks) == 0 {
		return collector.BatchResult{}, newDeliveryError(false, errors.New("Agent Trace Kafka Batch is invalid"))
	}
	messages := make([]KafkaMessage, len(batch.Chunks))
	result := collector.BatchResult{BatchID: batch.BatchID, Chunks: make([]collector.ChunkResult, len(batch.Chunks))}
	for index, chunk := range batch.Chunks {
		if strings.TrimSpace(string(chunk.Trace.TraceID)) == "" || len(chunk.Records) == 0 {
			return collector.BatchResult{}, newDeliveryError(false, errors.New("Agent Trace Kafka chunk is invalid"))
		}
		encoded, err := json.Marshal(KafkaTraceEnvelope{
			SchemaVersion: 1, BatchID: batch.BatchID, ProducerID: batch.ProducerID,
			CreatedAt: batch.CreatedAt.UTC(), Chunk: chunk,
		})
		if err != nil {
			return collector.BatchResult{}, newDeliveryError(false, fmt.Errorf("encode Agent Trace Kafka envelope: %w", err))
		}
		messages[index] = KafkaMessage{Topic: s.topic, Key: []byte(chunk.Trace.TraceID), Value: encoded}
		result.Chunks[index] = collector.ChunkResult{
			TraceID: chunk.Trace.TraceID, Status: collector.ChunkCommitted,
			CommittedThrough: len(chunk.Records),
		}
	}
	produceErrors := s.producer.ProduceSync(ctx, messages)
	if len(produceErrors) != 0 && len(produceErrors) != len(messages) {
		return collector.BatchResult{}, s.failedDelivery(batch.BatchID, errors.New("Kafka producer returned an invalid acknowledgement set"))
	}
	for index, err := range produceErrors {
		if err != nil {
			return collector.BatchResult{}, s.failedDelivery(batch.BatchID, fmt.Errorf("Kafka did not acknowledge Trace %s: %w", batch.Chunks[index].Trace.TraceID, err))
		}
	}
	s.clearFailures(batch.BatchID)
	return result, nil
}

func (s *KafkaSender) failedDelivery(batchID string, cause error) error {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	failures := s.failures[batchID] + 1
	if failures > s.maxRetries {
		delete(s.failures, batchID)
		return newDeliveryError(false, fmt.Errorf("Kafka Batch %s exhausted %d retries: %w", batchID, s.maxRetries, cause))
	}
	s.failures[batchID] = failures
	return newDeliveryError(true, cause)
}

func (s *KafkaSender) clearFailures(batchID string) {
	s.retryMu.Lock()
	delete(s.failures, batchID)
	s.retryMu.Unlock()
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
