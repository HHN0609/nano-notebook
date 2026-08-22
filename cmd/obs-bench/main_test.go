package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

func TestParseConfigRequiresReproducibleStageAInputs(t *testing.T) {
	config, err := parseConfig([]string{
		"-endpoint", "http://127.0.0.1:8082/internal/agent-observability/v2/batches",
		"-token", "test-token",
		"-dataset", "smoke-100-v1",
		"-seed", "smoke-seed-v1",
		"-event-epoch", "2026-08-17T00:00:00Z",
		"-roots", "100",
		"-rate", "25",
		"-start-delay", "250ms",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Roots != 100 || config.Rate != 25 || config.DatasetID != "smoke-100-v1" ||
		config.EventEpoch != time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) || config.StartDelay != 250*time.Millisecond {
		t.Fatalf("config=%#v", config)
	}
	if _, err := parseConfig([]string{"-token", "test-token", "-dataset", "x", "-seed", "y", "-event-epoch", "2026-08-17T00:00:00Z"}); err == nil {
		t.Fatal("missing endpoint was accepted")
	}
}

func TestParseConfigSelectsKafkaAcceptanceWithoutHTTPSecrets(t *testing.T) {
	config, err := parseConfig([]string{
		"-transport", "kafka", "-kafka-brokers", "127.0.0.1:59092",
		"-kafka-topic", "nano.observability.agent-trace.v1",
		"-dataset", "stage-b-smoke-v1", "-seed", "stage-b-seed-v1",
		"-event-epoch", "2026-08-17T00:00:00Z", "-roots", "100", "-rate", "25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Transport != "kafka" || len(config.KafkaBrokers) != 1 || config.KafkaBrokers[0] != "127.0.0.1:59092" ||
		config.KafkaTopic != "nano.observability.agent-trace.v1" || config.KafkaMaxRetries != 3 {
		t.Fatalf("config=%#v", config)
	}
	if _, err := parseConfig([]string{
		"-transport", "kafka", "-dataset", "x", "-seed", "y", "-event-epoch", "2026-08-17T00:00:00Z", "-roots", "1", "-rate", "1",
	}); err == nil {
		t.Fatal("Kafka transport without brokers was accepted")
	}
}

func TestParseConfigLabelsKafkaClickHouseAsStageC(t *testing.T) {
	config, err := parseConfig([]string{
		"-transport", "kafka", "-store", "clickhouse", "-kafka-brokers", "127.0.0.1:59092",
		"-dataset", "stage-c-smoke-v1", "-seed", "stage-c-seed-v1",
		"-event-epoch", "2026-08-17T00:00:00Z", "-roots", "100", "-rate", "25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Store != "clickhouse" || stageFor(config) != "C_kafka_clickhouse" {
		t.Fatalf("config=%#v stage=%q", config, stageFor(config))
	}
	if _, err := parseConfig([]string{
		"-store", "clickhouse", "-endpoint", "http://collector", "-token", "token",
		"-dataset", "x", "-seed", "y", "-event-epoch", "2026-08-17T00:00:00Z", "-roots", "1", "-rate", "1",
	}); err == nil {
		t.Fatal("direct HTTP plus ClickHouse was accepted as a comparable stage")
	}
}

func TestRunPublishesReferenceCycleThroughStageAHTTP(t *testing.T) {
	store := collector.NewMemoryStore()
	ingestor, err := collector.NewIngestor(collector.IngestorConfig{ProducerID: "nano-obs-bench/loadgen", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := collector.NewHTTPHandler(collector.HTTPConfig{
		Ingestor: ingestor, ServiceToken: "test-token", MaxBodyBytes: 2 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var output bytes.Buffer
	err = run(context.Background(), config{
		Endpoint: server.URL + "/internal/agent-observability/v2/batches", Token: "test-token",
		ProducerID: "nano-obs-bench/loadgen", DatasetID: "smoke-100-v1", Seed: "smoke-seed-v1",
		EventEpoch: time.Unix(1_700_000_000, 0).UTC(), Roots: 100, Rate: 100_000,
		Timeout: time.Minute, MaximumLate: time.Second,
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.RecordCount(); got != 1_620 {
		t.Fatalf("stored records=%d, want 1620", got)
	}
	if encoded := output.String(); !strings.Contains(encoded, `"root_agent_runs":100`) || !strings.Contains(encoded, `"records":1620`) {
		t.Fatalf("output=%s", encoded)
	}
}
