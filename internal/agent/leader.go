package agent

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type LeaderRoute string

const (
	LeaderContinueChat     LeaderRoute = "continue_chat"
	LeaderDelegateResearch LeaderRoute = "delegate_research"
)

var ErrInvalidLeaderRoute = errors.New("invalid Leader route")

type LeaderRouteRequest struct {
	Model       string
	UserMessage string
}

type LeaderRouter interface {
	DecideRoute(context.Context, LeaderRouteRequest) (LeaderRoute, error)
}

type ResearchPlanRequest struct {
	Model       string
	UserMessage string
}

type ResearchPlanner interface {
	ExpandQueries(context.Context, ResearchPlanRequest) ([]string, error)
}

type ModelLeaderRouter struct{ model DecisionModel }

func NewModelLeaderRouter(model DecisionModel) *ModelLeaderRouter {
	return &ModelLeaderRouter{model: model}
}

func (r *ModelLeaderRouter) DecideRoute(ctx context.Context, request LeaderRouteRequest) (LeaderRoute, error) {
	if r == nil || r.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return "", ErrInvalidLeaderRoute
	}
	outcome, err := r.model.Decide(ctx, models.ModelRequest{Model: request.Model, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: `You are Nano Notebook's Leader router. Return exactly continue_chat unless the user clearly asks to search, find, collect, research, or add new external source material. Return exactly delegate_research only for that explicit discovery intent. Never infer web access merely because current sources may be insufficient. Output one token and nothing else.`},
		{Role: models.RoleUser, Content: request.UserMessage},
	}})
	if err != nil || outcome.Final == nil || outcome.Proposal != nil {
		if err != nil {
			return "", err
		}
		return "", ErrInvalidLeaderRoute
	}
	route := LeaderRoute(strings.TrimSpace(outcome.Final.Text))
	if route != LeaderContinueChat && route != LeaderDelegateResearch {
		return "", ErrInvalidLeaderRoute
	}
	return route, nil
}

type ModelResearchPlanner struct{ model DecisionModel }

func NewModelResearchPlanner(model DecisionModel) *ModelResearchPlanner {
	return &ModelResearchPlanner{model: model}
}

func (p *ModelResearchPlanner) ExpandQueries(ctx context.Context, request ResearchPlanRequest) ([]string, error) {
	if p == nil || p.model == nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.UserMessage) == "" {
		return nil, ErrInvalidLeaderRoute
	}
	outcome, err := p.model.Decide(ctx, models.ModelRequest{Model: request.Model, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: `Expand the user's explicit source-discovery request into one to three useful web-search queries. Each output line must begin exactly with QUERY: followed by one query. Preserve the user's language and intent. Do not answer the request, include URLs, or output any other text.`},
		{Role: models.RoleUser, Content: request.UserMessage},
	}})
	if err != nil {
		return nil, err
	}
	if outcome.Final == nil || outcome.Proposal != nil {
		return nil, ErrInvalidLeaderRoute
	}
	queries := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, line := range strings.Split(outcome.Final.Text, "\n") {
		if len(queries) == 3 {
			break
		}
		if !strings.HasPrefix(line, "QUERY:") {
			continue
		}
		query := strings.TrimSpace(strings.TrimPrefix(line, "QUERY:"))
		if query == "" || utf8.RuneCountInString(query) > 500 {
			continue
		}
		key := strings.ToLower(query)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	if len(queries) == 0 {
		return nil, ErrInvalidLeaderRoute
	}
	return queries, nil
}
