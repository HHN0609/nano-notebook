package agenteval

import (
	"encoding/json"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestCompareExactCanonicalJSONIgnoresKeyOrder(t *testing.T) {
	evalCase := DecisionCase{
		ExpectedActionName:  "search_evidence",
		ExpectedActionInput: json.RawMessage(`{"query":"launch date","purpose":"answer"}`),
	}
	actual := models.ActionProposal{
		Name:  "search_evidence",
		Input: json.RawMessage(`{"purpose":"answer","query":"launch date"}`),
	}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, reason = %q", result.Reason)
	}
}

func TestCompareExactCanonicalJSONDetectsValueDrift(t *testing.T) {
	evalCase := DecisionCase{
		ExpectedActionName:  "search_evidence",
		ExpectedActionInput: json.RawMessage(`{"query":"launch date"}`),
	}
	actual := models.ActionProposal{
		Name:  "search_evidence",
		Input: json.RawMessage(`{"query":"launch year"}`),
	}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pass {
		t.Fatal("Pass = true, want false for differing input")
	}
}

func TestCompareActionNameMismatchFailsBeforeInspectingInput(t *testing.T) {
	evalCase := DecisionCase{ExpectedActionName: "search_evidence", ExpectedActionInput: json.RawMessage(`{"query":"x"}`)}
	actual := models.ActionProposal{Name: "delegate_research_agent", Input: json.RawMessage(`not even json`)}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pass {
		t.Fatal("Pass = true, want false for mismatched Action name")
	}
}

func TestCompareOmittedInputTreatedAsEmptyObject(t *testing.T) {
	evalCase := DecisionCase{ExpectedActionName: "current_time"}
	actual := models.ActionProposal{Name: "current_time", Input: json.RawMessage(`{}`)}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, reason = %q", result.Reason)
	}
}

func TestCompareRequiredKeysSubsetIgnoresOtherFields(t *testing.T) {
	evalCase := DecisionCase{
		ExpectedActionName:  "delegate_research_agent",
		ExpectedActionInput: json.RawMessage(`{"target":"research-agent","objective":"anything"}`),
		ComparisonMode:      ComparisonRequiredKeysSubset,
		RequiredInputKeys:   []string{"target"},
	}
	actual := models.ActionProposal{
		Name:  "delegate_research_agent",
		Input: json.RawMessage(`{"target":"research-agent","objective":"a completely different phrasing"}`),
	}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, reason = %q", result.Reason)
	}
}

func TestCompareRequiredKeysSubsetDetectsDriftOnRequiredKey(t *testing.T) {
	evalCase := DecisionCase{
		ExpectedActionName:  "delegate_research_agent",
		ExpectedActionInput: json.RawMessage(`{"target":"research-agent"}`),
		ComparisonMode:      ComparisonRequiredKeysSubset,
		RequiredInputKeys:   []string{"target"},
	}
	actual := models.ActionProposal{
		Name:  "delegate_research_agent",
		Input: json.RawMessage(`{"target":"wrong-agent"}`),
	}
	result, err := Compare(evalCase, actual)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pass {
		t.Fatal("Pass = true, want false for drifted required key")
	}
}
