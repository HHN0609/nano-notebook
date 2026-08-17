# Trace Analytics

The product dashboard and the operations dashboard answer different questions:

- `/admin/traces/analytics` aggregates bounded, authorized ClickHouse data for
  Agent developers. It never exposes Prompt, output, Replay, or raw payloads.
- Grafana shows whether Kafka, the Agent Trace Processor, ClickHouse, and the
  Analytics API are healthy. It uses only bounded operational labels.
- The operations dashboard also exposes the raw-to-summary watermark gap; a
  sustained gap means retained events are newer than the product projection.

The API defaults to 24 hours, rejects ranges over 30 days, uses server-selected
UTC buckets, and returns `schema_version`, actual filters, `generated_at`,
`fresh_through`, and nullable-data coverage. Success/error/retry rates use only
terminal Agent Runs as their denominator. Active Runs remain visible in Run
counts but never enter latency percentiles or terminal-rate denominators.

Token and cost absence is unknown, not zero. Costs remain split by currency.
Older rows without newly typed provider/tool/error/version/RAG fields appear in
an `unknown` bucket. Quality trends remain out of scope until real evaluation or
feedback data exists.

Access requires `platform.trace.analytics`, independently of
`platform.trace.read`. The Control Plane injects authorization scope and ignores
client-supplied Notebook scope. The browser calls the Control Plane only; the
Collector service credential and ClickHouse credentials never reach it.

For alert response and the product-vs-infrastructure decision boundary, see
[the Trace analytics pipeline runbook](BACKEND_ENGINEERING.md#trace-analytics-pipeline-runbook).
