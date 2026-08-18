# Kafka and ClickHouse Default Trace Cutover Design

**Date:** 2026-08-18

**Status:** Accepted for implementation

**Decision record:** `docs/technical-architecture/adr/0048-use-kafka-and-clickhouse-for-event-observability.md`

## 1. Outcome

Nano Notebook will make Kafka the default transport and ClickHouse the default write and query database for all newly produced Durable Agent Trace data. The product will not migrate historical Trace data from Observability PostgreSQL and will not add a PostgreSQL/ClickHouse dual-read path.

The cutover preserves the current Trace Explorer, Trace Analytics, encrypted Replay, authorization, audit, and deletion contracts for new Trace data. Existing Observability PostgreSQL data remains untouched but is no longer returned by the default Dashboard after cutover.

This design narrows the accepted migration architecture in ADR 0048 into a production cutover. The cutover is not complete merely because producers can publish to Kafka or ordinary Trace records can reach ClickHouse. Replay and purge must work end to end before Kafka and ClickHouse become defaults.

## 2. Scope

This cutover includes:

1. default Kafka delivery from Control Plane and Worker producers;
2. default Agent Trace Processor consumption into ClickHouse;
3. ClickHouse-backed Trace list, filter, detail, Span, Event, Link, and Analytics queries;
4. encrypted Replay attachment transfer, metadata retention, lookup, authorization, decryption, and access audit for ClickHouse-backed Traces;
5. Kafka-backed purge commands, ClickHouse Tombstones, physical Trace deletion, and Replay object deletion;
6. local Compose and production manifests that start the selected Kafka/ClickHouse path by default;
7. explicit HTTP/PostgreSQL rollback configuration; and
8. correctness, recovery, query-contract, Replay, purge, and lifecycle verification.

This cutover excludes:

- migration or backfill of historical Observability PostgreSQL Trace data;
- PostgreSQL/ClickHouse dual write or dual read;
- automatic query fallback to Observability PostgreSQL;
- deletion of the old Observability PostgreSQL database or its data;
- Kafka adoption for product Run, Job, Message, Checkpoint, or Source-processing state; and
- changing Application PostgreSQL as the product-state authority.

## 3. Default Architecture

```text
Control Plane / Worker
  agentobs Records
    -> bounded agentbatch Exporter
    -> Kafka Sender
    -> nano.observability.agent-trace.v1 (key = trace_id)
    -> Agent Trace Processor Consumer Group
    -> ClickHouse raw Trace authority and query projections

Dashboard
    -> Control Plane Admin API
    -> Collector Query API
    -> ClickHouse

Replay payload
    -> encrypted staging object storage
    -> Agent Trace Processor verification and transfer
    -> permanent Replay object storage
    -> ClickHouse Replay metadata/reference

Application PostgreSQL purge Outbox
    -> Kafka purge topic
    -> Agent Trace Processor
    -> ClickHouse Tombstone
    -> physical ClickHouse and Replay deletion
```

Application PostgreSQL remains authoritative for Run, Job, Checkpoint, Message, Trace anchor, authorization, and purge intent. Kafka is the finite-retention durable delivery boundary. ClickHouse is the raw and projected Durable Agent Trace authority for data created after cutover. Replay ciphertext remains authoritative in object storage; ClickHouse stores only bounded metadata and references.

## 4. Producer Construction and Defaults

Control Plane and Worker use one shared Agent Trace Sender construction boundary instead of separately hard-coding `HTTPSender`.

The transport configuration is:

```text
NANO_AGENT_TRACE_TRANSPORT=kafka|http
NANO_AGENT_TRACE_KAFKA_BROKERS=127.0.0.1:59092
NANO_AGENT_TRACE_KAFKA_TOPIC=nano.observability.agent-trace.v1
NANO_AGENT_TRACE_KAFKA_CLIENT_ID=<service-specific-id>
```

`kafka` is the default. `http` remains supported only as an explicit rollback choice and continues to require the Collector ingestion endpoint and service token.

Kafka construction uses the existing idempotent Franz producer and `KafkaSender`. Startup verifies broker readiness before the service begins accepting work. Invalid configuration or an unavailable broker fails startup; the service does not silently fall back to HTTP because silent fallback would split new Trace authority across stores.

The existing bounded `agentbatch.Exporter` remains in front of Kafka. A successful `Sender.Send` means Kafka acknowledged every Trace chunk, not that ClickHouse has projected it. Producer retry can duplicate a chunk, so downstream storage remains idempotent by stable record identity and canonical hash.

Observability availability still does not determine product success. Records created inside a product transaction are published only after that transaction commits. A later Kafka failure cannot roll back an accepted Run or Checkpoint. Queue saturation, retry, acknowledgement latency, and drops must be observable.

## 5. Processor and ClickHouse Write Semantics

The default storage consumer is `agent-trace-processor` with:

```text
NANO_AGENT_TRACE_PROCESSOR_STORE=clickhouse
```

For each Kafka message, the Processor:

1. validates the topic, Kafka key, producer, versioned envelope, Trace descriptor, Record limits, stable identities, canonical hashes, and Replay descriptors;
2. checks the retained ClickHouse Tombstone before accepting new Trace data;
3. treats the same identity and same canonical hash as an idempotent replay;
4. sends the same identity with a different hash to durable quarantine;
5. writes new raw records and the newest deterministic query projection revision;
6. persists Replay references only after their ciphertext has been verified and transferred; and
7. commits the Kafka offset only after the owned durable writes succeed.

Transient Kafka, ClickHouse, or object-storage failures leave the source offset uncommitted and retry. Permanent envelope, schema, identity, or payload failures are committed only after an immutable quarantine envelope succeeds. Delayed or out-of-order causal peers remain valid incomplete Trace state and converge when later records arrive.

Kafka is not the long-term Trace authority. ClickHouse raw tables retain the canonical payload, record identity, hash, event time, receive time, and Kafka source position. Query projections remain rebuildable from those raw records.

## 6. ClickHouse Query Contract

The Collector defaults to:

```text
NANO_COLLECTOR_STORE=clickhouse
```

The Control Plane continues to call the Collector HTTP Query API. The Dashboard and Control Plane do not learn ClickHouse-specific response types.

ClickHouse must serve the existing contracts for:

- recent list and cursor pagination;
- bounded time, status, model, Agent, workload, Notebook, Run, Chat, and Trace filters;
- Trace detail, Span tree, Events, Links, model analysis, Token/Cost, and Replay availability;
- cross-Trace overview, time-series, latency, breakdown, and tool analytics; and
- active, complete, incomplete, delayed, and Tombstoned states.

There is no PostgreSQL query fallback and no dual-read merge. A pre-cutover Trace that exists only in Observability PostgreSQL is intentionally absent from the default Dashboard. The old database remains available for manual rollback or offline inspection but is outside the new query authority.

ClickHouse product-query latency is an accepted trade-off of the selected architecture, but queries must remain within the existing product SLO and must return stable cursor and authorization semantics. Query unavailability returns an explicit service error rather than synthesizing data from Application or Observability PostgreSQL.

## 7. Replay Completion Gate

The current ClickHouse path rejects Trace chunks containing Replay attachments. The cutover removes that limitation before changing defaults.

For every attachment descriptor, the Processor must:

1. resolve the encrypted staging object;
2. validate class, schema, ciphertext byte count, and ciphertext SHA-256;
3. transfer or idempotently reconcile it under `agent-replay/<attachment_id>` in permanent Replay object storage;
4. persist the record identity, Trace, Span, class, schema, encryption envelope, expiry, object key, and integrity metadata in ClickHouse; and
5. make the attachment visible only with the projection revision that references it.

The Collector Replay query loads the ClickHouse reference and returns the sealed payload. The Control Plane retains `platform.trace.replay` authorization, opens the sealed payload using the existing key provider, and appends `platform_replay_access_audit` in Application PostgreSQL. Key material and plaintext never enter ClickHouse.

Same attachment identity and identical metadata/object content is idempotent. Conflicting metadata is quarantined. Missing or temporarily unavailable staging/permanent objects are retryable and do not advance the Kafka offset.

## 8. Purge and Tombstone Completion Gate

Purge intent remains an Application PostgreSQL governance fact. The existing purge Outbox is changed to publish a strongly versioned command to `nano.observability.agent-trace-purge.v1`, keyed by `trace_id`.

The Processor applies purge in this order:

1. write a retained ClickHouse Tombstone;
2. make all Trace and Replay queries reject the Trace immediately;
3. delete raw records and current/historical projection revisions;
4. delete owned Replay objects and attachment metadata;
5. record each owned-store acknowledgement; and
6. mark the purge command complete only when all physical deletion steps succeed.

The Tombstone outlives Kafka retention, the allowed late-arrival window, and relevant backup recovery windows. Trace messages that arrive or replay after Tombstone establishment are acknowledged without recreating query-visible data. Partial physical deletion retries from durable purge state and never removes the Tombstone first.

## 9. Deployment and Cutover

Local Compose starts Kafka, ClickHouse, topic initialization, and the ClickHouse Agent Trace Processor in the default profile. The PostgreSQL Processor remains available only through an explicit rollback or benchmark profile. `scripts/start` waits for topic initialization and Processor/Collector readiness before starting the web development workflow.

Production manifests require at least three Kafka brokers with replication factor three before claiming broker-failure tolerance. Development Compose retains one broker and replication factor one and must not be described as highly available.

Cutover is forward-only for data placement:

1. deploy Kafka, ClickHouse, topics, Processor, Collector query path, Replay support, purge support, and monitoring;
2. prove their readiness and contract gates while producers still use explicit HTTP rollback mode;
3. switch producers to the Kafka default;
4. verify new Trace, Replay, Analytics, and purge behavior from a real Run; and
5. leave historical Observability PostgreSQL data untouched.

Rollback changes producer transport to `http`, Processor store to `postgres`, and Collector store to `postgres`. Because there is no migration or dual write, rollback does not copy ClickHouse-only Traces into PostgreSQL. This deliberate discontinuity must be stated during any rollback; it cannot be hidden behind a merged Dashboard.

## 10. Observability and Failure Boundaries

The cutover exposes at least:

- producer pending/inflight records and bytes, drops, retries, and Kafka acknowledgement latency;
- Kafka partition lag, oldest unprocessed age, rebalance, and offset-commit failures;
- Processor persisted, retry, quarantine, Tombstoned, and Replay-transfer outcomes;
- ClickHouse insert/query latency, batch rows/bytes, raw/summary watermark gap, and mutation backlog;
- searchable freshness from Record creation through latest query-visible projection;
- Replay staging/permanent-object failures and expiry; and
- purge age, partial-store acknowledgements, and Tombstone count.

Kafka acknowledgement without ClickHouse visibility is buffered delivery, not searchable success. A growing lag or watermark gap beyond the freshness objective is an incident. No component reports a Trace as absent merely because its projection is delayed; query state distinguishes delayed/incomplete from not found where the raw facts permit it.

## 11. Testing and Acceptance

Implementation follows red-green-refactor in atomic slices.

### Producer and configuration

- tests prove Kafka is the Control Plane and Worker default;
- explicit HTTP construction remains supported;
- invalid transport, missing Kafka configuration, broker unavailability, flush, and shutdown have deterministic behavior; and
- both services use the shared construction boundary.

### Processor and storage

- integration tests prove Kafka commit-after-ClickHouse-write, transient retry, quarantine-before-commit, rebalance fencing, idempotent replay, identity conflict, late arrival, and projection convergence;
- ClickHouse raw and projection counts reconcile with the accepted Kafka corpus; and
- no newly produced Trace writes to Observability PostgreSQL in the default topology.

### Query

- the existing product query contract runs against ClickHouse for list, filters, cursors, detail, Span/Event/Link, model analysis, and authorization;
- the Analytics query contract runs against ClickHouse;
- queries do not fall back to PostgreSQL; and
- historical PostgreSQL-only Trace IDs return not found through the default topology.

### Replay and purge

- a real model Run with request/decision Replay reaches permanent object storage and can be opened through the authorized Dashboard path;
- missing permission remains forbidden and every Replay attempt is audited;
- Replay retry, duplicate, conflict, corruption, expiry, and unavailable-object behavior is covered;
- purge establishes a Tombstone before query hiding and physical deletion;
- late Kafka replay cannot resurrect a purged Trace; and
- partial Replay/ClickHouse deletion resumes without losing purge authority.

### Lifecycle gates

- focused unit, integration, race, Compose configuration, and service lifecycle tests pass;
- `./scripts/test-go` passes;
- `npm test -- --run`, `npm run lint`, and `npm run build` pass from `web/` when affected contracts reach the UI;
- a local end-to-end real Run proves Kafka acceptance, zero final lag, ClickHouse query visibility, Replay access, and purge behavior; and
- the final diff contains no historical Trace migration, dual-write, dual-read, or automatic PostgreSQL fallback.

The cutover is complete only when every acceptance item is evidenced. Merely setting environment defaults or successfully publishing an attachment-free benchmark fixture is insufficient.
