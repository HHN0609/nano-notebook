package agent

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentExecutionHost owns Role/executor compatibility dispatch. It does not
// interpret Role outcomes or delegation payloads.
type AgentExecutionHost struct {
	pool     *pgxpool.Pool
	registry *RoleRegistry
}

func NewAgentExecutionHost(pool *pgxpool.Pool, registry *RoleRegistry) (*AgentExecutionHost, error) {
	if pool == nil || registry == nil {
		return nil, errors.New("Agent Execution Host dependencies are incomplete")
	}
	return &AgentExecutionHost{pool: pool, registry: registry}, nil
}

func (h *AgentExecutionHost) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if h == nil || h.pool == nil || h.registry == nil {
		return ClassifyAttempt(errors.New("Agent Execution Host is invalid"), context.Cause(ctx))
	}
	var role AgentRole
	var executorVersion string
	if err := h.pool.QueryRow(ctx, `
		select agent_role,executor_version from agent_runs where id=$1
	`, attempt.RunID).Scan(&role, &executorVersion); err != nil {
		return ClassifyAttempt(err, context.Cause(ctx))
	}
	executor, err := h.registry.Resolve(role, executorVersion)
	if err != nil {
		return ClassifyAttempt(err, context.Cause(ctx))
	}
	return executor.ExecuteAttempt(ctx, attempt)
}

type LeaderRoleExecutor struct{ runtime *LeaderExecutor }
type ResearchRoleExecutor struct{ runtime *LeaderExecutor }

func NewLeaderRoleExecutor(runtime *LeaderExecutor) *LeaderRoleExecutor {
	return &LeaderRoleExecutor{runtime: runtime}
}

func NewResearchRoleExecutor(runtime *LeaderExecutor) *ResearchRoleExecutor {
	return &ResearchRoleExecutor{runtime: runtime}
}

func (e *LeaderRoleExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.runtime == nil {
		return ClassifyAttempt(errors.New("Leader Role Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.runtime.executeExpectedRole(ctx, attempt, RoleLeader), context.Cause(ctx))
}

func (e *ResearchRoleExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.runtime == nil {
		return ClassifyAttempt(errors.New("Research Role Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.runtime.executeExpectedRole(ctx, attempt, RoleResearch), context.Cause(ctx))
}
