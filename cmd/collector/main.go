package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	"github.com/huangxinxinyu/nano-notebook/internal/platform/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type collectorConfig struct {
	Addr                   string
	ServiceToken           string
	QueryToken             string
	ProducerID             string
	ProducerIDPrefix       string
	MaxBodyBytes           int64
	ClickHouseAddr         []string
	ClickHouseDatabase     string
	ClickHouseUser         string
	ClickHousePassword     string
	ClickHouseMaxOpenConns int
	ClickHouseMaxIdleConns int
	ClickHouseDialTimeout  time.Duration
	ReplayStagingS3        objectstore.S3Config
	ReplayS3               objectstore.S3Config
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config, err := loadConfig()
	if err != nil {
		slog.Error("Collector configuration invalid", "error", err)
		os.Exit(1)
	}
	if err := run(ctx, config); err != nil {
		slog.Error("Collector stopped with error", "error", err)
		os.Exit(1)
	}
	slog.Info("Collector stopped")
}

func loadConfig() (collectorConfig, error) {
	maxBodyBytes, err := envInt64("NANO_COLLECTOR_MAX_BODY_BYTES", 2*1024*1024)
	if err != nil {
		return collectorConfig{}, err
	}
	stagingUseTLS, err := envBool("NANO_REPLAY_STAGING_S3_USE_TLS", false)
	if err != nil {
		return collectorConfig{}, err
	}
	replayUseTLS, err := envBool("NANO_REPLAY_S3_USE_TLS", false)
	if err != nil {
		return collectorConfig{}, err
	}
	clickHouseDialTimeout, err := envDuration("NANO_CLICKHOUSE_DIAL_TIMEOUT", 10*time.Second)
	if err != nil {
		return collectorConfig{}, err
	}
	config := collectorConfig{
		Addr:                   env("NANO_COLLECTOR_ADDR", ":8082"),
		ServiceToken:           env("NANO_COLLECTOR_SERVICE_TOKEN", "nano-local-collector-token"),
		QueryToken:             env("NANO_COLLECTOR_QUERY_TOKEN", "nano-local-collector-query-token"),
		ProducerID:             env("NANO_COLLECTOR_PRODUCER_ID", "nano-worker"),
		ProducerIDPrefix:       env("NANO_COLLECTOR_PRODUCER_ID_PREFIX", "nano-"),
		MaxBodyBytes:           maxBodyBytes,
		ClickHouseAddr:         strings.Split(env("NANO_CLICKHOUSE_ADDR", "127.0.0.1:59004"), ","),
		ClickHouseDatabase:     env("NANO_CLICKHOUSE_DATABASE", "nano_observability"),
		ClickHouseUser:         env("NANO_CLICKHOUSE_USER", "nano_observability"),
		ClickHousePassword:     env("NANO_CLICKHOUSE_PASSWORD", "nano-observability"),
		ClickHouseMaxOpenConns: 16,
		ClickHouseMaxIdleConns: 8,
		ClickHouseDialTimeout:  clickHouseDialTimeout,
		ReplayStagingS3: objectstore.S3Config{
			Endpoint:        env("NANO_REPLAY_STAGING_S3_ENDPOINT", "127.0.0.1:59000"),
			AccessKeyID:     env("NANO_REPLAY_STAGING_S3_ACCESS_KEY_ID", "nano"),
			SecretAccessKey: env("NANO_REPLAY_STAGING_S3_SECRET_ACCESS_KEY", "nano-password"),
			Bucket:          env("NANO_REPLAY_STAGING_S3_BUCKET", "nano-agent-replay-staging"),
			Region:          env("NANO_REPLAY_STAGING_S3_REGION", "us-east-1"), UseTLS: stagingUseTLS,
		},
		ReplayS3: objectstore.S3Config{
			Endpoint:        env("NANO_REPLAY_S3_ENDPOINT", "127.0.0.1:59000"),
			AccessKeyID:     env("NANO_REPLAY_S3_ACCESS_KEY_ID", "nano"),
			SecretAccessKey: env("NANO_REPLAY_S3_SECRET_ACCESS_KEY", "nano-password"),
			Bucket:          env("NANO_REPLAY_S3_BUCKET", "nano-agent-replay"),
			Region:          env("NANO_REPLAY_S3_REGION", "us-east-1"), UseTLS: replayUseTLS,
		},
	}
	clickHouseInvalid := len(config.ClickHouseAddr) == 0 || strings.TrimSpace(config.ClickHouseAddr[0]) == "" || config.ClickHouseDatabase == "" ||
		config.ClickHouseUser == "" || config.ClickHousePassword == "" || config.ClickHouseDialTimeout <= 0
	if clickHouseInvalid || strings.TrimSpace(config.Addr) == "" ||
		strings.TrimSpace(config.ServiceToken) == "" || strings.TrimSpace(config.QueryToken) == "" ||
		(strings.TrimSpace(config.ProducerID) == "" && strings.TrimSpace(config.ProducerIDPrefix) == "") ||
		config.MaxBodyBytes < 1 ||
		strings.TrimSpace(config.ReplayStagingS3.Endpoint) == "" || strings.TrimSpace(config.ReplayStagingS3.AccessKeyID) == "" ||
		strings.TrimSpace(config.ReplayStagingS3.SecretAccessKey) == "" || strings.TrimSpace(config.ReplayStagingS3.Bucket) == "" ||
		strings.TrimSpace(config.ReplayS3.Endpoint) == "" || strings.TrimSpace(config.ReplayS3.AccessKeyID) == "" ||
		strings.TrimSpace(config.ReplayS3.SecretAccessKey) == "" || strings.TrimSpace(config.ReplayS3.Bucket) == "" {
		return collectorConfig{}, errors.New("Collector configuration is incomplete or inconsistent")
	}
	return config, nil
}

func run(ctx context.Context, config collectorConfig) error {
	return runClickHouseCollector(ctx, config)
}

func runClickHouseCollector(ctx context.Context, config collectorConfig) error {
	connection, err := openClickHouseConnection(config)
	if err != nil {
		return fmt.Errorf("open Collector ClickHouse: %w", err)
	}
	defer connection.Close()
	if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
		return fmt.Errorf("run Collector ClickHouse migrations: %w", err)
	}
	stagingObjects, replayObjects, err := openCollectorReplayStores(ctx, config)
	if err != nil {
		return err
	}
	metricsRegistry := metrics.NewRegistry()
	metricsCatalog := metrics.NewCatalog(metricsRegistry)
	metricsAddr := env("NANO_COLLECTOR_METRICS_ADDR", "0.0.0.0:9093")
	metricsServer := metrics.NewAdminServer(metricsAddr, metricsRegistry)
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Collector metrics listener failed", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metricsServer.Shutdown(shutdownCtx)
	}()
	store, err := collector.NewClickHouseStoreWithReplay(connection, stagingObjects, replayObjects)
	if err != nil {
		return err
	}
	store = store.WithMetrics(metricsCatalog)
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{
		ProducerID: config.ProducerID, ProducerIDPrefix: config.ProducerIDPrefix, Store: store,
	})
	if err != nil {
		return err
	}
	queryStore, err := collector.NewClickHouseTraceQueryStoreWithReplay(connection, replayObjects)
	if err != nil {
		return err
	}
	analyticsStore, err := collector.NewClickHouseTraceAnalyticsQueryStore(connection)
	if err != nil {
		return err
	}
	handler, err := collector.NewHTTPHandler(collector.HTTPConfig{
		Ingestor: ingestor, ServiceToken: config.ServiceToken, MaxBodyBytes: config.MaxBodyBytes,
		QueryStore: queryStore, AnalyticsStore: collector.WithTraceAnalyticsMetrics(analyticsStore, metricsCatalog), QueryToken: config.QueryToken,
		Readiness: func(readyCtx context.Context) error {
			return errors.Join(connection.Ping(readyCtx), stagingObjects.CheckReady(readyCtx), replayObjects.CheckReady(readyCtx))
		},
	})
	if err != nil {
		return err
	}
	shutdownTelemetry, err := telemetry.Start(ctx, "nano-collector")
	if err != nil {
		return fmt.Errorf("start Collector telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutdownCtx)
	}()
	telemetry.StartupSpan(ctx, "nano-collector")
	service, err := collector.NewHTTPService(collector.HTTPServiceConfig{
		Handler: otelhttp.NewHandler(handler, "collector"), ReadHeaderTimeout: 5 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	})
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", config.Addr)
	if err != nil {
		return fmt.Errorf("listen for Collector HTTP: %w", err)
	}
	slog.Info("Collector listening", "addr", config.Addr, "store", "clickhouse",
		"query_database_max_connections", config.ClickHouseMaxOpenConns)
	return service.Run(ctx, listener)
}

func openClickHouseConnection(config collectorConfig) (driver.Conn, error) {
	return clickhouse.Open(&clickhouse.Options{
		Addr: config.ClickHouseAddr,
		Auth: clickhouse.Auth{
			Database: config.ClickHouseDatabase,
			Username: config.ClickHouseUser,
			Password: config.ClickHousePassword,
		},
		Compression:     &clickhouse.Compression{Method: clickhouse.CompressionZSTD},
		DialTimeout:     config.ClickHouseDialTimeout,
		MaxOpenConns:    config.ClickHouseMaxOpenConns,
		MaxIdleConns:    config.ClickHouseMaxIdleConns,
		ConnMaxLifetime: time.Hour,
		BlockBufferSize: 10,
	})
}

func openCollectorReplayStores(ctx context.Context, config collectorConfig) (*objectstore.S3Store, *objectstore.S3Store, error) {
	stagingObjects, err := objectstore.NewS3Store(config.ReplayStagingS3)
	if err != nil {
		return nil, nil, fmt.Errorf("configure Collector staging object Store: %w", err)
	}
	replayObjects, err := objectstore.NewS3Store(config.ReplayS3)
	if err != nil {
		return nil, nil, fmt.Errorf("configure Collector Replay object Store: %w", err)
	}
	if err := stagingObjects.CheckReady(ctx); err != nil {
		return nil, nil, fmt.Errorf("check Collector staging object Store: %w", err)
	}
	if err := replayObjects.CheckReady(ctx); err != nil {
		return nil, nil, fmt.Errorf("check Collector Replay object Store: %w", err)
	}
	return stagingObjects, replayObjects, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
