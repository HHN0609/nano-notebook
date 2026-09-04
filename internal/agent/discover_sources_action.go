package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type DiscoverSourcesRequest struct {
	RunID    string
	ActionID string
	UserID   string
	ChatID   string
	Queries  []string
}

type DiscoverSourcesResult struct {
	SessionID              string `json:"discovery_session_id"`
	Status                 string `json:"status"`
	NovelCandidateCount    int    `json:"novel_candidate_count"`
	ExistingCandidateCount int    `json:"existing_candidate_count"`
	ExistingSelectedCount  int    `json:"existing_selected_count"`
}

type DiscoverSourcesBackend interface {
	Discover(context.Context, DiscoverSourcesRequest) (DiscoverSourcesResult, error)
}

type discoverSourcesAction struct {
	backend  DiscoverSourcesBackend
	provider ResearchProviderAvailability
}

type discoverSourcesInput struct {
	Queries []string `json:"queries"`
}

func NewDiscoverSourcesAction(backend DiscoverSourcesBackend, provider ResearchProviderAvailability) Action {
	if provider == nil {
		provider = alwaysAvailableResearchProvider{}
	}
	return &discoverSourcesAction{backend: backend, provider: provider}
}

func (*discoverSourcesAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "discover_sources",
		Description: "Find complementary public Source candidates for the member to review and import. Returns only session status and aggregate counts, never answer evidence.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["queries"],"properties":{"queries":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","minLength":1,"maxLength":500}}}}`),
	}
}

func (a *discoverSourcesAction) Available(execution Execution) (bool, string) {
	if execution.MemberRole != "owner" && execution.MemberRole != "editor" {
		return false, string(LeaderPolicyMembershipDenied)
	}
	if a.provider != nil && !a.provider.ResearchAvailable() {
		return false, string(LeaderPolicyProviderUnavailable)
	}
	return true, ""
}

func (*discoverSourcesAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeDiscoverSourcesInput(raw)
	return err
}

func (a *discoverSourcesAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if a == nil || a.backend == nil {
		return ActionResult{Status: ActionDomainError, ErrorCode: "discovery_unavailable"}, nil
	}
	input, err := decodeDiscoverSourcesInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	result, err := a.backend.Discover(ctx, DiscoverSourcesRequest{
		RunID: request.Attempt.RunID, ActionID: request.ActionID, UserID: request.UserID,
		ChatID: request.ChatID, Queries: append([]string(nil), input.Queries...),
	})
	if err != nil {
		return ActionResult{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func decodeDiscoverSourcesInput(raw json.RawMessage) (discoverSourcesInput, error) {
	var input discoverSourcesInput
	if err := decodeExactJSON(raw, &input); err != nil || len(input.Queries) < 1 || len(input.Queries) > 3 {
		return discoverSourcesInput{}, errors.New("discover_sources requires one to three queries")
	}
	seen := make(map[string]bool, len(input.Queries))
	for _, query := range input.Queries {
		trimmed := strings.TrimSpace(query)
		key := strings.ToLower(trimmed)
		if query != trimmed || trimmed == "" || utf8.RuneCountInString(trimmed) > 500 || seen[key] {
			return discoverSourcesInput{}, errors.New("discover_sources query is invalid or duplicated")
		}
		seen[key] = true
	}
	return input, nil
}
