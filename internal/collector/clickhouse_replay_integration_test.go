package collector_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

func TestClickHouseReplayTakesCustodyBeforeACKAndQueriesSealedPayload(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	stagingObjects := objectstore.NewMemoryStore()
	replayObjects := objectstore.NewMemoryStore()
	ciphertext := bytes.Repeat([]byte{0xa5}, 256)
	batch := clickHouseReplayBatch(t, fmt.Sprintf("ch-replay-%d", time.Now().UnixNano()), ciphertext, time.Now().UTC().Add(24*time.Hour))
	batch.ProtocolVersion = collector.DirectProtocolVersion
	batch.Chunks[0].SequenceAuthority = collector.SequenceAuthorityCollector
	batch.Chunks[0].FirstSequence = 0
	for index := range batch.Chunks[0].Records {
		batch.Chunks[0].Records[index].Sequence = 0
	}
	batch.Chunks[0].Attachments[0].RecordSequence = 0
	batch.Chunks[0].Attachments[0].RecordIdentityKey = batch.Chunks[0].Records[1].Record.IdentityKey
	attachment := batch.Chunks[0].Attachments[0]
	if err := stagingObjects.Put(ctx, attachment.StagingObjectKey, ciphertext); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStoreWithReplay(connection, stagingObjects, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ingestCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 10, Offset: time.Now().UnixNano(),
	})
	result, err := ingestor.Ingest(ingestCtx, batch)
	if err != nil || result.Chunks[0].Status != collector.ChunkCommitted || result.Chunks[0].CommittedThrough != 2 {
		t.Fatalf("Replay ingest result=%#v err=%v", result, err)
	}
	permanentKey := "agent-replay/" + attachment.AttachmentID
	storedCiphertext, err := replayObjects.Get(ctx, permanentKey, replay.MaxCiphertextBytes)
	if err != nil || !bytes.Equal(storedCiphertext, ciphertext) {
		t.Fatalf("permanent Replay object bytes=%d err=%v", len(storedCiphertext), err)
	}
	if err := stagingObjects.Delete(ctx, attachment.StagingObjectKey); err != nil {
		t.Fatal(err)
	}
	duplicate, err := ingestor.Ingest(ingestCtx, batch)
	if err != nil || duplicate.Chunks[0].Status != collector.ChunkCommitted {
		t.Fatalf("Replay duplicate without staging=%#v err=%v", duplicate, err)
	}

	queries, err := collector.NewClickHouseTraceQueryStoreWithReplay(connection, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	record := batch.Chunks[0].Records[1].Record
	opaque, err := queries.Replay(ctx, batch.Chunks[0].Trace.TraceID, record.SpanID, attachment.AttachmentID)
	if err != nil {
		t.Fatal(err)
	}
	if opaque.AttachmentID != attachment.AttachmentID || opaque.TraceID != batch.Chunks[0].Trace.TraceID ||
		opaque.SpanID != record.SpanID || opaque.Class != attachment.Class || !bytes.Equal(opaque.Sealed.Ciphertext, ciphertext) ||
		opaque.Sealed.CiphertextSHA256 != attachment.CiphertextSHA256 || opaque.Sealed.KeyID != attachment.KeyID {
		t.Fatalf("opaque Replay=%#v", opaque)
	}
	if _, err := queries.Replay(ctx, batch.Chunks[0].Trace.TraceID, agentobs.SpanID("wrong-span"), attachment.AttachmentID); !errors.Is(err, collector.ErrReplayNotFound) {
		t.Fatalf("wrong-span Replay error=%v", err)
	}
}

func TestClickHouseReplayMissingStagingIsRetryableAndPersistsNoTrace(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	stagingObjects := objectstore.NewMemoryStore()
	replayObjects := objectstore.NewMemoryStore()
	batch := clickHouseReplayBatch(t, fmt.Sprintf("ch-replay-missing-%d", time.Now().UnixNano()), bytes.Repeat([]byte{0xb6}, 128), time.Now().UTC().Add(time.Hour))
	store, err := collector.NewClickHouseStoreWithReplay(connection, stagingObjects, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, _ := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	ingestCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 11, Offset: time.Now().UnixNano(),
	})
	result, err := ingestor.Ingest(ingestCtx, batch)
	if err != nil {
		t.Fatalf("Replay ingest transport error=%v", err)
	}
	if got := result.Chunks[0]; got.Status != collector.ChunkRetryable || got.Code != collector.CodeAttachmentUnavailable || got.CommittedThrough != 0 {
		t.Fatalf("missing Replay result=%#v", got)
	}
	var rawRows, refRows uint64
	if err := connection.QueryRow(ctx, "SELECT count() FROM obs_trace_records_raw WHERE trace_id = ?", string(batch.Chunks[0].Trace.TraceID)).Scan(&rawRows); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRow(ctx, "SELECT count() FROM obs_replay_payload_refs WHERE trace_id = ?", string(batch.Chunks[0].Trace.TraceID)).Scan(&refRows); err != nil {
		t.Fatal(err)
	}
	if rawRows != 0 || refRows != 0 || replayObjects.Len() != 0 {
		t.Fatalf("missing Replay persisted raw/ref/object=%d/%d/%d", rawRows, refRows, replayObjects.Len())
	}
}

func TestClickHouseReplayRejectsMetadataConflictAndDetectsExpiryAndCorruption(t *testing.T) {
	ctx := context.Background()
	connection := openClickHouseTestConnection(t, ctx)
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}
	stagingObjects := objectstore.NewMemoryStore()
	replayObjects := objectstore.NewMemoryStore()
	ciphertext := bytes.Repeat([]byte{0xc7}, 192)
	batch := clickHouseReplayBatch(t, fmt.Sprintf("ch-replay-contract-%d", time.Now().UnixNano()), ciphertext, time.Now().UTC().Add(time.Hour))
	attachment := batch.Chunks[0].Attachments[0]
	if err := stagingObjects.Put(ctx, attachment.StagingObjectKey, ciphertext); err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewClickHouseStoreWithReplay(connection, stagingObjects, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, _ := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-worker", Store: store})
	ingestCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 12, Offset: time.Now().UnixNano(),
	})
	if result, err := ingestor.Ingest(ingestCtx, batch); err != nil || result.Chunks[0].Status != collector.ChunkCommitted {
		t.Fatalf("initial Replay ingest=%#v err=%v", result, err)
	}
	conflict := batch
	conflict.Chunks = append([]collector.TraceChunk(nil), batch.Chunks...)
	conflict.Chunks[0].Attachments = append([]collector.AttachmentDescriptor(nil), batch.Chunks[0].Attachments...)
	conflict.Chunks[0].Attachments[0].KeyID = "changed-key"
	conflicted, err := ingestor.Ingest(ingestCtx, conflict)
	if err != nil || conflicted.Chunks[0].Status != collector.ChunkRejected || conflicted.Chunks[0].Code != collector.CodeIdentityConflict {
		t.Fatalf("Replay metadata conflict=%#v err=%v", conflicted, err)
	}

	queries, err := collector.NewClickHouseTraceQueryStoreWithReplay(connection, replayObjects)
	if err != nil {
		t.Fatal(err)
	}
	record := batch.Chunks[0].Records[1].Record
	if err := replayObjects.Put(ctx, "agent-replay/"+attachment.AttachmentID, bytes.Repeat([]byte{0xd8}, len(ciphertext))); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.Replay(ctx, batch.Chunks[0].Trace.TraceID, record.SpanID, attachment.AttachmentID); !errors.Is(err, collector.ErrReplayUnavailable) {
		t.Fatalf("corrupted Replay error=%v", err)
	}

	expiredCiphertext := bytes.Repeat([]byte{0xe9}, 96)
	expired := clickHouseReplayBatch(t, fmt.Sprintf("ch-replay-expired-%d", time.Now().UnixNano()), expiredCiphertext, time.Now().UTC().Add(-time.Minute))
	if err := stagingObjects.Put(ctx, expired.Chunks[0].Attachments[0].StagingObjectKey, expiredCiphertext); err != nil {
		t.Fatal(err)
	}
	expiredCtx := collector.ContextWithKafkaSourcePosition(ctx, collector.KafkaSourcePosition{
		Topic: "nano.observability.agent-trace.v1", Partition: 12, Offset: time.Now().UnixNano() + 1,
	})
	if result, err := ingestor.Ingest(expiredCtx, expired); err != nil || result.Chunks[0].Status != collector.ChunkCommitted {
		t.Fatalf("expired Replay ingest=%#v err=%v", result, err)
	}
	expiredRecord := expired.Chunks[0].Records[1].Record
	if _, err := queries.Replay(ctx, expired.Chunks[0].Trace.TraceID, expiredRecord.SpanID, expired.Chunks[0].Attachments[0].AttachmentID); !errors.Is(err, collector.ErrReplayExpired) {
		t.Fatalf("expired Replay error=%v", err)
	}
}

func clickHouseReplayBatch(t *testing.T, suffix string, ciphertext []byte, expiresAt time.Time) collector.Batch {
	t.Helper()
	batch := collectorBatchFor(t, suffix)
	base := time.Now().UTC()
	for index := range batch.Chunks[0].Records {
		batch.Chunks[0].Records[index].Record.OccurredAt = base.Add(time.Duration(index) * time.Nanosecond)
		batch.Chunks[0].Records[index] = collectorEnvelope(t, index+1, batch.Chunks[0].Records[index].Record)
	}
	attachmentID := uuid.NewString()
	record := batch.Chunks[0].Records[1].Record
	record.Attributes = append(record.Attributes, agentobs.String(replay.ModelRequestAttachmentKey, attachmentID))
	batch.Chunks[0].Records[1] = collectorEnvelope(t, 2, record)
	digest := sha256.Sum256(ciphertext)
	batch.Chunks[0].Attachments = []collector.AttachmentDescriptor{{
		AttachmentID: attachmentID, RecordSequence: 2, Class: replay.ClassModelRequest,
		SchemaVersion: 1, PlaintextSHA256: strings.Repeat("b", 64),
		StagingObjectKey: "producer-staging/" + attachmentID, CiphertextBytes: len(ciphertext),
		CiphertextSHA256: hex.EncodeToString(digest[:]), Compression: replay.CompressionGZIP,
		Encryption: replay.EncryptionAES256GCM, KeyID: "dev-key-v1",
		WrappedKey: bytes.Repeat([]byte{0xc3}, 60), Nonce: bytes.Repeat([]byte{0xd4}, 12), ExpiresAt: expiresAt,
	}}
	return batch
}
