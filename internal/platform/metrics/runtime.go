package metrics

// The following metric names are the leak-detection surface the operator
// runbook depends on (docs/engineering/BACKEND_ENGINEERING.md). They come
// from client_golang's translation of Go's runtime/metrics package and are
// not guaranteed stable across Go or client_golang upgrades (PRD criterion
// 52) — RuntimeMetricNameSnapshotTest in runtime_test.go asserts they still
// exist so an upgrade that silently renames one fails CI instead of
// silently blinding the leak alerts.
const (
	// LiveHeapBytesMetric is the leak signal (PRD criterion 53): heap bytes
	// retained after a completed garbage collection. Deliberately not
	// process_resident_memory_bytes or go_memstats_heap_inuse_bytes, which
	// both reflect allocator retention and GC scheduling rather than an
	// actual leak.
	LiveHeapBytesMetric = "go_gc_heap_live_bytes"
	// GoroutineCountMetric is the count used for the goroutine-leak alert.
	GoroutineCountMetric = "go_goroutines"
	// HeapObjectsMetric is the live heap object count.
	HeapObjectsMetric = "go_gc_heap_objects_objects"
	// ProcessResidentMemoryMetric is process RSS, used for dashboards but
	// never as the leak-alert trigger.
	ProcessResidentMemoryMetric = "process_resident_memory_bytes"
)
