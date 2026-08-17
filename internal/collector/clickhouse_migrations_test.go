package collector

import (
	"context"
	"strings"
	"testing"
)

func TestClickHouseMigrationsDefineDurableRawRecordContract(t *testing.T) {
	sql := ClickHouseMigrationsSQL()
	for _, required := range []string{
		"create table if not exists obs_trace_records_raw",
		"canonical_sha256 FixedString(64)",
		"source_topic LowCardinality(String)",
		"source_partition Int32",
		"source_offset Int64",
		"ingest_version UInt64",
		"ENGINE = ReplacingMergeTree(ingest_version)",
		"PARTITION BY toYYYYMM(occurred_at)",
		"ORDER BY (notebook_id, trace_id, identity_key, sequence)",
		"TTL occurred_at + INTERVAL 90 DAY DELETE",
		"CODEC(ZSTD(3))",
		"create table if not exists obs_trace_summaries",
		"models Array(LowCardinality(String))",
		"projected_sequence UInt32",
		"ENGINE = ReplacingMergeTree(ingest_version)",
		"ORDER BY trace_id",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("ClickHouse migrations are missing %q", required)
		}
	}
}

func TestRunClickHouseMigrationsExecutesSchema(t *testing.T) {
	executor := &recordingClickHouseMigrationExecutor{}
	if err := RunClickHouseMigrations(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	if len(executor.queries) != len(clickHouseMigrationStatements) {
		t.Fatalf("ClickHouse migration queries=%d want=%d", len(executor.queries), len(clickHouseMigrationStatements))
	}
	for index, query := range executor.queries {
		if query != clickHouseMigrationStatements[index] {
			t.Fatalf("ClickHouse migration query %d changed", index)
		}
	}
}

type recordingClickHouseMigrationExecutor struct {
	queries []string
}

func (e *recordingClickHouseMigrationExecutor) Exec(_ context.Context, query string, _ ...any) error {
	e.queries = append(e.queries, query)
	return nil
}
