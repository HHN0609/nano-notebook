# Kafka and ClickHouse Observability Evaluation

Date: 2026-08-17

Environment: GCP `asia-southeast1-b`, ARM64 `c4a-standard-8` SUT, ARM64 `c4a-standard-4` load generator, 100 GiB Hyperdisk Balanced at 12,000 IOPS / 500 MiB/s

Workload: deterministic `agent-run-reference-v1`, 16.2 Durable Records and 1.05 total Agent Runs per root Agent Run

## Executive result

The migration improved the highest rate that remained lossless and within the five-second searchable-freshness target from a conservative 200 roots/s for direct HTTP plus PostgreSQL, and 230 roots/s for Kafka plus PostgreSQL, to 380 roots/s for Kafka plus ClickHouse. The final Stage C rate is 1.65x Stage B and 1.90x the conservative current-architecture baseline. The first long-run Stage C failure was 400 roots/s.

ClickHouse is not a universal query-latency improvement. On the existing transactional Trace list/filter/detail corpus it was 18.10x slower at p95 than PostgreSQL. It was substantially faster for cross-Trace analytics: 24.15x at p95 for the 1.615-million-record time-bucket aggregation and 3.67x for grouped duration percentiles. The architecture should therefore expose separate product-query and analytical read models instead of routing every Dashboard query through an analytical scan.

## Sustainable throughput and bottlenecks

All rates are offered root Agent Runs per second. A passing point requires zero producer drops, exact retained-row reconciliation, stable service processes, and no more than five seconds of searchable backlog.

| Stage | Highest passing point | First failing point | Observed bottleneck |
|---|---:|---:|---|
| A: direct HTTP + PostgreSQL | 200 conservative; 210 passed 120 s | 220-225 | Single exporter's bounded in-memory queue fills near a 210-220 roots/s service rate |
| B: Kafka + PostgreSQL | 230 | 250 | PostgreSQL projection queue remains beyond the five-second freshness target |
| C: Kafka + ClickHouse | 380 | 400 | ClickHouse validation/insert path exceeds five seconds after sustained merge pressure |

Stage C's authoritative 600-second passing run achieved 380.001 roots/s, retained 3,693,600 records and 239,400 Traces, had zero late arrivals and zero producer drops, and kept observed Kafka lag around 885-1,518 batches (about 2.2-3.8 seconds). It ended at lag zero with exact time-window reconciliation and all four consumers at zero restarts. The raw result is [`batched-authoritative-380-600s.json`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-c/batched-authoritative-380-600s.json).

At 400 roots/s, the producer still completed 600 seconds at 399.999 roots/s with zero drops, but Kafka lag reached 2,267 batches around minute seven and 2,993 around minute nine, exceeding the five-second freshness gate. It is therefore a failed sustained point even though its [`producer artifact`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-c/batched-authoritative-400-600s.json) is clean.

The Stage B passing run achieved 229.999 roots/s for 120 seconds, retained 447,120 records, and ended with zero Kafka and PostgreSQL projection backlog. Its raw result is [`confirm-230-120s.json`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-b/confirm-230-120s.json). At 250 roots/s, the projection queue still contained 1,164 entries after the run and violated the freshness gate.

Kafka separately absorbed 799.971 roots/s for 60 seconds with zero drops. That is a valid burst-ingress ceiling observation, not a sustainable searchable-throughput claim: storage lag accumulated and needed roughly 10-20 seconds after production stopped.

## Query latency

### Existing product query corpus

The `trace-product-query-v1` corpus contains recent lists, combined filters, deep cursors, exact identities, and ordinary/complex details. Both stages ran 20,000 HTTP requests at concurrency 16 against the same retained Trace identities.

| Store | Errors | p50 | p95 | p99 |
|---|---:|---:|---:|---:|
| PostgreSQL | 0 | 3.88 ms | 6.20 ms | 7.43 ms |
| ClickHouse after query correction | 0 | 65.06 ms | 112.19 ms | 129.38 ms |

ClickHouse was 18.10x slower at p95 and 17.40x slower at p99. The first ClickHouse query implementation was even worse because it selected every summary column before `LIMIT 1 BY trace_id`; the corrected implementation uses `ReplacingMergeTree FINAL`, removed errors, and reduced the final p95 to 112.19 ms. The remaining gap is architectural: this access pattern is index-oriented transactional lookup, not cross-row analytics.

Raw results: [`query-230-c16.json`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-b/query-230-c16.json) and [`query-final2-same-c16.json`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-c/query-final2-same-c16.json).

### Cross-Trace analytical queries

The analytical comparison uses exactly 1,615,140 raw records and 104,685 Trace summaries, concurrency 4, and equivalent PostgreSQL/ClickHouse queries.

| Query | PostgreSQL p95 / p99 | ClickHouse p95 / p99 | p95 speedup |
|---|---:|---:|---:|
| Event kind by minute | 1,255.96 / 1,256.02 ms | 52 / 58 ms | 24.15x |
| Duration p50/p95/p99 by agent and status | 36.73 / 37.02 ms | 10 / 12 ms | 3.67x |

The executable SQL is stored under [`benchmarks/observability`](../../benchmarks/observability/).

## Failure recovery and correctness

All four ClickHouse consumers were paused while the producer continued at 380 roots/s. The exact controlled run accumulated 6,609 Kafka batches. From consumer resume to Kafka lag zero and complete ClickHouse query visibility took 16.921 seconds, including the approximately 15-second Consumer Group session timeout/rebalance.

The final reconciliation was exact: 17,992,856 raw records and 1,166,210 unique Traces, with zero Kafka lag, zero producer drops, and zero consumer restarts. Measured data loss was zero.

Raw recovery evidence: [`producer result`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-c/batched-recovery-380-15s.json) and [`recovery result`](../../benchmarks/observability/results/2026-08-17-gcp-arm64/stage-c/batched-recovery-result.json).

Fresh consumer groups now explicitly reset to Kafka's earliest retained offset when no committed offset exists. Existing groups still resume from their committed offsets. This closes the migration/rebuild failure mode discovered during the ClickHouse historical replay.

## Storage efficiency

The storage comparison materialized the same 1,615,140 raw records and 104,685 summaries in both stores and forced ClickHouse merges before measuring active parts.

| Store scope | Bytes | Relative to ClickHouse |
|---|---:|---:|
| PostgreSQL all observability tables and indexes | 2,629,476,352 | 15.49x |
| PostgreSQL raw records + summaries and indexes | 1,632,108,544 | 9.61x |
| ClickHouse raw records + summaries | 169,778,263 | 1.00x |

Against the complete PostgreSQL observability schema, ClickHouse reduced retained storage by 93.5%. Against only PostgreSQL raw plus summary tables, it reduced storage by 89.6%.

## Implementation findings

1. Kafka improves application-side burst tolerance immediately, but Kafka acknowledgement alone does not prove searchable throughput. Consumer lag and projection freshness must remain part of the acceptance gate.
2. Consumer instance scaling only helps while Kafka partition ownership or client concurrency is the bottleneck. Four consumers did not fix the first ClickHouse implementation because every first Trace chunk performed a historical read.
3. The successful ClickHouse write optimization preserves canonical conflict authority. Concurrent chunks first share one batched `(notebook_id, trace_id)` existence query; only existing Traces load and reconcile full history, while genuinely new Traces validate from empty state. Replacing versions use Kafka source offsets so crash redelivery remains idempotent.
4. ClickHouse's primary wins are compressed append-heavy retention and cross-Trace aggregation. The existing product list/detail contract needs a dedicated read model if PostgreSQL is removed completely.

## Evaluated implementation scope

The measured implementation completes the Durable Agent Trace path used by the reference workload: Kafka production, keyed partitioning, manual Consumer Group commits, quarantine decisions, PostgreSQL and ClickHouse consumers, raw/summary retention, list/detail queries, historical replay from Kafka, and loss/recovery reconciliation.

It is not yet the complete production rollout described by ADR 0048. Structured OpenTelemetry log/trace topics, purge-command Tombstones and physical deletion, and ClickHouse Replay attachment metadata/query support remain unimplemented. ClickHouse currently rejects Trace chunks containing Replay attachments with a retryable error, and its Replay query returns not found. These are release gates, not benchmark invalidations, because the frozen workload contains no Replay attachments or purge traffic; they do prohibit claiming that the entire observability migration is production-complete.

## Claim boundary

A defensible resume statement may claim a measured 1.65x sustainable-throughput improvement from Kafka/PostgreSQL to Kafka/ClickHouse under the frozen workload, 93.5% lower complete observability storage, 24.15x lower p95 for the tested time-bucket aggregation, and zero-loss recovery of a 6,609-batch backlog in 16.921 seconds.

It must not claim that all queries became faster: the measured existing product corpus regressed from 6.20 ms to 112.19 ms p95. It also must not present the 800 roots/s burst as sustainable searchable throughput.
