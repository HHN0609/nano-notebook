package agent

import (
	"encoding/json"
	"testing"
)

func TestValidateResearchPlanJSONNormalizesScopeListFromModel(t *testing.T) {
	raw := `{
		"title":"Harness research",
		"objective":"Produce a decision report",
		"scope":["Current public implementations","DeepSeek, Claude Code, and Codex"],
		"research_questions":["How do their loops differ?"],
		"investigation_tracks":["Official source code"],
		"source_strategy":["Prefer current primary sources"],
		"analysis_method":["Compare shared dimensions"],
		"deliverable_outline":["Executive summary"],
		"completion_criteria":["Material claims are read-backed"],
		"clarifying_questions":[]
	}`

	canonical, err := ValidateResearchPlanJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal(canonical, &plan); err != nil {
		t.Fatal(err)
	}
	if got, ok := plan["scope"].(string); !ok || got != "Current public implementations\nDeepSeek, Claude Code, and Codex" {
		t.Fatalf("scope=%#v", plan["scope"])
	}
}
