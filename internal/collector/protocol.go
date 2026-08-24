package collector

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

const (
	ProtocolVersion       = 1
	DirectProtocolVersion = 2
)

type SequenceAuthority string

type WorkloadKind string

const (
	WorkloadAgentRun         WorkloadKind = "agent_run"
	WorkloadSourceProcessing WorkloadKind = "source_processing"
)

const (
	SequenceAuthorityProducer  SequenceAuthority = "producer"
	SequenceAuthorityCollector SequenceAuthority = "collector"
)

const (
	SupportedRecordSchemaVersion = 1
	SupportedSemanticConvention  = 1
)

type ChunkStatus string

const (
	ChunkCommitted ChunkStatus = "committed"
	ChunkRejected  ChunkStatus = "rejected"
	ChunkRetryable ChunkStatus = "retryable"
)

const (
	CodeIdentityConflict      = "identity_conflict"
	CodeCanonicalHash         = "canonical_hash_mismatch"
	CodeInvalidChunk          = "invalid_chunk"
	CodeInvalidLifecycle      = "invalid_lifecycle"
	CodeSequenceGap           = "sequence_gap"
	CodeDependencyMissing     = "dependency_missing"
	CodeTombstoned            = "tombstoned"
	CodeUnsupportedSchema     = "unsupported_schema"
	CodeAttachmentUnavailable = "attachment_unavailable"
	CodeAttachmentIntegrity   = "attachment_integrity"
)

type Batch struct {
	ProtocolVersion int          `json:"protocol_version"`
	BatchID         string       `json:"batch_id"`
	ProducerID      string       `json:"producer_id"`
	CreatedAt       time.Time    `json:"created_at"`
	Chunks          []TraceChunk `json:"chunks"`
}

type TraceDescriptor struct {
	TraceID                   agentobs.TraceID `json:"trace_id"`
	WorkloadKind              WorkloadKind     `json:"workload_kind,omitempty"`
	WorkloadID                string           `json:"workload_id,omitempty"`
	RunID                     string           `json:"run_id"`
	ChatID                    string           `json:"chat_id"`
	NotebookID                string           `json:"notebook_id"`
	RootSpanID                agentobs.SpanID  `json:"root_span_id"`
	AgentName                 string           `json:"agent_name"`
	SchemaVersion             int              `json:"schema_version"`
	SemanticConventionVersion int              `json:"semantic_convention_version"`
}

type TraceChunk struct {
	Trace             TraceDescriptor        `json:"trace"`
	SequenceAuthority SequenceAuthority      `json:"sequence_authority,omitempty"`
	FirstSequence     int                    `json:"first_sequence"`
	Records           []SequencedRecord      `json:"records"`
	Attachments       []AttachmentDescriptor `json:"attachments,omitempty"`
}

type AttachmentDescriptor struct {
	AttachmentID      string       `json:"attachment_id"`
	RecordSequence    int          `json:"record_sequence"`
	RecordIdentityKey string       `json:"record_identity_key,omitempty"`
	Class             replay.Class `json:"class"`
	SchemaVersion     int          `json:"schema_version"`
	PlaintextSHA256   string       `json:"plaintext_sha256"`
	StagingObjectKey  string       `json:"staging_object_key"`
	CiphertextBytes   int          `json:"ciphertext_bytes"`
	CiphertextSHA256  string       `json:"ciphertext_sha256"`
	Compression       string       `json:"compression"`
	Encryption        string       `json:"encryption"`
	KeyID             string       `json:"key_id"`
	WrappedKey        []byte       `json:"wrapped_key"`
	Nonce             []byte       `json:"nonce"`
	ExpiresAt         time.Time    `json:"expires_at"`
}

type SequencedRecord struct {
	Sequence        int             `json:"-"`
	Record          agentobs.Record `json:"-"`
	CanonicalSHA256 string          `json:"-"`
}

// ValidateDirectTraceChunk validates a producer-built chunk whose final
// sequence numbers are assigned by the retained store.
func ValidateDirectTraceChunk(chunk TraceChunk) error {
	if _, err := CanonicalTraceDescriptor(chunk.Trace); err != nil {
		return err
	}
	if chunk.SequenceAuthority != SequenceAuthorityCollector || chunk.FirstSequence != 0 || len(chunk.Records) == 0 {
		return errors.New("Collector direct Trace Chunk is empty or has producer-owned sequencing")
	}
	for _, envelope := range chunk.Records {
		if err := envelope.Record.Validate(); err != nil {
			return err
		}
		if envelope.Record.TraceID != chunk.Trace.TraceID || envelope.Record.SchemaVersion != chunk.Trace.SchemaVersion ||
			envelope.Record.SemanticConventionVersion != chunk.Trace.SemanticConventionVersion {
			return errors.New("Collector direct Trace Record changed its envelope")
		}
		hash, err := envelope.Record.CanonicalHash()
		if err != nil || envelope.CanonicalSHA256 != hex.EncodeToString(hash[:]) {
			return errors.New("Collector direct Trace Record canonical hash is invalid")
		}
	}
	// Direct producers identify Replay attachments by immutable Record identity;
	// the retained store assigns final sequence numbers. Validate against a
	// temporary sequence projection without mutating the wire descriptor.
	validationChunk := chunk
	validationChunk.Attachments = append([]AttachmentDescriptor(nil), chunk.Attachments...)
	sequences := make(map[string]int, len(chunk.Records))
	for index, envelope := range chunk.Records {
		sequences[envelope.Record.IdentityKey] = index + 1
	}
	for index := range validationChunk.Attachments {
		if validationChunk.Attachments[index].RecordSequence == 0 {
			validationChunk.Attachments[index].RecordSequence = sequences[validationChunk.Attachments[index].RecordIdentityKey]
		}
	}
	return validateAttachmentDescriptors(validationChunk)
}

type BatchResult struct {
	BatchID string        `json:"batch_id"`
	Chunks  []ChunkResult `json:"chunks"`
}

type ChunkResult struct {
	TraceID          agentobs.TraceID `json:"trace_id"`
	Status           ChunkStatus      `json:"status"`
	CommittedThrough int              `json:"committed_through"`
	Code             string           `json:"code,omitempty"`
}

type ChunkError struct {
	Code             string
	CommittedThrough int
	Retryable        bool
	Err              error
}

func (e *ChunkError) Error() string {
	if e == nil || e.Err == nil {
		return "Collector Trace Chunk rejected"
	}
	return e.Err.Error()
}

func (e *ChunkError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Store interface {
	CommitTraceChunk(context.Context, TraceChunk) (int, error)
}
