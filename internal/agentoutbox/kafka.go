package agentoutbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

type KafkaPurgeEnvelope struct {
	SchemaVersion int                    `json:"schema_version"`
	BatchID       string                 `json:"batch_id"`
	ProducerID    string                 `json:"producer_id"`
	CreatedAt     time.Time              `json:"created_at"`
	Command       collector.PurgeCommand `json:"command"`
}

type KafkaPurgeSenderStore interface {
	ClaimPurgeBatch(context.Context) (ClaimedPurgeBatch, bool, error)
	MarkPurgePublished(context.Context, ClaimedPurgeBatch) error
	ReleasePurgeBatch(context.Context, ClaimedPurgeBatch, string) error
}

type KafkaPurgeSenderConfig struct {
	Topic       string
	Producer    agentbatch.KafkaProducer
	ReportError func(error)
}

type KafkaPurgeSender struct {
	store       KafkaPurgeSenderStore
	topic       string
	producer    agentbatch.KafkaProducer
	reportError func(error)
}

func NewKafkaPurgeSender(store KafkaPurgeSenderStore, config KafkaPurgeSenderConfig) (*KafkaPurgeSender, error) {
	config.Topic = strings.TrimSpace(config.Topic)
	if store == nil || config.Topic == "" || config.Producer == nil {
		return nil, errors.New("Kafka purge Outbox Sender configuration is incomplete")
	}
	return &KafkaPurgeSender{store: store, topic: config.Topic, producer: config.Producer, reportError: config.ReportError}, nil
}

func (s *KafkaPurgeSender) SendOnce(ctx context.Context) (bool, error) {
	claimed, ok, err := s.store.ClaimPurgeBatch(ctx)
	if err != nil || !ok {
		return false, err
	}
	messages := make([]agentbatch.KafkaMessage, len(claimed.Batch.Commands))
	for index, command := range claimed.Batch.Commands {
		encoded, err := json.Marshal(KafkaPurgeEnvelope{
			SchemaVersion: 1, BatchID: claimed.Batch.BatchID, ProducerID: claimed.Batch.ProducerID,
			CreatedAt: claimed.Batch.CreatedAt.UTC(), Command: command,
		})
		if err != nil {
			return s.retryClaim(ctx, claimed, fmt.Errorf("encode Kafka purge command: %w", err))
		}
		messages[index] = agentbatch.KafkaMessage{Topic: s.topic, Key: []byte(command.TraceID), Value: encoded}
	}
	produceErrors := s.producer.ProduceSync(ctx, messages)
	if len(produceErrors) != 0 && len(produceErrors) != len(messages) {
		return s.retryClaim(ctx, claimed, errors.New("Kafka purge producer returned an invalid acknowledgement set"))
	}
	for index, err := range produceErrors {
		if err != nil {
			return s.retryClaim(ctx, claimed, fmt.Errorf("Kafka did not acknowledge purge for Trace %s: %w", claimed.Batch.Commands[index].TraceID, err))
		}
	}
	if err := s.store.MarkPurgePublished(ctx, claimed); err != nil {
		return true, fmt.Errorf("mark Kafka purge commands published: %w", err)
	}
	return true, nil
}

func (s *KafkaPurgeSender) retryClaim(ctx context.Context, claimed ClaimedPurgeBatch, cause error) (bool, error) {
	if err := s.store.ReleasePurgeBatch(ctx, claimed, CodeTransportFailure); err != nil {
		return true, errors.Join(cause, fmt.Errorf("release failed Kafka purge Batch: %w", err))
	}
	return true, cause
}

func (s *KafkaPurgeSender) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		return errors.New("Kafka purge Outbox Sender poll interval must be positive")
	}
	for {
		attempted, err := s.SendOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && s.reportError != nil {
			s.reportError(err)
		}
		if attempted && err == nil {
			continue
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (s *KafkaPurgeSender) ForceFlush(ctx context.Context) error {
	for {
		attempted, err := s.SendOnce(ctx)
		if err != nil {
			return err
		}
		if !attempted {
			return nil
		}
	}
}

func DecodeKafkaPurgeEnvelope(encoded []byte) (KafkaPurgeEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var envelope KafkaPurgeEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return KafkaPurgeEnvelope{}, fmt.Errorf("decode Agent Trace purge Kafka envelope: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return KafkaPurgeEnvelope{}, errors.New("Agent Trace purge Kafka envelope has trailing data")
	}
	command := envelope.Command
	if envelope.SchemaVersion != 1 || strings.TrimSpace(envelope.BatchID) == "" || strings.TrimSpace(envelope.ProducerID) == "" ||
		envelope.CreatedAt.IsZero() || strings.TrimSpace(command.CommandID) == "" || command.CommandVersion != 1 ||
		command.Kind != collector.CommandPurgeTrace || strings.TrimSpace(string(command.TraceID)) == "" ||
		strings.TrimSpace(command.RunID) == "" || command.RequestedAt.IsZero() {
		return KafkaPurgeEnvelope{}, errors.New("Agent Trace purge Kafka envelope is incomplete")
	}
	return envelope, nil
}
