package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentExecutionHost owns Role/executor compatibility dispatch. It does not
// interpret Role outcomes or delegation payloads.
type AgentExecutionHost struct {
	pool               *pgxpool.Pool
	legacyRegistry     *RoleRegistry
	configuredRegistry *ExecutorRegistry
}

func NewAgentExecutionHost(pool *pgxpool.Pool, legacyRegistry *RoleRegistry, configuredRegistry *ExecutorRegistry) (*AgentExecutionHost, error) {
	if pool == nil || legacyRegistry == nil || configuredRegistry == nil {
		return nil, errors.New("Agent Execution Host dependencies are incomplete")
	}
	return &AgentExecutionHost{pool: pool, legacyRegistry: legacyRegistry, configuredRegistry: configuredRegistry}, nil
}

func (h *AgentExecutionHost) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if h == nil || h.pool == nil || h.legacyRegistry == nil || h.configuredRegistry == nil {
		return ClassifyAttempt(errors.New("Agent Execution Host is invalid"), context.Cause(ctx))
	}
	var runtimeKind string
	var definitionIdentity, definitionSHA256, executorIdentity *string
	var definitionVersion *int
	var policyIdentity, policySHA256, providerModel *string
	var policyVersion *int
	var role *AgentRole
	var executorVersion *string
	if err := h.pool.QueryRow(ctx, `
		select runtime_kind,definition_identity,definition_version,definition_sha256,executor_identity,
			model_policy_identity,model_policy_version,model_policy_sha256,provider_model,agent_role,executor_version
		from agent_runs where id=$1
	`, attempt.RunID).Scan(
		&runtimeKind, &definitionIdentity, &definitionVersion, &definitionSHA256, &executorIdentity,
		&policyIdentity, &policyVersion, &policySHA256, &providerModel, &role, &executorVersion,
	); err != nil {
		return ClassifyAttempt(err, context.Cause(ctx))
	}
	pinned := pinnedExecution{runtimeKind: runtimeKind}
	if runtimeKind == "configured" && definitionIdentity != nil && definitionVersion != nil && policyIdentity != nil && policyVersion != nil &&
		definitionSHA256 != nil && executorIdentity != nil && policySHA256 != nil && providerModel != nil {
		pinned.definition = agentcatalog.Reference{Identity: *definitionIdentity, Version: *definitionVersion}
		pinned.definitionSHA256 = *definitionSHA256
		pinned.executorIdentity = *executorIdentity
		pinned.modelPolicy = agentcatalog.Reference{Identity: *policyIdentity, Version: *policyVersion}
		pinned.modelPolicySHA256 = *policySHA256
		pinned.providerModel = *providerModel
	} else if runtimeKind == "legacy_role" && role != nil && executorVersion != nil {
		pinned.legacyRole = *role
		pinned.legacyExecutorVersion = *executorVersion
	}
	executor, err := h.resolvePinnedExecution(pinned)
	if err != nil {
		return ClassifyAttempt(err, context.Cause(ctx))
	}
	return executor.ExecuteAttempt(ctx, attempt)
}

type pinnedExecution struct {
	runtimeKind           string
	definition            agentcatalog.Reference
	definitionSHA256      string
	executorIdentity      string
	modelPolicy           agentcatalog.Reference
	modelPolicySHA256     string
	providerModel         string
	legacyRole            AgentRole
	legacyExecutorVersion string
}

func (h *AgentExecutionHost) resolvePinnedExecution(pinned pinnedExecution) (DefinitionExecutor, error) {
	if h == nil || h.legacyRegistry == nil || h.configuredRegistry == nil {
		return nil, errors.New("Agent Execution Host is invalid")
	}
	switch pinned.runtimeKind {
	case "configured":
		resolved, err := h.configuredRegistry.Resolve(pinned.definition)
		if err != nil {
			return nil, err
		}
		if resolved.Definition.SHA256 != pinned.definitionSHA256 || resolved.Definition.Executor != pinned.executorIdentity ||
			resolved.ModelPolicy.Reference() != pinned.modelPolicy || resolved.ModelPolicy.SHA256 != pinned.modelPolicySHA256 ||
			resolved.ModelPolicy.ProviderModel != pinned.providerModel {
			return nil, fmt.Errorf("configured Agent Run pin mismatch for %s", pinned.definition)
		}
		return resolved.Executor, nil
	case "legacy_role":
		return h.legacyRegistry.Resolve(pinned.legacyRole, pinned.legacyExecutorVersion)
	default:
		return nil, fmt.Errorf("unsupported Agent Run runtime kind %q", pinned.runtimeKind)
	}
}

type LeaderRoleExecutor struct{ runtime *LeaderExecutor }
type ResearchRoleExecutor struct{ runtime *LeaderExecutor }
type ChatLeaderDefinitionExecutor struct{ runtime *LeaderExecutor }
type ResearchDefinitionExecutor struct{ runtime *LeaderExecutor }
type ResearchPlanningDefinitionExecutor struct{ controller *Controller }
type ResearchRootDefinitionExecutor struct{ controller *Controller }

func NewLeaderRoleExecutor(runtime *LeaderExecutor) *LeaderRoleExecutor {
	return &LeaderRoleExecutor{runtime: runtime}
}

func NewResearchRoleExecutor(runtime *LeaderExecutor) *ResearchRoleExecutor {
	return &ResearchRoleExecutor{runtime: runtime}
}

func NewChatLeaderDefinitionExecutor(runtime *LeaderExecutor) *ChatLeaderDefinitionExecutor {
	return &ChatLeaderDefinitionExecutor{runtime: runtime}
}

func NewResearchDefinitionExecutor(runtime *LeaderExecutor) *ResearchDefinitionExecutor {
	return &ResearchDefinitionExecutor{runtime: runtime}
}

func NewResearchPlanningDefinitionExecutor(controller *Controller) *ResearchPlanningDefinitionExecutor {
	return &ResearchPlanningDefinitionExecutor{controller: controller}
}

func NewResearchRootDefinitionExecutor(controller *Controller) *ResearchRootDefinitionExecutor {
	return &ResearchRootDefinitionExecutor{controller: controller}
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

func (e *ChatLeaderDefinitionExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.runtime == nil {
		return ClassifyAttempt(errors.New("Chat Leader Definition Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.runtime.executeExpectedDefinition(ctx, attempt, "chat_leader"), context.Cause(ctx))
}

func (e *ResearchDefinitionExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.runtime == nil {
		return ClassifyAttempt(errors.New("Research Definition Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.runtime.executeExpectedDefinition(ctx, attempt, "research"), context.Cause(ctx))
}

func (e *ResearchPlanningDefinitionExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.controller == nil {
		return ClassifyAttempt(errors.New("Research Planning Definition Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.controller.Execute(ctx, attempt), context.Cause(ctx))
}

func (e *ResearchRootDefinitionExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.controller == nil {
		return ClassifyAttempt(errors.New("Research Root Definition Executor is invalid"), context.Cause(ctx))
	}
	return ClassifyAttempt(e.controller.Execute(ctx, attempt), context.Cause(ctx))
}
