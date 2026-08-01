package metrics

import "github.com/prometheus/client_golang/prometheus"

// ReservedUnimplementedMetrics names metrics the catalog deliberately does
// not register. nano_model_first_token_seconds (PRD criterion 34) is
// reserved here: true token-level time-to-first-token does not exist in
// Nano today because internal/models.BifrostClient.Decide is a
// non-streaming request/response call. When streaming model support lands,
// implement this name rather than inventing a new one.
var ReservedUnimplementedMetrics = []string{"nano_model_first_token_seconds"}

// Catalog is the closed set of every Nano application metric. It is built
// once per process from a Registry; every domain package that emits a
// metric takes a *Catalog (or a narrower interface over it) rather than
// constructing its own collectors, so the metric surface stays centrally
// reviewable and the naming/label rules in registry.go apply uniformly.
type Catalog struct {
	// Task success (PRD section 4.3 / 6.1)
	TaskTerminal     *prometheus.CounterVec
	AgentAttempt     *prometheus.CounterVec
	AgentRunAttempts *prometheus.HistogramVec
	AgentRunDegraded *prometheus.CounterVec

	// Staged latency (PRD section 4.4 / 6.2)
	TaskQueueWait     *prometheus.HistogramVec
	ChatFirstProgress *prometheus.HistogramVec
	ModelCall         *prometheus.HistogramVec
	ToolExecution     *prometheus.HistogramVec
	RetrievalStage    *prometheus.HistogramVec
	RetrievalSearch   *prometheus.HistogramVec
	TaskEndToEnd      *prometheus.HistogramVec

	// Errors and HTTP/SSE edge (PRD section 4.5 / 6.3)
	ErrorTotal            *prometheus.CounterVec
	RetrievalDegraded     *prometheus.CounterVec
	HTTPRequests          *prometheus.CounterVec
	HTTPRequestDuration   *prometheus.HistogramVec
	SSEConnectionDuration *prometheus.HistogramVec
	SSEEventsSent         *prometheus.CounterVec
	LabelRejected         *prometheus.CounterVec

	// Runtime and leak surfaces (PRD section 4.6 / 6.4)
	RunHubSubscribers           prometheus.Gauge
	RunHubRunsTracked           prometheus.Gauge
	SSEConnectionsActive        *prometheus.GaugeVec
	HTTPInflightRequests        prometheus.Gauge
	WorkerInflightAttempts      prometheus.Gauge
	WorkerHeartbeatGoroutines   prometheus.Gauge
	CollectorMemoryStoreRecords prometheus.Gauge
	DBPoolConnections           *prometheus.GaugeVec
}

// NewCatalog constructs and registers every metric against reg. It panics
// (fails process startup) if any name or label is invalid, or if the same
// collector is registered twice, per PRD criterion 9 — a metrics catalog
// defect must be caught at boot, not discovered later as missing data.
func NewCatalog(reg *Registry) *Catalog {
	c := &Catalog{}

	c.LabelRejected = mustCounterVec(reg, "nano_metrics_label_rejected_total",
		"Rejections of an out-of-allowlist label value, substituted with \"other\".",
		[]string{"metric", "label"})

	c.TaskTerminal = mustCounterVec(reg, "nano_task_terminal_total",
		"Terminal Task outcomes.", []string{"task_kind", "task_variant", "outcome"})
	c.AgentAttempt = mustCounterVec(reg, "nano_agent_attempt_total",
		"Agent Attempt resolutions by disposition.", []string{"task_variant", "disposition"})
	c.AgentRunAttempts = mustHistogramVec(reg, "nano_agent_run_attempts",
		"Attempts consumed per terminal Run.", []string{"task_variant"}, AttemptCountBuckets)
	c.AgentRunDegraded = mustCounterVec(reg, "nano_agent_run_degraded_total",
		"Runs that reached completed while retrieval was degraded.", []string{"degradation"})

	c.TaskQueueWait = mustHistogramVec(reg, "nano_task_queue_wait_seconds",
		"Time from Job available_at to Worker claim.", []string{"task_kind", "task_variant"}, QueueWaitBuckets)
	c.ChatFirstProgress = mustHistogramVec(reg, "nano_chat_first_progress_seconds",
		"Time from admission to the first SSE run event.", []string{"task_variant"}, FirstProgressBuckets)
	c.ModelCall = mustHistogramVec(reg, "nano_model_call_seconds",
		"Single model round-trip latency.", []string{"model", "result_kind", "outcome"}, ModelCallBuckets)
	c.ToolExecution = mustHistogramVec(reg, "nano_tool_execution_seconds",
		"MCP tool invocation latency at the tool plane boundary.", []string{"tool", "outcome"}, ToolExecutionBuckets)
	c.RetrievalStage = mustHistogramVec(reg, "nano_retrieval_stage_seconds",
		"Per-stage retrieval pipeline latency.", []string{"stage", "outcome"}, RetrievalStageBuckets)
	c.RetrievalSearch = mustHistogramVec(reg, "nano_retrieval_search_seconds",
		"Whole Pipeline.Search call latency.", []string{"outcome"}, RetrievalSearchBuckets)
	c.TaskEndToEnd = mustHistogramVec(reg, "nano_task_end_to_end_seconds",
		"Time from admission to terminal state.", []string{"task_kind", "task_variant", "outcome"}, EndToEndBuckets)

	c.ErrorTotal = mustCounterVec(reg, "nano_error_total",
		"Typed failures by architectural layer and error code.", []string{"task_kind", "layer", "error_code"})
	c.RetrievalDegraded = mustCounterVec(reg, "nano_retrieval_degraded_total",
		"Declared retrieval degradations.", []string{"degradation"})
	c.HTTPRequests = mustCounterVec(reg, "nano_http_requests_total",
		"HTTP requests by route template.", []string{"route", "method", "code"})
	c.HTTPRequestDuration = mustHistogramVec(reg, "nano_http_request_duration_seconds",
		"HTTP request latency by route template; excludes SSE streams.", []string{"route", "method", "code"}, HTTPDurationBuckets)
	c.SSEConnectionDuration = mustHistogramVec(reg, "nano_sse_connection_duration_seconds",
		"SSE stream connection lifetime.", []string{"stream", "close_reason"}, SSEConnectionDurationBuckets)
	c.SSEEventsSent = mustCounterVec(reg, "nano_sse_events_sent_total",
		"SSE events sent by stream and event type.", []string{"stream", "event"})

	c.RunHubSubscribers = mustGauge(reg, "nano_runhub_subscribers",
		"Open SSE run-hub subscriber channels across all Runs.")
	c.RunHubRunsTracked = mustGauge(reg, "nano_runhub_runs_tracked",
		"Distinct Run IDs with at least one run-hub subscriber.")
	c.SSEConnectionsActive = mustGaugeVec(reg, "nano_sse_connections_active",
		"Currently open SSE stream connections.", []string{"stream"})
	c.HTTPInflightRequests = mustGauge(reg, "nano_http_inflight_requests",
		"HTTP requests currently being handled.")
	c.WorkerInflightAttempts = mustGauge(reg, "nano_worker_inflight_attempts",
		"Worker Attempt execution goroutines currently running.")
	c.WorkerHeartbeatGoroutines = mustGauge(reg, "nano_worker_heartbeat_goroutines",
		"Worker per-claim lease heartbeat goroutines currently running.")
	c.CollectorMemoryStoreRecords = mustGauge(reg, "nano_collector_memory_store_records",
		"Records held by the Collector in-memory Trace store, when used.")
	c.DBPoolConnections = mustGaugeVec(reg, "nano_db_pool_connections",
		"pgx pool connection counts by state.", []string{"pool", "state"})

	return c
}

func mustCounterVec(reg *Registry, name, help string, labels []string) *prometheus.CounterVec {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	reg.MustRegister(name, labels, v)
	return v
}

func mustHistogramVec(reg *Registry, name, help string, labels []string, buckets []float64) *prometheus.HistogramVec {
	v := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	reg.MustRegister(name, labels, v)
	return v
}

func mustGauge(reg *Registry, name, help string) prometheus.Gauge {
	v := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	reg.MustRegister(name, nil, v)
	return v
}

func mustGaugeVec(reg *Registry, name, help string, labels []string) *prometheus.GaugeVec {
	v := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	reg.MustRegister(name, labels, v)
	return v
}
