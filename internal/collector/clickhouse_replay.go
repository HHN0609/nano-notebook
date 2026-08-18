package collector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

type clickHouseReplayMetadata struct {
	AttachmentID     string           `json:"attachment_id"`
	TraceID          agentobs.TraceID `json:"trace_id"`
	RecordSequence   int              `json:"record_sequence"`
	Class            replay.Class     `json:"class"`
	SchemaVersion    int              `json:"schema_version"`
	PlaintextSHA256  string           `json:"plaintext_sha256"`
	ObjectKey        string           `json:"object_key"`
	CiphertextBytes  int              `json:"ciphertext_bytes"`
	CiphertextSHA256 string           `json:"ciphertext_sha256"`
	Compression      string           `json:"compression"`
	Encryption       string           `json:"encryption"`
	KeyID            string           `json:"key_id"`
	WrappedKey       []byte           `json:"wrapped_key"`
	Nonce            []byte           `json:"nonce"`
	ExpiresAtNano    int64            `json:"expires_at_unix_nano"`
}

func (s *ClickHouseStore) prepareReplayAttachments(ctx context.Context, chunk TraceChunk) ([]preparedAttachment, error) {
	if len(chunk.Attachments) == 0 {
		return nil, nil
	}
	if s.stagingObjects == nil || s.replayObjects == nil {
		return nil, &ChunkError{Code: CodeAttachmentUnavailable, Retryable: true, Err: errors.New("Collector ClickHouse Replay object stores are unavailable")}
	}
	prepared := make([]preparedAttachment, 0, len(chunk.Attachments))
	for _, descriptor := range chunk.Attachments {
		objectKey := "agent-replay/" + descriptor.AttachmentID
		stored, found, err := s.loadReplayRef(ctx, descriptor.AttachmentID)
		if err != nil {
			return nil, err
		}
		if found {
			if err := reconcilePayloadRef(stored, chunk.Trace.TraceID, descriptor, objectKey); err != nil {
				return nil, err
			}
		}
		if validReplayObject(ctx, s.replayObjects, objectKey, descriptor) {
			prepared = append(prepared, preparedAttachment{descriptor: descriptor, objectKey: objectKey})
			continue
		}
		ciphertext, err := s.stagingObjects.Get(ctx, descriptor.StagingObjectKey, int64(descriptor.CiphertextBytes))
		if err != nil {
			if errors.Is(err, objectstore.ErrObjectTooLarge) {
				return nil, &ChunkError{Code: CodeAttachmentIntegrity, Err: errors.New("Collector Replay ciphertext exceeds its declared size")}
			}
			return nil, &ChunkError{Code: CodeAttachmentUnavailable, Retryable: true, Err: fmt.Errorf("Collector Replay staging object unavailable: %w", err)}
		}
		if !replayCiphertextMatches(ciphertext, descriptor) {
			return nil, &ChunkError{Code: CodeAttachmentIntegrity, Err: errors.New("Collector Replay ciphertext size or hash changed")}
		}
		if err := s.replayObjects.Put(ctx, objectKey, ciphertext); err != nil {
			return nil, &ChunkError{Code: CodeAttachmentUnavailable, Retryable: true, Err: fmt.Errorf("store Collector Replay object: %w", err)}
		}
		prepared = append(prepared, preparedAttachment{descriptor: descriptor, objectKey: objectKey})
	}
	return prepared, nil
}

func validReplayObject(ctx context.Context, objects objectstore.Store, objectKey string, descriptor AttachmentDescriptor) bool {
	ciphertext, err := objects.Get(ctx, objectKey, int64(descriptor.CiphertextBytes))
	return err == nil && replayCiphertextMatches(ciphertext, descriptor)
}

func replayCiphertextMatches(ciphertext []byte, descriptor AttachmentDescriptor) bool {
	if len(ciphertext) != descriptor.CiphertextBytes {
		return false
	}
	digest := sha256.Sum256(ciphertext)
	return bytes.Equal([]byte(descriptor.CiphertextSHA256), []byte(hex.EncodeToString(digest[:])))
}

func replayMetadata(traceID agentobs.TraceID, prepared preparedAttachment) clickHouseReplayMetadata {
	descriptor := prepared.descriptor
	return clickHouseReplayMetadata{
		AttachmentID: descriptor.AttachmentID, TraceID: traceID, RecordSequence: descriptor.RecordSequence,
		Class: descriptor.Class, SchemaVersion: descriptor.SchemaVersion, PlaintextSHA256: descriptor.PlaintextSHA256,
		ObjectKey: prepared.objectKey, CiphertextBytes: descriptor.CiphertextBytes, CiphertextSHA256: descriptor.CiphertextSHA256,
		Compression: descriptor.Compression, Encryption: descriptor.Encryption, KeyID: descriptor.KeyID,
		WrappedKey: descriptor.WrappedKey, Nonce: descriptor.Nonce, ExpiresAtNano: descriptor.ExpiresAt.UnixNano(),
	}
}

func replayMetadataSHA256(metadata clickHouseReplayMetadata) (string, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (s *ClickHouseStore) writeReplayRefsBatch(ctx context.Context, requests []*clickHouseWriteRequest) error {
	count := 0
	for _, request := range requests {
		count += len(request.attachments)
	}
	if count == 0 {
		return nil
	}
	batch, err := s.connection.PrepareBatch(ctx, `
		INSERT INTO obs_replay_payload_refs (
			attachment_id, metadata_sha256, trace_id, record_sequence, class, schema_version,
			plaintext_sha256, object_key, ciphertext_bytes, ciphertext_sha256,
			compression, encryption, key_id, wrapped_key, nonce, expires_at,
			expires_at_unix_nano, state, source_topic, source_partition, source_offset, ingest_version
		) VALUES
	`)
	if err != nil {
		return err
	}
	defer batch.Close()
	for _, request := range requests {
		for _, prepared := range request.attachments {
			metadata := replayMetadata(request.trace.TraceID, prepared)
			metadataSHA256, err := replayMetadataSHA256(metadata)
			if err != nil {
				return err
			}
			if err := batch.Append(
				metadata.AttachmentID, metadataSHA256, string(metadata.TraceID), uint32(metadata.RecordSequence), string(metadata.Class), uint16(metadata.SchemaVersion),
				metadata.PlaintextSHA256, metadata.ObjectKey, uint32(metadata.CiphertextBytes), metadata.CiphertextSHA256,
				metadata.Compression, metadata.Encryption, metadata.KeyID, metadata.WrappedKey, metadata.Nonce,
				unixNanoTime(metadata.ExpiresAtNano), metadata.ExpiresAtNano, "available",
				request.source.Topic, request.source.Partition, request.source.Offset, uint64(request.source.Offset)+1,
			); err != nil {
				return err
			}
		}
	}
	if err := batch.Send(); err != nil {
		return err
	}
	for _, request := range requests {
		for _, prepared := range request.attachments {
			stored, found, err := s.loadReplayRef(ctx, prepared.descriptor.AttachmentID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("Collector ClickHouse Replay Attachment metadata was not committed")
			}
			if err := reconcilePayloadRef(stored, request.trace.TraceID, prepared.descriptor, prepared.objectKey); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ClickHouseStore) loadReplayRef(ctx context.Context, attachmentID string) (storedPayloadRef, bool, error) {
	rows, err := s.connection.Query(ctx, `
		SELECT attachment_id, metadata_sha256, trace_id, record_sequence, class, schema_version,
			plaintext_sha256, object_key, ciphertext_bytes, ciphertext_sha256,
			compression, encryption, key_id, wrapped_key, nonce, expires_at_unix_nano
		FROM obs_replay_payload_refs FINAL WHERE attachment_id = ?
	`, attachmentID)
	if err != nil {
		return storedPayloadRef{}, false, err
	}
	defer rows.Close()
	var stored storedPayloadRef
	found := false
	for rows.Next() {
		var candidate storedPayloadRef
		var metadataSHA256 string
		var recordSequence, ciphertextBytes uint32
		var schemaVersion uint16
		var traceID, class string
		if err := rows.Scan(
			&candidate.attachmentID, &metadataSHA256, &traceID, &recordSequence, &class, &schemaVersion,
			&candidate.plaintextSHA256, &candidate.objectKey, &ciphertextBytes, &candidate.ciphertextSHA256,
			&candidate.compression, &candidate.encryption, &candidate.keyID, &candidate.wrappedKey,
			&candidate.nonce, &candidate.expiresAtNano,
		); err != nil {
			return storedPayloadRef{}, false, err
		}
		candidate.traceID = agentobs.TraceID(traceID)
		candidate.recordSequence = int(recordSequence)
		candidate.class = replay.Class(class)
		candidate.schemaVersion = int(schemaVersion)
		candidate.ciphertextBytes = int(ciphertextBytes)
		metadata := replayMetadata(candidate.traceID, preparedAttachment{descriptor: AttachmentDescriptor{
			AttachmentID: candidate.attachmentID, RecordSequence: candidate.recordSequence, Class: candidate.class,
			SchemaVersion: candidate.schemaVersion, PlaintextSHA256: candidate.plaintextSHA256,
			CiphertextBytes: candidate.ciphertextBytes, CiphertextSHA256: candidate.ciphertextSHA256,
			Compression: candidate.compression, Encryption: candidate.encryption, KeyID: candidate.keyID,
			WrappedKey: candidate.wrappedKey, Nonce: candidate.nonce, ExpiresAt: unixNanoTime(candidate.expiresAtNano),
		}, objectKey: candidate.objectKey})
		actualSHA256, err := replayMetadataSHA256(metadata)
		if err != nil || actualSHA256 != metadataSHA256 {
			return storedPayloadRef{}, false, errors.New("Collector ClickHouse Replay metadata hash mismatch")
		}
		if found {
			return storedPayloadRef{}, false, &ChunkError{Code: CodeIdentityConflict, Err: errors.New("Collector ClickHouse Replay Attachment identity conflicts with stored metadata")}
		}
		stored, found = candidate, true
	}
	if err := rows.Err(); err != nil {
		return storedPayloadRef{}, false, err
	}
	return stored, found, nil
}
