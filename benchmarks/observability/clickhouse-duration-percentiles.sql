SELECT agent_name, status, count(),
       quantileExact(0.50)(duration_nanoseconds),
       quantileExact(0.95)(duration_nanoseconds),
       quantileExact(0.99)(duration_nanoseconds)
FROM nano_storage_eval.obs_trace_summaries
WHERE duration_nanoseconds IS NOT NULL
GROUP BY agent_name, status
ORDER BY agent_name, status
FORMAT Null
