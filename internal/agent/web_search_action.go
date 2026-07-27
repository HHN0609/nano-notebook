package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type webSearchAction struct {
	provider websearch.Provider
}

type webSearchInput struct {
	Queries []string `json:"queries"`
}

type webSearchOutput struct {
	Results []webSearchQueryResult `json:"results"`
}

type webSearchQueryResult struct {
	Query      string                   `json:"query"`
	Candidates []webSearchCandidateView `json:"candidates"`
}

type webSearchCandidateView struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	DisplayURL  string `json:"display_url,omitempty"`
	Description string `json:"description"`
	Rank        int    `json:"rank"`
}

func NewWebSearchAction(provider websearch.Provider) Action {
	return &webSearchAction{provider: provider}
}

func (*webSearchAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "web_search",
		Description: "Search the public web sequentially for one to three explicit bounded queries.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["queries"],"properties":{"queries":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","minLength":1,"maxLength":500}}}}`),
	}
}

func (a *webSearchAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeWebSearchInput(raw)
	return err
}

func (a *webSearchAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if a == nil || a.provider == nil {
		return ActionResult{}, websearch.ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	input, err := decodeWebSearchInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	output := webSearchOutput{Results: make([]webSearchQueryResult, 0, len(input.Queries))}
	for _, query := range input.Queries {
		candidates, err := a.provider.Search(ctx, websearch.Request{Query: query, Count: 10})
		if err != nil {
			return ActionResult{}, err
		}
		if len(candidates) > 10 {
			return ActionResult{}, websearch.ErrInvalidResponse
		}
		result := webSearchQueryResult{Query: query, Candidates: make([]webSearchCandidateView, 0, len(candidates))}
		for _, candidate := range candidates {
			result.Candidates = append(result.Candidates, webSearchCandidateView{
				Title: candidate.Title, URL: candidate.URL, DisplayURL: candidate.DisplayURL,
				Description: candidate.Description, Rank: candidate.Rank,
			})
		}
		output.Results = append(output.Results, result)
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func decodeWebSearchInput(raw json.RawMessage) (webSearchInput, error) {
	var input webSearchInput
	if err := decodeExactJSON(raw, &input); err != nil || len(input.Queries) < 1 || len(input.Queries) > 3 {
		return webSearchInput{}, errors.New("web_search requires one to three queries")
	}
	seen := make(map[string]bool, len(input.Queries))
	for _, query := range input.Queries {
		trimmed := strings.TrimSpace(query)
		key := strings.ToLower(trimmed)
		if query != trimmed || trimmed == "" || utf8.RuneCountInString(trimmed) > 500 || seen[key] {
			return webSearchInput{}, errors.New("web_search query is invalid or duplicated")
		}
		seen[key] = true
	}
	return input, nil
}
