package metrics

import "github.com/prometheus/client_golang/prometheus"

// OtherLabel is substituted for any label value outside its declared
// allowlist, per PRD criterion 12: a metric label domain that is open in
// code must be closed before it reaches a Prometheus label, because an
// open-ended label value is an unbounded-cardinality bug waiting to happen.
const OtherLabel = "other"

// Allowlist closes an open-ended label domain. Build one per label with the
// exact set of values the code is known to emit today; call Value at every
// emission site instead of writing the raw string into a label.
type Allowlist struct {
	metric   string
	label    string
	values   map[string]struct{}
	rejected *prometheus.CounterVec
}

// NewAllowlist builds a closed allowlist for one (metric, label) pair. Every
// rejection increments nano_metrics_label_rejected_total{metric,label},
// which criterion 13 treats as an instrumentation defect signal, not a
// normal operating condition.
func NewAllowlist(metric, label string, values []string, rejected *prometheus.CounterVec) *Allowlist {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return &Allowlist{metric: metric, label: label, values: set, rejected: rejected}
}

// Value returns v unchanged if it is in the allowlist, otherwise records a
// rejection and returns OtherLabel.
func (a *Allowlist) Value(v string) string {
	if a == nil {
		return OtherLabel
	}
	if _, ok := a.values[v]; ok {
		return v
	}
	if a.rejected != nil {
		a.rejected.WithLabelValues(a.metric, a.label).Inc()
	}
	return OtherLabel
}

// Contains reports whether v is a member of the allowlist without recording
// a rejection; used by tests that assert coverage rather than emit metrics.
func (a *Allowlist) Contains(v string) bool {
	if a == nil {
		return false
	}
	_, ok := a.values[v]
	return ok
}
