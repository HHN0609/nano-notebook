package collector

import (
	"bytes"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

type StoredTrace struct {
	Trace            TraceDescriptor
	CommittedThrough int
	ProjectedThrough int
	Tombstoned       bool
	Records          []SequencedRecord
}

type preparedAttachment struct {
	descriptor AttachmentDescriptor
	objectKey  string
}

type storedPayloadRef struct {
	attachmentID     string
	traceID          agentobs.TraceID
	recordSequence   int
	class            replay.Class
	schemaVersion    int
	plaintextSHA256  string
	objectKey        string
	ciphertextBytes  int
	ciphertextSHA256 string
	compression      string
	encryption       string
	keyID            string
	wrappedKey       []byte
	nonce            []byte
	expiresAtNano    int64
}

func reconcilePayloadRef(stored storedPayloadRef, traceID agentobs.TraceID, descriptor AttachmentDescriptor, objectKey string) error {
	if stored.attachmentID != descriptor.AttachmentID || stored.traceID != traceID || stored.recordSequence != descriptor.RecordSequence ||
		stored.class != descriptor.Class || stored.schemaVersion != descriptor.SchemaVersion ||
		stored.plaintextSHA256 != descriptor.PlaintextSHA256 || stored.objectKey != objectKey ||
		stored.ciphertextBytes != descriptor.CiphertextBytes || stored.ciphertextSHA256 != descriptor.CiphertextSHA256 ||
		stored.compression != descriptor.Compression || stored.encryption != descriptor.Encryption ||
		stored.keyID != descriptor.KeyID || !bytes.Equal(stored.wrappedKey, descriptor.WrappedKey) ||
		!bytes.Equal(stored.nonce, descriptor.Nonce) || stored.expiresAtNano != descriptor.ExpiresAt.UnixNano() {
		return &ChunkError{Code: CodeIdentityConflict, Err: errors.New("Collector Replay Attachment identity changed")}
	}
	return nil
}
