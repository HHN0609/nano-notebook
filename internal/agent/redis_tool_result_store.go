package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var toolResultKeyPrefixPattern = regexp.MustCompile(`^[a-z0-9:-]{1,64}:$`)

const toolResultIntegrityChunkBytes = 50 * 1024

type redisToolResultMetadata struct {
	SchemaVersion       int       `json:"schema_version"`
	ResultRef           string    `json:"result_ref"`
	UserID              string    `json:"user_id"`
	ChatID              string    `json:"chat_id"`
	RunID               string    `json:"run_id"`
	ActionID            string    `json:"action_id"`
	ToolName            string    `json:"tool_name"`
	MediaType           string    `json:"media_type"`
	Encoding            string    `json:"encoding"`
	ResultBytes         int       `json:"result_bytes"`
	SHA256              string    `json:"sha256"`
	CreatedAt           time.Time `json:"created_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	IntegrityChunkBytes int       `json:"integrity_chunk_bytes"`
	IntegritySHA256     []string  `json:"integrity_sha256"`
}

type RedisToolResultStoreConfig struct {
	URL               string
	KeyPrefix         string
	OperationTimeout  time.Duration
	MaximumValueBytes int
}

type RedisToolResultStore struct {
	client            *redis.Client
	keyPrefix         string
	operationTimeout  time.Duration
	maximumValueBytes int
	metrics           *ToolResultCacheMetrics
}

func (s *RedisToolResultStore) WithMetrics(recorder *ToolResultCacheMetrics) *RedisToolResultStore {
	if s != nil {
		s.metrics = recorder
	}
	return s
}

func NewRedisToolResultStore(config RedisToolResultStoreConfig) (*RedisToolResultStore, error) {
	if strings.TrimSpace(config.URL) == "" || !toolResultKeyPrefixPattern.MatchString(config.KeyPrefix) ||
		config.OperationTimeout <= 0 || config.MaximumValueBytes < 1 {
		return nil, errors.New("invalid Redis Tool Result Store configuration")
	}
	options, err := redis.ParseURL(config.URL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis Tool Result Store URL: %w", err)
	}
	options.DialTimeout = config.OperationTimeout
	options.ReadTimeout = config.OperationTimeout
	options.WriteTimeout = config.OperationTimeout
	return &RedisToolResultStore{
		client: redis.NewClient(options), keyPrefix: config.KeyPrefix,
		operationTimeout: config.OperationTimeout, maximumValueBytes: config.MaximumValueBytes,
	}, nil
}

func (s *RedisToolResultStore) CheckReady(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("Redis Tool Result Store is unavailable")
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	return s.client.Ping(opCtx).Err()
}

func (s *RedisToolResultStore) Put(ctx context.Context, envelope ToolResultEnvelope, ttl time.Duration) error {
	startedAt := time.Now()
	if s == nil || s.client == nil || ttl <= 0 || len(envelope.Body) > s.maximumValueBytes || validateToolResultEnvelope(envelope) != nil {
		return errors.New("invalid Redis Tool Result envelope")
	}
	metadata := redisToolResultMetadataFromEnvelope(envelope)
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	_, err = s.client.TxPipelined(opCtx, func(pipe redis.Pipeliner) error {
		pipe.Set(opCtx, s.metadataKey(envelope.ResultRef), payload, ttl)
		pipe.Set(opCtx, s.bodyKey(envelope.ResultRef), envelope.Body, ttl)
		return nil
	})
	outcome := "stored"
	bytes := len(envelope.Body)
	if err != nil {
		outcome, bytes = "error", 0
	}
	s.metrics.RecordOperation("write", outcome, bytes, time.Since(startedAt))
	return err
}

func (s *RedisToolResultStore) Get(ctx context.Context, resultRef string) (ToolResultEnvelope, error) {
	startedAt := time.Now()
	if s == nil || s.client == nil || !toolResultReferencePattern.MatchString(resultRef) {
		return ToolResultEnvelope{}, ErrToolResultExpired
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	commands, err := s.client.Pipelined(opCtx, func(pipe redis.Pipeliner) error {
		pipe.Get(opCtx, s.metadataKey(resultRef))
		pipe.Get(opCtx, s.bodyKey(resultRef))
		return nil
	})
	if errors.Is(err, redis.Nil) {
		s.metrics.RecordOperation("read", "miss", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultExpired
	}
	if err != nil {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, err
	}
	if len(commands) != 2 {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultCorrupt
	}
	metadataPayload, metadataErr := commands[0].(*redis.StringCmd).Bytes()
	body, bodyErr := commands[1].(*redis.StringCmd).Bytes()
	if errors.Is(metadataErr, redis.Nil) || errors.Is(bodyErr, redis.Nil) {
		s.metrics.RecordOperation("read", "miss", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultExpired
	}
	if metadataErr != nil || bodyErr != nil {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, firstToolResultStoreError(metadataErr, bodyErr)
	}
	metadata, metadataErr := decodeRedisToolResultMetadata(metadataPayload, resultRef)
	if metadataErr != nil {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, metadataErr
	}
	envelope := metadata.envelope(body)
	if validateToolResultEnvelope(envelope) != nil {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultCorrupt
	}
	s.metrics.RecordOperation("read", "hit", len(envelope.Body), time.Since(startedAt))
	return envelope, nil
}

func (s *RedisToolResultStore) ReadRange(ctx context.Context, resultRef string, offset, maximumBytes int) (ToolResultEnvelope, []byte, error) {
	startedAt := time.Now()
	if s == nil || s.client == nil || !toolResultReferencePattern.MatchString(resultRef) {
		return ToolResultEnvelope{}, nil, ErrToolResultExpired
	}
	if offset < 0 || maximumBytes < 1 {
		return ToolResultEnvelope{}, nil, ErrToolResultInvalidOffset
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	metadataPayload, err := s.client.Get(opCtx, s.metadataKey(resultRef)).Bytes()
	if errors.Is(err, redis.Nil) {
		s.metrics.RecordOperation("read_range", "miss", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, ErrToolResultExpired
	}
	if err != nil {
		s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, err
	}
	metadata, err := decodeRedisToolResultMetadata(metadataPayload, resultRef)
	if err != nil {
		s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, err
	}
	if offset > metadata.ResultBytes {
		return ToolResultEnvelope{}, nil, ErrToolResultInvalidOffset
	}
	envelope := metadata.envelope(nil)
	if offset == metadata.ResultBytes {
		exists, existsErr := s.client.Exists(opCtx, s.bodyKey(resultRef)).Result()
		if existsErr != nil {
			s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
			return ToolResultEnvelope{}, nil, existsErr
		}
		if exists == 0 {
			s.metrics.RecordOperation("read_range", "miss", 0, time.Since(startedAt))
			return ToolResultEnvelope{}, nil, ErrToolResultExpired
		}
		s.metrics.RecordOperation("read_range", "hit", 0, time.Since(startedAt))
		return envelope, nil, nil
	}
	end := offset + maximumBytes
	if end > metadata.ResultBytes {
		end = metadata.ResultBytes
	}
	firstChunk := offset / metadata.IntegrityChunkBytes
	lastChunk := (end - 1) / metadata.IntegrityChunkBytes
	alignedStart := firstChunk * metadata.IntegrityChunkBytes
	alignedEnd := (lastChunk+1)*metadata.IntegrityChunkBytes - 1
	if alignedEnd >= metadata.ResultBytes {
		alignedEnd = metadata.ResultBytes - 1
	}
	alignedBody, err := s.client.GetRange(opCtx, s.bodyKey(resultRef), int64(alignedStart), int64(alignedEnd)).Bytes()
	if errors.Is(err, redis.Nil) {
		s.metrics.RecordOperation("read_range", "miss", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, ErrToolResultExpired
	}
	if err != nil {
		s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, err
	}
	if len(alignedBody) != alignedEnd-alignedStart+1 {
		exists, existsErr := s.client.Exists(opCtx, s.bodyKey(resultRef)).Result()
		if existsErr != nil {
			s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
			return ToolResultEnvelope{}, nil, existsErr
		}
		if exists == 0 {
			s.metrics.RecordOperation("read_range", "miss", 0, time.Since(startedAt))
			return ToolResultEnvelope{}, nil, ErrToolResultExpired
		}
		s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, ErrToolResultCorrupt
	}
	if !metadata.verifyChunks(firstChunk, lastChunk, alignedBody) {
		s.metrics.RecordOperation("read_range", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, nil, ErrToolResultCorrupt
	}
	startInAligned := offset - alignedStart
	endInAligned := startInAligned + end - offset
	body := append([]byte(nil), alignedBody[startInAligned:endInAligned]...)
	s.metrics.RecordOperation("read_range", "hit", len(body), time.Since(startedAt))
	return envelope, body, nil
}

// SampleRedisEvictions exposes Redis's cumulative evicted_keys counter as a
// gauge. It is intentionally sampled out of band from result reads.
func (s *RedisToolResultStore) SampleRedisEvictions(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("Redis Tool Result Store is unavailable")
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	info, err := s.client.Info(opCtx, "stats").Result()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(info, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || key != "evicted_keys" {
			continue
		}
		count, parseErr := strconv.ParseFloat(value, 64)
		if parseErr != nil {
			return fmt.Errorf("parse Redis evicted_keys: %w", parseErr)
		}
		s.metrics.SetRedisEvictedKeys(count)
		return nil
	}
	return errors.New("Redis INFO stats omitted evicted_keys")
}

func (s *RedisToolResultStore) ObserveRedisEvictions(ctx context.Context, interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_ = s.SampleRedisEvictions(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *RedisToolResultStore) Delete(ctx context.Context, resultRef string) error {
	if s == nil || s.client == nil || !toolResultReferencePattern.MatchString(resultRef) {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	return s.client.Del(opCtx, s.metadataKey(resultRef), s.bodyKey(resultRef)).Err()
}

func (s *RedisToolResultStore) RemainingTTL(ctx context.Context, resultRef string) (time.Duration, error) {
	if s == nil || s.client == nil || !toolResultReferencePattern.MatchString(resultRef) {
		return 0, ErrToolResultExpired
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	commands, err := s.client.Pipelined(opCtx, func(pipe redis.Pipeliner) error {
		pipe.TTL(opCtx, s.metadataKey(resultRef))
		pipe.TTL(opCtx, s.bodyKey(resultRef))
		return nil
	})
	if err != nil || len(commands) != 2 {
		return 0, firstToolResultStoreError(err, ErrToolResultExpired)
	}
	metadataTTL, metadataErr := commands[0].(*redis.DurationCmd).Result()
	bodyTTL, bodyErr := commands[1].(*redis.DurationCmd).Result()
	if metadataErr != nil || bodyErr != nil || metadataTTL <= 0 || bodyTTL <= 0 {
		return 0, ErrToolResultExpired
	}
	if bodyTTL < metadataTTL {
		return bodyTTL, nil
	}
	return metadataTTL, nil
}

func (s *RedisToolResultStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *RedisToolResultStore) metadataKey(resultRef string) string {
	return s.keyPrefix + "{" + resultRef + "}:meta"
}

func (s *RedisToolResultStore) bodyKey(resultRef string) string {
	return s.keyPrefix + "{" + resultRef + "}:body"
}

func redisToolResultMetadataFromEnvelope(envelope ToolResultEnvelope) redisToolResultMetadata {
	metadata := redisToolResultMetadata{
		SchemaVersion: envelope.SchemaVersion, ResultRef: envelope.ResultRef,
		UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID,
		ActionID: envelope.ActionID, ToolName: envelope.ToolName,
		MediaType: envelope.MediaType, Encoding: envelope.Encoding,
		ResultBytes: envelope.ResultBytes, SHA256: envelope.SHA256,
		CreatedAt: envelope.CreatedAt, ExpiresAt: envelope.ExpiresAt,
		IntegrityChunkBytes: toolResultIntegrityChunkBytes,
	}
	for start := 0; start < len(envelope.Body); start += metadata.IntegrityChunkBytes {
		end := start + metadata.IntegrityChunkBytes
		if end > len(envelope.Body) {
			end = len(envelope.Body)
		}
		metadata.IntegritySHA256 = append(metadata.IntegritySHA256, hashPayload(envelope.Body[start:end]))
	}
	return metadata
}

func decodeRedisToolResultMetadata(payload []byte, resultRef string) (redisToolResultMetadata, error) {
	var metadata redisToolResultMetadata
	if json.Unmarshal(payload, &metadata) != nil || metadata.validate() != nil || metadata.ResultRef != resultRef {
		return redisToolResultMetadata{}, ErrToolResultCorrupt
	}
	return metadata, nil
}

func (m redisToolResultMetadata) validate() error {
	if m.SchemaVersion != 1 || !toolResultReferencePattern.MatchString(m.ResultRef) ||
		strings.TrimSpace(m.UserID) == "" || strings.TrimSpace(m.ChatID) == "" || strings.TrimSpace(m.RunID) == "" ||
		strings.TrimSpace(m.ActionID) == "" || !actionNamePattern.MatchString(m.ToolName) ||
		m.MediaType != "application/json" || m.Encoding != "json" || m.ResultBytes < 0 || len(m.SHA256) != 64 ||
		m.CreatedAt.IsZero() || !m.ExpiresAt.After(m.CreatedAt) || m.IntegrityChunkBytes != toolResultIntegrityChunkBytes {
		return ErrToolResultCorrupt
	}
	expectedChunks := 0
	if m.ResultBytes > 0 {
		expectedChunks = (m.ResultBytes + m.IntegrityChunkBytes - 1) / m.IntegrityChunkBytes
	}
	if len(m.IntegritySHA256) != expectedChunks {
		return ErrToolResultCorrupt
	}
	for _, hash := range append([]string{m.SHA256}, m.IntegritySHA256...) {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return ErrToolResultCorrupt
		}
	}
	return nil
}

func (m redisToolResultMetadata) envelope(body []byte) ToolResultEnvelope {
	return ToolResultEnvelope{
		SchemaVersion: m.SchemaVersion, ResultRef: m.ResultRef,
		UserID: m.UserID, ChatID: m.ChatID, RunID: m.RunID,
		ActionID: m.ActionID, ToolName: m.ToolName,
		MediaType: m.MediaType, Encoding: m.Encoding,
		ResultBytes: m.ResultBytes, SHA256: m.SHA256,
		CreatedAt: m.CreatedAt, ExpiresAt: m.ExpiresAt, Body: body,
	}
}

func (m redisToolResultMetadata) verifyChunks(firstChunk, lastChunk int, alignedBody []byte) bool {
	if firstChunk < 0 || lastChunk < firstChunk || lastChunk >= len(m.IntegritySHA256) {
		return false
	}
	localOffset := 0
	for chunk := firstChunk; chunk <= lastChunk; chunk++ {
		chunkBytes := m.IntegrityChunkBytes
		if remaining := m.ResultBytes - chunk*m.IntegrityChunkBytes; remaining < chunkBytes {
			chunkBytes = remaining
		}
		if chunkBytes < 0 || localOffset+chunkBytes > len(alignedBody) ||
			hashPayload(alignedBody[localOffset:localOffset+chunkBytes]) != m.IntegritySHA256[chunk] {
			return false
		}
		localOffset += chunkBytes
	}
	return localOffset == len(alignedBody)
}

func firstToolResultStoreError(errorsToCheck ...error) error {
	for _, err := range errorsToCheck {
		if err != nil {
			return err
		}
	}
	return ErrToolResultCorrupt
}

func validateToolResultEnvelope(envelope ToolResultEnvelope) error {
	if envelope.SchemaVersion != 1 || !toolResultReferencePattern.MatchString(envelope.ResultRef) ||
		strings.TrimSpace(envelope.UserID) == "" || strings.TrimSpace(envelope.ChatID) == "" || strings.TrimSpace(envelope.RunID) == "" ||
		strings.TrimSpace(envelope.ActionID) == "" || !actionNamePattern.MatchString(envelope.ToolName) ||
		envelope.MediaType != "application/json" || envelope.Encoding != "json" || envelope.ResultBytes != len(envelope.Body) ||
		hashPayload(envelope.Body) != envelope.SHA256 || envelope.CreatedAt.IsZero() || !envelope.ExpiresAt.After(envelope.CreatedAt) {
		return errors.New("invalid Tool Result envelope")
	}
	return nil
}
