#!/usr/bin/env bash
set -euo pipefail

bootstrap_server="${NANO_KAFKA_BOOTSTRAP_SERVER:-kafka:19092}"
replication_factor="${NANO_KAFKA_REPLICATION_FACTOR:-1}"
topics_bin="/opt/kafka/bin/kafka-topics.sh"
configs_bin="/opt/kafka/bin/kafka-configs.sh"

create_topic() {
  local topic="$1"
  local partitions="$2"
  local retention_ms="$3"
  local min_insync_replicas="${4:-1}"

  "${topics_bin}" \
    --bootstrap-server "${bootstrap_server}" \
    --create \
    --if-not-exists \
    --topic "${topic}" \
    --partitions "${partitions}" \
    --replication-factor "${replication_factor}" \
    --config cleanup.policy=delete \
    --config retention.ms="${retention_ms}" \
    --config min.insync.replicas="${min_insync_replicas}" \
    --config compression.type=producer
}

configure_min_isr() {
  local topic="$1"
  local min_insync_replicas="$2"

  "${configs_bin}" \
    --bootstrap-server "${bootstrap_server}" \
    --alter \
    --entity-type topics \
    --entity-name "${topic}" \
    --add-config min.insync.replicas="${min_insync_replicas}"
}

application_min_isr="${NANO_KAFKA_MIN_INSYNC_REPLICAS:-1}"
create_topic nano.observability.agent-trace.v1 12 604800000 "${application_min_isr}"
create_topic nano.observability.agent-trace-purge.v1 3 604800000 "${application_min_isr}"
create_topic nano.observability.agent-trace-quarantine.v1 3 2592000000 "${application_min_isr}"
configure_min_isr nano.observability.agent-trace.v1 "${application_min_isr}"
configure_min_isr nano.observability.agent-trace-purge.v1 "${application_min_isr}"
configure_min_isr nano.observability.agent-trace-quarantine.v1 "${application_min_isr}"
create_topic nano.observability.otel-logs.v1 12 604800000
create_topic nano.observability.otel-traces.v1 12 604800000
