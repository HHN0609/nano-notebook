package agent

import (
	"context"
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

func TestModelLeaderRouterAcceptsOnlyExactClosedRoute(t *testing.T) {
	for _, route := range []LeaderRoute{LeaderContinueChat, LeaderDelegateResearch} {
		model := &leaderDecisionModel{outcome: models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: string(route)}}}}
		got, err := NewModelLeaderRouter(model).DecideRoute(context.Background(), LeaderRouteRequest{Model: "route-model", UserMessage: "help"})
		if err != nil || got != route || len(model.requests) != 1 || len(model.requests[0].Messages) != 2 {
			t.Fatalf("route=%q got=%q err=%v requests=%+v", route, got, err, model.requests)
		}
	}
	model := &leaderDecisionModel{outcome: models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "search_the_web"}}}}
	if _, err := NewModelLeaderRouter(model).DecideRoute(context.Background(), LeaderRouteRequest{Model: "route-model", UserMessage: "help"}); !errors.Is(err, ErrInvalidLeaderRoute) {
		t.Fatalf("invalid route error=%v", err)
	}
}

func TestModelResearchPlannerParsesAtMostThreeBoundedQueries(t *testing.T) {
	model := &leaderDecisionModel{outcome: models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "QUERY: film production workflow\nQUERY: screenplay cinematography editing\nQUERY: location scouting film budget\nQUERY: ignored"}}}}
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
}
