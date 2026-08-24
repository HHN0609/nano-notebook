package agentbatch

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

// SynchronousTraceSink is a queue-free benchmark/test adapter. Product
// processes use KafkaTraceSink instead.
type SynchronousTraceSink struct {
	producerID string
	sender     Sender
}

func NewSynchronousTraceSink(producerID string, sender Sender) (*SynchronousTraceSink, error) {
	producerID = strings.TrimSpace(producerID)
	if producerID == "" || sender == nil {
		return nil, errors.New("synchronous Agent Trace Sink configuration is incomplete")
	}
	return &SynchronousTraceSink{producerID: producerID, sender: sender}, nil
}

func (s *SynchronousTraceSink) Offer(ctx context.Context, envelope Envelope) error {
	if s == nil || s.sender == nil {
		return errors.New("nil synchronous Agent Trace Sink")
	}
	if _, err := validateEnvelope(envelope); err != nil {
		return err
	}
	hash, err := envelope.Record.CanonicalHash()
	if err != nil {
		return err
	}
	chunk := collector.TraceChunk{
		Trace: envelope.Trace, SequenceAuthority: collector.SequenceAuthorityCollector,
		Records:     []collector.SequencedRecord{{Record: envelope.Record, CanonicalSHA256: hex.EncodeToString(hash[:])}},
		Attachments: append([]collector.AttachmentDescriptor(nil), envelope.Attachments...),
	}
	if err := collector.ValidateDirectTraceChunk(chunk); err != nil {
		return err
	}
	batch := collector.Batch{
		ProtocolVersion: collector.DirectProtocolVersion, BatchID: uuid.NewString(),
		ProducerID: s.producerID, CreatedAt: time.Now().UTC(), Chunks: []collector.TraceChunk{chunk},
	}
	result, err := s.sender.Send(ctx, batch)
	if err != nil {
		return err
	}
	return validateResult(batch, result)
}

func (*SynchronousTraceSink) ForceFlush(context.Context) error { return nil }
