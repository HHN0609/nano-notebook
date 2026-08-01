package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestAllowlistReturnsKnownValueUnchanged(t *testing.T) {
	rejected := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nano_test_rejected_total"}, []string{"metric", "label"})
	allow := NewAllowlist("nano_test_total", "outcome", []string{"completed", "failed"}, rejected)
	if got := allow.Value("completed"); got != "completed" {
		t.Fatalf("expected known value passed through unchanged, got %q", got)
	}
}

func TestAllowlistRejectsUnknownValueAndIncrementsCounter(t *testing.T) {
	rejected := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nano_test_rejected_total"}, []string{"metric", "label"})
	allow := NewAllowlist("nano_test_total", "outcome", []string{"completed", "failed"}, rejected)
	if got := allow.Value("something_new"); got != OtherLabel {
		t.Fatalf("expected unknown value to map to %q, got %q", OtherLabel, got)
	}
	metric := &dto.Metric{}
	if err := rejected.WithLabelValues("nano_test_total", "outcome").Write(metric); err != nil {
		t.Fatalf("write rejected counter: %v", err)
	}
	if metric.GetCounter().GetValue() != 1 {
		t.Fatalf("expected rejection counter to be 1, got %v", metric.GetCounter().GetValue())
	}
}

func TestAllowlistNilIsSafeAndAlwaysRejects(t *testing.T) {
	var allow *Allowlist
	if got := allow.Value("anything"); got != OtherLabel {
		t.Fatalf("nil allowlist must fail closed to %q, got %q", OtherLabel, got)
	}
	if allow.Contains("anything") {
		t.Fatal("nil allowlist must never claim to contain a value")
	}
}

func TestAllowlistContainsDoesNotRecordRejection(t *testing.T) {
	rejected := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nano_test_rejected_total"}, []string{"metric", "label"})
	allow := NewAllowlist("nano_test_total", "outcome", []string{"completed"}, rejected)
	if allow.Contains("unknown") {
		t.Fatal("expected Contains to report false for an unknown value")
	}
	metric := &dto.Metric{}
	if err := rejected.WithLabelValues("nano_test_total", "outcome").Write(metric); err != nil {
		t.Fatalf("write rejected counter: %v", err)
	}
	if metric.GetCounter().GetValue() != 0 {
		t.Fatal("Contains must not increment the rejected counter")
	}
}
