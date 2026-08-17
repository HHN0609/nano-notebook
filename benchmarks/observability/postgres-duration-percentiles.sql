SELECT agent_name, status, count(*),
       percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_nanoseconds),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_nanoseconds),
       percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_nanoseconds)
FROM obs_trace_summaries
WHERE duration_nanoseconds IS NOT NULL
GROUP BY agent_name, status
ORDER BY agent_name, status;
