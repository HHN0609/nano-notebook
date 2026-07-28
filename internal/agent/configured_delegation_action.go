package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type configuredDelegationAction struct {
	pool       *pgxpool.Pool
	catalog    agentcatalog.Catalog
	child      agentcatalog.Definition
	definition models.ActionDefinition
	validator  *jsonschema.Resolved
}

// NewConfiguredDelegationToolRegistrations derives every callable child tool
// from the immutable catalog. Parent scoping remains an MCP Host concern, so a
// model can never supply or discover an arbitrary child identity.
func NewConfiguredDelegationToolRegistrations(catalog agentcatalog.Catalog, pool *pgxpool.Pool) ([]MCPToolRegistration, error) {
	if pool == nil {
		return nil, errors.New("configured Delegation Tool store is nil")
	}
	children := make(map[agentcatalog.Reference]bool)
	for _, parent := range catalog.Definitions() {
		for _, child := range parent.Children {
			children[child] = true
		}
	}
	registrations := make([]MCPToolRegistration, 0, len(children))
	for _, definition := range catalog.Definitions() {
		if !children[definition.Reference()] {
			continue
		}
		if definition.Delegation == nil || strings.TrimSpace(definition.Delegation.Description) == "" {
			return nil, fmt.Errorf("configured child %s has no delegation metadata", definition.Reference())
		}
		contract, ok := catalog.ResolveContract(definition.Contracts.Input)
		if !ok {
			return nil, fmt.Errorf("configured child %s has no input Contract", definition.Reference())
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(contract.Schema, &schema); err != nil {
			return nil, fmt.Errorf("configured child %s input schema: %w", definition.Reference(), err)
		}
		validator, err := schema.Resolve(nil)
		if err != nil {
			return nil, fmt.Errorf("configured child %s input schema: %w", definition.Reference(), err)
		}
		name, err := agentcatalog.DelegationToolName(definition.Reference())
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, MCPToolRegistration{
			Action: &configuredDelegationAction{
				pool: pool, catalog: catalog, child: definition, validator: validator,
				definition: models.ActionDefinition{
					Name: name, Description: strings.TrimSpace(definition.Delegation.Description),
					InputSchema: append(json.RawMessage(nil), contract.Schema...),
				},
			},
			Scheduling: agentcatalog.ToolExclusiveDelegation,
		})
	}
	return registrations, nil
}

func (a *configuredDelegationAction) Definition() models.ActionDefinition {
	if a == nil {
		return models.ActionDefinition{}
	}
	definition := a.definition
	definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	return definition
}

// Configured delegation is selected by the owning Executor's reviewed control
// flow. The scheduling receipt must never be offered as an ordinary composer
// result that a model could consume and continue past.
func (*configuredDelegationAction) Available(Execution) bool { return false }

func (a *configuredDelegationAction) ValidateInput(payload json.RawMessage) error {
	if a == nil || a.validator == nil {
		return errors.New("configured Delegation Tool is invalid")
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("configured Delegation input must be an object")
	}
	return a.validator.Validate(value)
}

func (a *configuredDelegationAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if a == nil || a.pool == nil || request.Attempt.RunID == "" || request.Attempt.JobID == "" || request.Attempt.LeaseToken == "" ||
		!stableActionIDPattern.MatchString(request.ActionID) || request.Definition.Identity == "" || len(request.DefinitionSHA256) != 64 {
		return ActionResult{}, errors.New("invalid configured Delegation request")
	}
	if err := a.ValidateInput(request.Input); err != nil {
		return ActionResult{}, err
	}
	delegationID, err := a.schedule(ctx, request)
	if err != nil {
		return ActionResult{}, err
	}
	payload, err := json.Marshal(struct {
		DelegationID string `json:"delegation_id"`
		State        string `json:"state"`
	}{DelegationID: delegationID, State: string(DelegationWaiting)})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func (a *configuredDelegationAction) schedule(ctx context.Context, request ActionRequest) (string, error) {
	runtime := &PostgresRuntime{pool: a.pool}
	tx, err := runtime.workerTx(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockCheckpointAuthority(ctx, tx, request.Attempt); err != nil {
		return "", err
	}

	childReference := a.child.Reference()
	childPolicy, ok := a.catalog.ResolveModelPolicy(a.child.ModelPolicy)
	if !ok {
		return "", errors.New("configured child Model Policy is missing")
	}
	var treeID, timeZone string
	var relationship, authorized bool
	if err := tx.QueryRow(ctx, `
		select parent.tree_id,coalesce(parent.parent_context_manifest->>'time_zone','UTC'),
			definition.children @> jsonb_build_array($5::text),
			exists(
				select 1 from chat_runs product
				join chat_chats chat on chat.id=product.chat_id
				join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=product.user_id
				where product.root_agent_run_id=tree.root_agent_run_id and member.role in ('owner','editor')
			)
		from agent_runs parent
		join agent_trees tree on tree.id=parent.tree_id
		join agent_definition_versions definition
		  on definition.definition_identity=parent.definition_identity
		 and definition.definition_version=parent.definition_version
		 and definition.canonical_sha256=parent.definition_sha256
		where parent.id=$1 and parent.runtime_kind='configured' and parent.status='running'
		  and parent.definition_identity=$2 and parent.definition_version=$3 and parent.definition_sha256=$4
	`, request.Attempt.RunID, request.Definition.Identity, request.Definition.Version, request.DefinitionSHA256, childReference.String()).Scan(
		&treeID, &timeZone, &relationship, &authorized,
	); err != nil {
		return "", err
	}
	if !relationship || !authorized {
		return "", errors.New("configured child relationship is not authorized")
	}

	var existingID, existingChild string
	err = tx.QueryRow(ctx, `
		select id,child_run_id from agent_run_delegations
		where parent_run_id=$1 and action_id=$2
	`, request.Attempt.RunID, request.ActionID).Scan(&existingID, &existingChild)
	if err == nil {
		var matches bool
		if err := tx.QueryRow(ctx, `
			select definition_identity=$2 and definition_version=$3
			from agent_runs where id=$1
		`, existingChild, childReference.Identity, childReference.Version).Scan(&matches); err != nil {
			return "", err
		}
		if !matches {
			return "", errors.New("configured Delegation action identity conflict")
		}
		return existingID, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	encodedManifest, err := json.Marshal(struct {
		Input    json.RawMessage `json:"input"`
		TimeZone string          `json:"time_zone"`
	}{Input: append(json.RawMessage(nil), request.Input...), TimeZone: timeZone})
	if err != nil {
		return "", err
	}
	manifest, err := canonicalJSONObject(encodedManifest)
	if err != nil || len(manifest) > a.child.Limits.ContextBytes {
		return "", errors.New("configured child context exceeds its Contract")
	}
	if err := chargeConfiguredTreeInTx(ctx, tx, request.Attempt.RunID, treeBudgetCharge{ContextBytes: len(manifest)}); err != nil {
		return "", err
	}
	childRunID := "run_" + uuid.NewString()
	childJobID := "job_" + uuid.NewString()
	delegationID := "dlg_" + uuid.NewString()
	if _, err := tx.Exec(ctx, `
		insert into agent_runs(
			id,status,runtime_kind,tree_id,definition_identity,definition_version,definition_sha256,
			executor_identity,model_policy_identity,model_policy_version,model_policy_sha256,provider_model,parent_context_manifest
		) values($1,'queued','configured',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)
	`, childRunID, treeID, a.child.Identity, a.child.Version, a.child.SHA256, a.child.Executor,
		childPolicy.Identity, childPolicy.Version, childPolicy.SHA256, childPolicy.ProviderModel, string(manifest)); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into agent_jobs(id,kind,run_id,status) values($1,'agent_run',$2,'queued')`, childJobID, childRunID); err != nil {
		return "", err
	}
	if err := StartRunTraceInTx(ctx, tx, childRunID, childPolicy.ProviderModel, childReference.String(), nil); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_run_delegations(id,parent_run_id,child_run_id,child_ordinal,depth,state,action_id)
		values($1,$2,$3,0,1,'waiting',$4)
	`, delegationID, request.Attempt.RunID, childRunID, request.ActionID); err != nil {
		return "", err
	}

	proposal, err := NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: a.definition.Name, Input: append(json.RawMessage(nil), request.Input...),
	}}})
	if err != nil {
		return "", err
	}
	checkpoint := Checkpoint{SequenceNo: 1, PendingCheckpoint: proposal}
	if err := tx.QueryRow(ctx, `
		insert into agent_run_checkpoints(
			run_id,sequence_no,identity_key,kind,decision_no,payload_version,payload,payload_sha256
		) values($1,1,$2,$3,$4,$5,$6::jsonb,$7)
		returning created_at
	`, request.Attempt.RunID, proposal.IdentityKey, string(proposal.Kind), proposal.DecisionNo,
		proposal.PayloadVersion, []byte(proposal.Payload), proposal.PayloadSHA256).Scan(&checkpoint.CreatedAt); err != nil {
		return "", err
	}
	if err := RecordCheckpointAcceptedInTx(ctx, tx, request.Attempt, checkpoint); err != nil {
		return "", err
	}
	waitingTag, err := tx.Exec(ctx, `
		update agent_jobs set status='waiting',lease_token=null,lease_expires_at=null,updated_at=now()
		where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid
	`, request.Attempt.JobID, request.Attempt.RunID, request.Attempt.LeaseToken)
	if err != nil {
		return "", err
	}
	if waitingTag.RowsAffected() != 1 {
		return "", ErrLeaseLost
	}
	if err := RecordDelegationCreatedInTx(ctx, tx, request.Attempt.RunID, childRunID); err != nil {
		return "", err
	}
	if err := RecordAttemptWaitingInTx(ctx, tx, request.Attempt.RunID, request.Attempt.JobID, request.Attempt.AttemptNo); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_jobs',$1)`, childJobID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, request.Attempt.RunID); err != nil {
		return "", err
	}
	return delegationID, tx.Commit(ctx)
}
