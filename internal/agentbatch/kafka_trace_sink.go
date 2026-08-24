package agentbatch

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/twmb/franz-go/pkg/kgo"
)

var ErrKafkaTraceMessageTooLarge = errors.New("Agent Trace Kafka message exceeds the configured byte bound")

type KafkaTraceDeliveryResult = string

const (
	KafkaTraceAcknowledged KafkaTraceDeliveryResult = "acknowledged"
	KafkaTraceBufferFull   KafkaTraceDeliveryResult = "buffer_full"
	KafkaTraceTimedOut     KafkaTraceDeliveryResult = "timed_out"
	KafkaTraceFailed       KafkaTraceDeliveryResult = "failed"
)

type KafkaTraceRejectionReason = string

const (
	KafkaTraceInvalid         KafkaTraceRejectionReason = "invalid"
	KafkaTraceMessageTooLarge KafkaTraceRejectionReason = "message_too_large"
	KafkaTraceShutdown        KafkaTraceRejectionReason = "shutdown"
)

// KafkaTraceProducer is the bounded, non-blocking producer surface used by
// product Trace delivery. Pending payload ownership remains inside Kafka.
type KafkaTraceProducer interface {
	TryProduce(context.Context, KafkaMessage, func(error))
	Flush(context.Context) error
	BufferedRecords() int64
	BufferedBytes() int64
	Close()
}

// KafkaTraceObserver records bounded producer outcomes without Trace identity
// labels. Implementations must remain fast because Delivery runs in franz-go's
// serialized callback path.
type KafkaTraceObserver interface {
	KafkaTraceOfferRejected(KafkaTraceRejectionReason)
	KafkaTraceDelivery(KafkaTraceDeliveryResult, time.Duration)
	KafkaTraceBuffered(records, bytes int64)
	KafkaTraceShutdownFailure()
}

type KafkaTraceSinkConfig struct {
	ProducerID      string
	Topic           string
	Producer        KafkaTraceProducer
	MaxMessageBytes int
	Observer        KafkaTraceObserver
	Logger          *slog.Logger
	Now             func() time.Time
	NewBatchID      func() string
}

// KafkaTraceSink publishes one Trace Record per Kafka message. It owns only
// lifecycle synchronization; it does not retain Record payloads or batch them.
type KafkaTraceSink struct {
	producerID      string
	topic           string
	producer        KafkaTraceProducer
	maxMessageBytes int
	observer        KafkaTraceObserver
	logger          *slog.Logger
	now             func() time.Time
	newBatchID      func() string

	produceCtx  context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	closing     bool
	done        chan struct{}
	shutdownErr error
	failureLogs atomic.Uint64
}

func NewKafkaTraceSink(config KafkaTraceSinkConfig) (*KafkaTraceSink, error) {
	config.ProducerID = strings.TrimSpace(config.ProducerID)
	config.Topic = strings.TrimSpace(config.Topic)
	if config.ProducerID == "" || config.Topic == "" || config.Producer == nil || config.MaxMessageBytes < 1 {
		return nil, errors.New("Agent Trace Kafka Sink configuration is incomplete")
	}
	if config.Observer == nil {
		config.Observer = noopKafkaTraceObserver{}
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewBatchID == nil {
		config.NewBatchID = uuid.NewString
	}
	produceCtx, cancel := context.WithCancel(context.Background())
	return &KafkaTraceSink{
		producerID: config.ProducerID, topic: config.Topic, producer: config.Producer,
		maxMessageBytes: config.MaxMessageBytes, observer: config.Observer, logger: config.Logger,
		now: config.Now, newBatchID: config.NewBatchID, produceCtx: produceCtx, cancel: cancel,
		done: make(chan struct{}),
	}, nil
}

func (s *KafkaTraceSink) Offer(_ context.Context, envelope Envelope) error {
	if s == nil || s.producer == nil {
		return errors.New("nil Agent Trace Kafka Sink")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closing {
		s.observer.KafkaTraceOfferRejected(KafkaTraceShutdown)
		return ErrShutdown
	}
	if _, err := validateEnvelope(envelope); err != nil {
		s.observer.KafkaTraceOfferRejected(KafkaTraceInvalid)
		return err
	}
	hash, err := envelope.Record.CanonicalHash()
	if err != nil {
		s.observer.KafkaTraceOfferRejected(KafkaTraceInvalid)
		return err
	}
	chunk := collector.TraceChunk{
		Trace: envelope.Trace, SequenceAuthority: collector.SequenceAuthorityCollector,
		Records:     []collector.SequencedRecord{{Record: envelope.Record, CanonicalSHA256: hex.EncodeToString(hash[:])}},
		Attachments: append([]collector.AttachmentDescriptor(nil), envelope.Attachments...),
	}
	if err := collector.ValidateDirectTraceChunk(chunk); err != nil {
		s.observer.KafkaTraceOfferRejected(KafkaTraceInvalid)
		return err
	}
	encoded, err := json.Marshal(KafkaTraceEnvelope{
		SchemaVersion: 1, BatchID: s.newBatchID(), ProducerID: s.producerID,
		CreatedAt: s.now().UTC(), Chunk: chunk,
	})
	if err != nil {
		s.observer.KafkaTraceOfferRejected(KafkaTraceInvalid)
		return fmt.Errorf("encode Agent Trace Kafka envelope: %w", err)
	}
	if len(encoded) > s.maxMessageBytes {
		s.observer.KafkaTraceOfferRejected(KafkaTraceMessageTooLarge)
		return fmt.Errorf("%w: encoded bytes=%d limit=%d", ErrKafkaTraceMessageTooLarge, len(encoded), s.maxMessageBytes)
	}

	submittedAt := s.now()
	s.producer.TryProduce(s.produceCtx, KafkaMessage{
		Topic: s.topic, Key: []byte(chunk.Trace.TraceID), Value: encoded,
	}, func(deliveryErr error) {
		result := classifyKafkaTraceDelivery(deliveryErr)
		s.observer.KafkaTraceDelivery(result, s.now().Sub(submittedAt))
		s.observeBuffered()
		if deliveryErr != nil {
			s.logDeliveryFailure(result, deliveryErr)
		}
	})
	s.observeBuffered()
	return nil
}

func (s *KafkaTraceSink) Flush(ctx context.Context) error {
	if s == nil || s.producer == nil {
		return errors.New("nil Agent Trace Kafka Sink")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.producer.Flush(ctx)
}

func (s *KafkaTraceSink) ForceFlush(ctx context.Context) error { return s.Flush(ctx) }

func (s *KafkaTraceSink) Shutdown(ctx context.Context) error {
	if s == nil || s.producer == nil {
		return errors.New("nil Agent Trace Kafka Sink")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closing {
		done := s.done
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.RLock()
			err := s.shutdownErr
			s.mu.RUnlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.closing = true
	s.mu.Unlock()

	err := s.producer.Flush(ctx)
	if err != nil {
		s.observer.KafkaTraceShutdownFailure()
	}
	s.cancel()
	s.producer.Close()
	s.observeBuffered()
	s.mu.Lock()
	s.shutdownErr = err
	close(s.done)
	s.mu.Unlock()
	return err
}

func (s *KafkaTraceSink) observeBuffered() {
	s.observer.KafkaTraceBuffered(s.producer.BufferedRecords(), s.producer.BufferedBytes())
}

func (s *KafkaTraceSink) logDeliveryFailure(result KafkaTraceDeliveryResult, err error) {
	count := s.failureLogs.Add(1)
	if count == 1 || count%1000 == 0 {
		s.logger.Warn("Agent Trace Kafka delivery failed", "result", result, "error", err, "failures_observed", count)
	}
}

func classifyKafkaTraceDelivery(err error) KafkaTraceDeliveryResult {
	switch {
	case err == nil:
		return KafkaTraceAcknowledged
	case errors.Is(err, kgo.ErrMaxBuffered):
		return KafkaTraceBufferFull
	case errors.Is(err, kgo.ErrRecordTimeout):
		return KafkaTraceTimedOut
	default:
		return KafkaTraceFailed
	}
}

type noopKafkaTraceObserver struct{}

func (noopKafkaTraceObserver) KafkaTraceOfferRejected(KafkaTraceRejectionReason)          {}
func (noopKafkaTraceObserver) KafkaTraceDelivery(KafkaTraceDeliveryResult, time.Duration) {}
func (noopKafkaTraceObserver) KafkaTraceBuffered(int64, int64)                            {}
func (noopKafkaTraceObserver) KafkaTraceShutdownFailure()                                 {}
