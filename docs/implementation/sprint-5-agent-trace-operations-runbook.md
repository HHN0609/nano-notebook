# Agent Trace Kafka and ClickHouse Operations Runbook

Durable Agent Trace records leave Worker and Control Plane through Kafka and are
retained and queried in ClickHouse. Application PostgreSQL owns product state,
lightweight Trace anchors, and durable purge intent; it is not the Trace log store.

## Responsibility and data boundaries

```text
Worker / Control Plane
  -> franz-go TryProduce (bounded volatile client buffer)
  -> nano.observability.agent-trace.v1 (key = trace_id)
  -> Agent Trace Processor
  -> ClickHouse raw records and query projections

Application PostgreSQL
  -> Run, Job, Checkpoint, Message, authorization, Trace anchor, purge intent
```

- Product commits do not wait for Kafka acknowledgement. A valid `Offer` hands one
  Record to franz-go and returns; Kafka batches and compresses messages on the wire.
- `acks=all` means Kafka acknowledged the message. It does not mean the Processor has
  committed it to ClickHouse or that it is searchable.
- A hard process loss, full producer buffer, delivery timeout, or final producer error
  before acknowledgement can lose diagnostic Trace data. Producer metrics and
  rate-limited logs expose those outcomes.
- Kafka owns producer buffering and retry within the delivery timeout. There is no
  application Trace queue, delayed Batch loop, HTTP fallback, or outer retry counter.
- Deletion has a different guarantee: purge intent remains durable in Application
  PostgreSQL, and the purge sender waits for Kafka acknowledgement before completing
  the command.
- Replay ciphertext remains in object storage. Trace messages carry only validated
  Replay attachment descriptors.

## Kafka and ClickHouse topology

Local Compose is deliberately a one-broker KRaft development topology with topic
replication factor 1 and `min.insync.replicas=1`. It is not highly available.

Production Compose uses three combined broker/controllers. Application Trace, purge,
and quarantine topics use replication factor 3 and `min.insync.replicas=2`; producers
use all three bootstrap addresses and `acks=all`. ClickHouse is the normal raw Trace
and query-projection store. The `postgres-trace-rollback` profile is dormant and is not
part of the default path.

## Required producer configuration

Worker and Control Plane use Kafka only:

- `NANO_AGENT_TRACE_KAFKA_BROKERS`
- `NANO_AGENT_TRACE_KAFKA_TOPIC`
- `NANO_AGENT_TRACE_KAFKA_CLIENT_ID`

Worker purge delivery additionally uses:

- `NANO_AGENT_TRACE_KAFKA_PURGE_TOPIC`
- `NANO_AGENT_TRACE_KAFKA_PURGE_CLIENT_ID`
- `NANO_TRACE_PURGE_MAX_COMMANDS`
- `NANO_TRACE_PURGE_LEASE_DURATION`
- `NANO_TRACE_PURGE_POLL_INTERVAL`
- `NANO_TRACE_PURGE_BASE_BACKOFF`
- `NANO_TRACE_PURGE_MAX_BACKOFF`

The product code owns the franz-go bounds: 10,000 buffered Records, 32 MiB buffered
key/value bytes, a 512 KiB single-message bound, a 10-second delivery timeout, and a
5 ms linger. Changing these is an architecture/operations decision, not an ad hoc
environment override.

The removed settings `NANO_AGENT_TRACE_TRANSPORT`,
`NANO_AGENT_TRACE_KAFKA_MAX_RETRIES`, and `NANO_TRACE_BATCH_MAX_*` have no product
effect.

## Producer health and failure semantics

Watch these producer metrics per process:

- `nano_agent_trace_producer_offer_rejected_total{reason}`
- `nano_agent_trace_producer_deliveries_total{result}`
- `nano_agent_trace_producer_delivery_duration_seconds{result}`
- `nano_agent_trace_producer_buffered_records`
- `nano_agent_trace_producer_buffered_bytes`
- `nano_agent_trace_producer_shutdown_failures_total`

Delivery results are bounded to `acknowledged`, `buffer_full`, `timed_out`, and
`failed`. No Trace, Run, Chat, or user identity is used as a metric label.

| Failure | Product effect | Trace effect | Operator action |
|---|---|---|---|
| Producer buffer full | none | new Record is rejected asynchronously | inspect buffered gauges and broker health |
| Broker/network outage | none | callbacks time out or fail; pre-ack tail may be lost | restore Kafka and verify acknowledgements recover |
| Worker hard crash | normal Job recovery | unacknowledged producer tail may be incomplete | restart Worker; do not fabricate Trace records |
| Processor/ClickHouse outage | none | Kafka lag and oldest age grow | repair Processor/ClickHouse and let it catch up |
| Invalid/oversized envelope | instrumentation call reports an error | Record never reaches Kafka | fix producer correctness defect |
| Purge delivery failure | product deletion command stays durable | purge is delayed | restore Kafka; never advance or delete the command manually |

## Backlog response

Use consumer lag and oldest-message age together. Locate the affected partition and
the oldest retained message, stop the cause, then let the Processor catch up. Do not
manually advance consumer offsets to make a graph green.

Temporary processing failures retain the offset for retry. Permanently invalid data is
written to durable quarantine before its offset is committed. `acks=all` does not
replace this consumer-side retention/quarantine boundary.

## Shutdown

On shutdown, each product process stops taking work, marks its Kafka Trace Sink closed,
flushes franz-go with the bounded process shutdown context, and closes the producer
once. A flush failure increments the shutdown metric and is logged because remaining
buffered records may be lost.

## Verification gates

```sh
scripts/test-go
go test -race ./internal/agentbatch -run TestKafkaTraceSink -count=1
go test ./infra/compose -count=1
```

Acceptance also requires a static check that product code contains no
`agentbatch.Exporter`, `NewExporter`, `ErrQueueFull`, application pending/inflight
Trace queue stats, production HTTP Trace selection, or blocking `Produce`/
`ProduceSync` call on the product Trace path.
