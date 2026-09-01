package agent

import (
	"context"
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
	envelope := testToolResultEnvelope([]byte(`{"markdown":"real redis round trip"}`))
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
	if err := store.Delete(ctx, envelope.ResultRef); err != nil {
		t.Fatal(err)
	}
	reader := ToolResultReader{Store: store, MaximumPageBytes: 64}
	if _, err := reader.Read(ctx, ToolResultScope{UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID}, envelope.ResultRef, 0, 64); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("early eviction/missing read error=%v", err)
	}
}
