package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

type config struct {
	Transport     string
	Store         string
	Endpoint      string
	Token         string
	KafkaBrokers  []string
	KafkaTopic    string
	KafkaClientID string
	ProducerID    string
	DatasetID     string
	Seed          string
	EventEpoch    time.Time
	Roots         uint64
	Rate          float64
	StartDelay    time.Duration
	Timeout       time.Duration
	MaximumLate   time.Duration
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("obs-bench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var parsed config
	var eventEpoch string
	var kafkaBrokers string
	flags.StringVar(&parsed.Transport, "transport", "http", "acceptance transport: http or kafka")
	flags.StringVar(&parsed.Store, "store", "postgres", "retained observability Store: postgres or clickhouse")
	flags.StringVar(&parsed.Endpoint, "endpoint", "", "Stage A Collector v2 Batch endpoint")
	flags.StringVar(&parsed.Token, "token", "", "Collector service token")
	flags.StringVar(&kafkaBrokers, "kafka-brokers", "", "comma-separated Kafka bootstrap brokers")
	flags.StringVar(&parsed.KafkaTopic, "kafka-topic", "nano.observability.agent-trace.v1", "Durable Agent Trace topic")
	flags.StringVar(&parsed.KafkaClientID, "kafka-client-id", "nano-obs-bench-producer", "Kafka producer client identity")
	flags.StringVar(&parsed.ProducerID, "producer-id", "nano-obs-bench/loadgen", "Collector producer identity")
	flags.StringVar(&parsed.DatasetID, "dataset", "", "versioned dataset identity")
	flags.StringVar(&parsed.Seed, "seed", "", "deterministic fixture seed")
	flags.StringVar(&eventEpoch, "event-epoch", "", "fixture event epoch in RFC3339")
	flags.Uint64Var(&parsed.Roots, "roots", 0, "number of root Agent Runs")
	flags.Float64Var(&parsed.Rate, "rate", 0, "offered root Agent Runs per second")
	flags.DurationVar(&parsed.StartDelay, "start-delay", time.Second, "delay before the first open-loop arrival")
	flags.DurationVar(&parsed.Timeout, "timeout", 10*time.Minute, "overall run timeout")
	flags.DurationVar(&parsed.MaximumLate, "maximum-late", 5*time.Millisecond, "maximum valid arrival lateness")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	parsed.Transport = strings.TrimSpace(parsed.Transport)
	parsed.Store = strings.TrimSpace(parsed.Store)
	parsed.Endpoint = strings.TrimSpace(parsed.Endpoint)
	parsed.Token = strings.TrimSpace(parsed.Token)
	parsed.KafkaTopic = strings.TrimSpace(parsed.KafkaTopic)
	parsed.KafkaClientID = strings.TrimSpace(parsed.KafkaClientID)
	for _, broker := range strings.Split(kafkaBrokers, ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			parsed.KafkaBrokers = append(parsed.KafkaBrokers, broker)
		}
	}
	parsed.ProducerID = strings.TrimSpace(parsed.ProducerID)
	parsed.DatasetID = strings.TrimSpace(parsed.DatasetID)
	parsed.Seed = strings.TrimSpace(parsed.Seed)
	if (parsed.Transport != "http" && parsed.Transport != "kafka") || (parsed.Store != "postgres" && parsed.Store != "clickhouse") ||
		(parsed.Transport == "http" && parsed.Store != "postgres") || parsed.ProducerID == "" || parsed.DatasetID == "" || parsed.Seed == "" ||
		eventEpoch == "" || parsed.Roots == 0 || parsed.Rate <= 0 || parsed.StartDelay < 0 || parsed.Timeout <= 0 || parsed.MaximumLate < 0 {
		return config{}, errors.New("observability benchmark configuration is incomplete")
	}
	if parsed.Transport == "http" && (parsed.Endpoint == "" || parsed.Token == "") {
		return config{}, errors.New("Stage A HTTP benchmark configuration is incomplete")
	}
	if parsed.Transport == "kafka" && (len(parsed.KafkaBrokers) == 0 || parsed.KafkaTopic == "" || parsed.KafkaClientID == "") {
		return config{}, errors.New("Kafka benchmark configuration is incomplete")
	}
	var err error
	parsed.EventEpoch, err = time.Parse(time.RFC3339Nano, eventEpoch)
	if err != nil {
		return config{}, fmt.Errorf("parse event epoch: %w", err)
	}
	parsed.EventEpoch = parsed.EventEpoch.UTC()
	return parsed, nil
}

type runOutput struct {
	SchemaVersion             int              `json:"schema_version"`
	Stage                     obsbench.Stage   `json:"stage"`
	DatasetID                 string           `json:"dataset_id"`
	ManifestSHA256            string           `json:"manifest_sha256"`
	OfferedRootRunsPerSecond  float64          `json:"offered_root_runs_per_second"`
	AchievedRootRunsPerSecond float64          `json:"achieved_root_runs_per_second"`
	RootAgentRuns             uint64           `json:"root_agent_runs"`
	TotalAgentRuns            uint64           `json:"total_agent_runs"`
	Records                   uint64           `json:"records"`
	LateArrivals              uint64           `json:"late_arrivals"`
	ElapsedMilliseconds       float64          `json:"elapsed_ms"`
	ProducerStats             agentbatch.Stats `json:"producer_stats"`
}

func main() {
	parsed, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, parsed, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config config, output io.Writer) error {
	workload := obsbench.ReferenceWorkloadV1()
	manifest, err := obsbench.NewManifest(workload, config.DatasetID, config.Roots, config.EventEpoch)
	if err != nil {
		return err
	}
	_, manifestSHA256, err := manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	schedule, err := obsbench.NewArrivalSchedule(config.Rate, config.Roots)
	if err != nil {
		return err
	}
	var sender agentbatch.Sender
	stage := stageFor(config)
	closeSender := func() {}
	if config.Transport == "kafka" {
		producer, err := agentbatch.NewFranzKafkaProducer(agentbatch.FranzKafkaConfig{
			Brokers: config.KafkaBrokers, ClientID: config.KafkaClientID,
			MaxBufferedRecords: 10_000, MaxBufferedBytes: 32 * 1024 * 1024,
			DeliveryTimeout: 10 * time.Second, Linger: 5 * time.Millisecond,
		})
		if err != nil {
			return err
		}
		if err := producer.Ping(ctx); err != nil {
			producer.Close()
			return fmt.Errorf("check Kafka benchmark readiness: %w", err)
		}
		closeSender = producer.Close
		sender, err = agentbatch.NewKafkaSender(agentbatch.KafkaSenderConfig{Topic: config.KafkaTopic, Producer: producer})
	} else {
		sender, err = agentbatch.NewHTTPSender(agentbatch.HTTPSenderConfig{
			Endpoint: config.Endpoint, ServiceToken: config.Token, HTTPClient: &http.Client{Timeout: 10 * time.Second},
		})
	}
	if err != nil {
		closeSender()
		return err
	}
	defer closeSender()
	exporter, err := agentbatch.NewExporter(agentbatch.Config{
		ProducerID: config.ProducerID, Sender: sender,
		MaxPendingRecords: 10_000, MaxPendingBytes: 32 * 1024 * 1024,
		MaxBatchRecords: 128, MaxBatchBytes: 512 * 1024, MaxDelay: 250 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	startAt := time.Now().UTC().Add(config.StartDelay)
	result, runErr := obsbench.RunLevel(runCtx, obsbench.RunnerConfig{
		Workload: workload, Seed: config.Seed, EventEpoch: config.EventEpoch,
		Schedule: schedule, StartAt: startAt, MaximumArrivalLateness: config.MaximumLate, Sink: exporter,
	})
	shutdownErr := exporter.Shutdown(runCtx)
	if err := errors.Join(runErr, shutdownErr); err != nil {
		return err
	}
	finishedAt := time.Now().UTC()
	elapsed := finishedAt.Sub(startAt)
	if elapsed <= 0 {
		return errors.New("observability benchmark elapsed time is not positive")
	}
	summary := runOutput{
		SchemaVersion: 1, Stage: stage, DatasetID: config.DatasetID,
		ManifestSHA256: manifestSHA256, OfferedRootRunsPerSecond: config.Rate,
		AchievedRootRunsPerSecond: float64(result.RootAgentRuns) / elapsed.Seconds(),
		RootAgentRuns:             result.RootAgentRuns, TotalAgentRuns: result.TotalAgentRuns, Records: result.Records,
		LateArrivals: result.LateArrivals, ElapsedMilliseconds: float64(elapsed) / float64(time.Millisecond),
		ProducerStats: exporter.Stats(),
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(summary)
}

func stageFor(config config) obsbench.Stage {
	if config.Transport == "kafka" {
		if config.Store == "clickhouse" {
			return obsbench.StageKafkaClickHouse
		}
		return obsbench.StageKafkaPostgres
	}
	return obsbench.StageDirectPostgres
}
