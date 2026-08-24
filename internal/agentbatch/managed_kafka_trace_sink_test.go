package agentbatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewManagedKafkaTraceSinkChecksReadinessAndOwnsProducerShutdown(t *testing.T) {
	client := &managedKafkaTraceClient{}
	sink, err := NewManagedKafkaTraceSink(context.Background(), ManagedKafkaTraceSinkConfig{
		ProducerID: "nano-worker/test", Brokers: []string{"kafka-a:9092"},
		Topic: "nano.observability.agent-trace.v1", ClientID: "nano-worker-agent-trace",
		MaxBufferedRecords: 10_000, MaxBufferedBytes: 32 * 1024 * 1024,
		DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond, MaxMessageBytes: 512 * 1024,
		newKafkaClient: func(FranzKafkaConfig) (managedKafkaTraceProducer, error) { return client, nil },
	})
	if err != nil {
		t.Fatalf("NewManagedKafkaTraceSink: %v", err)
	}
	if client.pingCalls != 1 {
		t.Fatalf("Kafka readiness calls=%d want=1", client.pingCalls)
	}
	if err := sink.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.flushCalls != 1 || client.closeCalls != 1 {
		t.Fatalf("flush=%d close=%d", client.flushCalls, client.closeCalls)
	}
}

func TestNewManagedKafkaTraceSinkClosesProducerWhenReadinessFails(t *testing.T) {
	wantErr := errors.New("broker unavailable")
	client := &managedKafkaTraceClient{pingErr: wantErr}
	_, err := NewManagedKafkaTraceSink(context.Background(), ManagedKafkaTraceSinkConfig{
		ProducerID: "nano-control-plane/test", Brokers: []string{"kafka-a:9092"},
		Topic: "nano.observability.agent-trace.v1", ClientID: "nano-control-plane-agent-trace",
		MaxBufferedRecords: 10_000, MaxBufferedBytes: 32 * 1024 * 1024,
		DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond, MaxMessageBytes: 512 * 1024,
		newKafkaClient: func(FranzKafkaConfig) (managedKafkaTraceProducer, error) { return client, nil },
	})
	if !errors.Is(err, wantErr) || client.closeCalls != 1 {
		t.Fatalf("readiness error=%v close=%d", err, client.closeCalls)
	}
}

type managedKafkaTraceClient struct {
	pingCalls  int
	flushCalls int
	closeCalls int
	pingErr    error
}

func (*managedKafkaTraceClient) TryProduce(context.Context, KafkaMessage, func(error)) {}
func (c *managedKafkaTraceClient) Flush(context.Context) error {
	c.flushCalls++
	return nil
}
func (*managedKafkaTraceClient) BufferedRecords() int64 { return 0 }
func (*managedKafkaTraceClient) BufferedBytes() int64   { return 0 }
func (c *managedKafkaTraceClient) Ping(context.Context) error {
	c.pingCalls++
	return c.pingErr
}
func (c *managedKafkaTraceClient) Close() { c.closeCalls++ }
