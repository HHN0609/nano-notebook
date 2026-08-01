package metrics

// Bucket boundaries are declared once per stage so that a stage's target
// SLO threshold (docs/sprint/SPRINT-12-PRD.md section 4.7) always falls
// exactly on a bucket edge (PRD criterion 29).

// QueueWaitBuckets includes 5 explicitly: the default Worker scanInterval
// (internal/worker/service.go) is 5s, and at low traffic the polling loop,
// not load, dominates queue-wait p95 (PRD criterion 31-32).
var QueueWaitBuckets = []float64{0.5, 1, 2, 3, 5, 8, 13, 21, 34, 60}

// FirstProgressBuckets targets the provisional 3s p95 SLO on its edge.
var FirstProgressBuckets = []float64{0.25, 0.5, 1, 1.5, 2, 3, 5, 8, 13, 21}

// ModelCallBuckets covers a single non-streaming model round trip.
var ModelCallBuckets = []float64{0.5, 1, 2, 3, 5, 8, 13, 21, 34, 60, 90}

// ToolExecutionBuckets targets the provisional 2s p95 SLO on its edge.
var ToolExecutionBuckets = []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 8, 13, 21}

// RetrievalStageBuckets covers one stage of the retrieval pipeline.
var RetrievalStageBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.8, 1.5, 3, 5}

// RetrievalSearchBuckets covers the whole Pipeline.Search call, targeting
// the provisional 800ms p95 SLO on its edge.
var RetrievalSearchBuckets = []float64{0.05, 0.1, 0.25, 0.5, 0.8, 1.5, 3, 5, 8}

// EndToEndBuckets extends past 210s, the default Worker run timeout
// (internal/worker/service.go NewServiceWithConcurrency), and targets the
// provisional 45s p95 / 120s p99 SLOs on their edges (PRD criterion 40).
var EndToEndBuckets = []float64{1, 3, 5, 10, 20, 30, 45, 60, 90, 120, 180, 210, 300}

// HTTPDurationBuckets covers ordinary request/response API latency; SSE
// streams are excluded from this histogram (PRD criterion 49) and use
// SSEConnectionDurationBuckets instead.
var HTTPDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// SSEConnectionDurationBuckets covers long-lived stream lifetimes rather
// than request latency.
var SSEConnectionDurationBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}

// AttemptCountBuckets is a histogram of Attempts consumed per terminal Run,
// with integer buckets so retry amplification is visible directly
// (PRD criterion 25).
var AttemptCountBuckets = []float64{1, 2, 3, 4, 5}
