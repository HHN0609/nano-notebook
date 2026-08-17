package collector

import (
	"context"
	"errors"
)

type clickHouseMigrationExecutor interface {
	Exec(context.Context, string, ...any) error
}

func RunClickHouseMigrations(ctx context.Context, executor clickHouseMigrationExecutor) error {
	if executor == nil {
		return errors.New("nil Collector ClickHouse migration executor")
	}
	for _, statement := range clickHouseMigrationStatements {
		if err := executor.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// ClickHouseMigrationsSQL returns the idempotent schema for retained Durable
// Agent Trace facts. Raw rows repeat their Trace descriptor intentionally so a
// retained fact never depends on a mutable parent row.
func ClickHouseMigrationsSQL() string {
	return clickHouseRawMigrationSQL + "\n" + clickHouseSummaryMigrationSQL
}

var clickHouseMigrationStatements = []string{clickHouseRawMigrationSQL, clickHouseSummaryMigrationSQL}

const clickHouseRawMigrationSQL = `
create table if not exists obs_trace_records_raw (
	trace_id String CODEC(ZSTD(3)),
	workload_kind LowCardinality(String),
	workload_id String CODEC(ZSTD(3)),
	run_id String CODEC(ZSTD(3)),
	chat_id String CODEC(ZSTD(3)),
	notebook_id String CODEC(ZSTD(3)),
	root_span_id String CODEC(ZSTD(3)),
	agent_name LowCardinality(String),
	trace_schema_version UInt16,
	semantic_convention_version UInt16,
	sequence UInt32,
	identity_key String CODEC(ZSTD(3)),
	kind LowCardinality(String),
	span_id String CODEC(ZSTD(3)),
	parent_span_id String CODEC(ZSTD(3)),
	target_trace_id String CODEC(ZSTD(3)),
	target_span_id String CODEC(ZSTD(3)),
	name String CODEC(ZSTD(3)),
	status LowCardinality(String),
	occurred_at DateTime64(9, 'UTC'),
	occurred_at_unix_nano Int64,
	payload_version UInt16,
	canonical_payload String CODEC(ZSTD(3)),
	canonical_sha256 FixedString(64),
	source_topic LowCardinality(String),
	source_partition Int32,
	source_offset Int64,
	ingest_version UInt64,
	ingested_at DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(ingest_version)
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (notebook_id, trace_id, identity_key, sequence)
TTL occurred_at + INTERVAL 90 DAY DELETE
SETTINGS index_granularity = 8192;
`

const clickHouseSummaryMigrationSQL = `
create table if not exists obs_trace_summaries (
	trace_id String CODEC(ZSTD(3)),
	workload_kind LowCardinality(String),
	workload_id String CODEC(ZSTD(3)),
	run_id String CODEC(ZSTD(3)),
	chat_id String CODEC(ZSTD(3)),
	notebook_id String CODEC(ZSTD(3)),
	root_span_id String CODEC(ZSTD(3)),
	agent_name LowCardinality(String),
	started_at DateTime64(9, 'UTC'),
	started_at_unix_nano Int64,
	last_observed_unix_nano Int64,
	ended_at_unix_nano Nullable(Int64),
	duration_nanoseconds Nullable(Int64),
	status LowCardinality(String),
	active Bool,
	models Array(LowCardinality(String)),
	input_tokens Nullable(Int64),
	output_tokens Nullable(Int64),
	total_tokens Nullable(Int64),
	cost_known Bool,
	cost_amount Nullable(Float64),
	cost_currency LowCardinality(String),
	cost_source LowCardinality(String),
	attempt_count UInt32,
	committed_sequence UInt32,
	projected_sequence UInt32,
	ingest_version UInt64,
	projected_at DateTime64(9, 'UTC') DEFAULT now64(9)
)
ENGINE = ReplacingMergeTree(ingest_version)
PARTITION BY toYYYYMM(started_at)
ORDER BY trace_id
TTL started_at + INTERVAL 90 DAY DELETE
SETTINGS index_granularity = 8192;
`
