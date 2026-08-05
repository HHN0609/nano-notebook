package agenteval

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Observation is one DecisionCase's replay result.
type Observation struct {
	CaseID  string
	Pass    bool
	Skipped bool // true when the replayed decision proposed a multi-action batch — v1 cannot judge those
	Reason  string

	ActualActionName  string
	ActualActionInput []byte
}

// DecisionReplayExecutor replays DecisionCases against the CURRENT
// production model/catalog/registry. It performs no writes — no
// AppendCheckpoint, no PublishFinal — it is a read-only auditor/replayer,
// the same trust tier as internal/rageval's ProductRunExecutor, not its
// LiveProductExecutor.
type DecisionReplayExecutor struct {
	pool           *pgxpool.Pool
	base           *agent.PostgresRuntime
	mcpHost        *agent.MCPToolHost
	chatDefinition agentcatalog.Reference
	model          agent.DecisionModel
}

func NewDecisionReplayExecutor(pool *pgxpool.Pool, base *agent.PostgresRuntime, mcpHost *agent.MCPToolHost, chatDefinition agentcatalog.Reference, model agent.DecisionModel) *DecisionReplayExecutor {
	return &DecisionReplayExecutor{pool: pool, base: base, mcpHost: mcpHost, chatDefinition: chatDefinition, model: model}
}

// ExecuteCase reconstructs the exact context evalCase.RunID's decision
// evalCase.DecisionNo saw (via the same production code path
// Controller.Execute uses to build a live decision, not the lossy
// encrypted Replay system), replays it against the current model, and
// compares the resulting Action proposal to evalCase's hand-labeled
// expectation.
func (e *DecisionReplayExecutor) ExecuteCase(ctx context.Context, evalCase DecisionCase) (Observation, error) {
	runtime, definition, err := agent.ReplayControllerRuntime(ctx, e.base, e.chatDefinition, evalCase.RunID)
	if err != nil {
		return Observation{}, fmt.Errorf("resolve replay runtime for run %q: %w", evalCase.RunID, err)
	}

	execution, err := runtime.LoadForReplay(ctx, evalCase.RunID)
	if err != nil {
		return Observation{}, fmt.Errorf("load Execution for run %q: %w", evalCase.RunID, err)
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return Observation{}, err
	}
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		_ = tx.Rollback(ctx)
		return Observation{}, err
	}
	checkpoints, err := agent.LoadRunCheckpointsBefore(ctx, tx, evalCase.RunID, evalCase.DecisionNo)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Observation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Observation{}, err
	}

	prefix, err := agent.LoadCheckpointPrefix(ctx, checkpoints)
	if err != nil {
		return Observation{}, fmt.Errorf("reconstruct Checkpoint prefix before decision %d: %w", evalCase.DecisionNo, err)
	}

	attempt := agent.Attempt{RunID: evalCase.RunID, JobID: "evalreplay", AttemptNo: 1, LeaseToken: uuid.NewString()}
	remainingActions := execution.ActionLimit - prefix.AcceptedActions
	session, err := e.mcpHost.OpenAttempt(ctx, agent.AttemptToolScope{
		Definition: definition, Attempt: attempt, DefaultTimeZone: execution.TimeZone,
		RemainingActions: remainingActions,
	})
	if err != nil {
		return Observation{}, fmt.Errorf("open MCP Attempt session for run %q: %w", evalCase.RunID, err)
	}
	defer func() { _ = session.Close() }()

	definitions, err := session.ActionDefinitions(ctx, agent.ActionPolicy{RemainingActions: remainingActions, Execution: &execution})
	if err != nil {
		return Observation{}, fmt.Errorf("resolve Action Definitions for run %q: %w", evalCase.RunID, err)
	}

	request, err := runtime.BuildDecisionRequest(ctx, execution, prefix, definitions)
	if err != nil {
		return Observation{}, fmt.Errorf("build decision request before decision %d: %w", evalCase.DecisionNo, err)
	}
	request.InvocationPolicy = execution.ModelInvocation

	outcome, err := e.model.Decide(ctx, request)
	if err != nil {
		return Observation{}, fmt.Errorf("model Decide for run %q decision %d: %w", evalCase.RunID, evalCase.DecisionNo, err)
	}
	if err := outcome.Validate(); err != nil {
		return Observation{CaseID: evalCase.ID, Reason: "model returned an invalid decision: " + err.Error()}, nil
	}

	switch {
	case outcome.Final != nil:
		return Observation{CaseID: evalCase.ID, Reason: "model produced a Final Draft, expected an Action proposal"}, nil
	case len(outcome.Proposal.Actions) != 1:
		return Observation{CaseID: evalCase.ID, Skipped: true, Reason: fmt.Sprintf("model proposed %d Actions in one batch; v1 only judges single-Action decisions", len(outcome.Proposal.Actions))}, nil
	}

	actual := outcome.Proposal.Actions[0]
	result, err := Compare(evalCase, actual)
	if err != nil {
		return Observation{}, fmt.Errorf("compare replayed Action for case %q: %w", evalCase.ID, err)
	}
	return Observation{
		CaseID: evalCase.ID, Pass: result.Pass, Reason: result.Reason,
		ActualActionName: actual.Name, ActualActionInput: []byte(actual.Input),
	}, nil
}
