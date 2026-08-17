# Kafka and ClickHouse Observability Implementation Plan

**Goal:** Produce measured A/B/C throughput, bottleneck, and query-latency results while migrating Durable Agent Trace delivery from direct HTTP/PostgreSQL through Kafka/PostgreSQL to Kafka/ClickHouse without changing the Dashboard contract.

**Branch:** `main`; existing working changes are this migration's accepted design documents. No commit or push occurs without user approval.

**Method:** strict RED/GREEN/REFACTOR. Preserve the direct path until final parity. Record every benchmark artifact and never publish an unmeasured claim.

## Slice 1: Versioned Benchmark Domain and Result Contract

**Files:**

- `internal/obsbench/workload.go`
- `internal/obsbench/workload_test.go`
- `internal/obsbench/result.go`
- `internal/obsbench/result_test.go`

**RED:** Specify `agent-run-reference-v1` as a deterministic 100-root cycle and prove every generated root has stable identifiers, one supported scenario, correlated records, exact logical amplification, and stable JSON. Specify a versioned result document that records stage, environment, offered/achieved Root Runs/s, Records/s, MiB/s, knee/saturation status, p95/p99, errors, loss, lag, resource evidence, and artifact hashes.

**GREEN:** Implement immutable scenario templates, deterministic fixture identities/timestamps, validation, and strict result encoding. No network or storage dependency enters this package.

**Verify:** `go test ./internal/obsbench -count=1` and deterministic golden regeneration.

## Slice 2: Stage A Load Generator and Query Driver

**Files:**

- `cmd/obs-bench/main.go`
- `cmd/obs-bench/main_test.go`
- `internal/obsbench/runner.go`
- `internal/obsbench/runner_test.go`
- `scripts/obs-bench`

**RED:** Prove open-loop arrivals are scheduled independently of response latency, late arrivals invalidate a level, warmup is excluded, samples retain monotonic timestamps, and a run cannot be reported when correctness counts disagree.

**GREEN:** Add fixture seeding, rate-level execution, direct Collector HTTP publishing, product-query sampling, NDJSON raw output, and deterministic summary generation. Keep provider/model calls out of the benchmark so it measures the observability path rather than external API variance.

**Verify:** fake-clock/fake-sender unit tests, local PostgreSQL integration, and one non-authoritative smoke run.

## Slice 3: Kafka Envelope and Producer Transport

**Files:**

- `internal/agentbatch/kafka.go`
- `internal/agentbatch/kafka_test.go`
- `internal/agentbatch/kafka_codec.go`
- `internal/agentbatch/kafka_codec_test.go`
- `cmd/control-plane/main.go` and tests
- `cmd/worker/main.go` and tests

**RED:** Prove one message per Trace chunk, `trace_id` keying, strict schema envelope, all-chunk acknowledgement, partial acknowledgement retry, idempotent replay, size bounds, readiness, flush, and clean shutdown. Prove HTTP remains the default and invalid transport configuration fails startup.

**GREEN:** Add a small Kafka-client interface around franz-go, a production implementation with idempotent writes and `acks=all`, and explicit `http|kafka` transport construction.

**Verify:** focused package/command tests and `go test -race ./internal/agentbatch`.

## Slice 4: Agent Trace Processor and Stage B PostgreSQL

**Files:**

- `internal/agenttraceprocessor/*`
- `cmd/agent-trace-processor/*`
- existing Collector Ingestor/PostgreSQL adapters
- processor integration tests

**RED:** Prove commit-after-store, no commit on transient storage failure, quarantine-before-commit for permanent failure, duplicate replay, conflicting identity, offset fencing on rebalance, partition concurrency, and graceful drain.

**GREEN:** Implement manual Consumer Group offset management and adapt the existing Ingestor/PostgreSQL Store without changing Trace query results.

**Verify:** unit tests with a fake consumer, Kafka/PostgreSQL integration, restart recovery, and Stage B smoke.

## Slice 5: Local Kafka and Stage Manifests

**Files:**

- `infra/compose/compose.yaml`
- `infra/kafka/*`
- `scripts/prepare-observability-kafka`
- lifecycle/config tests

**RED:** Validate pinned images, KRaft readiness, topic count/configuration, persistent volumes, internal/external listeners, and explicit A/B/C service selections.

**GREEN:** Add official Kafka service, deterministic topic initialization, health checks, and stage-specific Compose profiles.

**Verify:** `docker compose config`, topic describe, producer/consumer smoke, restart persistence, and existing service gates.

## Slice 6: ClickHouse Raw Store and Projection

**Files:**

- `internal/collector/clickhouse_migrations.go`
- `internal/collector/clickhouse_store.go`
- `internal/collector/clickhouse_store_integration_test.go`
- `internal/collector/clickhouse_projection.go`
- projection integration tests
- `infra/clickhouse/*`

**RED:** Prove raw insert, same-hash replay, conflicting-hash quarantine decision, insert-success/offset-commit retry, late/out-of-order convergence, incomplete state, batching, TTL metadata, and stable record counts independent of background merges.

**GREEN:** Add clickhouse-go native batches, replacing raw/projection tables, latest-revision queries, deterministic projection, and storage metrics.

**Verify:** ClickHouse integration, restart recovery, duplicate storm, late arrival, and disk/row reconciliation.

## Slice 7: ClickHouse Query Parity and Analytics

**Files:**

- `internal/collector/clickhouse_query.go`
- query parity/integration tests
- Collector command store selection
- admin query contract tests

**RED:** Run the existing list/filter/cursor/detail contract against PostgreSQL and ClickHouse fixtures and require equivalent normalized responses. Add equivalent cross-Trace Token/Cost, error/latency, and Retry/Action/Delegation aggregates.

**GREEN:** Implement the ClickHouse Query Store behind the existing Collector HTTP API and add the separate analytical API used by the benchmark.

**Verify:** product corpus parity, analytical parity, cursor stability under concurrent insert, and query concurrency smoke.

## Slice 8: Purge and Generic OTel Lanes

**Files:**

- Kafka purge sender/processor tests and adapters
- `infra/observability/otel-collector/*`
- Compose services and configuration tests

**RED:** Prove Tombstone-before-delete, late replay suppression, physical raw/projection deletion, Replay deletion acknowledgement, and purge retry. Validate separate log/Operational Trace topics and exclusion of Metrics.

**GREEN:** Move purge delivery to Kafka and configure pinned OTel Collector ingress/storage pipelines with Kafka and ClickHouse exporters.

**Verify:** deletion races, retention configuration, OTLP log/span smoke, and Prometheus regression.

## Slice 9: Controlled GCP A/B/C Evaluation

**Files:**

- `scripts/obs-bench-gcp/*`
- `benchmarks/observability/manifests/*`
- generated raw artifacts under an ignored run directory
- `docs/implementation/observability-kafka-clickhouse-benchmark-report.md`

**Steps:** Start the approved two-VM environment, install pinned ARM64 services, calibrate 100,000 roots, freeze/grow the Hyperdisk if required, seed 1,000,000 roots, and execute Stage A before replacing it. Then execute B and C with identical manifests. Stop both VMs after every active session.

**Verify:** repeated pass/fail boundaries, exact record reconciliation, raw artifact hashes, environment inventory, query corpora, Kafka lag/recovery, and disk usage.

## Final Gates

- focused RED/GREEN evidence per slice;
- `./scripts/test-go` and targeted race/integration suites;
- Web tests, lint, and production build when the query contract or UI changes;
- Compose validation and restart durability;
- A/B/C raw data and reproducible summary generation;
- no invented resume values and no comparison across differing manifests;
- no unintended files, credentials, running GCP VMs, or unreviewed commits.
