package agent

import (
	"context"
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
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	err = s.client.Set(opCtx, s.key(envelope.ResultRef), payload, ttl).Err()
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
	payload, err := s.client.Get(opCtx, s.key(resultRef)).Bytes()
	if errors.Is(err, redis.Nil) {
		s.metrics.RecordOperation("read", "miss", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultExpired
	}
	if err != nil {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, err
	}
	var envelope ToolResultEnvelope
	if json.Unmarshal(payload, &envelope) != nil || validateToolResultEnvelope(envelope) != nil || envelope.ResultRef != resultRef {
		s.metrics.RecordOperation("read", "error", 0, time.Since(startedAt))
		return ToolResultEnvelope{}, ErrToolResultCorrupt
	}
	s.metrics.RecordOperation("read", "hit", len(envelope.Body), time.Since(startedAt))
	return envelope, nil
}

// SampleRedisEvictions exposes Redis's cumulative evicted_keys counter as a
// gauge. It is intentionally sampled out of band so cache reads stay one GET.
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
	return s.client.Del(opCtx, s.key(resultRef)).Err()
}

func (s *RedisToolResultStore) RemainingTTL(ctx context.Context, resultRef string) (time.Duration, error) {
	if s == nil || s.client == nil || !toolResultReferencePattern.MatchString(resultRef) {
		return 0, ErrToolResultExpired
	}
	opCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()
	ttl, err := s.client.TTL(opCtx, s.key(resultRef)).Result()
	if err != nil || ttl <= 0 {
		if err == nil {
			err = ErrToolResultExpired
		}
		return 0, err
	}
	return ttl, nil
}

func (s *RedisToolResultStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *RedisToolResultStore) key(resultRef string) string { return s.keyPrefix + resultRef }

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
