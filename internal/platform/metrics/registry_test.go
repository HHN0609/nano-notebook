package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestValidateMetricNameRequiresNanoNamespace(t *testing.T) {
	if err := ValidateMetricName("task_terminal_total"); err == nil {
		t.Fatal("expected error for missing nano_ prefix")
	}
}

func TestValidateMetricNameRequiresBaseUnitSuffix(t *testing.T) {
	if err := ValidateMetricName("nano_task_terminal"); err == nil {
		t.Fatal("expected error for missing unit suffix")
	}
}

func TestValidateMetricNameAcceptsStandardSuffixes(t *testing.T) {
	for _, name := range []string{
		"nano_task_end_to_end_seconds",
		"nano_metrics_label_rejected_total",
		"nano_task_payload_bytes",
		"nano_runhub_subscribers",
		"nano_db_pool_connections",
	} {
		if err := ValidateMetricName(name); err != nil {
			t.Fatalf("expected %q to be valid, got %v", name, err)
		}
	}
}

func TestValidateMetricNameExemptsGoAndProcessCollectors(t *testing.T) {
	if err := ValidateMetricName("go_goroutines"); err != nil {
		t.Fatalf("go_ collectors must be exempt: %v", err)
	}
	if err := ValidateMetricName("process_resident_memory_bytes"); err != nil {
		t.Fatalf("process_ collectors must be exempt: %v", err)
	}
}

func TestRegistryRejectsInvalidMetricName(t *testing.T) {
	reg := NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "bad_name_total"})
	if err := reg.Register("bad_name_total", nil, counter); err == nil {
		t.Fatal("expected registration to fail for a non-namespaced name")
	}
}

func TestRegistryRejectsInvalidLabelName(t *testing.T) {
	reg := NewRegistry()
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nano_widgets_total"}, []string{"Bad-Label"})
	if err := reg.Register("nano_widgets_total", []string{"Bad-Label"}, vec); err == nil {
		t.Fatal("expected registration to fail for an invalid label name")
	}
}

func TestRegistryRejectsDuplicateCollector(t *testing.T) {
	reg := NewRegistry()
	first := prometheus.NewCounter(prometheus.CounterOpts{Name: "nano_widgets_total"})
	second := prometheus.NewCounter(prometheus.CounterOpts{Name: "nano_widgets_total"})
	if err := reg.Register("nano_widgets_total", nil, first); err != nil {
		t.Fatalf("first registration should succeed: %v", err)
	}
	if err := reg.Register("nano_widgets_total", nil, second); err == nil {
		t.Fatal("expected duplicate collector registration to fail")
	}
}

func TestMustRegisterPanicsOnInvalidName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected MustRegister to panic on an invalid name")
		}
	}()
	reg := NewRegistry()
	reg.MustRegister("not_namespaced_total", nil, prometheus.NewCounter(prometheus.CounterOpts{Name: "not_namespaced_total"}))
}

func TestNewRegistryExposesGoAndProcessCollectors(t *testing.T) {
	reg := NewRegistry()
	mfs, err := reg.Prometheus().Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	found := map[string]bool{}
	for _, mf := range mfs {
		found[mf.GetName()] = true
	}
	for _, name := range []string{LiveHeapBytesMetric, GoroutineCountMetric, HeapObjectsMetric, ProcessResidentMemoryMetric} {
		if !found[name] {
			t.Fatalf("expected %q to be exposed by the default registry, gathered %d families", name, len(mfs))
		}
	}
}
