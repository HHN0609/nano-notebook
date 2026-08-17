package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agenttraceprocessor"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	StoreBackend           string
	DatabaseURL            string
	DatabaseMaxConns       int32
	ClickHouseAddr         []string
	ClickHouseDatabase     string
	ClickHouseUser         string
	ClickHousePassword     string
	ClickHouseMaxOpenConns int
	ClickHouseMaxIdleConns int
	ClickHouseDialTimeout  time.Duration
	Brokers                []string
	Topic                  string
	QuarantineTopic        string
	GroupID                string
	ClientID               string
	ProducerIDPrefix       string
	MaxPollRecords         int
	FetchMaxBytes          int64
	FetchMaxWait           time.Duration
	SessionTimeout         time.Duration
	RebalanceTimeout       time.Duration
	ReplayStagingS3        objectstore.S3Config
	ReplayS3               objectstore.S3Config
	MetricsAddr            string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("Agent Trace Processor configuration invalid", "error", err)
		os.Exit(1)
	}
	if err := run(ctx, config); err != nil {
		slog.Error("Agent Trace Processor stopped with error", "error", err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	value := func(key, fallback string) string {
		if result := strings.TrimSpace(getenv(key)); result != "" {
			return result
		}
		return fallback
	}
	parseInt := func(key string, fallback int64) (int64, error) {
		raw := value(key, strconv.FormatInt(fallback, 10))
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", key, err)
		}
		return parsed, nil
	}
	parseDuration := func(key string, fallback time.Duration) (time.Duration, error) {
		parsed, err := time.ParseDuration(value(key, fallback.String()))
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", key, err)
		}
		return parsed, nil
	}
	databaseMaxConns, err := parseInt("NANO_AGENT_TRACE_PROCESSOR_DATABASE_MAX_CONNS", 16)
	if err != nil {
		return config{}, err
	}
	clickHouseMaxOpenConns, err := parseInt("NANO_CLICKHOUSE_MAX_OPEN_CONNS", 16)
	if err != nil {
		return config{}, err
	}
	clickHouseMaxIdleConns, err := parseInt("NANO_CLICKHOUSE_MAX_IDLE_CONNS", 8)
	if err != nil {
		return config{}, err
	}
	clickHouseDialTimeout, err := parseDuration("NANO_CLICKHOUSE_DIAL_TIMEOUT", 10*time.Second)
	if err != nil {
		return config{}, err
	}
	maxPollRecords, err := parseInt("NANO_AGENT_TRACE_PROCESSOR_MAX_POLL_RECORDS", 128)
	if err != nil {
		return config{}, err
	}
	fetchMaxBytes, err := parseInt("NANO_AGENT_TRACE_PROCESSOR_FETCH_MAX_BYTES", 8*1024*1024)
	if err != nil {
		return config{}, err
	}
	fetchMaxWait, err := parseDuration("NANO_AGENT_TRACE_PROCESSOR_FETCH_MAX_WAIT", 100*time.Millisecond)
	if err != nil {
		return config{}, err
	}
	sessionTimeout, err := parseDuration("NANO_AGENT_TRACE_PROCESSOR_SESSION_TIMEOUT", 15*time.Second)
	if err != nil {
		return config{}, err
	}
	rebalanceTimeout, err := parseDuration("NANO_AGENT_TRACE_PROCESSOR_REBALANCE_TIMEOUT", 5*time.Minute)
	if err != nil {
		return config{}, err
	}
	parsed := config{
		StoreBackend: value("NANO_AGENT_TRACE_PROCESSOR_STORE", "postgres"),
		DatabaseURL:  value("NANO_AGENT_TRACE_PROCESSOR_DATABASE_URL", ""), DatabaseMaxConns: int32(databaseMaxConns),
		ClickHouseAddr:     strings.Split(value("NANO_CLICKHOUSE_ADDR", ""), ","),
		ClickHouseDatabase: value("NANO_CLICKHOUSE_DATABASE", "nano_observability"),
		ClickHouseUser:     value("NANO_CLICKHOUSE_USER", ""), ClickHousePassword: value("NANO_CLICKHOUSE_PASSWORD", ""),
		ClickHouseMaxOpenConns: int(clickHouseMaxOpenConns), ClickHouseMaxIdleConns: int(clickHouseMaxIdleConns),
		ClickHouseDialTimeout: clickHouseDialTimeout,
		Brokers:               strings.Split(value("NANO_KAFKA_BROKERS", ""), ","),
		Topic:                 value("NANO_AGENT_TRACE_TOPIC", "nano.observability.agent-trace.v1"),
		QuarantineTopic:       value("NANO_AGENT_TRACE_QUARANTINE_TOPIC", "nano.observability.agent-trace-quarantine.v1"),
		GroupID:               value("NANO_AGENT_TRACE_PROCESSOR_GROUP_ID", "nano-agent-trace-storage-v1"),
		ClientID:              value("NANO_AGENT_TRACE_PROCESSOR_CLIENT_ID", "nano-agent-trace-processor"),
		ProducerIDPrefix:      value("NANO_AGENT_TRACE_PROCESSOR_PRODUCER_ID_PREFIX", "nano-"),
		MaxPollRecords:        int(maxPollRecords), FetchMaxBytes: fetchMaxBytes, FetchMaxWait: fetchMaxWait,
		SessionTimeout: sessionTimeout, RebalanceTimeout: rebalanceTimeout,
		ReplayStagingS3: objectstore.S3Config{
			Endpoint: value("NANO_REPLAY_STAGING_S3_ENDPOINT", ""), AccessKeyID: value("NANO_REPLAY_STAGING_S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: value("NANO_REPLAY_STAGING_S3_SECRET_ACCESS_KEY", ""), Bucket: value("NANO_REPLAY_STAGING_S3_BUCKET", ""),
			Region: value("NANO_REPLAY_STAGING_S3_REGION", "us-east-1"),
		},
		ReplayS3: objectstore.S3Config{
			Endpoint: value("NANO_REPLAY_S3_ENDPOINT", ""), AccessKeyID: value("NANO_REPLAY_S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: value("NANO_REPLAY_S3_SECRET_ACCESS_KEY", ""), Bucket: value("NANO_REPLAY_S3_BUCKET", ""),
			Region: value("NANO_REPLAY_S3_REGION", "us-east-1"),
		},
		MetricsAddr: value("NANO_AGENT_TRACE_PROCESSOR_METRICS_ADDR", "0.0.0.0:9096"),
	}
	postgresInvalid := parsed.StoreBackend == "postgres" && (parsed.DatabaseURL == "" || parsed.DatabaseMaxConns < 1 || parsed.DatabaseMaxConns > 256)
	clickHouseInvalid := parsed.StoreBackend == "clickhouse" &&
		(len(parsed.ClickHouseAddr) == 0 || strings.TrimSpace(parsed.ClickHouseAddr[0]) == "" || parsed.ClickHouseDatabase == "" ||
			parsed.ClickHouseUser == "" || parsed.ClickHousePassword == "" || parsed.ClickHouseMaxOpenConns < 1 ||
			parsed.ClickHouseMaxOpenConns > 256 || parsed.ClickHouseMaxIdleConns < 0 ||
			parsed.ClickHouseMaxIdleConns > parsed.ClickHouseMaxOpenConns || parsed.ClickHouseDialTimeout <= 0)
	if (parsed.StoreBackend != "postgres" && parsed.StoreBackend != "clickhouse") || postgresInvalid || clickHouseInvalid ||
		len(parsed.Brokers) == 0 || strings.TrimSpace(parsed.Brokers[0]) == "" || parsed.Topic == "" || parsed.QuarantineTopic == "" ||
		parsed.GroupID == "" || parsed.ClientID == "" || parsed.ProducerIDPrefix == "" || parsed.MaxPollRecords < 1 ||
		parsed.FetchMaxBytes < 1 || parsed.FetchMaxWait <= 0 || parsed.SessionTimeout <= 0 || parsed.RebalanceTimeout <= 0 ||
		parsed.ReplayStagingS3.Endpoint == "" || parsed.ReplayStagingS3.AccessKeyID == "" || parsed.ReplayStagingS3.SecretAccessKey == "" || parsed.ReplayStagingS3.Bucket == "" ||
		parsed.ReplayS3.Endpoint == "" || parsed.ReplayS3.AccessKeyID == "" || parsed.ReplayS3.SecretAccessKey == "" || parsed.ReplayS3.Bucket == "" || parsed.MetricsAddr == "" {
		return config{}, errors.New("Agent Trace Processor configuration is incomplete or inconsistent")
	}
	return parsed, nil
}

func run(ctx context.Context, config config) error {
	metricsRegistry := metrics.NewRegistry()
	metricsCatalog := metrics.NewCatalog(metricsRegistry)
	metricsServer := metrics.NewAdminServer(config.MetricsAddr, metricsRegistry)
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Agent Trace Processor metrics listener failed", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metricsServer.Shutdown(shutdownCtx)
	}()
	stagingObjects, err := objectstore.NewS3Store(config.ReplayStagingS3)
	if err != nil {
		return err
	}
	replayObjects, err := objectstore.NewS3Store(config.ReplayS3)
	if err != nil {
		return err
	}
	if err := errors.Join(stagingObjects.CheckReady(ctx), replayObjects.CheckReady(ctx)); err != nil {
		return err
	}
	var store collector.Store
	switch config.StoreBackend {
	case "postgres":
		poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
		if err != nil {
			return fmt.Errorf("parse Agent Trace Processor PostgreSQL configuration: %w", err)
		}
		poolConfig.MaxConns = config.DatabaseMaxConns
		pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			return fmt.Errorf("open Agent Trace Processor PostgreSQL: %w", err)
		}
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("ping Agent Trace Processor PostgreSQL: %w", err)
		}
		if err := collector.RunMigrations(ctx, pool); err != nil {
			return fmt.Errorf("migrate Agent Trace Processor PostgreSQL: %w", err)
		}
		store, err = collector.NewPostgresStoreWithReplay(pool, stagingObjects, replayObjects)
		if err != nil {
			return err
		}
	case "clickhouse":
		connection, err := clickhouse.Open(&clickhouse.Options{
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
		if err != nil {
			return fmt.Errorf("open Agent Trace Processor ClickHouse: %w", err)
		}
		defer connection.Close()
		if err := connection.Ping(ctx); err != nil {
			return fmt.Errorf("ping Agent Trace Processor ClickHouse: %w", err)
		}
		if err := collector.RunClickHouseMigrations(ctx, connection); err != nil {
			return fmt.Errorf("migrate Agent Trace Processor ClickHouse: %w", err)
		}
		clickHouseStore, storeErr := collector.NewClickHouseStore(connection)
		if storeErr != nil {
			return storeErr
		}
		store = clickHouseStore.WithMetrics(metricsCatalog)
	default:
		return fmt.Errorf("unsupported Agent Trace Processor Store %q", config.StoreBackend)
	}
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerIDPrefix: config.ProducerIDPrefix, Store: store})
	if err != nil {
		return err
	}
	consumer, err := agenttraceprocessor.NewFranzConsumer(agenttraceprocessor.FranzConsumerConfig{
		Brokers: config.Brokers, Topic: config.Topic, GroupID: config.GroupID, ClientID: config.ClientID,
		MaxPollRecords: config.MaxPollRecords, FetchMaxBytes: config.FetchMaxBytes, FetchMaxWait: config.FetchMaxWait,
		SessionTimeout: config.SessionTimeout, RebalanceTimeout: config.RebalanceTimeout,
		Metrics: metricsCatalog,
	})
	if err != nil {
		return err
	}
	defer consumer.Close()
	quarantineProducer, err := agentbatch.NewFranzKafkaProducer(agentbatch.FranzKafkaConfig{
		Brokers: config.Brokers, ClientID: config.ClientID + "-quarantine",
		MaxBufferedRecords: 1_000, MaxBufferedBytes: 8 * 1024 * 1024,
		DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	defer quarantineProducer.Close()
	if err := errors.Join(consumer.Ping(ctx), quarantineProducer.Ping(ctx)); err != nil {
		return err
	}
	quarantine, err := agenttraceprocessor.NewKafkaQuarantineWriter(agenttraceprocessor.KafkaQuarantineConfig{
		Topic: config.QuarantineTopic, Producer: quarantineProducer,
	})
	if err != nil {
		return err
	}
	processor, err := agenttraceprocessor.New(agenttraceprocessor.Config{Topic: config.Topic, Ingestor: ingestor, Quarantine: quarantine, Metrics: metricsCatalog})
	if err != nil {
		return err
	}
	runner, err := agenttraceprocessor.NewRunner(agenttraceprocessor.RunnerConfig{
		Consumer: consumer, Handler: processor, RetryBackoff: 250 * time.Millisecond,
		Metrics:     metricsCatalog,
		ReportError: func(err error) { slog.Error("Agent Trace processing failed", "error", err) },
	})
	if err != nil {
		return err
	}
	slog.Info("Agent Trace Processor started", "topic", config.Topic, "group_id", config.GroupID, "brokers", config.Brokers, "store", config.StoreBackend)
	return runner.Run(ctx)
}
