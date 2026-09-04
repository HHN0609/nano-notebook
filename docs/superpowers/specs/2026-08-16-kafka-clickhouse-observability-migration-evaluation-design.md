# Kafka and ClickHouse Observability Migration and Evaluation Design

> Historical migration design. The evaluated PostgreSQL stages remain benchmark
> history; the rollback backend and its database were fully retired on 2026-09-04.

**Date:** 2026-08-16

**Status:** Accepted for implementation

**Decision record:** `docs/technical-architecture/adr/0048-use-kafka-and-clickhouse-for-event-observability.md`

## 1. Outcome

Nano Notebook will move event-shaped observability off direct Collector HTTP plus Observability PostgreSQL and onto Kafka-backed delivery with ClickHouse retention and querying. The migration must preserve the existing Durable Agent Trace product contract while separating the responsibilities of transport, durable storage, projection, query, Replay, purge, and generic OpenTelemetry signals.

The work is complete only when the current architecture and the two migration stages have been measured on identical ARM64 infrastructure and the final report contains defensible values for:

- maximum sustainable root Agent Runs per second;
- the repeatable load at which degradation begins and the first saturated load;
- product and analytical query p95/p99 latency;
- Kafka buffering and recovery behavior with zero broker-acknowledged record loss; and
- retained bytes per root Agent Run.

The report must not substitute synthetic component QPS for the end-to-end Agent Run result or claim an improvement that was not reproduced from preserved raw evidence.

## 2. Scope

This design covers four paths:

1. Durable Agent Trace messages produced by Nano processes, carried by Kafka, processed by the Agent Trace Processor, and stored and queried in ClickHouse.
2. Structured application logs carried through an OpenTelemetry Collector Kafka pipeline into ClickHouse.
3. Operational OpenTelemetry spans carried through a separate OpenTelemetry Collector Kafka pipeline into ClickHouse.
4. Durable purge commands carried independently and applied across ClickHouse and Replay object storage.

Prometheus Metrics stay on the existing scrape path. Encrypted Replay payloads stay in object storage. Product state, Trace anchors, and authoritative purge intent stay in Application PostgreSQL. Kafka is not introduced for Jobs, Checkpoints, Messages, Source processing state, or other product commands.

The first implementation sequence makes Durable Agent Trace measurable end to end. Generic log and Operational Trace lanes then reuse standard OpenTelemetry Collector components rather than expanding the Nano-specific processor.

## 3. Architecture

```text
Control Plane / Worker
  Durable Agent Trace Batch
    -> bounded agentbatch Exporter
    -> Kafka Sender
    -> nano.observability.agent-trace.v1
    -> Agent Trace Processor Consumer Group
    -> PostgreSQL (Stage B) or ClickHouse (Stage C)

Control Plane / Worker / Collector / Processor
  OTLP logs and Operational spans
    -> OpenTelemetry Collector ingress
    -> separate Kafka topics
    -> OpenTelemetry Collector storage consumers
    -> ClickHouse OTel tables

Application PostgreSQL purge Outbox
    -> nano.observability.agent-trace-purge.v1
    -> Agent Trace Processor
    -> ClickHouse Tombstone + physical deletion + Replay deletion

Prometheus Metrics ---------------------------> Prometheus
Encrypted Replay payloads -------------------> object storage
Product state and purge intent --------------> Application PostgreSQL
```

Kafka topics are signal contracts, not deployment queues chosen ad hoc:

| Topic | Key | Consumer | Purpose |
|---|---|---|---|
| `nano.observability.agent-trace.v1` | `trace_id` | `nano-agent-trace-storage-v1` | Durable Agent Trace Batch chunks |
| `nano.observability.agent-trace-purge.v1` | `trace_id` | `nano-agent-trace-storage-v1` | Purge commands and Tombstone establishment |
| `nano.observability.agent-trace-quarantine.v1` | original topic/partition/offset | operator and replay tooling | Permanent schema, identity, and payload failures |
| `nano.observability.otel-logs.v1` | no cross-record ordering contract | OTel log storage group | Structured application logs |
| `nano.observability.otel-traces.v1` | `trace_id` | OTel trace storage group | Operational spans |

Local development and the benchmark use one Kafka broker in KRaft mode. Topic replication factor is one there because only two benchmark VMs exist and broker high availability is not the experiment. Production configuration requires at least three brokers and replication factor three before the transport can be described as broker-failure tolerant.

## 4. Producer and Acceptance Semantics

`internal/agentbatch.Sender` remains the transport boundary. Existing direct HTTP delivery is retained as the Stage A mode. A Kafka Sender implements the same interface for Stages B and C.

The Kafka Sender splits a Collector Batch into one message per Trace chunk, uses `trace_id` as the Kafka key, encodes a strongly versioned envelope, and waits for every broker acknowledgement. If only part of a Batch is acknowledged, the Sender returns a retryable error and retries the entire Batch. Duplicate chunks are therefore expected and downstream idempotency is mandatory.

The Sender returns a successful `BatchResult` when Kafka has acknowledged every chunk, not when ClickHouse has projected it. This is the precise acceptance boundary used by the freshness metric. The existing bounded in-memory Exporter continues to isolate product operations from transport latency and retry. A record that never reaches Kafka may be reported as a producer drop, but it cannot fail a committed product operation. Once Kafka acknowledges a message, it must reach retained storage or durable quarantine without silent loss.

The Go Kafka client uses idempotent production, `acks=all`, bounded buffering, compression, delivery timeouts, and explicit readiness checks. Producer identity, topic, schema version, payload size, acknowledgement latency, retry count, buffered records/bytes, and drops are observable.

No full Trace Outbox is reintroduced into Application PostgreSQL. That earlier design was superseded and would both change product transaction latency and invalidate the before/after comparison. The existing PostgreSQL Outbox remains only for purge commands because deletion intent is a product-governance fact.

## 5. Agent Trace Processor

The Agent Trace Processor is a separate Go command and independently scalable process. It owns no Agent execution or product state. Each instance joins the same Consumer Group; Kafka assigns a Trace partition to at most one active group member at a time.

Processing one polled partition follows this order:

1. decode the versioned envelope with unknown-field rejection;
2. validate topic, key, producer identity, Trace descriptor, schema version, record limits, canonical hashes, and attachment references;
3. write the raw records durably and idempotently;
4. update or append the affected query projection revision;
5. commit the Kafka offset only after durable storage succeeds;
6. on a permanent failure, first write an immutable quarantine envelope, then commit the source offset;
7. on a transient failure, leave the offset uncommitted and retry with bounded backoff.

Consumer rebalances cannot allow work from a revoked partition to commit an offset. Partition processing is sequential by default and parallel across assigned partitions. Storage batching may combine messages from multiple partitions, but offset commits remain bounded by the last fully persisted message in each partition.

Stage B adapts the processor to the existing Collector Ingestor and PostgreSQL Store. Stage C uses the same message and offset semantics with ClickHouse storage. This makes the Kafka effect measurable separately from the database effect.

## 6. ClickHouse Durable Agent Trace Model

ClickHouse keeps immutable logical facts and versioned replacement rows. It does not rely on arrival order or claim physical exactly-once insertion.

### 6.1 Raw authority

`agent_trace_records` contains the full canonical Durable Agent Trace record plus bounded extracted columns needed for validation and pruning:

- tenant/Notebook, Trace, workload, Run, Chat, Span, parent Span, Attempt, and Agent identities;
- record identity key, canonical SHA-256, record kind, schema versions, sequence, event time, and receive time;
- model, status, error, Token, Cost, Action, Delegation, and Replay-reference fields when present;
- canonical payload bytes and Kafka topic/partition/offset provenance; and
- an ingestion version used by `ReplacingMergeTree`.

The table partitions by event month, orders by Notebook/Trace/record identity, applies the Durable Agent Trace TTL, and uses ZSTD codecs. Kafka retries may create physical duplicates until merges occur, so correctness queries select the latest value per logical identity rather than depending on background merge timing.

Before inserting a batch, the processor reads existing hashes for candidate identity keys. Same identity plus same hash is an idempotent replay. Same identity plus a different hash goes to quarantine. Since all messages for one Trace share a Kafka key, normal concurrent processors cannot race the same Trace; the replacing engine remains the recovery guard for insert-success/offset-commit uncertainty.

### 6.2 Query projections

`agent_trace_summaries` stores one replaceable revision per Trace for Dashboard lists and cross-Trace analysis. It contains the existing summary contract: workload, Run, Chat, Notebook, Agent, model, status, active/terminal/incomplete state, timing, Attempts, Token usage, known Cost, error fields, and Replay availability.

`agent_trace_spans`, `agent_trace_events`, and `agent_trace_links` retain the current detail response shapes as replaceable revisions. Projection code is deterministic from the latest logical raw record set. Delayed records recompute the affected Trace and append a newer projection revision; queries select the latest revision. Missing causal peers produce `incomplete`, never fabricated structure.

List queries use the summary table's time/Notebook ordering and bounded dimensions. Exact identity filters use dedicated data-skipping indexes. Detail queries prune by Notebook and Trace. Analytical queries first select the latest summary revision per Trace and then aggregate Token, Cost, error, latency, Retry, Action, and Delegation measures over bounded time windows and dimensions.

The ClickHouse adapter must return the same API types and filter semantics as the current PostgreSQL `TraceQueryStore`. The Dashboard and Control Plane do not gain a ClickHouse-specific contract.

### 6.3 Batching

The storage consumer batches independently of producer Batch boundaries. It flushes by row count, byte count, or delay and targets at least 1,000 rows per insert under load, while still respecting the five-second freshness objective at low volume. Batch configuration and actual rows/bytes per insert are recorded in every benchmark manifest.

## 7. OpenTelemetry Logs and Operational Traces

Generic signals use a pinned OpenTelemetry Collector Contrib distribution:

- ingress pipelines receive OTLP logs and spans, apply memory limiting and batching, and export protobuf messages to their separate Kafka topics;
- trace messages are partitioned by Trace ID;
- storage pipelines consume with separate Consumer Groups and export to the ClickHouse OTel exporter;
- logs and traces use separate retention and tables; and
- Metrics are excluded from these Kafka pipelines and continue to Prometheus.

The OTel Collector ClickHouse exporter is beta for logs and traces, so its exact version, table DDL, breaking-change notes, and image digest are pinned in the deployment manifest. Nano's Durable Agent Trace never uses those generic OTel tables as authority.

## 8. Purge, Retention, and Replay

An authorized deletion writes purge intent to the existing Application PostgreSQL Outbox. The purge sender publishes a versioned command to the purge topic. The Agent Trace Processor first writes a retained Tombstone keyed by Trace ID. From that point, queries reject the Trace and new/replayed Trace messages are acknowledged without recreating visible data.

The processor then removes ClickHouse raw and projection rows asynchronously, deletes owned Replay objects, records each store acknowledgement, and marks the purge complete only after every owned store succeeds. The Tombstone outlives Kafka retention, late-arrival bounds, and backup recovery windows.

Default retention is seven days for Kafka replay, thirty days for structured logs, fourteen days for Operational Traces, ninety days for Durable Agent Traces, thirty days for quarantine, seven days for encrypted Replay, and fifteen days for Prometheus Metrics. Environment overrides are recorded and may not differ between A/B/C measurements unless retention itself is the experiment.

## 9. Configuration and Deployment

Transport and store selection are explicit:

```text
NANO_AGENT_TRACE_TRANSPORT=http|kafka
NANO_AGENT_TRACE_KAFKA_BROKERS=...
NANO_AGENT_TRACE_KAFKA_TOPIC=nano.observability.agent-trace.v1
NANO_AGENT_TRACE_PROCESSOR_STORE=postgres|clickhouse
NANO_CLICKHOUSE_ADDR=...
NANO_CLICKHOUSE_DATABASE=nano_observability
```

Secrets are never stored in the repository. Local Compose uses development-only credentials and adds pinned Kafka, ClickHouse, OTel ingress, OTel storage, and Agent Trace Processor services with health checks and persistent volumes.

The benchmark SUT places Kafka logs, PostgreSQL data, ClickHouse data, and raw experiment output on the controlled Hyperdisk under distinct directories. Stage resets remove only verified benchmark data directories and recreate the selected stage from a versioned manifest. The load generator never hosts a database, broker, Collector, or processor.

Current implementation candidates are franz-go, clickhouse-go/v2, the official Apache Kafka 4.3.1 image, a pinned ClickHouse 26.3 LTS Jammy image, and a pinned OpenTelemetry Collector Contrib image. Final module and image versions plus digests are locked before the baseline environment manifest is signed.

## 10. Evaluation

The authoritative comparison is:

- **Stage A:** bounded producer -> direct Collector HTTP -> Observability PostgreSQL;
- **Stage B:** bounded producer -> Kafka -> Agent Trace Processor -> Observability PostgreSQL; and
- **Stage C:** bounded producer -> Kafka -> Agent Trace Processor -> ClickHouse.

All three run on the same `c4a-standard-8` SUT and `c4a-standard-4` load generator, with the same private network, final Hyperdisk size/performance, Agent Run records, retained cardinality, query semantics, durability, warmup, measurement windows, and saturation rules.

The reference workload is the accepted deterministic 100-root cycle: fifty direct answers, thirty one-Action Runs, ten two-Action Runs, five Delegations, and five retry/recovery Runs. Root Agent Runs/s is the headline. Total Runs/s, Records/s, MiB/s, child Runs/root, and Records/root expose amplification.

Calibration uses 100,000 fully materialized roots. The primary comparison uses 1,000,000. Retention scale targets 5,000,000 and falls back to 2,000,000 only when calibration proves the larger dataset cannot preserve the required disk headroom.

An open-loop load schedule doubles from one root Run/s until failure, refines the boundary, and repeats the last pass/first fail. Maximum sustainable throughput is the highest rate passing correctness, scheduling, error, acceptance latency, searchable freshness, stable lag/backlog, query latency, process stability, memory, and disk gates. The performance knee is reported separately from saturation.

`trace-product-query-v1` measures the current list/filter/detail contract and participates in the headline mixed-load SLO. `trace-analytics-query-v1` measures equivalent PostgreSQL and ClickHouse cross-Trace aggregates separately. Raw outputs include request samples, stage metrics, Kafka offsets/lag, database system metrics, process/container resource metrics, record counts/hashes, schemas, configurations, image digests, and repeated-run summaries.

## 11. Testing and Delivery Order

Every behavior change follows red-green-refactor. The implementation is delivered in independently verifiable slices:

1. versioned benchmark fixture and result schema, followed by a Stage A baseline run;
2. Kafka envelope and Sender with codec, keying, acknowledgement, duplicate, partial-failure, and shutdown tests;
3. Agent Trace Processor with manual offset, rebalance, transient retry, permanent quarantine, and Stage B PostgreSQL integration tests;
4. ClickHouse migrations and raw idempotency/conflict tests;
5. deterministic ClickHouse projection, query parity, late-arrival, deletion, and Stage C integration tests;
6. standard OTel Kafka/ClickHouse lanes for logs and Operational Traces;
7. local restart/recovery and complete Go/Web/Compose gates;
8. controlled GCP calibration, A/B/C experiments, correctness reconciliation, report generation, and resume-claim review.

The PostgreSQL baseline is captured before Stage A code or schema is removed. The direct path remains selectable until Stage C passes parity, recovery, and benchmark gates. No resume percentage or multiplier is written before the corresponding raw runs and environment manifest are preserved.
