SELECT date_trunc('minute', occurred_at) AS minute, kind, count(*)
FROM obs_trace_records
GROUP BY minute, kind
ORDER BY minute, kind;
