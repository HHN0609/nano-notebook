package observability

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// alertRuleFile mirrors the small subset of Prometheus rule file structure
// this lint test needs — just enough to walk every alert and check its
// annotations.
type alertRuleFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// TestEveryAlertHasARunbookLink enforces PRD criterion 69: an alert without
// a runbook annotation fails this test rather than shipping silently.
func TestEveryAlertHasARunbookLink(t *testing.T) {
	path := filepath.Join("rules", "alert_rules.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file alertRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	total := 0
	for _, group := range file.Groups {
		for _, rule := range group.Rules {
			if rule.Alert == "" {
				continue
			}
			total++
			runbook := rule.Annotations["runbook"]
			if runbook == "" {
				t.Errorf("alert %q in group %q has no runbook annotation", rule.Alert, group.Name)
			}
			if rule.Annotations["summary"] == "" {
				t.Errorf("alert %q in group %q has no summary annotation", rule.Alert, group.Name)
			}
		}
	}
	if total == 0 {
		t.Fatal("no alerts found — alert_rules.yml parsing likely broke")
	}
	t.Logf("checked %d alerts for a runbook link", total)
}
