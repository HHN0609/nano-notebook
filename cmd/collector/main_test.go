package main

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadConfigUsesOnlyClickHouse(t *testing.T) {
	t.Setenv("NANO_COLLECTOR_STORE", "postgres")
	t.Setenv("NANO_COLLECTOR_DATABASE_URL", "postgres://retired")
	t.Setenv("NANO_CLICKHOUSE_ADDR", "clickhouse-a:9000,clickhouse-b:9000")
	t.Setenv("NANO_CLICKHOUSE_DATABASE", "nano_observability")
	t.Setenv("NANO_CLICKHOUSE_USER", "nano_observability")
	t.Setenv("NANO_CLICKHOUSE_PASSWORD", "password")
	t.Setenv("NANO_COLLECTOR_ADDR", ":18082")
	t.Setenv("NANO_COLLECTOR_SERVICE_TOKEN", "test-service-token")
	t.Setenv("NANO_COLLECTOR_QUERY_TOKEN", "test-query-token")
	t.Setenv("NANO_COLLECTOR_PRODUCER_ID", "test-worker")
	t.Setenv("NANO_COLLECTOR_PRODUCER_ID_PREFIX", "test-")
	t.Setenv("NANO_REPLAY_STAGING_S3_ENDPOINT", "staging.internal:9000")
	t.Setenv("NANO_REPLAY_STAGING_S3_ACCESS_KEY_ID", "collector-staging-reader")
	t.Setenv("NANO_REPLAY_STAGING_S3_SECRET_ACCESS_KEY", "staging-reader-secret")
	t.Setenv("NANO_REPLAY_STAGING_S3_BUCKET", "worker-staging")
	t.Setenv("NANO_REPLAY_S3_ENDPOINT", "replay.internal:9000")
	t.Setenv("NANO_REPLAY_S3_ACCESS_KEY_ID", "collector-replay-writer")
	t.Setenv("NANO_REPLAY_S3_SECRET_ACCESS_KEY", "replay-writer-secret")
	t.Setenv("NANO_REPLAY_S3_BUCKET", "collector-replay")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	for _, retired := range []string{"StoreBackend", "DatabaseURL", "DatabaseMaxConns", "DatabaseMinConns", "ProjectionDatabaseMaxConns", "ProjectionDatabaseMinConns", "QueryDatabaseMaxConns", "QueryDatabaseMinConns"} {
		if _, found := reflect.TypeOf(config).FieldByName(retired); found {
			t.Errorf("Collector config still exposes retired field %s", retired)
		}
	}
	if config.Addr != ":18082" || config.ServiceToken != "test-service-token" || config.QueryToken != "test-query-token" || config.ProducerID != "test-worker" || config.ProducerIDPrefix != "test-" {
		t.Fatalf("Collector config = %#v", config)
	}
	if config.ReplayStagingS3.Endpoint != "staging.internal:9000" || config.ReplayStagingS3.AccessKeyID != "collector-staging-reader" || config.ReplayStagingS3.Bucket != "worker-staging" ||
		config.ReplayS3.Endpoint != "replay.internal:9000" || config.ReplayS3.AccessKeyID != "collector-replay-writer" || config.ReplayS3.Bucket != "collector-replay" {
		t.Fatalf("Collector Replay config = %#v", config)
	}
}

func TestLoadConfigSelectsClickHouseQueryStore(t *testing.T) {
	t.Setenv("NANO_COLLECTOR_STORE", "clickhouse")
	t.Setenv("NANO_CLICKHOUSE_ADDR", "clickhouse-a:9000,clickhouse-b:9000")
	t.Setenv("NANO_CLICKHOUSE_DATABASE", "nano_observability")
	t.Setenv("NANO_CLICKHOUSE_USER", "nano_observability")
	t.Setenv("NANO_CLICKHOUSE_PASSWORD", "password")

	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.ClickHouseAddr) != 2 ||
		config.ClickHouseDatabase != "nano_observability" || config.ClickHouseUser != "nano_observability" ||
		config.ClickHousePassword != "password" || config.ClickHouseDialTimeout != 10*time.Second {
		t.Fatalf("config=%#v", config)
	}
}
