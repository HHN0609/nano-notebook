# Nano Notebook Sprint 12 PRD

## Document Status

- **Sprint:** Sprint 12
- **Status:** Draft
- **Date:** 2026-08-01
- **Theme:** Prometheus operational metrics — task success, staged latency, typed error rate, and runtime leak detection
- **Delivery boundary:** Aggregate metrics, alert rules, and an operator runbook only; no log pipeline, no APM vendor, no replacement of the existing Trace plane

## 1. Decision

Nano currently has a strong *per-run forensic* observability plane — `agentobs` records, the Collector, Replay, and Jaeger — and no *aggregate operational* plane. An operator can reconstruct one Agent Run in complete detail but cannot answer "what fraction of Chat tasks succeeded in the last hour", "where did the p99 go", or "is the Worker leaking memory".

Sprint 12 adds a Prometheus metrics plane alongside the existing Trace plane. The two planes stay separate on purpose:

- **Traces** answer *what happened in this run* — high cardinality, sampled, retained short, queried by ID.
- **Metrics** answer *how is the system behaving* — bounded cardinality, unsampled, retained long, queried by aggregation.

Metrics never carry Member, Notebook, Chat, Run, Source, or Message identity. Any question that requires an identity is a Trace question, and the Trace plane already answers it.

## 2. Problem

Four operational questions have no answer today.

1. **Task success rate.** `agent_runs.status` and `studio_outputs.status` hold terminal state in Postgres, but there is no time-series of the completed/failed/cancelled mix, so regressions are invisible until a user reports one.
2. **Where latency goes.** A Chat task spans admission, queue wait, Worker claim, model calls, MCP tool execution, and retrieval. Only two of these are timed in code (`models.Metadata.Latency` and `retrieval.SearchDiagnostics`), and neither is aggregated. A slow Chat cannot be attributed to a stage.
3. **Error rate by cause.** `ClassifyAttempt` already produces a precise typed cause for every failed Attempt, and that classification is discarded after it decides retry-versus-terminal. There is no counter of *why* things fail.
4. **Memory and goroutine leaks.** Control Plane, Worker, and Collector are long-lived Go processes holding in-memory state — the SSE subscriber map (`internal/app/run_hub.go`), per-claim heartbeat goroutines (`internal/worker/service.go`), the Collector memory store, and pgx pools. None of it is observable. A leak is currently discovered by OOM.

## 3. Sprint Goal

Deliver these dependent slices:

1. one shared `internal/platform/metrics` package, a dedicated admin listener per service, and Prometheus in both Compose stacks;
2. task success and error-rate metrics driven by the existing terminal-state and `ClassifyAttempt` taxonomies;
3. staged latency histograms covering queue wait, first progress, model call, tool execution, retrieval stage, and end-to-end;
4. Go runtime, process, and application-specific leak gauges with a documented steady-state invariant;
5. recording rules, multi-window burn-rate alerts, a Grafana dashboard, and an operator runbook;
6. acceptance evidence including a metric-name snapshot test, a cardinality budget test, and a drain-and-compare soak test.

## 4. Success Criteria

Sprint 12 is complete only when all of the following are true.

### 4.1 Exposition and transport

1. Control Plane, Worker, Collector, Fetcher, and Document Renderer each expose Prometheus text-format metrics at `GET /metrics`.
2. `/metrics` is served from a dedicated admin listener bound to a separate port, never from the public API mux, and is not routable through nginx.
3. The Worker, which serves no public HTTP today, gains only this admin listener and no other route.
4. Every metric uses the `nano_` namespace except standard `go_*` and `process_*` collectors.
5. Every metric name ends in its base unit — `_seconds`, `_bytes`, `_total` for counters — per Prometheus naming conventions; no metric reports milliseconds or nanoseconds.
6. Metrics carry no `service`, `instance`, `pod`, or `version` label; those arrive from scrape target relabeling.
7. Prometheus is added to `infra/compose/compose.yaml` and `compose.prod.yaml` with a 15s scrape interval and 15d retention, scraping all five services by service name.
8. Grafana is added to both stacks with the Sprint 12 dashboard provisioned as code, not created by hand.
9. Registering a duplicate collector, or a collector whose name violates the naming rules above, fails process startup rather than being silently dropped.

### 4.2 Cardinality discipline

10. No metric carries a label whose value derives from a Member, Notebook, Chat, Message, Run, Job, Source, Trace, Span, or Evidence identifier.
11. No metric carries a free-text label — query text, error message, model output, prompt content, or file name.
12. Every label with an open-ended domain in code is mapped through an explicit allowlist at emit time; a value outside the allowlist is recorded as `other` and increments `nano_metrics_label_rejected_total{metric,label}`.
13. `nano_metrics_label_rejected_total` being non-zero is treated as an instrumentation defect, not as normal operation, and is alerted on.
14. HTTP metrics label the route *template* — `/api/v1/notebooks/{id}/studio-outputs` — never the request path; the prefix-dispatch mux in `internal/app/server.go` requires this mapping to be explicit and table-driven.
15. Total active series per service instance stays under 15,000, verified by a test that enumerates every registered collector's label domains and multiplies them out.

### 4.3 Task success rate

16. A **Task** is one durable unit of user-visible work with a terminal state. Sprint 12 recognizes exactly three Task kinds: `agent_run`, `studio_output`, and `source_processing`.
17. `nano_task_terminal_total{task_kind,task_variant,outcome}` increments exactly once per Task reaching terminal state, in the same transaction boundary that writes the terminal row, and is never incremented for a non-terminal transition.
18. `outcome` is one of `completed`, `failed`, `cancelled`, or `expired`.
19. `cancelled` is excluded from both the success numerator and the denominator. A Member stopping a Run is an intent, not a defect; folding it into either side makes the rate track user behavior instead of system health.
20. `expired` — deadline exhaustion — counts as a failure, because the system, not the Member, ended the Task.
21. Task success rate is therefore defined once, as a recording rule, and every dashboard and alert consumes that rule rather than restating the expression.
22. `task_variant` for `agent_run` is the pinned Agent Definition identity from the Sprint 10/11 catalog, restricted to the six released roots and `other`.
23. `task_variant` for `source_processing` is the Source kind; for `studio_output` it is the four released Output kinds.
24. `nano_agent_attempt_total{task_variant,disposition}` records every Attempt resolution with `disposition` drawn exactly from `AttemptDisposition` — `completed`, `waiting`, `retryable`, `terminal`, `abandoned`.
25. `nano_agent_run_attempts` is a histogram of Attempts consumed per terminal Run with integer buckets `1,2,3,4,5,+Inf`, so retry amplification is visible separately from success rate.
26. `nano_agent_run_degraded_total{degradation}` counts Runs that reached `completed` while retrieval was degraded, with `degradation` drawn from the existing values `dense_unavailable`, `bm25_unavailable`, `reranker_unavailable`.
27. Criterion 26 exists because a degraded success is not a success for a source-grounded product; success rate read alone must not be able to hide a silently degraded retrieval channel.

### 4.4 Staged latency

28. Every latency metric is a Histogram. Summary is prohibited, because Summary quantiles cannot be aggregated across service instances.
29. Bucket boundaries are declared once per stage as named constants and are chosen so that the stage's target SLO threshold falls exactly on a bucket edge.
30. `nano_task_queue_wait_seconds{task_kind,task_variant}` measures `available_at` to Worker claim, observed in `Queue.ClaimNext`, and therefore excludes intentional retry backoff while including scheduling delay.
31. Queue-wait buckets include `5` explicitly, because the default Worker `scanInterval` is 5s and the polling loop, not load, dominates this stage at low traffic.
32. Sprint 12 records, as an accepted finding, that queue-wait p95 cannot fall below roughly half the scan interval while the Worker polls; moving to Postgres `LISTEN/NOTIFY` is the fix and is out of scope for this Sprint.
33. `nano_chat_first_progress_seconds{task_variant}` measures accepted admission to the first SSE `run` event that carries a status change, and is the Sprint 12 definition of first-response latency.
34. Sprint 12 records, as an accepted finding, that true token-level time-to-first-token does not exist in Nano today because `BifrostClient.Decide` is a non-streaming request/response call. The metric name `nano_model_first_token_seconds` is reserved and deliberately left unimplemented.
35. `nano_model_call_seconds{model,result_kind,outcome}` is populated from the already-computed `ModelOutcome.Metadata.Latency`; the `model` label is the *requested* model identity constrained to the configured allowlist.
36. `nano_tool_execution_seconds{tool,outcome}` covers every MCP tool invocation through the tool plane, with `tool` bounded by the registered tool set and `outcome` one of `completed`, `failed`.
37. Tool timing is measured at the tool plane boundary so that a tool bypassing MCP would be invisible, preserving the Sprint 11 invariant that no execution path parallels MCP.
38. `nano_retrieval_stage_seconds{stage,outcome}` covers exactly the five stages already present in `SearchDiagnostics` — `dense`, `bm25`, `fuse`, `evidence_load`, `rerank` — populated from the existing `DurationNanoseconds` fields without adding new timing code.
39. `nano_retrieval_search_seconds{outcome}` covers the whole `Pipeline.Search` call, and `nano_retrieval_degraded_total{degradation}` counts each declared degradation.
40. `nano_task_end_to_end_seconds{task_kind,task_variant,outcome}` measures accepted admission to terminal state, with buckets extending past `210` because that is the default Worker run timeout.
41. Tail latency is served by `histogram_quantile` over the histograms above at p50/p90/p95/p99; no separate tail metric is introduced.
42. A recording rule publishes *unattributed time* — end-to-end minus the sum of queue wait, model call, tool execution, and retrieval — so that a stage missing instrumentation shows up as a growing gap rather than as silence.

### 4.5 Error rate and cause

43. `nano_error_total{task_kind,layer,error_code}` increments once per typed failure, where `layer` is one of `model`, `tool`, `retrieval`, `storage`, `authorization`, `contract`, `budget`, `lifecycle`.
44. `error_code` is populated from the existing classification produced by `ClassifyAttempt`, `models.ErrorKind`, and `ToolErrorKind`, mapped through the Sprint 12 allowlist.
45. The allowlist contains exactly: `model_timeout`, `model_unavailable`, `model_invalid_response`, `tool_authorization`, `tool_schema`, `tool_invariant`, `tool_infrastructure`, `discovery_timeout`, `discovery_rate_limited`, `discovery_unavailable`, `discovery_not_configured`, `discovery_invalid_query`, `discovery_invalid_response`, `attempt_timeout`, `run_deadline_exceeded`, `lease_lost`, `cancelled`, `retrieval_unavailable`, `result_contract_invalid`, `grounding_invalid`, `other`.
46. The allowlist is enforced at emit time. The existing `safeAttemptErrorCode` regex bounds error-code *format* but not its *domain*, so it is not sufficient protection for a Prometheus label.
47. Adding a new error code to `ClassifyAttempt` without adding it to the allowlist causes a test failure, not a silent `other`.
48. `nano_http_requests_total{route,method,code}` and `nano_http_request_duration_seconds{route,method,code}` cover every API route, with `code` as the numeric status.
49. SSE streams are excluded from the HTTP duration histogram and covered instead by `nano_sse_connections_active{stream}`, `nano_sse_connection_duration_seconds{stream,close_reason}`, and `nano_sse_events_sent_total{stream,event}`.
50. Criterion 49 exists because a long-lived SSE connection recorded as a request duration destroys the API latency distribution.

### 4.6 Runtime and leak detection

51. Every service registers `collectors.NewGoCollector` with `WithGoCollectorRuntimeMetrics(MetricsAll)` and `collectors.NewProcessCollector`.
52. The exported names of the four runtime metrics the runbook depends on — post-GC live heap, goroutine count, heap objects, and process resident memory — are asserted by a snapshot test, because `runtime/metrics` names are translated by `client_golang` and must not drift silently across Go or library upgrades.
53. Leak alerting keys on **post-GC live heap**, not on resident memory and not on `go_memstats_heap_inuse_bytes`. Resident memory reflects allocator retention and GC scheduling; live heap after collection is the only signal that distinguishes a leak from ordinary garbage.
54. `nano_runhub_subscribers` and `nano_runhub_runs_tracked` expose the size of both levels of the `runHub` subscriber map.
55. `nano_worker_inflight_attempts` and `nano_worker_heartbeat_goroutines` expose the Worker's per-claim goroutine population.
56. `nano_collector_memory_store_records` exposes the Collector in-memory store depth.
57. `nano_db_pool_connections{pool,state}` exposes pgx pool `acquired`, `idle`, `total`, and `max` for every pool.
58. `nano_http_inflight_requests` and `nano_sse_connections_active` complete the set of unbounded-growth surfaces.
59. Sprint 12 declares a **steady-state invariant**: with no admitted Tasks and no connected clients for five minutes, `nano_runhub_subscribers`, `nano_runhub_runs_tracked`, `nano_worker_inflight_attempts`, `nano_worker_heartbeat_goroutines`, `nano_http_inflight_requests`, and `nano_sse_connections_active` are all zero, and `go_goroutines` is within 10% of the process baseline.
60. A soak test admits at least 200 Chat Tasks, drains, forces GC, and asserts the steady-state invariant plus live heap within 20% of the pre-soak baseline.
61. `net/http/pprof` is mounted on the admin listener only, never on the public mux, so that a live heap alert can be escalated to `go tool pprof -base` without a redeploy.
62. The runbook documents the escalation path: live-heap alert fires, capture a heap profile, capture a second after one hour, diff with `-base`, and correlate the growing allocation site with the application gauge that is also growing.

### 4.7 SLOs and alerting

63. Sprint 12 publishes provisional SLO targets, explicitly marked provisional and to be recalibrated from the first two weeks of real data rather than defended as correct on day one.
64. Provisional targets: Chat task success ≥ 97% over 7d; first progress p95 ≤ 3s; Chat end-to-end p95 ≤ 45s and p99 ≤ 120s; retrieval search p95 ≤ 800ms; tool execution p95 ≤ 2s; queue wait p95 ≤ 6s.
65. Availability alerting uses multi-window multi-burn-rate error-budget alerts — a fast 1h window and a slow 6h window — not a single static threshold on instantaneous error rate.
66. Latency alerting keys on p95 and p99 over a 30m window, never on an average.
67. Leak alerting requires both sustained live-heap growth over a 6h window *and* a process uptime exceeding that window, so that a restart cannot produce a false positive.
68. A separate alert fires on monotonic `go_goroutines` growth over 1h, because a goroutine leak usually precedes the heap signal and is cheaper to diagnose.
69. Every alert carries a runbook link; an alert without one fails the alert-rule lint test.
70. Alert rules and recording rules are unit-tested with `promtool test rules` against fixture series, including at least one true-positive and one false-positive case per leak alert.

### 4.8 Regression and evidence

71. Instrumentation adds no query to any request path; every metric is derived from data already in hand at its observation point.
72. Metric emission never blocks, never allocates unboundedly, and never fails a Task; a metrics error is logged and dropped.
73. Existing Sprint 1–11 tests remain green, and the Trace plane, Replay, and Collector behavior are unchanged.
74. `docs/sprint/SPRINT-12-ACCEPTANCE.md` maps every criterion above to a direct test, source, dashboard, or alert-rule-test evidence reference before the Sprint is marked accepted.

## 5. Canonical Terms

- **Task** — one durable unit of user-visible work with a terminal state: an Agent Run, a Studio Output, or a Source Processing pipeline.
- **Stage** — one attributable segment of a Task's wall-clock time: queue wait, model call, tool execution, retrieval, or unattributed remainder.
- **Outcome** — a Task's terminal disposition: `completed`, `failed`, `cancelled`, or `expired`.
- **Disposition** — an Attempt's resolution, from the existing `AttemptDisposition` type.
- **Layer** — the architectural boundary at which a failure was classified.
- **Allowlist** — the closed set of permitted values for a label whose source domain is open in code.
- **Steady state** — the system with no admitted Tasks and no connected clients, against which leak gauges must read zero.
- **Live heap** — heap bytes retained after a completed garbage collection; the leak signal.
- **Metrics plane** — bounded-cardinality aggregate time series. Distinct from the **Trace plane**, which is per-run and identity-bearing.

## 6. Metric Catalog

### 6.1 Success

| Metric | Type | Labels |
| --- | --- | --- |
| `nano_task_terminal_total` | Counter | `task_kind`, `task_variant`, `outcome` |
| `nano_agent_attempt_total` | Counter | `task_variant`, `disposition` |
| `nano_agent_run_attempts` | Histogram | `task_variant` |
| `nano_agent_run_degraded_total` | Counter | `degradation` |

### 6.2 Staged latency

| Metric | Type | Labels | Source |
| --- | --- | --- | --- |
| `nano_task_queue_wait_seconds` | Histogram | `task_kind`, `task_variant` | `Queue.ClaimNext` |
| `nano_chat_first_progress_seconds` | Histogram | `task_variant` | admission → first SSE status change |
| `nano_model_call_seconds` | Histogram | `model`, `result_kind`, `outcome` | `ModelOutcome.Metadata.Latency` |
| `nano_tool_execution_seconds` | Histogram | `tool`, `outcome` | MCP tool plane boundary |
| `nano_retrieval_stage_seconds` | Histogram | `stage`, `outcome` | `SearchDiagnostics` |
| `nano_retrieval_search_seconds` | Histogram | `outcome` | `Pipeline.Search` |
| `nano_task_end_to_end_seconds` | Histogram | `task_kind`, `task_variant`, `outcome` | admission → terminal |

### 6.3 Errors

| Metric | Type | Labels |
| --- | --- | --- |
| `nano_error_total` | Counter | `task_kind`, `layer`, `error_code` |
| `nano_retrieval_degraded_total` | Counter | `degradation` |
| `nano_http_requests_total` | Counter | `route`, `method`, `code` |
| `nano_http_request_duration_seconds` | Histogram | `route`, `method`, `code` |
| `nano_sse_connection_duration_seconds` | Histogram | `stream`, `close_reason` |
| `nano_sse_events_sent_total` | Counter | `stream`, `event` |
| `nano_metrics_label_rejected_total` | Counter | `metric`, `label` |

### 6.4 Runtime and leak surfaces

| Metric | Type | Labels | Leak surface |
| --- | --- | --- | --- |
| `nano_runhub_subscribers` | Gauge | — | SSE subscriber channels |
| `nano_runhub_runs_tracked` | Gauge | — | outer subscriber map |
| `nano_sse_connections_active` | Gauge | `stream` | open SSE responses |
| `nano_http_inflight_requests` | Gauge | — | handler goroutines |
| `nano_worker_inflight_attempts` | Gauge | — | per-claim execution goroutines |
| `nano_worker_heartbeat_goroutines` | Gauge | — | per-claim lease heartbeats |
| `nano_collector_memory_store_records` | Gauge | — | in-memory Trace store |
| `nano_db_pool_connections` | Gauge | `pool`, `state` | pgx pool exhaustion |
| `go_*`, `process_*` | — | — | runtime baseline |

## 7. Operator Flow

1. **Detect.** A burn-rate or latency alert fires against a recording rule, never against an ad-hoc expression.
2. **Localize by stage.** Open the dashboard's stage-decomposition panel. One stage's p95 will have moved, or the unattributed-time series will have grown — which itself localizes the gap to missing instrumentation.
3. **Localize by cause.** Break `nano_error_total` down by `layer`, then by `error_code`. The taxonomy is the same one the retry logic already uses, so a spike in `retryable` codes and a spike in `terminal` codes point at different remediations.
4. **Cross to the Trace plane.** Metrics stop at the aggregate. Take the stage and error code to the Collector query API and pull representative Traces — that is where identity lives.
5. **Leak path.** A live-heap or goroutine alert routes to the leak runbook instead: check whether an application gauge is growing alongside; if one is, the leak is that surface and the code path is known; if none is, capture and diff heap profiles from the admin listener.

## 8. Explicit Non-Goals

1. No log aggregation, no Loki, no structured-log pipeline. `slog` output stays as it is.
2. No replacement, duplication, or modification of the `agentobs` Trace plane, the Collector, Replay, or Jaeger.
3. No per-Member, per-Notebook, or per-Run metric labels, and no metric that could be used to re-identify a Member.
4. No token-level time-to-first-token, which requires streaming model support Nano does not have.
5. No RAG answer-quality metrics. Grounding and citation quality stay in the offline `rageval` harness; Sprint 12 measures only whether retrieval *ran* and how fast, never whether it was *right*.
6. No cost or token-spend dashboards, even though the Trace attributes exist; that is a separate product concern.
7. No autoscaling, no capacity planning, and no load generation beyond the soak test.
8. No paging integration, no on-call rotation, no external alertmanager receiver — alert rules are delivered and verified, routing is not.
9. No replacement of the Worker polling loop with `LISTEN/NOTIFY`, despite Sprint 12 identifying it as the queue-wait floor.

## 9. Delivery Slices

1. **Platform.** `internal/platform/metrics` with registry construction, naming and allowlist enforcement, the admin listener, Go/process collectors, pprof mounting, and Prometheus plus Grafana in both Compose stacks.
2. **Edge.** HTTP route-template instrumentation, SSE connection and event metrics, and the in-flight gauges.
3. **Task lifecycle.** Terminal-state counters at each of the three Task kinds' terminal transactions, Attempt dispositions, retry-amplification histogram, and degraded-success counter.
4. **Stage latency.** Queue wait at claim, first progress at the SSE boundary, model call from existing metadata, tool execution at the MCP boundary, and retrieval stages wired from existing `SearchDiagnostics`.
5. **Error taxonomy.** The allowlist, its enforcement, the drift-detection test, and `nano_error_total` emission at each classification site.
6. **Leak plane.** Application gauges on every unbounded-growth surface, the runtime metric name snapshot test, the steady-state invariant, and the soak test.
7. **Rules and runbook.** Recording rules, multi-window burn-rate alerts, latency alerts, leak alerts, `promtool` rule tests, the provisioned dashboard, and `docs/engineering` runbook sections for each alert.
8. **Acceptance.** `docs/sprint/SPRINT-12-ACCEPTANCE.md` with the metric-name snapshot, the cardinality budget calculation, soak-test output, and rule-test output.
