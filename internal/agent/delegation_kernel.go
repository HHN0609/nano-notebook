package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type DelegationState string

const (
	DelegationWaiting   DelegationState = "waiting"
	DelegationSucceeded DelegationState = "succeeded"
	DelegationFailed    DelegationState = "failed"
	DelegationCancelled DelegationState = "cancelled"
)

type DelegationLifecycle struct {
	State     DelegationState
	ErrorCode string
	Consumed  bool
}

func (d *DelegationLifecycle) Terminalize(state DelegationState, errorCode string) error {
	if d == nil || d.State != DelegationWaiting || (state != DelegationSucceeded && state != DelegationFailed && state != DelegationCancelled) {
		return errors.New("invalid delegation terminal transition")
	}
	errorCode = strings.TrimSpace(errorCode)
	if state == DelegationSucceeded && errorCode != "" || state != DelegationSucceeded && errorCode == "" {
		return errors.New("invalid delegation terminal shape")
	}
	d.State = state
	d.ErrorCode = errorCode
	return nil
}

func (d *DelegationLifecycle) Consume() error {
	if d == nil || d.State == DelegationWaiting || d.Consumed {
		return errors.New("delegation is not consumable")
	}
	d.Consumed = true
	return nil
}

type DelegationKernel struct{}

type CreateDelegationCommand struct {
	ParentAttempt      Attempt
	ChildRunID         string
	ChildJobID         string
	ChildRole          AgentRole
	ChildPromptVersion string
}

func (DelegationKernel) CreateInTx(ctx context.Context, tx pgx.Tx, command CreateDelegationCommand) error {
	if tx == nil || command.ChildRole != RoleResearch || command.ChildRunID == "" || command.ChildJobID == "" || command.ChildPromptVersion == "" {
		return errors.New("invalid delegation creation command")
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_runs(
			id,user_id,chat_id,input_message_id,status,model,prompt_version,agent_config_id,executor_version,time_zone,
			deadline_at,action_decision_limit,final_decision_limit,action_limit,action_batch_limit,
			action_result_byte_limit,action_results_byte_limit,agent_role
		)
		select $2,r.user_id,r.chat_id,r.input_message_id,'queued',profile.model,$4,r.agent_config_id,
			profile.executor_version,r.time_zone,r.deadline_at,
			coalesce((profile.run_config->>'action_decision_limit')::integer,1),
			coalesce((profile.run_config->>'final_decision_limit')::integer,1),
			coalesce((profile.run_config->>'action_limit')::integer,3),
			coalesce((profile.run_config->>'action_batch_limit')::integer,1),
			coalesce((profile.run_config->>'action_result_byte_limit')::integer,16384),
			coalesce((profile.run_config->>'action_results_byte_limit')::integer,65536),$3
		from agent_runs r
		join agent_role_profiles profile on profile.configuration_set_id=r.agent_config_id and profile.role=$3
		where r.id=$1 and r.agent_role='leader' and r.status='running' and r.deadline_at>now()
	`, command.ParentAttempt.RunID, command.ChildRunID, command.ChildRole, command.ChildPromptVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into agent_jobs(id,kind,run_id,status) values($1,'agent_run',$2,'queued')`, command.ChildJobID, command.ChildRunID); err != nil {
		return err
	}
	delegationID := "dlg_" + command.ChildRunID
	if _, err := tx.Exec(ctx, `
		insert into agent_run_delegations(
			id,parent_run_id,child_run_id,parent_role,child_role,child_ordinal,depth,state
		) values($1,$2,$3,'leader','research',0,1,'waiting')
	`, delegationID, command.ParentAttempt.RunID, command.ChildRunID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		update agent_jobs set status='waiting',lease_token=null,lease_expires_at=null,updated_at=now()
		where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid
	`, command.ParentAttempt.JobID, command.ParentAttempt.RunID, command.ParentAttempt.LeaseToken)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_jobs',$1)`, command.ChildJobID); err != nil {
		return err
	}
	return nil
}

func (DelegationKernel) TerminalizeInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, state DelegationState, errorCode string) error {
	lifecycle := DelegationLifecycle{State: DelegationWaiting}
	if err := lifecycle.Terminalize(state, errorCode); err != nil {
		return err
	}
	runStatus, jobStatus := "failed", "failed"
	if state == DelegationSucceeded {
		runStatus, jobStatus = "completed", "succeeded"
	} else if state == DelegationCancelled {
		runStatus, jobStatus = "cancelled", "cancelled"
	}
	runTag, err := tx.Exec(ctx, `
		update agent_runs set status=$2,error_code=nullif($3,''),finished_at=now(),updated_at=now()
		where id=$1 and agent_role='research' and status='running'
	`, attempt.RunID, runStatus, errorCode)
	if err != nil {
		return err
	}
	jobTag, err := tx.Exec(ctx, `
		update agent_jobs set status=$2,lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now()
		where id=$1 and run_id=$3 and status='running' and lease_token=$4::uuid
	`, attempt.JobID, jobStatus, attempt.RunID, attempt.LeaseToken)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 || jobTag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	delegationTag, err := tx.Exec(ctx, `
		update agent_run_delegations set state=$2,error_code=nullif($3,''),completed_at=now(),updated_at=now()
		where child_run_id=$1 and state='waiting'
	`, attempt.RunID, state, errorCode)
	if err != nil {
		return err
	}
	if delegationTag.RowsAffected() != 1 {
		return errors.New("delegation terminal handoff lost authority")
	}
	if _, err := tx.Exec(ctx, `
		update agent_runs parent set status='queued',updated_at=now()
		from agent_run_delegations d
		where d.child_run_id=$1 and parent.id=d.parent_run_id and parent.status='running' and parent.deadline_at>now()
	`, attempt.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update agent_jobs parent_job set status='queued',updated_at=now()
		from agent_run_delegations d join agent_runs parent on parent.id=d.parent_run_id
		where d.child_run_id=$1 and parent_job.run_id=d.parent_run_id and parent_job.status='waiting'
		  and parent.status='queued' and parent.deadline_at>now()
	`, attempt.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		select pg_notify('nano_agent_jobs',parent_job.id)
		from agent_run_delegations d join agent_jobs parent_job on parent_job.run_id=d.parent_run_id
		where d.child_run_id=$1 and parent_job.status='queued'
	`, attempt.RunID); err != nil {
		return err
	}
	return nil
}

func (DelegationKernel) ConsumeInTx(ctx context.Context, tx pgx.Tx, parentRunID string, terminal DelegationState) error {
	if terminal != DelegationSucceeded && terminal != DelegationFailed && terminal != DelegationCancelled {
		return errors.New("invalid delegation consumption state")
	}
	tag, err := tx.Exec(ctx, `
		update agent_run_delegations set consumed_at=now(),updated_at=now()
		where parent_run_id=$1 and state=$2 and consumed_at is null
	`, parentRunID, terminal)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("delegation %s is not consumable", parentRunID)
	}
	return nil
}

func (DelegationKernel) CancelTreeInTx(ctx context.Context, tx pgx.Tx, parentRunID, errorCode string, at time.Time) error {
	if parentRunID == "" || strings.TrimSpace(errorCode) == "" || at.IsZero() {
		return errors.New("invalid delegation cancellation")
	}
	_, err := tx.Exec(ctx, `
		with cancelled as (
			update agent_run_delegations set state='cancelled',error_code=$2,completed_at=$3,updated_at=$3
			where parent_run_id=$1 and state='waiting' returning child_run_id
		), cancelled_runs as (
			update agent_runs set status='cancelled',error_code=$2,finished_at=$3,updated_at=$3
			where id in (select child_run_id from cancelled) and status in ('queued','running') returning id
		)
		update agent_jobs set status='cancelled',lease_token=null,lease_expires_at=null,finished_at=$3,updated_at=$3
		where run_id in (select id from cancelled_runs) and status in ('queued','running')
	`, parentRunID, errorCode, at)
	return err
}
