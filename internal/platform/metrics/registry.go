// Package metrics is the Prometheus operational metrics plane described in
// docs/sprint/SPRINT-12-PRD.md. It is a leaf package: it may depend only on
// the prometheus client, never on internal/agent, internal/models,
// internal/retrieval, internal/app, internal/jobs, internal/worker, or
// internal/collector. Those packages depend on this one, never the reverse,
// which keeps the Trace plane (internal/agentobs) and the Metrics plane
// structurally separate rather than separate by convention only.
package metrics

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Namespace is the mandatory prefix for every application metric.
const Namespace = "nano"

var (
	metricNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	labelNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	// unitSuffixes are the permitted terminal suffixes for a metric name,
	// per Prometheus naming conventions (criterion 5). A Counter's name
	// additionally must end in _total, checked separately.
	unitSuffixes = []string{"_seconds", "_bytes", "_total", "_ratio"}
	// gaugeSuffixExceptions covers bounded, unitless gauges (counts of
	// things) which Prometheus convention allows without a unit suffix.
	gaugeSuffixExceptions = regexp.MustCompile(`^nano_[a-z0-9_]*(_active|_records|_connections|_subscribers|_runs_tracked|_attempts|_goroutines|_requests|_rejected)$`)
)

// Registry wraps a prometheus.Registry and enforces the Sprint 12 naming
// rules at registration time: an invalid name or a duplicate collector fails
// immediately rather than being silently dropped (PRD criterion 9).
type Registry struct {
	inner *prometheus.Registry
}

// NewRegistry builds an empty Registry with the standard Go runtime and
// process collectors already registered (PRD criterion 51).
func NewRegistry() *Registry {
	r := &Registry{inner: prometheus.NewRegistry()}
	r.inner.MustRegister(
		collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(collectors.MetricsAll)),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return r
}

// Prometheus returns the underlying registry, for use by promhttp and by
// tests that need to enumerate registered collectors.
func (r *Registry) Prometheus() *prometheus.Registry {
	return r.inner
}

// Register validates name, checks every declared label name, and registers
// the collector. It returns an error instead of panicking so callers can
// decide whether a failure is fatal; NewCatalog (see catalog.go) treats any
// error here as a fatal startup error per PRD criterion 9.
func (r *Registry) Register(name string, labelNames []string, collector prometheus.Collector) error {
	if err := ValidateMetricName(name); err != nil {
		return fmt.Errorf("metric %q: %w", name, err)
	}
	for _, label := range labelNames {
		if !labelNamePattern.MatchString(label) {
			return fmt.Errorf("metric %q: invalid label name %q", name, label)
		}
	}
	if err := r.inner.Register(collector); err != nil {
		return fmt.Errorf("metric %q: %w", name, err)
	}
	return nil
}

// MustRegister is Register but fails process startup (panics) on error,
// which is the intended behavior for metric catalog construction at boot.
func (r *Registry) MustRegister(name string, labelNames []string, collector prometheus.Collector) {
	if err := r.Register(name, labelNames, collector); err != nil {
		panic(err)
	}
}

// ValidateMetricName enforces the nano_ namespace and a base-unit suffix
// (PRD criteria 4-5), except for the standard go_/process_ collectors which
// are exempt because their names are owned by client_golang, not Nano.
func ValidateMetricName(name string) error {
	if strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") {
		return nil
	}
	if !strings.HasPrefix(name, Namespace+"_") {
		return fmt.Errorf("metric name must start with %q", Namespace+"_")
	}
	if !metricNamePattern.MatchString(name) {
		return fmt.Errorf("metric name must match %s", metricNamePattern.String())
	}
	for _, suffix := range unitSuffixes {
		if strings.HasSuffix(name, suffix) {
			return nil
		}
	}
	if gaugeSuffixExceptions.MatchString(name) {
		return nil
	}
	return fmt.Errorf("metric name must end in a base unit (%s) or a recognized bounded-count suffix", strings.Join(unitSuffixes, ", "))
}
