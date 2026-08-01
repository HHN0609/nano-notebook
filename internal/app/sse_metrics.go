package app

import (
	"sync"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

// sseMetricsScope tracks one SSE connection's lifecycle metrics
// (PRD criteria 49, 58): nano_sse_connections_active while open,
// nano_sse_connection_duration_seconds and its close_reason once it ends,
// and nano_sse_events_sent_total per event written. finish is safe to call
// more than once — every streamRun/streamSourceDiscovery/streamNotebookSources
// return path calls it explicitly and a deferred call also guards the paths
// that don't.
type sseMetricsScope struct {
	catalog           *metrics.Catalog
	stream            string
	startedAt         time.Time
	closeReasons      *metrics.Allowlist
	once              sync.Once
	firstProgressOnce sync.Once
}

func newSSEMetricsScope(catalog *metrics.Catalog, stream string) *sseMetricsScope {
	scope := &sseMetricsScope{catalog: catalog, stream: stream, startedAt: time.Now()}
	if catalog != nil {
		scope.closeReasons = metrics.NewAllowlist("nano_sse_connection_duration_seconds", "close_reason", metrics.SSECloseReasonValues, catalog.LabelRejected)
		catalog.SSEConnectionsActive.WithLabelValues(scope.streamLabel()).Inc()
	}
	return scope
}

func (s *sseMetricsScope) streamLabel() string {
	streams := map[string]struct{}{}
	for _, v := range metrics.SSEStreamValues {
		streams[v] = struct{}{}
	}
	if _, ok := streams[s.stream]; ok {
		return s.stream
	}
	return metrics.OtherLabel
}

func (s *sseMetricsScope) eventSent(event string) {
	if s == nil || s.catalog == nil {
		return
	}
	s.catalog.SSEEventsSent.WithLabelValues(s.streamLabel(), event).Inc()
}

// firstProgress observes nano_chat_first_progress_seconds exactly once per
// connection, at the first event written after subscribe. It measures
// connect-to-first-byte rather than the PRD's literal admission-to-first-
// event wording (docs/sprint/SPRINT-12-PRD.md criterion 33): admission time
// is not currently threaded into the stream handler without an added
// lookup, so this is documented as a known scope reduction in
// SPRINT-12-ACCEPTANCE.md rather than silently redefined.
func (s *sseMetricsScope) recordFirstProgress(recorder *agent.TaskMetricsRecorder, taskVariant string, connectedAt time.Time) {
	if s == nil || s.catalog == nil || recorder == nil {
		return
	}
	s.firstProgressOnce.Do(func() {
		s.catalog.ChatFirstProgress.WithLabelValues(taskVariant).Observe(time.Since(connectedAt).Seconds())
	})
}

func (s *sseMetricsScope) finish(closeReason string) {
	if s == nil || s.catalog == nil {
		return
	}
	s.once.Do(func() {
		s.catalog.SSEConnectionsActive.WithLabelValues(s.streamLabel()).Dec()
		s.catalog.SSEConnectionDuration.WithLabelValues(s.streamLabel(), s.closeReasons.Value(closeReason)).Observe(time.Since(s.startedAt).Seconds())
	})
}
