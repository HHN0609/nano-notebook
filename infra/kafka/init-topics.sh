#!/usr/bin/env bash
set -euo pipefail

bootstrap_server="${NANO_KAFKA_BOOTSTRAP_SERVER:-kafka:19092}"
topics_bin="/opt/kafka/bin/kafka-topics.sh"

create_topic() {
  local topic="$1"
  local partitions="$2"
  local retention_ms="$3"

  "${topics_bin}" \
    --bootstrap-server "${bootstrap_server}" \
    --create \
    --if-not-exists \
    --topic "${topic}" \
    --partitions "${partitions}" \
    --replication-factor 1 \
    --config cleanup.policy=delete \
    --config retention.ms="${retention_ms}" \
    --config compression.type=producer
}

create_topic nano.observability.agent-trace.v1 12 604800000
create_topic nano.observability.agent-trace-purge.v1 3 604800000
create_topic nano.observability.agent-trace-quarantine.v1 3 2592000000
create_topic nano.observability.otel-logs.v1 12 604800000
create_topic nano.observability.otel-traces.v1 12 604800000
