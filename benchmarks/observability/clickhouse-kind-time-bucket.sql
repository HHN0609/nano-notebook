SELECT toStartOfMinute(occurred_at) AS minute, kind, count()
FROM nano_storage_eval.obs_trace_records_raw
GROUP BY minute, kind
ORDER BY minute, kind
FORMAT Null
