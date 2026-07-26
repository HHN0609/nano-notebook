package agent

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

func TestResearchCheckpointEnvelopeHasStableRoleStepOrdinalIdentity(t *testing.T) {
	left, err := NewRoleCheckpoint(RoleResearch, ResearchStepQueryPlan, 0, ResearchQueryPlan{Queries: []string{"one", "two"}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRoleCheckpoint(RoleResearch, ResearchStepQueryPlan, 0, ResearchQueryPlan{Queries: []string{"one", "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if left.IdentityKey != "research/query_plan/0" || left.PayloadSHA256 != right.PayloadSHA256 || len(left.PayloadSHA256) != 64 {
		t.Fatalf("left=%+v right=%+v", left, right)
	}
}

func TestResearchProgressReturnsOnlyFirstMissingOrdinal(t *testing.T) {
	progress := ResearchProgress{
		Plan:    []string{"one", "two", "three"},
		Results: map[int][]websearch.Candidate{0: {{Title: "one", URL: "https://one.example"}}, 2: {{Title: "three", URL: "https://three.example"}}},
	}
	ordinal, query, ok := progress.FirstMissing()
	if !ok || ordinal != 1 || query != "two" {
		t.Fatalf("ordinal=%d query=%q ok=%t", ordinal, query, ok)
	}
	progress.Results[1] = []websearch.Candidate{}
	if _, _, ok := progress.FirstMissing(); ok {
		t.Fatal("empty but accepted result was treated as missing")
	}
}

func TestRoleCheckpointRejectsInvalidRoleStepAndOrdinal(t *testing.T) {
	for _, input := range []struct {
		role    AgentRole
		step    string
		ordinal int
	}{
		{RoleLeader, ResearchStepQueryPlan, 0},
		{RoleResearch, "unknown", 0},
		{RoleResearch, ResearchStepSearchResult, 3},
	} {
		if _, err := NewRoleCheckpoint(input.role, input.step, input.ordinal, map[string]any{}); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}
