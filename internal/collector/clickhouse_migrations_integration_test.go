package collector

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestClickHouseMigrationsAgainstRealServer(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("NANO_TEST_CLICKHOUSE_ADDR"))
	if address == "" {
		t.Skip("NANO_TEST_CLICKHOUSE_ADDR is not set")
	}
	connection, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{address},
		Auth: clickhouse.Auth{
			Database: "nano_observability",
			Username: "nano_observability",
			Password: "nano-observability",
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := connection.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := RunClickHouseMigrations(ctx, connection); err != nil {
		t.Fatal(err)
	}

	var createStatement string
	if err := connection.QueryRow(ctx, "SHOW CREATE TABLE obs_trace_records_raw").Scan(&createStatement); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ReplacingMergeTree(ingest_version)",
		"PARTITION BY toYYYYMM(occurred_at)",
		"ORDER BY (notebook_id, trace_id, identity_key, sequence)",
		"TTL occurred_at + toIntervalDay(90)",
	} {
		if !strings.Contains(createStatement, required) {
			t.Errorf("live ClickHouse schema is missing %q:\n%s", required, createStatement)
		}
	}
	if err := connection.QueryRow(ctx, "SHOW CREATE TABLE obs_replay_payload_refs").Scan(&createStatement); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ReplacingMergeTree(ingest_version)", "ORDER BY (attachment_id, metadata_sha256)"} {
		if !strings.Contains(createStatement, required) {
			t.Errorf("live ClickHouse Replay schema is missing %q:\n%s", required, createStatement)
		}
	}
	for table, orderBy := range map[string]string{
		"obs_trace_tombstones":  "ORDER BY (trace_id, command_sha256)",
		"obs_trace_purge_state": "ORDER BY (command_id, command_sha256)",
	} {
		if err := connection.QueryRow(ctx, "SHOW CREATE TABLE "+table).Scan(&createStatement); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(createStatement, orderBy) {
			t.Errorf("live ClickHouse %s schema is missing %q:\n%s", table, orderBy, createStatement)
		}
	}
}
