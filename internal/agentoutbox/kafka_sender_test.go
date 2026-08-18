package agentoutbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agentoutbox"
)

func TestKafkaPurgeSenderPublishesOneTraceKeyedCommandAndMarksPublished(t *testing.T) {
	claimed := purgeClaimFixture()
	second := claimed.Batch.Commands[0]
	second.CommandID, second.TraceID, second.RunID = "purge/trace-second", "trace-second", "run-second"
	claimed.Batch.Commands = append(claimed.Batch.Commands, second)
	store := &purgeSenderStore{claimed: claimed, claimOK: true}
	producer := &purgeKafkaProducer{}
	sender, err := agentoutbox.NewKafkaPurgeSender(store, agentoutbox.KafkaPurgeSenderConfig{
		Topic: "nano.observability.agent-trace-purge.v1", Producer: producer,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempted, err := sender.SendOnce(context.Background())
	if err != nil || !attempted || store.publishedCalls != 1 || store.applyCalls != 0 || store.releaseCalls != 0 {
		t.Fatalf("SendOnce attempted=%t err=%v published=%d apply=%d release=%d", attempted, err, store.publishedCalls, store.applyCalls, store.releaseCalls)
	}
	if len(producer.messages) != 2 || string(producer.messages[0].Key) != "trace-sender" || string(producer.messages[1].Key) != "trace-second" {
		t.Fatalf("Kafka purge messages=%#v", producer.messages)
	}
	for index, message := range producer.messages {
		envelope, err := agentoutbox.DecodeKafkaPurgeEnvelope(message.Value)
		if err != nil {
			t.Fatalf("decode purge message %d: %v", index, err)
		}
		if envelope.SchemaVersion != 1 || envelope.BatchID != claimed.Batch.BatchID || envelope.ProducerID != claimed.Batch.ProducerID ||
			envelope.Command.TraceID != claimed.Batch.Commands[index].TraceID {
			t.Fatalf("purge envelope %d=%#v", index, envelope)
		}
	}
}

func TestKafkaPurgeSenderReleasesWholeClaimAfterPartialBrokerACK(t *testing.T) {
	store := &purgeSenderStore{claimed: purgeClaimFixture(), claimOK: true}
	producer := &purgeKafkaProducer{errors: []error{errors.New("broker unavailable")}}
	sender, err := agentoutbox.NewKafkaPurgeSender(store, agentoutbox.KafkaPurgeSenderConfig{
		Topic: "nano.observability.agent-trace-purge.v1", Producer: producer,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempted, err := sender.SendOnce(context.Background())
	if err == nil || !attempted || store.publishedCalls != 0 || store.releaseCalls != 1 || store.releaseCode != agentoutbox.CodeTransportFailure {
		t.Fatalf("SendOnce attempted=%t err=%v published=%d release=%d code=%q", attempted, err, store.publishedCalls, store.releaseCalls, store.releaseCode)
	}
}

type purgeKafkaProducer struct {
	messages []agentbatch.KafkaMessage
	errors   []error
}

func (p *purgeKafkaProducer) ProduceSync(_ context.Context, messages []agentbatch.KafkaMessage) []error {
	p.messages = append([]agentbatch.KafkaMessage(nil), messages...)
	return append([]error(nil), p.errors...)
}
