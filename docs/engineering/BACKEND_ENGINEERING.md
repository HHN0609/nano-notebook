# Backend Engineering

Reusable backend engineering standards for coding agents. Use this with
`AGENTS.md` when a goal touches APIs, storage, background work, consistency,
security, observability, or production behavior.

## Core Principles

- Durable product state must have a clear source of truth.
- Cache, queues, locks, and coordination systems are operational layers, not
  substitutes for durable invariants.
- State transitions must be explicit, validated, and observable.
- Cross-process calls need timeouts, cancellation, retry policy, and structured
  error handling.
- Background jobs, event consumers, webhooks, and retryable mutation paths should
  be idempotent.
- Assume duplicate work, delayed messages, partial failure, stale reads, and
  out-of-order observations.
- Prefer boring, inspectable designs over clever distributed protocols.

## Domain Boundaries

- Keep product concepts distinct even when implementation would be shorter if
  they were merged.
- Keep vendor and provider terms behind integration boundaries.
- Core services should speak product language, not database table names or
  third-party API shapes.
- Add abstraction only when there is a clear boundary or a known second
  implementation.

## API Contracts

- Design APIs around stable resources and standard operations.
- Use explicit custom actions only when CRUD semantics do not fit.
- Keep request and response shapes stable once used by clients.
- Use consistent field names and stable error shapes with machine-readable codes.
- Include request IDs or trace IDs in responses and logs.
- Use cursor pagination for lists that can grow.
- Make filters and sort fields explicit allowlists.

Mutation APIs should be retry-safe:

- Prefer caller-supplied IDs or idempotency keys for create operations.
- Repeated requests with the same idempotency key and same parameters should
  return the same semantic result.
- Repeated requests with the same idempotency key and different parameters
  should fail clearly.
- Store enough response metadata to answer retries consistently.

Long-running APIs should return a durable resource the client can poll or
subscribe to.

## Idempotency

Use one or more durable mechanisms:

- unique constraints,
- idempotency-key records,
- processed-event records,
- compare-and-swap state transitions,
- leases with fencing tokens,
- upserts when duplicate creation is expected.

Side effects and idempotency records should commit atomically when they share a
correctness boundary. If they cannot, use an outbox or make the downstream side
effect independently idempotent.

Never rely on in-memory idempotency for durable behavior.

## State Machines And Workers

State machines are correctness boundaries:

- define allowed transitions in code,
- reject invalid transitions,
- record who or what caused the transition,
- persist timestamps for important lifecycle points,
- keep coarse product state stable and put detailed progress in events.

Workers should:

- claim work with a lease or equivalent ownership record,
- renew ownership explicitly for long-running work,
- treat expired ownership as ambiguous,
- reload authoritative state before recovery actions,
- make cleanup safe to repeat.

## Concurrency And Backpressure

- Every concurrent task needs an owner and a shutdown path.
- Pass cancellation context through request, worker, database, cache, and
  external calls.
- Bound worker pools, queues, buffers, fanout, page sizes, request bodies, and
  external calls.
- Avoid fire-and-forget work unless it is durably recorded and recoverable.
- Do not hold locks while making network calls.
- Prefer database constraints and transactional updates over distributed locks
  for durable correctness.

## Timeouts, Retries, And Fallbacks

- All cross-process calls need timeouts.
- Retry only idempotent or read-only operations.
- Retry at one layer in the call stack.
- Use capped exponential backoff with jitter.
- Bound retry duration by the caller's deadline.
- Do not retry validation errors, authorization errors, or deterministic
  conflicts.
- Log final failure with attempt count and error class.

Fallback paths must meet the same correctness bar as primary paths. They must
not hide data loss, stale state, or permission failures.

## Database And Transactions

Schema design starts from access patterns:

- document expected read and write paths before adding tables,
- use stable opaque IDs for externally visible identifiers,
- use foreign keys, unique constraints, and check constraints for invariants,
- add indexes that match real query paths,
- avoid redundant indexes and unbounded scans on growing tables.

Migrations should be production-minded:

- split risky changes into expand, migrate, and contract phases,
- backfill large tables in batches,
- avoid long transactions and table-wide locks,
- keep destructive changes separate and documented.

Transactions should be small and purposeful:

- put writes for one invariant in the same transaction,
- keep slow network calls outside transactions,
- use row locks only for named invariants,
- include expected current state in state transition updates.

## Events And Consistency

- Use append-only events for traceability when state changes need an audit trail.
- Events should have stable types, structured payloads, and enough metadata to
  debug without live external systems.
- Use an outbox when a database write must cause an external message or side
  effect.
- Consumers must tolerate duplicate, delayed, and out-of-order delivery.
- Do not claim exactly-once behavior across process boundaries.

## Cache Consistency

- Cache is an optimization, not the source of truth.
- Cached values must be reconstructable from durable state or an external source
  of truth.
- Define key shape, TTL, invalidation trigger, and allowed staleness.
- Prevent older cache fills from overwriting newer invalidations.
- Provide a bypass or rebuild path for operational recovery.
- Observe hit rate, miss rate, stale reads, invalidation lag, rebuild failures,
  and cache latency.

## Observability

Every important operation needs correlation:

- request ID,
- actor or tenant ID,
- resource ID,
- job or operation ID,
- external provider request ID when available.

Use structured logs for state transitions, worker claims, retry exhaustion,
idempotency hits and mismatches, webhook handling, cache invalidation, and
external provider operations.

Metrics should cover request rate, latency, errors, queue depth, worker lag,
state duration, retry counts, lease expiration, database latency, cache latency,
and idempotency mismatches.

## Security And Data Safety

- Treat user content, prompts, traces, patches, logs, and artifacts as user data.
- Never log secrets, access tokens, environment variables, or authorization
  headers.
- Redact credentials before persistence.
- Enforce authorization before resource access.
- Prefer deny-by-default permission checks.
- Make destructive actions explicit and audited.
- Store enough audit context to answer who did what and which credentials or
  provider were used.

## Backend Change Checklist

Before committing a backend change, answer the relevant questions:

- What durable invariant does this change introduce or rely on?
- Is the operation safe to retry?
- What prevents duplicate side effects?
- What happens if the process crashes between the database write and the
  external side effect?
- What happens if the external side effect succeeds but the process times out?
- Which constraints enforce correctness?
- Which query paths need indexes or query-plan review?
- Does this migration lock, rewrite, or scan a table that can grow large?
- What is the maximum concurrency and how is it bounded?
- What is the allowed consistency window?
- If cache is wrong, how is it detected and rebuilt?
- Are events safe under duplicate, delayed, or out-of-order delivery?
- What logs, metrics, or traces prove the system is healthy?

## Sprint 12 Operational Metrics

`internal/platform/metrics` is the Prometheus operational plane
(docs/sprint/SPRINT-12-PRD.md). It answers "how is the system behaving in
aggregate" — bounded cardinality, unsampled, retained 15d. It is
structurally separate from `internal/agentobs` (the Trace plane), which
answers "what happened in this one Run" — high cardinality, sampled,
identity-bearing. A question that needs a Member, Notebook, or Run
identifier is a Trace question; take it to the Collector query API, never to
a metric label.

Every service exposes `/metrics` and `/debug/pprof/*` from a private admin
listener on its own port (control-plane 9091, worker 9092, collector 9093,
fetcher 9094, document-renderer 9095), never on the public API mux and never
published to a host port in Compose — the port is reachable only inside the
Docker network (or `host.docker.internal` for the host-process dev
services), which is the actual security boundary, not the bind address.

Dashboard: Grafana → "Nano Operations" folder → "Nano Operations
(Sprint 12)" (`infra/observability/grafana/dashboards/sprint-12-operations.json`).
Rules: `infra/observability/prometheus/rules/{recording_rules,alert_rules}.yml`,
unit-tested by `infra/observability/prometheus/rules/rules_test.yml`
(`promtool test rules rules_test.yml`).

### General diagnostic flow

1. **Detect.** An alert fires against a recording rule, never an ad-hoc
   expression — check the Grafana panel for the same metric to confirm the
   shape, not just the threshold breach.
2. **Localize by stage.** The "Staged latency p95" panel breaks end-to-end
   time into queue wait, model call, tool execution, retrieval, and first
   progress. Whichever stage moved is where the regression lives. If none
   moved but end-to-end still grew, check "Unattributed Task time" — a
   growing gap means a stage is missing instrumentation, not that time
   vanished.
3. **Localize by cause.** Break `nano_error_total` down by `layer`, then by
   `error_code` (the "Error rate by layer" / "Error codes" panels). The
   taxonomy is the same one `ClassifyAttempt` uses for retry-versus-terminal
   decisions (`internal/agent/attempt_disposition.go`), so a spike in
   `retryable`-sourced codes and a spike in `terminal`-sourced codes point
   at different fixes.
4. **Cross to the Trace plane.** Metrics stop at the aggregate. Take the
   stage and error code to `/api/admin/traces` (or the Collector query API
   directly) and pull representative Traces for that window — that is
   where Member/Notebook/Run identity lives.

### Alert runbook: NanoTaskSuccessFastBurn

Chat/Studio/Research task success is burning its 7-day error budget more
than 14x too fast in both the 5m and 1h windows — a severe, recent
regression. Check `nano_task_terminal_total{outcome="failed"}` broken down
by `task_variant` to find which Agent Definition is failing, then follow
step 3 above. A concentrated single-`task_variant` spike usually means a
bad deploy of that Definition's prompt/contract; a broad spike across all
variants usually means the model provider or Postgres is degraded.

### Alert runbook: NanoTaskSuccessSlowBurn

Same signal as the fast-burn alert but sustained at a lower rate (>6x
budget) over 30m/6h — a persistent, lower-severity regression rather than
an outage. Treat as a ticket, not a page; investigate within the day using
the same layer/code breakdown.

### Alert runbook: NanoRetrievalDegradedSuccessRateHigh

Runs are reaching `completed` while `nano_agent_run_degraded_total` shows
one or more retrieval channels down (dense, BM25, or reranker). Success
rate alone is hiding this — a source-grounded product returning "successful"
answers off a degraded retrieval channel is a real defect. Check Qdrant
health first (dense/BM25 both live in `qdrantstore`), then the reranker
model's availability.

### Alert runbook: NanoChatEndToEndLatencyHigh / NanoChatEndToEndLatencyCritical

Chat end-to-end p95/p99 exceeds the provisional 45s/120s SLO. Follow the
"localize by stage" flow above first — this alert alone doesn't say which
stage is slow. If queue wait dominates, see `NanoQueueWaitLatencyHigh`
below; if model call dominates, check the configured model's provider
status; if retrieval dominates, see `NanoRetrievalSearchLatencyHigh`.

### Alert runbook: NanoFirstProgressLatencyHigh

Time from SSE subscribe to first stream byte exceeds the provisional 3s
SLO. This measures connect-to-first-byte, not true admission-to-first-token
(Nano's model calls are not streaming — see `docs/sprint/SPRINT-12-PRD.md`
criterion 34). A regression here usually means `runProjection`
(`internal/app/server.go`) is slow, which usually means a slow Postgres
query on the `agent_runs`/`agent_jobs` join, not model latency.

### Alert runbook: NanoRetrievalSearchLatencyHigh

`Pipeline.Search` p95 exceeds the provisional 800ms SLO
(`internal/retrieval/pipeline.go`). Check the "Staged latency" panel's
per-stage breakdown (`nano_retrieval_stage_seconds{stage}`) — `dense` and
`bm25` slowness usually means Qdrant load; `rerank` slowness usually means
the reranker model; `evidence_load` slowness usually means Postgres.

### Alert runbook: NanoQueueWaitLatencyHigh

Queue wait p95 exceeds the provisional 6s SLO. First check whether this is
real: the default Worker `scanInterval` is 5s
(`internal/worker/service.go`), so roughly half that is a physical floor at
low traffic, not a regression (PRD criterion 32). If the number is well
above that floor, check `nano_worker_inflight_attempts` against
`AgentInteractiveConcurrency` — if inflight is pinned at the concurrency
limit, the Worker needs more capacity, not a code fix.

### Alert runbook: NanoUnattributedTaskTimeGrowing

More than 30% of measured end-to-end Task time isn't accounted for by
queue wait, model call, tool execution, or retrieval. This means a stage in
the request path is missing instrumentation — check recent deploys for a
new code path (a new Action, a new delegation hop) that doesn't yet emit a
staged-latency metric, rather than assuming the system got slower.

### Alert runbook: NanoMetricsLabelRejected

A metric received a label value outside its closed allowlist
(`internal/platform/metrics/allowlists.go`), which
`nano_metrics_label_rejected_total` caught and safely mapped to `other`
instead of creating an unbounded series. This is always an instrumentation
defect: find the new value in the recent deploy (a new Agent Definition
identity, a new error code, a new tool name) and add it to the relevant
allowlist in `internal/agent/task_metrics.go` or
`internal/platform/metrics/allowlists.go`.

### Alert runbook: NanoLiveHeapGrowing

Post-GC live heap (`go_gc_heap_live_bytes` — deliberately not RSS or
`heap_inuse`, which reflect allocator retention and GC scheduling rather
than a leak) grew more than 50% over 6h on a process that has been up that
whole time. Escalation path:

1. Check whether an application-level gauge grew alongside it:
   `nano_runhub_subscribers`, `nano_worker_inflight_attempts`,
   `nano_worker_heartbeat_goroutines`, `nano_collector_memory_store_records`,
   `nano_db_pool_connections`, `nano_sse_connections_active`.
2. If one did, that gauge names the leaking surface and the owning code
   path directly — go fix that path.
3. If none did, capture a heap profile from the admin listener:
   `go tool pprof http://<host>:<admin-port>/debug/pprof/heap`. Capture a
   second one an hour later, then `go tool pprof -base=<first> <second>` to
   diff and find the growing allocation site.

### Alert runbook: NanoGoroutineCountGrowing

Goroutine count grew more than 50% over 1h. This usually precedes the
live-heap signal and is cheaper to diagnose — check the same three
goroutine-producing gauges as step 1 above
(`nano_runhub_subscribers`/`nano_worker_inflight_attempts`/`nano_worker_heartbeat_goroutines`)
before reaching for a profiler; a leaked SSE subscription or a stuck
heartbeat goroutine shows up here first.

### Alert runbook: NanoRunHubSteadyStateViolated

`nano_runhub_subscribers` is non-zero with no in-flight HTTP requests and
no recent SSE events sent for 20m — the steady-state invariant
(PRD criterion 59) is violated outside of a soak test. This means an
`unsubscribe()` call in `internal/app/run_hub.go`'s `streamRun` (or the
`source_sse.go` / `studio_routes.go` equivalents) is not running on some
exit path — check for a new early `return` added without the existing
`defer unsubscribe()` covering it.

### Alert runbook: NanoDBPoolNearExhaustion

A named pool (`nano_db_pool_connections{pool,state}`) is above 90%
acquired for 10m — requests will start blocking or failing imminently.
Check for a connection leak (a `tx` or query result not being closed/
committed/rolled back on every path) before assuming the pool just needs a
higher `MaxConns`; `internal/app/db.go` and each service's pool
configuration set `MaxConns` deliberately low to catch leaks early rather
than paper over them with a bigger pool.
