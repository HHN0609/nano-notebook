package agenttraceprocessor_test

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agenttraceprocessor"
)

func TestNewFranzConsumerRequiresBoundedGroupConfiguration(t *testing.T) {
	if _, err := agenttraceprocessor.NewFranzConsumer(agenttraceprocessor.FranzConsumerConfig{}); err == nil {
		t.Fatal("empty Kafka consumer configuration was accepted")
	}
	consumer, err := agenttraceprocessor.NewFranzConsumer(agenttraceprocessor.FranzConsumerConfig{
		Brokers: []string{"127.0.0.1:59092"}, Topic: traceTopic, PurgeTopic: "nano.observability.agent-trace-purge.v1",
		GroupID: "nano-agent-trace-storage-v1", ClientID: "nano-agent-trace-processor-test",
		MaxPollRecords: 64, FetchMaxBytes: 2 * 1024 * 1024,
		FetchMaxWait: 100 * time.Millisecond, SessionTimeout: 10 * time.Second, RebalanceTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer.Close()
}
