package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type LeaderRoute string

const (
	LeaderContinueChat     LeaderRoute = "continue_chat"
	LeaderDelegateResearch LeaderRoute = "delegate_research"
)

var ErrInvalidLeaderRoute = errors.New("invalid Leader route")

type LeaderIntentReason string

const (
	LeaderReasonOrdinaryConversation                LeaderIntentReason = "ordinary_conversation"
	LeaderReasonExistingSourceWork                  LeaderIntentReason = "existing_source_work"
	LeaderReasonAmbiguousDiscoveryIntent            LeaderIntentReason = "ambiguous_discovery_intent"
	LeaderReasonExternalInformationWithoutDiscovery LeaderIntentReason = "external_information_without_discovery_request"
	LeaderReasonExplicitSourceDiscovery             LeaderIntentReason = "explicit_source_discovery"
)

type LeaderRouteDecision struct {
	Route      LeaderRoute        `json:"route"`
	ReasonCode LeaderIntentReason `json:"reason_code"`
}

type LeaderRouteRequest struct {
	Model       string
	UserMessage string
	RecentPairs []LeaderConversationPair
}

type LeaderConversationPair struct {
	User      string
	Assistant string
}

type LeaderRouter interface {
	DecideRoute(context.Context, LeaderRouteRequest) (LeaderRouteDecision, error)
}

type TracedLeaderRouter interface {
	DecideRouteTraced(context.Context, *agentobs.Tracer, LeaderRouteRequest, ModelTraceOptions) (LeaderRouteDecision, error)
}

type ResearchPlanRequest struct {
	Model       string
	UserMessage string
}

type ResearchPlanner interface {
	ExpandQueries(context.Context, ResearchPlanRequest) ([]string, error)
}

type TracedResearchPlanner interface {
	ExpandQueriesTraced(context.Context, *agentobs.Tracer, ResearchPlanRequest, ModelTraceOptions) ([]string, error)
}

type ModelLeaderRouter struct{ model DecisionModel }

func NewModelLeaderRouter(model DecisionModel) *ModelLeaderRouter {
	return &ModelLeaderRouter{model: model}
}

func (r *ModelLeaderRouter) DecideRoute(ctx context.Context, request LeaderRouteRequest) (LeaderRouteDecision, error) {
	return r.decideRoute(ctx, nil, request, ModelTraceOptions{})
}

func (r *ModelLeaderRouter) DecideRouteTraced(ctx context.Context, tracer *agentobs.Tracer, request LeaderRouteRequest, options ModelTraceOptions) (LeaderRouteDecision, error) {
	return r.decideRoute(ctx, tracer, request, options)
}

func (r *ModelLeaderRouter) decideRoute(ctx context.Context, tracer *agentobs.Tracer, request LeaderRouteRequest, options ModelTraceOptions) (LeaderRouteDecision, error) {
	if r == nil || r.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return LeaderRouteDecision{}, ErrInvalidLeaderRoute
	}
	modelRequest := models.ModelRequest{Model: request.Model, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: mustPromptContent("agent.leader-router", 1)},
		{Role: models.RoleUser, Content: buildLeaderRouteMessage(request.UserMessage, request.RecentPairs)},
	}, ActionDefinitions: []models.ActionDefinition{leaderRouteActionDefinition()}, RequiredActionName: "select_leader_route"}
	var outcome models.ModelOutcome
	var err error
	if tracer == nil {
		outcome, err = r.model.Decide(ctx, modelRequest)
	} else {
		outcome, err = InvokeDecisionModel(ctx, tracer, r.model, modelRequest, 1, options)
	}
	if err != nil {
		return LeaderRouteDecision{}, err
	}
	if outcome.Final != nil || outcome.Proposal == nil || len(outcome.Proposal.Actions) != 1 || outcome.Proposal.Actions[0].Name != "select_leader_route" {
		return LeaderRouteDecision{}, ErrInvalidLeaderRoute
	}
	var decision LeaderRouteDecision
	if err := decodeExactJSON(outcome.Proposal.Actions[0].Input, &decision); err != nil || !decision.Valid() {
		return LeaderRouteDecision{}, ErrInvalidLeaderRoute
	}
	return decision, nil
}

func buildLeaderRouteMessage(current string, pairs []LeaderConversationPair) string {
	internalPairs := make([]completedConversationPair, 0, len(pairs))
	for _, pair := range pairs {
		internalPairs = append(internalPairs, completedConversationPair{user: pair.User, assistant: pair.Assistant})
	}
	bounded := boundConversationPairs(internalPairs, 3, 4000, 1200)
	var message strings.Builder
	message.WriteString("RECENT COMPLETED CONTEXT (reference only):\n")
	if len(bounded) == 0 {
		message.WriteString("(none)\n")
	} else {
		for index, pair := range bounded {
			_, _ = fmt.Fprintf(&message, "Pair %d user: %s\nPair %d assistant: %s\n", index+1, pair.user, index+1, pair.assistant)
		}
	}
	message.WriteString("\nCURRENT MESSAGE (authoritative):\n")
	message.WriteString(truncateRunes(strings.TrimSpace(current), 4000))
	return message.String()
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

func (p *ModelResearchPlanner) expandQueries(ctx context.Context, tracer *agentobs.Tracer, request ResearchPlanRequest, options ModelTraceOptions) ([]string, error) {
	if p == nil || p.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return nil, ErrInvalidLeaderRoute
	}
	modelRequest := models.ModelRequest{Model: request.Model, Messages: []models.ModelMessage{
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

func (decision LeaderRouteDecision) Valid() bool {
	switch decision.Route {
	case LeaderDelegateResearch:
		return decision.ReasonCode == LeaderReasonExplicitSourceDiscovery
	case LeaderContinueChat:
		switch decision.ReasonCode {
		case LeaderReasonOrdinaryConversation, LeaderReasonExistingSourceWork, LeaderReasonAmbiguousDiscoveryIntent, LeaderReasonExternalInformationWithoutDiscovery:
			return true
		}
	}
	return false
}

func leaderRouteActionDefinition() models.ActionDefinition {
	return models.ActionDefinition{Name: "select_leader_route", Description: "Submit the requested Leader route and bounded intent reason.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["route","reason_code"],"properties":{"route":{"type":"string","enum":["continue_chat","delegate_research"]},"reason_code":{"type":"string","enum":["ordinary_conversation","existing_source_work","ambiguous_discovery_intent","external_information_without_discovery_request","explicit_source_discovery"]}}}`)}
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
