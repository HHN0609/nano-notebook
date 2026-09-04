package main

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadConfigBuildsClickHouseProcessor(t *testing.T) {
	env := map[string]string{
		"NANO_AGENT_TRACE_PROCESSOR_STORE":         "postgres",
		"NANO_AGENT_TRACE_PROCESSOR_DATABASE_URL":  "postgres://retired",
		"NANO_CLICKHOUSE_ADDR":                     "clickhouse-a:9000,clickhouse-b:9000",
		"NANO_CLICKHOUSE_DATABASE":                 "nano_observability",
		"NANO_CLICKHOUSE_USER":                     "nano-observability",
		"NANO_CLICKHOUSE_PASSWORD":                 "password",
		"NANO_KAFKA_BROKERS":                       "kafka:9092",
		"NANO_REPLAY_STAGING_S3_ENDPOINT":          "minio:9000",
		"NANO_REPLAY_STAGING_S3_ACCESS_KEY_ID":     "nano",
		"NANO_REPLAY_STAGING_S3_SECRET_ACCESS_KEY": "password",
		"NANO_REPLAY_STAGING_S3_BUCKET":            "staging",
		"NANO_REPLAY_S3_ENDPOINT":                  "minio:9000",
		"NANO_REPLAY_S3_ACCESS_KEY_ID":             "nano",
		"NANO_REPLAY_S3_SECRET_ACCESS_KEY":         "password",
		"NANO_REPLAY_S3_BUCKET":                    "replay",
	}
	config, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if len(config.ClickHouseAddr) != 2 ||
		config.ClickHouseDatabase != "nano_observability" || config.ClickHouseUser != "nano-observability" ||
		config.ClickHousePassword != "password" || config.ClickHouseMaxOpenConns != 16 ||
		config.ClickHouseMaxIdleConns != 8 || config.ClickHouseDialTimeout != 10*time.Second {
		t.Fatalf("config=%#v", config)
	}
	if config.PurgeTopic != "nano.observability.agent-trace-purge.v1" || config.PurgeProducerID != "nano-worker" {
		t.Fatalf("purge config=%#v", config)
	}
	for _, retired := range []string{"StoreBackend", "DatabaseURL", "DatabaseMaxConns"} {
		if _, found := reflect.TypeOf(config).FieldByName(retired); found {
			t.Errorf("Agent Trace Processor config still exposes retired field %s", retired)
		}
	}
}

func TestLoadConfigRejectsIncompleteClickHouse(t *testing.T) {
	env := map[string]string{
		"NANO_AGENT_TRACE_PROCESSOR_STORE": "clickhouse",
		"NANO_CLICKHOUSE_ADDR":             "clickhouse:9000",
	}
	if _, err := loadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("incomplete selected ClickHouse Store was accepted")
	}
}

func TestLoadConfigRejectsMissingDurableDependencies(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("empty Agent Trace Processor configuration was accepted")
	}
}
