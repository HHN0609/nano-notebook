package agenttraceprocessor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
)

type KafkaQuarantineConfig struct {
	Topic    string
	Producer agentbatch.KafkaProducer
}

type KafkaQuarantineWriter struct {
	topic    string
	producer agentbatch.KafkaProducer
}

func NewKafkaQuarantineWriter(config KafkaQuarantineConfig) (*KafkaQuarantineWriter, error) {
	config.Topic = strings.TrimSpace(config.Topic)
	if config.Topic == "" || config.Producer == nil {
		return nil, errors.New("Kafka quarantine configuration is incomplete")
	}
	return &KafkaQuarantineWriter{topic: config.Topic, producer: config.Producer}, nil
}

func (w *KafkaQuarantineWriter) Write(ctx context.Context, envelope QuarantineEnvelope) error {
	if w == nil || w.producer == nil {
		return errors.New("nil Kafka quarantine writer")
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode quarantine envelope: %w", err)
	}
	key := fmt.Sprintf("%s/%d/%d", envelope.SourceTopic, envelope.SourcePartition, envelope.SourceOffset)
	errorsByMessage := w.producer.ProduceSync(ctx, []agentbatch.KafkaMessage{{
		Topic: w.topic, Key: []byte(key), Value: encoded,
	}})
	if len(errorsByMessage) == 0 {
		return nil
	}
	if len(errorsByMessage) != 1 {
		return errors.New("Kafka quarantine producer returned an invalid acknowledgement set")
	}
	if errorsByMessage[0] != nil {
		return fmt.Errorf("Kafka did not acknowledge quarantine envelope: %w", errorsByMessage[0])
	}
	return nil
}
