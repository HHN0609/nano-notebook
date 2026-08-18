package agentbatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewManagedSenderDefaultsToKafkaChecksReadinessAndClosesOnce(t *testing.T) {
	client := &managedKafkaClient{}
	managed, err := NewManagedSender(context.Background(), ManagedSenderConfig{
		Kafka: ManagedKafkaConfig{
			Brokers: []string{"kafka-a:9092"}, Topic: "nano.observability.agent-trace.v1", ClientID: "nano-worker-agent-trace",
			MaxBufferedRecords: 10_000, MaxBufferedBytes: 32 * 1024 * 1024,
			DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond,
		},
		newKafkaClient: func(FranzKafkaConfig) (managedKafkaProducer, error) { return client, nil },
	})
	if err != nil {
		t.Fatalf("NewManagedSender: %v", err)
	}
	if client.pingCalls != 1 {
		t.Fatalf("Kafka readiness calls = %d, want 1", client.pingCalls)
	}
	managed.Close()
	managed.Close()
	if client.closeCalls != 1 {
		t.Fatalf("Kafka close calls = %d, want 1", client.closeCalls)
	}
}

func TestNewManagedSenderClosesKafkaClientWhenReadinessFails(t *testing.T) {
	wantErr := errors.New("broker unavailable")
	client := &managedKafkaClient{pingErr: wantErr}
	_, err := NewManagedSender(context.Background(), ManagedSenderConfig{
		Transport: TraceTransportKafka,
		Kafka: ManagedKafkaConfig{
			Brokers: []string{"kafka-a:9092"}, Topic: "nano.observability.agent-trace.v1", ClientID: "nano-control-plane-agent-trace",
			MaxBufferedRecords: 10_000, MaxBufferedBytes: 32 * 1024 * 1024,
			DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond,
		},
		newKafkaClient: func(FranzKafkaConfig) (managedKafkaProducer, error) { return client, nil },
	})
	if !errors.Is(err, wantErr) || client.closeCalls != 1 {
		t.Fatalf("readiness error = %v, close calls = %d", err, client.closeCalls)
	}
}

func TestNewManagedSenderSupportsExplicitHTTPAndRejectsUnknownTransport(t *testing.T) {
	managed, err := NewManagedSender(context.Background(), ManagedSenderConfig{
		Transport: TraceTransportHTTP,
		HTTP:      HTTPSenderConfig{Endpoint: "http://collector:8082/internal/agent-observability/v2/batches", ServiceToken: "secret"},
	})
	if err != nil {
		t.Fatalf("NewManagedSender HTTP: %v", err)
	}
	managed.Close()
	if _, err := NewManagedSender(context.Background(), ManagedSenderConfig{Transport: "udp"}); err == nil {
		t.Fatal("NewManagedSender accepted unknown transport")
	}
}

type managedKafkaClient struct {
	pingCalls  int
	closeCalls int
	pingErr    error
}

func (*managedKafkaClient) ProduceSync(context.Context, []KafkaMessage) []error { return nil }

func (c *managedKafkaClient) Ping(context.Context) error {
	c.pingCalls++
	return c.pingErr
}

func (c *managedKafkaClient) Close() { c.closeCalls++ }
