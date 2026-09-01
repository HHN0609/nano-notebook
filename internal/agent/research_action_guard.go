package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// researchDeduplicatingAction prevents a long Research Run from spending its
// external-call budget on an identical accepted tool input. The current
// proposal is already checkpointed when Execute runs, so two occurrences mean
// an earlier complete Agent Step used the same tool and canonical input.
type researchDeduplicatingAction struct {
	pool   *pgxpool.Pool
	action Action
}

func NewResearchDeduplicatingAction(pool *pgxpool.Pool, action Action) Action {
	if pool == nil || action == nil {
		return action
	}
	return &researchDeduplicatingAction{pool: pool, action: action}
}

func (a *researchDeduplicatingAction) Definition() models.ActionDefinition {
	return a.action.Definition()
}

func (a *researchDeduplicatingAction) ValidateInput(raw json.RawMessage) error {
	return a.action.ValidateInput(raw)
}

func (a *researchDeduplicatingAction) CrashReplaySafe() bool {
	policy, ok := a.action.(CrashReplayPolicy)
	return ok && policy.CrashReplaySafe()
}

func (a *researchDeduplicatingAction) CacheLongToolResults(definition agentcatalog.Reference) bool {
	policy, ok := a.action.(ToolResultCacheEligibility)
	return ok && policy.CacheLongToolResults(definition)
}

func (a *researchDeduplicatingAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	var executorIdentity *string
	if err := a.pool.QueryRow(ctx, `select executor_identity from agent_runs where id=$1`, request.Attempt.RunID).Scan(&executorIdentity); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return a.action.Execute(ctx, request)
		}
		return ActionResult{}, err
	}
	if executorIdentity == nil || *executorIdentity != "research_root" {
		return a.action.Execute(ctx, request)
	}
	rows, err := a.pool.Query(ctx, `select payload from agent_run_checkpoints where run_id=$1 and kind='action_proposal' order by sequence_no`, request.Attempt.RunID)
	if err != nil {
		return ActionResult{}, err
	}
	defer rows.Close()
	payloads := make([][]byte, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return ActionResult{}, err
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		return ActionResult{}, err
	}
	if hasRepeatedResearchAction(payloads, a.action.Definition().Name, request.Input) {
		return ActionResult{Status: ActionDomainError, ErrorCode: "research_duplicate_action"}, nil
	}
	return a.action.Execute(ctx, request)
}

func hasRepeatedResearchAction(payloads [][]byte, name string, input json.RawMessage) bool {
	want, err := CanonicalJSONObject(input)
	if err != nil {
		return false
	}
	matches := 0
	for _, raw := range payloads {
		var proposal proposalCheckpointPayload
		if json.Unmarshal(raw, &proposal) != nil {
			continue
		}
		for _, action := range proposal.Actions {
			if action.Name != name {
				continue
			}
			got, err := CanonicalJSONObject(action.Input)
			if err == nil && bytes.Equal(got, want) {
				matches++
				if matches > 1 {
					return true
				}
			}
		}
	}
	return false
}
