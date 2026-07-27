package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type webSearchActionProvider struct {
	requests []websearch.Request
	err      error
}

func (p *webSearchActionProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	if p.err != nil {
		return nil, p.err
	}
	return []websearch.Candidate{{Title: "Title " + request.Query, URL: "https://example.com/" + request.Query, Description: "Snippet", Rank: 1}}, nil
}

func TestWebSearchActionExecutesOneBoundedOrderedBatch(t *testing.T) {
	provider := &webSearchActionProvider{}
	action := NewWebSearchAction(provider)
	input := json.RawMessage(`{"queries":["alpha","beta","gamma"]}`)
	if err := action.ValidateInput(input); err != nil {
		t.Fatal(err)
	}
	result, err := action.Execute(context.Background(), ActionRequest{ActionID: "decision:1/action:0", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || len(provider.requests) != 3 {
		t.Fatalf("result=%+v requests=%+v", result, provider.requests)
	}
	for index, query := range []string{"alpha", "beta", "gamma"} {
		if provider.requests[index].Query != query || provider.requests[index].Count != 10 {
			t.Fatalf("request %d=%+v", index, provider.requests[index])
		}
	}
	var output struct {
		Results []struct {
			Query      string                `json:"query"`
			Candidates []websearch.Candidate `json:"candidates"`
		} `json:"results"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil || len(output.Results) != 3 || output.Results[1].Query != "beta" {
		t.Fatalf("output=%s parsed=%+v err=%v", result.Output, output, err)
	}
}

func TestWebSearchActionRejectsUnboundedOrMutableInputShape(t *testing.T) {
	action := NewWebSearchAction(&webSearchActionProvider{})
	tooLong := strings.Repeat("界", 501)
	for _, input := range []string{
		`{}`, `{"queries":[]}`, `{"queries":["a","b","c","d"]}`,
		`{"queries":["same","SAME"]}`, `{"queries":[" "]}`,
		`{"queries":["a"],"count":20}`, `{"queries":["` + tooLong + `"]}`,
	} {
		if err := action.ValidateInput(json.RawMessage(input)); err == nil {
			t.Fatalf("accepted input=%s", input)
		}
	}
}

func TestWebSearchActionPreservesProviderFailureForToolClassification(t *testing.T) {
	provider := &webSearchActionProvider{err: websearch.ErrRateLimited}
	action := NewWebSearchAction(provider)
	_, err := action.Execute(context.Background(), ActionRequest{Input: json.RawMessage(`{"queries":["alpha"]}`)})
	if !errors.Is(err, websearch.ErrRateLimited) {
		t.Fatalf("err=%v", err)
	}
}

func TestResearchExecutorUsesOneMCPWebSearchActionAndKeepsAcceptedLegacyResult(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	provider := &webSearchActionProvider{}
	registry, err := NewMCPToolRegistry(MCPToolRegistration{Action: NewWebSearchAction(provider), Scheduling: agentcatalog.ToolOrderedSync})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	executor := &LeaderExecutor{researchToolHost: host, researchDefinition: agentcatalog.MustParseReference("research.source-discovery@1")}
	accepted := websearch.Candidate{Title: "Accepted", URL: "https://accepted.example", Rank: 1}
	groups, err := executor.searchResearchQueries(context.Background(),
		Attempt{RunID: "run", JobID: "job", AttemptNo: 2, LeaseToken: "lease"},
		leaderRunContext{TimeZone: "UTC"},
		ResearchProgress{Plan: []string{"alpha", "beta"}, Results: map[int][]websearch.Candidate{0: {accepted}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 || len(groups) != 2 || len(groups[0]) != 1 || groups[0][0] != accepted || groups[1][0].Title != "Title beta" {
		t.Fatalf("requests=%+v groups=%+v", provider.requests, groups)
	}
}
