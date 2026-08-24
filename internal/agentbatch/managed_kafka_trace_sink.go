package agentbatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	DefaultKafkaTraceMaxBufferedRecords = 10_000
	DefaultKafkaTraceMaxBufferedBytes   = 32 * 1024 * 1024
	DefaultKafkaTraceMaxMessageBytes    = 512 * 1024
	DefaultKafkaTraceDeliveryTimeout    = 10 * time.Second
	DefaultKafkaTraceLinger             = 5 * time.Millisecond
	DefaultKafkaTraceReadinessTimeout   = 10 * time.Second
)

type ManagedKafkaTraceSinkConfig struct {
	ProducerID         string
	Brokers            []string
	Topic              string
	ClientID           string
	MaxBufferedRecords int
	MaxBufferedBytes   int
	DeliveryTimeout    time.Duration
	Linger             time.Duration
	ReadinessTimeout   time.Duration
	MaxMessageBytes    int
	Observer           KafkaTraceObserver
	Logger             *slog.Logger

	newKafkaClient func(FranzKafkaConfig) (managedKafkaTraceProducer, error)
}

type managedKafkaTraceProducer interface {
	KafkaTraceProducer
	Ping(context.Context) error
}

func NewManagedKafkaTraceSink(ctx context.Context, config ManagedKafkaTraceSinkConfig) (*KafkaTraceSink, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.ReadinessTimeout == 0 {
		config.ReadinessTimeout = DefaultKafkaTraceReadinessTimeout
	}
	if config.ReadinessTimeout < 0 {
		return nil, errors.New("Agent Trace Kafka readiness timeout must be positive")
	}
	factory := config.newKafkaClient
	if factory == nil {
		factory = func(franzConfig FranzKafkaConfig) (managedKafkaTraceProducer, error) {
			return NewFranzKafkaProducer(franzConfig)
		}
	}
	client, err := factory(FranzKafkaConfig{
		Brokers: config.Brokers, ClientID: config.ClientID,
		MaxBufferedRecords: config.MaxBufferedRecords, MaxBufferedBytes: config.MaxBufferedBytes,
		DeliveryTimeout: config.DeliveryTimeout, Linger: config.Linger,
	})
	if err != nil {
		return nil, fmt.Errorf("create Agent Trace Kafka producer: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, config.ReadinessTimeout)
	err = client.Ping(readyCtx)
	cancel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("check Agent Trace Kafka readiness: %w", err)
	}
	sink, err := NewKafkaTraceSink(KafkaTraceSinkConfig{
		ProducerID: config.ProducerID, Topic: config.Topic, Producer: client,
		MaxMessageBytes: config.MaxMessageBytes, Observer: config.Observer, Logger: config.Logger,
	})
	if err != nil {
		client.Close()
		return nil, err
	}
	return sink, nil
}
