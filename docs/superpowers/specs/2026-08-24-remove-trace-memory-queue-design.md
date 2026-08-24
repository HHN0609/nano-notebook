# Remove the Trace Memory Queue Design

**Date:** 2026-08-24

**Status:** Approved

**Supersedes:** the producer queue, transport-selection, retry, and rollback sections of `2026-08-18-kafka-clickhouse-default-trace-cutover-design.md`

## 1. Outcome

Nano Notebook will remove the application-owned `agentbatch.Exporter` memory queue from Durable Agent Trace production. Worker and Control Plane will publish each validated Trace Record directly into the bounded franz-go producer with `TryProduce`. Kafka will be the only production Trace transport and will own buffering, network batching, compression, broker retry, and acknowledgement.

The storage path remains:

```text
Worker / Control Plane
  -> Kafka producer client
  -> nano.observability.agent-trace.v1 (key = trace_id)
  -> Agent Trace Processor
  -> ClickHouse raw Trace authority and query projections
```

Application PostgreSQL remains authoritative for Run, Job, Checkpoint, Message, authorization, Trace anchors, and purge intent. It is not part of the normal Trace log storage path. The optional Observability PostgreSQL rollback profile is independent of this producer change and is not a reason to retain an HTTP Trace producer or an application memory queue.

## 2. Motivation

The current Kafka path has two bounded in-process buffering layers:

1. `agentbatch.Exporter`: 10,000 Records / 32 MiB, with a 128-Record / 512 KiB / 250 ms application Batch policy; and
2. franz-go: 10,000 Records / 32 MiB, with a 5 ms producer linger.

The outer Exporter also retries whole Collector Batches while franz-go already owns idempotent Kafka production, broker retry, compression, delivery timeout, and batching. This duplicates buffering and retry ownership, increases memory and lifecycle complexity, and makes the Kafka producer path harder to explain and operate.

The desired boundary is one queue with one owner: the Kafka client owns volatile pre-broker buffering. ClickHouse idempotency remains based on stable Record identity and canonical content hash, not on the application Batch ID.

## 3. Scope

This change includes:

1. delete `internal/agentbatch.Exporter`, its queue loop, batching policy, retry loop, stats, tests, and production construction;
2. add a Kafka-native Agent Trace Sink that implements the existing `agent.TraceSink` `Offer` contract;
3. publish with franz-go `TryProduce`, never blocking `Produce` or `ProduceSync` on the product Trace path;
4. emit one Kafka message per Trace Record while retaining the current versioned Kafka Trace envelope and `trace_id` key;
5. make delivery acknowledgement, buffer saturation, asynchronous failure, buffered capacity, and acknowledgement latency observable;
6. flush and close the Kafka producer directly during process shutdown;
7. remove production HTTP Trace transport selection and its environment/config validation;
8. remove the product-side `NANO_AGENT_TRACE_KAFKA_MAX_RETRIES` policy because franz-go owns retry within the delivery timeout; and
9. adapt `obs-bench` so HTTP benchmarking remains synchronous and queue-free while Kafka benchmarking can exercise the Kafka-native Sink.

This change does not include:

- changes to Kafka consumer offset, retry, quarantine, rebalance, or ClickHouse persistence semantics;
- changes to the Kafka purge Outbox or quarantine writers, which intentionally wait for durable command acknowledgement;
- historical Trace migration, PostgreSQL/ClickHouse dual write, or query fallback;
- deletion of dormant Observability PostgreSQL data or rollback-profile containers;
- making pre-broker Trace delivery durable across producer process crashes; or
- changing Trace identity, canonical hash, Replay, purge, authorization, or retention contracts.

## 4. Selected Architecture

### 4.1 Kafka Trace Sink

A new Kafka-native Sink owns Trace envelope construction and producer lifecycle. It exposes the small product-facing surface:

```go
type TraceSink interface {
    Offer(context.Context, agentbatch.Envelope) error
}
```

Its construction requires:

- producer identity;
- Kafka topic;
- a bounded asynchronous Kafka producer;
- a bounded delivery observer for counters, gauges, latency, and logs; and
- a clock and ID generator where deterministic tests need them.

The Sink does not own a slice, channel, goroutine, retry map, timer, pending Batch, or application queue. All pending payload bytes belong to franz-go.

The Sink may own only bounded lifecycle synchronization: a process-lifetime context, a closing flag, and a lock that fences concurrent `Offer` calls from `Flush`/`Close`. Shutdown acquires the exclusive lifecycle boundary after all in-progress `Offer` calls leave their short critical section, marks the Sink closed, and then flushes. This synchronization contains no Record payloads and is not a queue.

### 4.2 One Record per Kafka Message

For each `Offer`:

1. validate the Trace descriptor, Record, Replay attachment descriptors, and encoded-size limit;
2. compute the Record canonical SHA-256;
3. build one current-schema `KafkaTraceEnvelope` containing one `TraceChunk` with one `SequencedRecord` and the Record's Replay attachments;
4. assign a unique diagnostic `batch_id` for envelope compatibility;
5. use `trace_id` as the Kafka key; and
6. call franz-go `TryProduce` with a short, non-blocking delivery callback.

The Record passed to franz-go uses the Sink's process-lifetime context, not the caller's request or transaction context. Caller cancellation after `Offer` returns must not cancel a Record already handed to Kafka. Sink shutdown remains the lifecycle authority for bounded flush and close.

The existing consumer already accepts a Trace Chunk containing one or more Records. A one-Record Chunk therefore requires no consumer schema version or storage-contract change. Multiple messages for the same Trace route to the same partition where possible, while correctness continues to rely on Record idempotency rather than partition order.

Kafka still batches these individual messages into compressed Produce requests. Removing semantic application batching increases Kafka Record count but does not imply one network request per Trace Record. The existing benchmark can measure the resulting message-rate and storage-overhead trade-off; this refactor makes no throughput-parity claim.

## 5. Non-Blocking and Failure Semantics

franz-go `Produce` is not acceptable because it blocks when `MaxBufferedRecords` or `MaxBufferedBytes` is reached. The Sink must use `TryProduce`, which reports `kgo.ErrMaxBuffered` through the delivery callback without waiting for capacity.

`Offer` returns synchronously only for product-owned validation or lifecycle failures, including:

- invalid Trace/Record/attachment data;
- a single encoded message exceeding the configured producer/message bound; or
- publication after Sink shutdown has begun.

After validation, `Offer` hands the Record to franz-go and returns without waiting for a broker acknowledgement. Buffer saturation and later Kafka delivery failures are asynchronous diagnostic outcomes. They increment bounded metrics and emit structured logs, but cannot roll back an already committed product transaction or fail a later Agent operation.

This deliberately changes the old outer-queue failure boundary: an application queue-full error can no longer propagate through `DeliveryRequired`. Invalid Trace data remains a required instrumentation error because it is a producer correctness defect; Kafka availability is operational degradation.

The precise acceptance states are:

```text
validated     = product envelope is well formed
buffered      = franz-go accepted it into volatile producer memory
acknowledged  = Kafka broker acknowledged it with acks=all
searchable    = Processor committed it to ClickHouse and the query projection converged
```

Only `acknowledged` is Kafka acceptance. `Offer == nil` alone is not evidence of broker acceptance or ClickHouse visibility. Process exit, buffer saturation, delivery timeout, or final producer failure before acknowledgement can lose Trace data; every observed loss must be visible, never silent.

## 6. Retry and Idempotency

The product Trace path removes the application `MaxRetries` counter and whole-Batch retry loop. franz-go owns idempotent producer sequencing and broker retry within `RecordDeliveryTimeout`.

Uncertain acknowledgement and producer replay can still create duplicate messages. The Agent Trace Processor continues to enforce:

- same `identity_key` plus same canonical hash: idempotent replay;
- same `identity_key` plus different canonical hash: immutable quarantine conflict; and
- offset commit only after ClickHouse persistence or durable permanent-failure quarantine.

The per-message `batch_id` is diagnostic envelope metadata. It is not a deduplication or business-correctness key.

## 7. Observability

The producer path will expose bounded, identity-free metrics for:

- Trace offers rejected synchronously by reason;
- Kafka delivery callbacks by `acknowledged`, `buffer_full`, `timed_out`, and `failed` result;
- callback acknowledgement latency;
- current franz-go buffered Records and bytes; and
- flush/close failures.

Callbacks must remain fast and must not call blocking producer APIs, flush, or perform per-Record unbounded logging. Error classification maps arbitrary broker errors into a small allowlist. Metrics are authoritative for sustained failure volume; structured logs are rate-limited diagnostics. Trace IDs and Batch IDs remain in Trace storage and sampled/rate-limited logs rather than Prometheus labels.

Existing Kafka consumer lag, oldest-message age, Processor outcome, searchable freshness, and raw/summary watermark metrics continue to prove post-acknowledgement health.

## 8. Configuration and Lifecycle

Worker and Control Plane will construct a Kafka Trace Sink directly. Production Trace configuration retains:

```text
NANO_AGENT_TRACE_KAFKA_BROKERS
NANO_AGENT_TRACE_KAFKA_TOPIC
NANO_AGENT_TRACE_KAFKA_CLIENT_ID
```

The bounded producer record/byte limits, delivery timeout, linger, compression, and `acks=all` remain explicit code-owned defaults unless a separately approved operations requirement makes them environment configurable.

Local Compose remains a one-broker KRaft development topology with replication factor one and must not be described as highly available. Production Compose retains three combined broker/controller nodes, all three bootstrap addresses, and replication factor three. Before removing the production HTTP fallback, the Agent Trace, purge, and quarantine topics must explicitly require `min.insync.replicas=2`; replication factor three plus `acks=all` alone does not prevent acknowledgement after the ISR has degraded to one replica.

Production Trace configuration removes:

```text
NANO_AGENT_TRACE_TRANSPORT
NANO_AGENT_TRACE_KAFKA_MAX_RETRIES
```

Startup still pings Kafka and fails closed before accepting work when brokers are unavailable or configuration is invalid. There is no silent HTTP fallback.

Shutdown follows:

1. stop accepting new process work;
2. mark the Trace Sink closing so new `Offer` calls fail deterministically;
3. call franz-go `Flush(ctx)` with the existing bounded shutdown context;
4. report any remaining buffered/delivery failure; and
5. close the producer exactly once.

The shutdown path does not recreate an application drain queue.

## 9. HTTP and PostgreSQL Boundaries

Worker and Control Plane no longer support HTTP Trace production. `ManagedSender` and transport selection are removed from the product boot path.

The HTTP Batch Sender may remain as a synchronous benchmark/test adapter. `obs-bench --transport=http` can construct and send explicit benchmark Batches without an application queue because blocking behavior is acceptable in the load-generator control path and is measured rather than hidden.

The default Trace storage path remains ClickHouse. Application PostgreSQL continues to own product state and purge intent. The dormant `postgres-trace-rollback` profile is separate deployment cleanup and is not modified unless implementation references make a minimal consistency change necessary. No new Trace writes are introduced into PostgreSQL.

## 10. Testing Strategy

Implementation follows red-green-refactor. Each production behavior begins with a failing focused test.

### Kafka-native Sink

- one valid Envelope produces one current-schema message with one Record and the correct `trace_id` key;
- multiple Offers do not create an application queue or wait for acknowledgements;
- `TryProduce` is used and ordinary blocking `Produce`/`ProduceSync` is unreachable on the product Trace path;
- caller-context cancellation after `Offer` does not cancel buffered Kafka delivery;
- buffer-full and asynchronous failures become bounded callback outcomes;
- broker acknowledgement records latency and success;
- invalid Records, oversized messages, and post-shutdown Offers fail synchronously;
- Replay attachments survive the one-Record envelope unchanged; and
- concurrent Offer/shutdown is race-free, and flush/close are bounded and idempotent.

### Product wiring

- Worker and Control Plane construct Kafka Trace Sinks directly and still fail startup on Kafka readiness failure;
- HTTP and unknown Trace transport configuration paths no longer exist;
- queue Batch-size/delay/pending configuration is absent;
- product Kafka retry-count configuration is absent; and
- Agent/product work does not wait for a broker acknowledgement or fail from `ErrMaxBuffered`.

### Regression and integration

- Processor schema, idempotency, retry, quarantine, offset, ClickHouse, Replay, and purge tests remain green;
- `obs-bench` HTTP and Kafka modes compile and retain explicit acceptance semantics;
- focused tests run under the race detector where concurrency/lifecycle behavior is involved;
- Compose configuration tests prove Kafka is the only Worker/Control Plane Trace transport;
- production Compose tests prove three brokers, topic replication factor three, and `min.insync.replicas=2`, while local development remains explicitly RF=1;
- `./scripts/test-go` passes; and
- a final static audit finds no `agentbatch.Exporter`, `NewExporter`, `ErrQueueFull`, pending/inflight queue stats, producer-side Trace queue loop, or production HTTP Trace transport selection.

## 11. Acceptance Criteria

The change is complete only when all of the following are evidenced:

1. `internal/agentbatch/exporter.go` and its queue tests are deleted;
2. no product process owns a Trace Record queue, channel, pending slice, delayed Batch timer, or application retry loop;
3. product Trace publication uses franz-go `TryProduce` and is bounded by the Kafka client's configured Records/bytes;
4. Kafka key, versioned envelope, Record canonical hash, Replay descriptors, and Processor/ClickHouse semantics remain correct;
5. buffer saturation and asynchronous delivery failure are observable and cannot block or reverse product work;
6. Worker and Control Plane have no production HTTP Trace fallback;
7. shutdown flushes and closes Kafka directly within a deadline;
8. production Trace/control topics require RF=3 and `min.insync.replicas=2` before the HTTP fallback is removed;
9. all focused, race, integration, Compose, and full Go gates pass; and
10. the complete diff contains no unrelated behavior change or user work.

Removing constructor calls while retaining a renamed or hidden application queue does not satisfy this design. Passing unit tests without proving the absence of queue ownership and blocking producer calls does not satisfy acceptance.
