package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type leaderDecisionModel struct {
	outcome  models.ModelOutcome
	err      error
	requests []models.ModelRequest
}

func TestMergeResearchCandidatesInterleavesQueriesAndPrefersDomainDiversity(t *testing.T) {
	groups := [][]websearch.Candidate{
		{}, {}, {},
	}
	for index := 0; index < 10; index++ {
		groups[0] = append(groups[0], websearch.Candidate{Title: "A", URL: "https://a.example/item/" + string(rune('a'+index)), DisplayURL: "a.example"})
		groups[1] = append(groups[1], websearch.Candidate{Title: "B", URL: "https://b.example/item/" + string(rune('a'+index)), DisplayURL: "b.example"})
		groups[2] = append(groups[2], websearch.Candidate{Title: "C", URL: "https://c.example/item/" + string(rune('a'+index)), DisplayURL: "c.example"})
	}
	merged := mergeResearchCandidates(groups)
	if len(merged) != 10 {
		t.Fatalf("merged=%d", len(merged))
	}
	seen := map[string]bool{}
	for _, candidate := range merged[:3] {
		seen[candidate.DisplayURL] = true
	}
	if !seen["a.example"] || !seen["b.example"] || !seen["c.example"] {
		t.Fatalf("first candidates do not cover query groups: %+v", merged[:3])
	}
}

func (m *leaderDecisionModel) Decide(_ context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
	m.requests = append(m.requests, request)
	return m.outcome, m.err
}

func TestModelLeaderRouterRequiresTypedClosedDecision(t *testing.T) {
	input := json.RawMessage(`{"route":"delegate_research","reason_code":"explicit_source_discovery"}`)
	model := &leaderDecisionModel{outcome: actionOutcome("select_leader_route", input)}
	got, err := NewModelLeaderRouter(model).DecideRoute(context.Background(), LeaderRouteRequest{Model: "route-model", UserMessage: "find sources"})
	if err != nil || got.Route != LeaderDelegateResearch || got.ReasonCode != LeaderReasonExplicitSourceDiscovery {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if len(model.requests) != 1 || model.requests[0].RequiredActionName != "select_leader_route" || len(model.requests[0].ActionDefinitions) != 1 {
		t.Fatalf("request=%+v", model.requests)
	}
}

func TestModelLeaderRouterFailsClosedOnInvalidMissingMixedOrInconsistentContract(t *testing.T) {
	tests := []models.ModelOutcome{
		{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "delegate_research"}}},
		actionOutcome("other", json.RawMessage(`{"route":"delegate_research","reason_code":"explicit_source_discovery"}`)),
		actionOutcome("select_leader_route", json.RawMessage(`{"route":"delegate_research","reason_code":"ordinary_conversation"}`)),
		actionOutcome("select_leader_route", json.RawMessage(`{"route":"continue_chat","reason_code":"unknown"}`)),
		actionOutcome("select_leader_route", json.RawMessage(`{"route":"continue_chat","reason_code":"ordinary_conversation"} trailing`)),
		{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "continue_chat"}, Proposal: actionOutcome("select_leader_route", json.RawMessage(`{"route":"continue_chat","reason_code":"ordinary_conversation"}`)).Proposal}},
	}
	for _, outcome := range tests {
		model := &leaderDecisionModel{outcome: outcome}
		if _, err := NewModelLeaderRouter(model).DecideRoute(context.Background(), LeaderRouteRequest{Model: "route-model", UserMessage: "help"}); !errors.Is(err, ErrInvalidLeaderRoute) {
			t.Fatalf("outcome=%+v err=%v", outcome, err)
		}
	}
}

func TestModelResearchPlannerRequiresOneTypedBoundedQueryBatch(t *testing.T) {
	model := &leaderDecisionModel{outcome: actionOutcome("submit_research_queries", json.RawMessage(`{"queries":["film production workflow","screenplay cinematography editing","location scouting film budget"]}`))}
	queries, err := NewModelResearchPlanner(model).ExpandQueries(context.Background(), ResearchPlanRequest{Model: "research-model", UserMessage: "collect film resources"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"film production workflow", "screenplay cinematography editing", "location scouting film budget"}
	if len(queries) != len(want) {
		t.Fatalf("queries=%v", queries)
	}
	for index := range want {
		if queries[index] != want[index] {
			t.Fatalf("queries=%v want=%v", queries, want)
		}
	}
	if len(model.requests) != 1 || model.requests[0].RequiredActionName != "submit_research_queries" || len(model.requests[0].ActionDefinitions) != 1 {
		t.Fatalf("request=%+v", model.requests)
	}
}

func TestModelResearchPlannerFailsClosedOnMalformedOrNonTypedPlan(t *testing.T) {
	tests := []models.ModelOutcome{
		{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "QUERY: old format"}}},
		actionOutcome("other", json.RawMessage(`{"queries":["valid"]}`)),
		actionOutcome("submit_research_queries", json.RawMessage(`{"queries":[]}`)),
		actionOutcome("submit_research_queries", json.RawMessage(`{"queries":["a","b","c","d"]}`)),
		actionOutcome("submit_research_queries", json.RawMessage(`{"queries":["same"," SAME "]}`)),
		actionOutcome("submit_research_queries", json.RawMessage(`{"queries":["valid"],"extra":true}`)),
	}
	for _, outcome := range tests {
		model := &leaderDecisionModel{outcome: outcome}
		if _, err := NewModelResearchPlanner(model).ExpandQueries(context.Background(), ResearchPlanRequest{Model: "research-model", UserMessage: "collect"}); !errors.Is(err, ErrInvalidLeaderRoute) {
			t.Fatalf("outcome=%+v err=%v", outcome, err)
		}
	}
}

func actionOutcome(name string, input json.RawMessage) models.ModelOutcome {
	return models.ModelOutcome{ModelDecision: models.ModelDecision{Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: name, Input: input}}}}}
}
