#!/usr/bin/env bash
set -euo pipefail

expected_raw=${1:?expected raw record count is required}
expected_traces=${2:?expected Trace count is required}
timeout_seconds=${3:-120}
start_ms=$(date +%s%3N)

for ((attempt = 0; attempt < timeout_seconds * 2; attempt++)); do
  mapfile -t counts < <(
    docker exec nano-bench-clickhouse clickhouse-client \
      --user nano_observability \
      --password nano-observability \
      --database nano_observability \
      --multiquery \
      --query 'select count() from obs_trace_records_raw; select uniqExact(trace_id) from obs_trace_summaries;'
  )
  raw=${counts[0]:-0}
  traces=${counts[1]:-0}
  if ((raw >= expected_raw && traces >= expected_traces)); then
    end_ms=$(date +%s%3N)
    printf 'RECOVERY_MS=%d RAW=%d TRACES=%d\n' "$((end_ms - start_ms))" "$raw" "$traces"
    exit 0
  fi
  sleep 0.5
done

printf 'RECOVERY_TIMEOUT RAW=%d TRACES=%d\n' "$raw" "$traces" >&2
exit 1
