package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedisToolResultStoreRejectsUnsafeConfiguration(t *testing.T) {
	for _, config := range []RedisToolResultStoreConfig{
		{},
		{URL: "redis://localhost:6379/0", KeyPrefix: "", OperationTimeout: time.Second, MaximumValueBytes: 1024},
		{URL: "redis://localhost:6379/0", KeyPrefix: "unsafe prefix", OperationTimeout: time.Second, MaximumValueBytes: 1024},
		{URL: "redis://localhost:6379/0", KeyPrefix: "nano:tool-result:", OperationTimeout: 0, MaximumValueBytes: 1024},
	} {
		if _, err := NewRedisToolResultStore(config); err == nil {
			t.Fatalf("accepted unsafe config %#v", config)
		}
	}
}

func TestRedisToolResultStoreImplementsRangeReads(t *testing.T) {
	if _, ok := any(&RedisToolResultStore{}).(ToolResultRangeStore); !ok {
		t.Fatal("Redis Tool Result Store does not implement bounded range reads")
	}
}

func TestRedisToolResultMetadataVerifiesOnlyRequestedAlignedChunks(t *testing.T) {
	body := []byte(strings.Repeat("a", toolResultIntegrityChunkBytes) + strings.Repeat("b", toolResultIntegrityChunkBytes) + "tail")
	envelope := testToolResultEnvelope(body)
	metadata := redisToolResultMetadataFromEnvelope(envelope)

	secondChunk := body[toolResultIntegrityChunkBytes : 2*toolResultIntegrityChunkBytes]
	if !metadata.verifyChunks(1, 1, secondChunk) {
		t.Fatal("valid requested chunk was rejected")
	}
	corrupt := append([]byte(nil), secondChunk...)
	corrupt[len(corrupt)/2] ^= 1
	if metadata.verifyChunks(1, 1, corrupt) {
		t.Fatal("corrupt requested chunk was accepted")
	}
	if metadata.verifyChunks(0, 1, secondChunk) {
		t.Fatal("misaligned chunk bytes were accepted")
	}
}

func TestRedisToolResultStoreRoundTripWithAbsoluteTTL(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("NANO_TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("NANO_TEST_REDIS_URL is required")
	}
	store, err := NewRedisToolResultStore(RedisToolResultStoreConfig{
		URL: redisURL, KeyPrefix: "nano:test-tool-result:", OperationTimeout: 2 * time.Second, MaximumValueBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"markdown": strings.Repeat("real redis range evidence 🛡️ ", 5000)})
	if err != nil {
		t.Fatal(err)
	}
	envelope := testToolResultEnvelope(body)
	envelope.ResultRef = "tr_integration_reference_20260901"
	envelope.CreatedAt = time.Now().UTC()
	envelope.ExpiresAt = envelope.CreatedAt.Add(30 * time.Minute)
	defer store.Delete(ctx, envelope.ResultRef)

	if err := store.Put(ctx, envelope, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(ctx, envelope.ResultRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Body) != string(envelope.Body) || loaded.ResultRef != envelope.ResultRef {
		t.Fatalf("loaded envelope = %#v", loaded)
	}
	ttl, err := store.RemainingTTL(ctx, envelope.ResultRef)
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 29*time.Minute || ttl > 30*time.Minute {
		t.Fatalf("TTL = %s", ttl)
	}
	if _, err := store.Get(ctx, envelope.ResultRef); err != nil {
		t.Fatal(err)
	}
	ttlAfterRead, err := store.RemainingTTL(ctx, envelope.ResultRef)
	if err != nil {
		t.Fatal(err)
	}
	if ttlAfterRead > ttl {
		t.Fatalf("read renewed absolute TTL: before=%s after=%s", ttl, ttlAfterRead)
	}
	rangeOffset := toolResultIntegrityChunkBytes - 7
	rangeMetadata, ranged, err := store.ReadRange(ctx, envelope.ResultRef, rangeOffset, 64)
	if err != nil {
		t.Fatal(err)
	}
	if rangeMetadata.Body != nil || string(ranged) != string(envelope.Body[rangeOffset:rangeOffset+64]) {
		t.Fatalf("range metadata/body=%#v/%q", rangeMetadata, ranged)
	}
	if err := store.client.Del(ctx, store.bodyKey(envelope.ResultRef)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadRange(ctx, envelope.ResultRef, 0, 64); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("missing ranged body error=%v", err)
	}
	if _, _, err := store.ReadRange(ctx, envelope.ResultRef, len(envelope.Body), 64); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("missing ranged body at terminal offset error=%v", err)
	}
	if err := store.Delete(ctx, envelope.ResultRef); err != nil {
		t.Fatal(err)
	}
	reader := ToolResultReader{Store: store, MaximumPageBytes: 64}
	if _, err := reader.Read(ctx, ToolResultScope{UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID}, envelope.ResultRef, 0, 64); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("early eviction/missing read error=%v", err)
	}
}
