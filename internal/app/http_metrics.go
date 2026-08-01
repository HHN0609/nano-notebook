package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

// withHTTPMetrics records nano_http_requests_total, nano_http_request_duration_seconds,
// and nano_http_inflight_requests for every ordinary request/response route
// (PRD criteria 48, 58). SSE streams call sseEventStreamed/finishSSEStream
// directly instead and are excluded here (PRD criterion 49) because a
// long-lived connection would otherwise destroy the request-duration
// histogram's distribution.
func (s *Server) withHTTPMetrics(next http.Handler) http.Handler {
	if s == nil || s.metrics == nil {
		return next
	}
	methodAllow := metrics.NewAllowlist("nano_http_requests_total", "method", metrics.HTTPMethodValues, s.metrics.LabelRejected)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSSERoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.metrics.HTTPInflightRequests.Inc()
		defer s.metrics.HTTPInflightRequests.Dec()
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		method := methodAllow.Value(r.Method)
		code := strconv.Itoa(recorder.status)
		s.metrics.HTTPRequests.WithLabelValues(route, method, code).Inc()
		s.metrics.HTTPRequestDuration.WithLabelValues(route, method, code).Observe(time.Since(started).Seconds())
	})
}

func isSSERoute(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch {
	case len(r.URL.Path) >= len("/events") && r.URL.Path[len(r.URL.Path)-len("/events"):] == "/events":
		return true
	default:
		return false
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
