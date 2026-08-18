package agentbatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

type TraceTransport string

const (
	TraceTransportKafka TraceTransport = "kafka"
	TraceTransportHTTP  TraceTransport = "http"
)

// ManagedKafkaConfig owns the bounded producer and topic configuration used by
// a long-running Nano process.
type ManagedKafkaConfig struct {
	Brokers            []string
	Topic              string
	ClientID           string
	MaxBufferedRecords int
	MaxBufferedBytes   int
	DeliveryTimeout    time.Duration
	Linger             time.Duration
	ReadinessTimeout   time.Duration
}

// ManagedSenderConfig selects the production Agent Trace delivery transport.
// An empty Transport intentionally means Kafka, the product default.
type ManagedSenderConfig struct {
	Transport TraceTransport
	HTTP      HTTPSenderConfig
	Kafka     ManagedKafkaConfig

	newKafkaClient func(FranzKafkaConfig) (managedKafkaProducer, error)
}

type managedKafkaProducer interface {
	KafkaProducer
	Ping(context.Context) error
	Close()
}

// ManagedSender keeps transport lifecycle outside the bounded Exporter while
// still satisfying its small Sender interface.
type ManagedSender struct {
	sender    Sender
	closeOnce sync.Once
	close     func()
}

func NewManagedSender(ctx context.Context, config ManagedSenderConfig) (*ManagedSender, error) {
	transport := TraceTransport(strings.ToLower(strings.TrimSpace(string(config.Transport))))
	if transport == "" {
		transport = TraceTransportKafka
	}
	switch transport {
	case TraceTransportHTTP:
		sender, err := NewHTTPSender(config.HTTP)
		if err != nil {
			return nil, err
		}
		return &ManagedSender{sender: sender, close: func() {}}, nil
	case TraceTransportKafka:
		return newManagedKafkaSender(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported Agent Trace transport %q", transport)
	}
}

func newManagedKafkaSender(ctx context.Context, config ManagedSenderConfig) (*ManagedSender, error) {
	kafka := config.Kafka
	kafka.Topic = strings.TrimSpace(kafka.Topic)
	if kafka.ReadinessTimeout == 0 {
		kafka.ReadinessTimeout = 10 * time.Second
	}
	if kafka.ReadinessTimeout < 0 {
		return nil, errors.New("Agent Trace Kafka readiness timeout must be positive")
	}
	factory := config.newKafkaClient
	if factory == nil {
		factory = func(franzConfig FranzKafkaConfig) (managedKafkaProducer, error) {
			return NewFranzKafkaProducer(franzConfig)
		}
	}
	client, err := factory(FranzKafkaConfig{
		Brokers: kafka.Brokers, ClientID: kafka.ClientID,
		MaxBufferedRecords: kafka.MaxBufferedRecords, MaxBufferedBytes: kafka.MaxBufferedBytes,
		DeliveryTimeout: kafka.DeliveryTimeout, Linger: kafka.Linger,
	})
	if err != nil {
		return nil, fmt.Errorf("create Agent Trace Kafka producer: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, kafka.ReadinessTimeout)
	err = client.Ping(readyCtx)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("check Agent Trace Kafka readiness: %w", err)
	}
	sender, err := NewKafkaSender(KafkaSenderConfig{Topic: kafka.Topic, Producer: client})
	if err != nil {
		client.Close()
		return nil, err
	}
	return &ManagedSender{sender: sender, close: client.Close}, nil
}

func (s *ManagedSender) Send(ctx context.Context, batch collector.Batch) (collector.BatchResult, error) {
	if s == nil || s.sender == nil {
		return collector.BatchResult{}, errors.New("nil managed Agent Trace Sender")
	}
	return s.sender.Send(ctx, batch)
}

func (s *ManagedSender) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
}
