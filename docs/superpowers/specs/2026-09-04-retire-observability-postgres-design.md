# Retire Observability PostgreSQL Design

**Date:** 2026-09-04

**Status:** Approved

**Supersedes:** the PostgreSQL rollback and retention boundaries in `2026-08-18-kafka-clickhouse-default-trace-cutover-design.md` and `2026-08-24-remove-trace-memory-queue-design.md`

## 1. Outcome

Nano Notebook will completely retire the separate Observability PostgreSQL database. Kafka remains the Durable Agent Trace delivery boundary and ClickHouse remains the only Trace write, projection, query, analytics, Replay-metadata, and purge store.

Application PostgreSQL remains mandatory and unchanged as the authority for product state, including users, Notebooks, Chats, Messages, Agent Runs, Jobs, Checkpoints, authorization, Trace anchors, and purge intent. This retirement must never delete or repurpose Application PostgreSQL data.

After the change, a clean local start must not create, start, migrate, probe, or test an `observability-postgres` service. The retired local volume may be deleted only by its exact Compose volume identity after the new topology has passed startup and health verification.

## 2. Scope

This retirement includes:

1. remove the `observability-postgres` Compose service and its named volume from local and production manifests;
2. remove the `agent-trace-processor-postgres` service and `postgres-trace-rollback` profile;
3. remove the Collector PostgreSQL store, migrations, projector, rebuild path, PostgreSQL configuration branches, and tests whose only purpose is the retired store;
4. make `cmd/migrate` and `scripts/migrate` migrate Application PostgreSQL only;
5. update `scripts/start`, `scripts/bootstrap`, `scripts/health`, `scripts/test-go`, capacity checks, and readiness fixtures so they require only Application PostgreSQL plus the Kafka/ClickHouse Trace topology;
6. remove obsolete Prometheus targets and active operational documentation for the retired services and environment variables;
7. preserve historical PRDs, accepted designs, and plans as an accurate record of the former architecture, while adding explicit supersession pointers where an old document could otherwise be mistaken for current operations; and
8. delete the exact local `compose_observability-postgres-data` volume only after code and runtime acceptance pass.

This retirement excludes:

- deleting or changing `compose_postgres-data`;
- changing Application PostgreSQL schemas or authority;
- changing Kafka producer semantics, topics, or retry ownership;
- changing ClickHouse Trace schemas or query contracts except where removal of dead fallback code requires compilation cleanup;
- migrating historical Observability PostgreSQL Trace data into ClickHouse; and
- preserving a runtime rollback to the retired PostgreSQL Trace store.

Historical Trace data that exists only in Observability PostgreSQL is intentionally discarded when the local volume is deleted. There is no backfill, export, or dual-read phase.

## 3. Resulting Architecture

```text
Product state
  Control Plane / Worker
    -> Application PostgreSQL

Durable Agent Trace
  Control Plane / Worker
    -> Kafka
    -> Agent Trace Processor
    -> ClickHouse

Trace queries
  Dashboard
    -> Control Plane
    -> Collector Query API
    -> ClickHouse

Replay payloads
  encrypted S3-compatible object storage
    + ClickHouse metadata and references
```

There is one PostgreSQL service in the supported topology: Application PostgreSQL. “PostgreSQL” in current runbooks and health output refers only to that product database.

## 4. Code and Configuration Boundaries

The Collector and Agent Trace Processor will accept only the ClickHouse backend. PostgreSQL-specific constructors, pool configuration, migration hooks, runtime selection, and validation branches will be removed instead of left dormant.

The following retired configuration surface must disappear from active code and scripts:

```text
NANO_COLLECTOR_DATABASE_URL
NANO_AGENT_TRACE_PROCESSOR_DATABASE_URL
NANO_AGENT_TRACE_PROCESSOR_STORE=postgres
NANO_COLLECTOR_STORE=postgres
postgres-trace-rollback
```

Where a store selector no longer has more than one valid value, production startup should construct the ClickHouse implementation directly rather than retain a fake single-value switch.

`cmd/migrate` will own only Application PostgreSQL migrations. Collector startup will continue to initialize any required ClickHouse schema through the existing ClickHouse migration path.

## 5. Local Development and Tests

`scripts/start` will:

1. start the default Compose topology;
2. wait for Application PostgreSQL, Kafka initialization, the ClickHouse Agent Trace Processor, and the ClickHouse Collector;
3. run Application PostgreSQL migrations only; and
4. start Control Plane, Worker, and Vite.

`scripts/health` must check the current services and the Collector at its actual local endpoint (`127.0.0.1:58082`), not the retired host-side port `8082`.

The Go test gate will stop creating Observability PostgreSQL test databases. Tests for transport-neutral Trace validation and contracts remain. PostgreSQL-store-only integration tests are deleted; equivalent ClickHouse tests remain the supported storage evidence.

## 6. Documentation Policy

Current README and runbooks must describe only Kafka/ClickHouse Trace storage. Historical sprint PRDs, old implementation plans, and accepted design records remain in Git because deleting them would erase design history. Documents that explicitly promised a PostgreSQL rollback path will receive a short supersession note pointing to this design when they are likely to be used operationally.

No document may describe the retired Observability PostgreSQL database as a current fallback, production store, required test dependency, or supported local service after this change.

## 7. Destructive Cleanup

The only authorized local data deletion is the exact Docker volume:

```text
compose_observability-postgres-data
```

Before deletion, verify that:

- the volume name matches exactly;
- `compose_postgres-data` is a distinct volume and remains present;
- no supported service references the retired volume; and
- the replacement local stack passes startup and health checks.

The retired volume is not backed up. Its contents are obsolete historical Trace data and are accepted as unrecoverable after deletion. Application PostgreSQL and all other volumes remain untouched.

## 8. Testing and Acceptance

Implementation follows red-green-refactor in independently reviewable slices.

Acceptance requires evidence that:

1. repository-wide active-code searches find no retired service, profile, database URL, or PostgreSQL Trace backend wiring;
2. Compose configuration tests prove the local and production topologies contain Application PostgreSQL, Kafka, ClickHouse, the ClickHouse Processor, and the ClickHouse Collector, with no Observability PostgreSQL service or volume;
3. migration tests prove `cmd/migrate` needs only `NANO_DATABASE_URL` and does not connect to a Collector database;
4. startup/readiness tests prove `scripts/start` and `scripts/health` use the current topology and Collector endpoint;
5. focused Go tests and the full `./scripts/test-go` gate pass;
6. a stopped then freshly started local instance reaches healthy status without `observability-postgres` running;
7. one real Chat Run completes and its Trace becomes query-visible through ClickHouse; and
8. `compose_observability-postgres-data` is removed while `compose_postgres-data` remains present.

The task is not complete if only `scripts/start` is patched while the rollback backend, tests, configuration, or local data volume remains.
