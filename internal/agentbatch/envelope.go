package agentbatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

var ErrShutdown = errors.New("Agent Trace Kafka Sink is shut down")

type Envelope struct {
	Trace       collector.TraceDescriptor
	Record      agentobs.Record
	Attachments []collector.AttachmentDescriptor
}

// Sender is retained for synchronous benchmark/test adapters only.
type Sender interface {
	Send(context.Context, collector.Batch) (collector.BatchResult, error)
}

func validateEnvelope(envelope Envelope) (int, error) {
	if _, err := collector.CanonicalTraceDescriptor(envelope.Trace); err != nil {
		return 0, errors.New("Agent Trace envelope descriptor is invalid")
	}
	if err := envelope.Record.Validate(); err != nil {
		return 0, err
	}
	if envelope.Record.TraceID != envelope.Trace.TraceID || envelope.Record.SchemaVersion != envelope.Trace.SchemaVersion ||
		envelope.Record.SemanticConventionVersion != envelope.Trace.SemanticConventionVersion {
		return 0, errors.New("Agent Trace record changed its envelope")
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return 0, err
	}
	return len(encoded), nil
}

func validateResult(batch collector.Batch, result collector.BatchResult) error {
	if result.BatchID != batch.BatchID || len(result.Chunks) != len(batch.Chunks) {
		return newDeliveryError(false, errors.New("Collector Batch result does not match the request"))
	}
	for index, chunk := range result.Chunks {
		if chunk.TraceID != batch.Chunks[index].Trace.TraceID {
			return newDeliveryError(false, errors.New("Collector Batch result does not match the request"))
		}
		switch chunk.Status {
		case collector.ChunkCommitted:
		case collector.ChunkRetryable:
			return newDeliveryError(true, fmt.Errorf("Collector asked to retry Trace %s: %s", chunk.TraceID, chunk.Code))
		default:
			return newDeliveryError(false, fmt.Errorf("Collector rejected Trace %s: %s", chunk.TraceID, chunk.Code))
		}
	}
	return nil
}
