package agentbatch_test

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
)

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
