package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

var ErrInvalidLeaderRoute = errors.New("invalid Leader route")

type ResearchPlanRequest struct {
	Model            string
	UserMessage      string
	InvocationPolicy models.ModelInvocationPolicy
}

type ResearchPlanner interface {
	ExpandQueries(context.Context, ResearchPlanRequest) ([]string, error)
}

type ConfiguredResearchPlanner interface {
	PlanWebSearch(context.Context, ResearchPlanRequest) (models.ActionProposal, error)
}

type TracedConfiguredResearchPlanner interface {
	PlanWebSearchTraced(context.Context, *agentobs.Tracer, ResearchPlanRequest, ModelTraceOptions) (models.ActionProposal, error)
}

type TracedResearchPlanner interface {
	ExpandQueriesTraced(context.Context, *agentobs.Tracer, ResearchPlanRequest, ModelTraceOptions) ([]string, error)
}

type ModelResearchPlanner struct{ model DecisionModel }

func NewModelResearchPlanner(model DecisionModel) *ModelResearchPlanner {
	return &ModelResearchPlanner{model: model}
}

func (p *ModelResearchPlanner) ExpandQueries(ctx context.Context, request ResearchPlanRequest) ([]string, error) {
	return p.expandQueries(ctx, nil, request, ModelTraceOptions{})
}

func (p *ModelResearchPlanner) ExpandQueriesTraced(ctx context.Context, tracer *agentobs.Tracer, request ResearchPlanRequest, options ModelTraceOptions) ([]string, error) {
	return p.expandQueries(ctx, tracer, request, options)
}

func (p *ModelResearchPlanner) PlanWebSearch(ctx context.Context, request ResearchPlanRequest) (models.ActionProposal, error) {
	return p.planWebSearch(ctx, nil, request, ModelTraceOptions{})
}

func (p *ModelResearchPlanner) PlanWebSearchTraced(ctx context.Context, tracer *agentobs.Tracer, request ResearchPlanRequest, options ModelTraceOptions) (models.ActionProposal, error) {
	return p.planWebSearch(ctx, tracer, request, options)
}

func (p *ModelResearchPlanner) planWebSearch(ctx context.Context, tracer *agentobs.Tracer, request ResearchPlanRequest, options ModelTraceOptions) (models.ActionProposal, error) {
	if p == nil || p.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return models.ActionProposal{}, ErrInvalidLeaderRoute
	}
	definition := (&webSearchAction{}).Definition()
	modelRequest := models.ModelRequest{Model: request.Model, InvocationPolicy: request.InvocationPolicy, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: mustPromptContent("agent.research-planner", 1)},
		{Role: models.RoleUser, Content: request.UserMessage},
	}, ActionDefinitions: []models.ActionDefinition{definition}, RequiredActionName: definition.Name}
	var outcome models.ModelOutcome
	var err error
	if tracer == nil {
		outcome, err = p.model.Decide(ctx, modelRequest)
	} else {
		outcome, err = InvokeDecisionModel(ctx, tracer, p.model, modelRequest, 1, options)
	}
	if err != nil {
		return models.ActionProposal{}, err
	}
	if outcome.Final != nil || outcome.Proposal == nil || len(outcome.Proposal.Actions) != 1 || outcome.Proposal.Actions[0].Name != definition.Name {
		return models.ActionProposal{}, ErrInvalidLeaderRoute
	}
	proposal := outcome.Proposal.Actions[0]
	if _, err := decodeWebSearchInput(proposal.Input); err != nil {
		return models.ActionProposal{}, ErrInvalidLeaderRoute
	}
	return proposal, nil
}

func (p *ModelResearchPlanner) expandQueries(ctx context.Context, tracer *agentobs.Tracer, request ResearchPlanRequest, options ModelTraceOptions) ([]string, error) {
	if p == nil || p.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return nil, ErrInvalidLeaderRoute
	}
	modelRequest := models.ModelRequest{Model: request.Model, InvocationPolicy: request.InvocationPolicy, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: mustPromptContent("agent.research-planner", 1)},
		{Role: models.RoleUser, Content: request.UserMessage},
	}, ActionDefinitions: []models.ActionDefinition{researchQueriesActionDefinition()}, RequiredActionName: "submit_research_queries"}
	var outcome models.ModelOutcome
	var err error
	if tracer == nil {
		outcome, err = p.model.Decide(ctx, modelRequest)
	} else {
		outcome, err = InvokeDecisionModel(ctx, tracer, p.model, modelRequest, 1, options)
	}
	if err != nil {
		return nil, err
	}
	if outcome.Final != nil || outcome.Proposal == nil || len(outcome.Proposal.Actions) != 1 || outcome.Proposal.Actions[0].Name != "submit_research_queries" {
		return nil, ErrInvalidLeaderRoute
	}
	var submitted struct {
		Queries []string `json:"queries"`
	}
	if err := decodeExactJSON(outcome.Proposal.Actions[0].Input, &submitted); err != nil || len(submitted.Queries) < 1 || len(submitted.Queries) > 3 {
		return nil, ErrInvalidLeaderRoute
	}
	queries := make([]string, 0, len(submitted.Queries))
	seen := make(map[string]struct{}, 3)
	for _, value := range submitted.Queries {
		query := strings.TrimSpace(value)
		if query == "" || utf8.RuneCountInString(query) > 500 {
			return nil, ErrInvalidLeaderRoute
		}
		key := strings.ToLower(query)
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrInvalidLeaderRoute
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	if len(queries) == 0 {
		return nil, ErrInvalidLeaderRoute
	}
	return queries, nil
}

func researchQueriesActionDefinition() models.ActionDefinition {
	return models.ActionDefinition{Name: "submit_research_queries", Description: "Submit one to three bounded Web Search queries.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["queries"],"properties":{"queries":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"string","minLength":1,"maxLength":500}}}}`)}
}

func decodeExactJSON(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("multiple JSON values")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values")
	}
	return nil
}
