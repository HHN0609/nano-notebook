package agent

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// TerminalizeAttemptStateInTx applies a safe terminal disposition without
// exposing Role-owned payload semantics to the execution host or Job queue.
func TerminalizeAttemptStateInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, errorCode string) error {
	if tx == nil || attempt.JobID == "" || attempt.RunID == "" || attempt.LeaseToken == "" || attempt.AttemptNo < 1 ||
		!safeAttemptErrorCode.MatchString(errorCode) {
		return errors.New("invalid Attempt terminalization")
	}
	var runtimeKind string
	var role *AgentRole
	var executorIdentity *string
	if err := tx.QueryRow(ctx, `select runtime_kind,agent_role,executor_identity from agent_runs where id=$1`, attempt.RunID).Scan(&runtimeKind, &role, &executorIdentity); err != nil {
		return err
	}
	isResearch := (runtimeKind == "legacy_role" && role != nil && *role == RoleResearch) ||
		(runtimeKind == "configured" && executorIdentity != nil && *executorIdentity == "research")
	isChatLeader := (runtimeKind == "legacy_role" && role != nil && *role == RoleLeader) ||
		(runtimeKind == "configured" && executorIdentity != nil && *executorIdentity == "chat_leader")
	isStudio := runtimeKind == "configured" && executorIdentity != nil && *executorIdentity == "studio_structured_output"
	if isResearch {
		if err := FailResearchPayloadInTx(ctx, tx, attempt.RunID, errorCode); err != nil {
			return err
		}
		if err := (DelegationKernel{}).TerminalizeInTx(ctx, tx, attempt, DelegationFailed, errorCode); err != nil {
			return err
		}
	} else if isChatLeader || isStudio {
		jobTag, err := tx.Exec(ctx, `
			update agent_jobs set status='failed',lease_token=null,lease_expires_at=null,last_error_code=$4,
				finished_at=now(),updated_at=now()
			where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid
		`, attempt.JobID, attempt.RunID, attempt.LeaseToken, errorCode)
		if err != nil {
			return err
		}
		runTag, err := tx.Exec(ctx, `
			update agent_runs set status='failed',error_code=$2,finished_at=now(),updated_at=now()
			where id=$1 and status='running' and output_message_id is null
		`, attempt.RunID, errorCode)
		if err != nil {
			return err
		}
		if jobTag.RowsAffected() != 1 || runTag.RowsAffected() != 1 {
			return ErrLeaseLost
		}
		if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
			return err
		}
	} else {
		return errors.New("unsupported Agent Role terminalization")
	}
	_, err := tx.Exec(ctx, `update agent_jobs set last_error_code=$2 where id=$1`, attempt.JobID, errorCode)
	return err
}
