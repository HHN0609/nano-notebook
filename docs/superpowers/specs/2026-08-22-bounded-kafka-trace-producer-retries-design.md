# Bounded Kafka Trace Producer Retries

Date: 2026-08-22

## Goal

Prevent the Durable Agent Trace producer from retrying the same Kafka Batch forever after repeated uncertain or retryable delivery failures.

## Scope

This change applies only to the Kafka Trace sender used by Worker, Control Plane, and the observability benchmark. It does not change:

- the HTTP Trace transport;
- Kafka consumer retry and offset-commit behavior;
- ClickHouse idempotency or quarantine behavior;
- the durable PostgreSQL-backed Trace purge outbox.

## Design

`KafkaSender` will track consecutive failed send attempts by stable `batch_id`. `MaxRetries` means retries after the initial attempt. Production configuration defaults to `3`, so one Batch may be sent at most four times.

For a retryable Kafka acknowledgement failure:

1. before the limit, return the existing retryable delivery error so `Exporter` retries the same in-flight Batch;
2. on the final allowed failure, clear the Batch retry state and return a non-retryable delivery error;
3. `Exporter` then drops that in-flight Batch, records its existing dropped/error state, and continues with later queued Batches.

Successful delivery clears retry state immediately. Invalid input remains non-retryable and does not create retry state. Retry state is protected for concurrent callers and removed on success or terminal failure, so completed Batch IDs do not accumulate.

The limit counts outer `KafkaSender.Send` calls. Franz-go may still perform bounded internal broker retries within one call under its delivery timeout; those internal attempts are not counted separately.

## Configuration

Add `MaxRetries` to the Kafka sender configuration and wire a default of `3` through Worker, Control Plane, and the observability benchmark Kafka paths. A negative value is invalid; zero means only the initial attempt and no outer retry.

## Failure Semantics

Reaching the limit may drop diagnostic Trace records that never received a Kafka acknowledgement. It must not block or fail an already committed product operation. Downstream delivery remains at-least-once: if Kafka accepted an earlier attempt but its acknowledgement was lost, duplicate delivery is still handled by `identity_key + canonical_sha256` idempotency.

## Verification

Tests will prove that:

- failures before the limit remain retryable;
- the final allowed failure becomes non-retryable;
- total sends equal `1 + MaxRetries`;
- a successful retry clears Batch state;
- a terminally failed Batch does not prevent a later Batch from being sent;
- invalid retry configuration is rejected;
- existing Kafka sender and Exporter tests continue to pass.
