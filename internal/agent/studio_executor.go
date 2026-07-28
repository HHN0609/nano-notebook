package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/studio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StudioDefinitionExecutor struct {
	pool        *pgxpool.Pool
	controllers map[agentcatalog.Reference]*Controller
}

func NewStudioDefinitionExecutor(
	pool *pgxpool.Pool,
	base *PostgresRuntime,
	model DecisionModel,
	registry *ActionRegistry,
	host *MCPToolHost,
	catalog agentcatalog.Catalog,
) (*StudioDefinitionExecutor, error) {
	if pool == nil || base == nil || model == nil || registry == nil || host == nil {
		return nil, errors.New("Studio Executor dependencies are incomplete")
	}
	runtime := &studioRuntime{pool: pool, base: base, catalog: catalog}
	executor := &StudioDefinitionExecutor{pool: pool, controllers: make(map[agentcatalog.Reference]*Controller, 4)}
	for _, reference := range []string{"studio.report@1", "studio.flashcards@1", "studio.mind-map@1", "studio.data-table@1"} {
		parsed := agentcatalog.MustParseReference(reference)
		definition, ok := catalog.ResolveDefinition(parsed)
		if !ok || definition.Executor != "studio_structured_output" {
			return nil, fmt.Errorf("Studio Definition %s is unavailable", parsed)
		}
		executor.controllers[parsed] = NewMCPController(runtime, model, registry, host, parsed)
	}
	return executor, nil
}

func (e *StudioDefinitionExecutor) ExecuteAttempt(ctx context.Context, attempt Attempt) AttemptResolution {
	if e == nil || e.pool == nil {
		return ClassifyAttempt(errors.New("Studio Executor is invalid"), context.Cause(ctx))
	}
	var identity string
	var version int
	if err := e.pool.QueryRow(ctx, `select definition_identity,definition_version from agent_runs where id=$1 and runtime_kind='configured'`, attempt.RunID).Scan(&identity, &version); err != nil {
		return ClassifyAttempt(err, context.Cause(ctx))
	}
	controller := e.controllers[agentcatalog.Reference{Identity: identity, Version: version}]
	if controller == nil {
		return AttemptResolution{Disposition: AttemptTerminal, ErrorCode: "studio_definition_invalid"}
	}
	return ClassifyAttempt(controller.Execute(ctx, attempt), context.Cause(ctx))
}

type studioRuntime struct {
	pool    *pgxpool.Pool
	base    *PostgresRuntime
	catalog agentcatalog.Catalog
}

func (r *studioRuntime) Load(ctx context.Context, attempt Attempt) (Execution, error) {
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer tx.Rollback(ctx)
	var execution Execution
	var temperature float64
	var maxOutputTokens, timeoutMS int
	var deadlineValid, authorized bool
	err = tx.QueryRow(ctx, `
		select run.id,output.id,output.created_by_user_id,run.provider_model,
			run.definition_identity||'@'||run.definition_version::text,tree.absolute_deadline,
			greatest(0,(definition.limits->>'model_calls')::integer-1),1,
			(definition.limits->>'actions')::integer,(definition.limits->>'action_batch')::integer,
			(definition.limits->>'result_bytes')::integer,(definition.limits->>'result_bytes')::integer,
			run.selected_source_count,policy.temperature,policy.max_output_tokens,policy.timeout_ms,
			tree.absolute_deadline>now(),exists(
				select 1 from notebook_memberships member
				where member.notebook_id=output.notebook_id and member.user_id=output.created_by_user_id
			)
		from agent_runs run
		join agent_jobs job on job.run_id=run.id
		join agent_trees tree on tree.id=run.tree_id
		join studio_outputs output on output.root_agent_run_id=tree.root_agent_run_id
		join agent_definition_versions definition on definition.definition_identity=run.definition_identity and definition.definition_version=run.definition_version
		join agent_model_policy_versions policy on policy.policy_identity=run.model_policy_identity and policy.policy_version=run.model_policy_version
			and policy.canonical_sha256=run.model_policy_sha256 and policy.provider_model=run.provider_model
		where run.id=$1 and job.id=$2 and job.lease_token=$3::uuid and job.attempt_no=$4
			and run.status='running' and job.status='running' and job.lease_expires_at>now()
			and output.status='running'
	`, attempt.RunID, attempt.JobID, attempt.LeaseToken, attempt.AttemptNo).Scan(
		&execution.RunID, &execution.InputMessageID, &execution.UserID, &execution.Model,
		&execution.AgentConfigID, &execution.DeadlineAt, &execution.ActionDecisionLimit,
		&execution.FinalDecisionLimit, &execution.ActionLimit, &execution.ActionBatchLimit,
		&execution.ActionResultByteLimit, &execution.ActionResultsByteLimit, &execution.SelectedSourceCount,
		&temperature, &maxOutputTokens, &timeoutMS, &deadlineValid, &authorized,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, ErrLeaseLost
	}
	if err != nil {
		return Execution{}, err
	}
	if !deadlineValid {
		return Execution{}, ErrRunDeadlineExceeded
	}
	if !authorized {
		return Execution{}, errors.New("Studio Run is no longer authorized")
	}
	execution.Attempt = attempt
	execution.PromptVersion = GroundedPromptVersion
	execution.TimeZone = "UTC"
	execution.ModelInvocation = models.ModelInvocationPolicy{
		Temperature: &temperature, MaxOutputTokens: maxOutputTokens, Timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return execution, nil
}

func (r *studioRuntime) LoadCheckpointPrefix(ctx context.Context, attempt Attempt) (CheckpointPrefix, error) {
	return r.base.LoadCheckpointPrefix(ctx, attempt)
}

func (r *studioRuntime) CheckAuthority(ctx context.Context, attempt Attempt) error {
	return r.base.CheckAuthority(ctx, attempt)
}

func (r *studioRuntime) AppendCheckpoint(ctx context.Context, attempt Attempt, checkpoint PendingCheckpoint) (Checkpoint, error) {
	return r.base.AppendCheckpoint(ctx, attempt, checkpoint)
}

func (r *studioRuntime) Fail(ctx context.Context, attempt Attempt, errorCode string) error {
	return r.base.Fail(ctx, attempt, errorCode)
}

func (r *studioRuntime) StartAttemptTrace(ctx context.Context, attempt Attempt) (context.Context, *agentobs.Tracer, error) {
	return r.base.StartAttemptTrace(ctx, attempt)
}

func (r *studioRuntime) PreviousActionSpan(ctx context.Context, attempt Attempt, actionID string) (agentobs.SpanContext, bool, error) {
	return r.base.PreviousActionSpan(ctx, attempt, actionID)
}

func (r *studioRuntime) ReplayStager() ReplayStager { return r.base.ReplayStager() }

func (r *studioRuntime) BuildQueryContextRequest(ctx context.Context, execution Execution, definition models.ActionDefinition) (models.ModelRequest, models.ActionProposal, int, error) {
	if definition.Name != "search_evidence" || execution.SelectedSourceCount < 1 {
		return models.ModelRequest{}, models.ActionProposal{}, 0, errors.New("Studio retrieval requires search_evidence")
	}
	kind, locale, _, err := r.outputContext(ctx, execution.RunID)
	if err != nil {
		return models.ModelRequest{}, models.ActionProposal{}, 0, err
	}
	query := fmt.Sprintf("Create a %s that covers the key facts, themes, and comparisons in all selected sources", kind)
	input, _ := json.Marshal(searchEvidenceInput{Query: query, Purpose: "Ground a Studio Output in the selected Sources."})
	fallback := models.ActionProposal{Name: "search_evidence", Input: input}
	request := models.ModelRequest{
		Model: execution.Model,
		Messages: []models.ModelMessage{
			{Role: models.RoleSystem, Content: "Call search_evidence exactly once to retrieve the selected Sources needed for this Studio Output."},
			{Role: models.RoleUser, Content: fmt.Sprintf("Output kind: %s\nLanguage: %s\nUse all selected Sources.", kind, locale)},
		},
		ActionDefinitions: []models.ActionDefinition{definition}, RequiredActionName: "search_evidence",
	}
	return request, fallback, 0, nil
}

func (r *studioRuntime) BuildDecisionRequest(ctx context.Context, execution Execution, prefix CheckpointPrefix, definitions []models.ActionDefinition) (models.ModelRequest, error) {
	if prefix.Final != nil || len(prefix.Proposals) != 1 || prefix.Proposals[0].Actions[0].Result == nil {
		return models.ModelRequest{}, errors.New("Studio final generation requires one completed evidence search")
	}
	kind, locale, prompt, err := r.outputContext(ctx, execution.RunID)
	if err != nil {
		return models.ModelRequest{}, err
	}
	request := models.ModelRequest{Model: execution.Model, Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: prompt},
		{Role: models.RoleUser, Content: fmt.Sprintf("Create the %s now. Language: %s.", kind, locale)},
	}}
	candidates, err := r.base.loadSearchEvidenceModelCandidates(ctx, execution, prefix)
	if err != nil {
		return models.ModelRequest{}, err
	}
	for _, proposal := range prefix.Proposals {
		proposalMessage := models.ModelMessage{Role: models.RoleAssistant}
		for _, action := range proposal.Actions {
			proposalMessage.ActionCalls = append(proposalMessage.ActionCalls, models.ModelActionCall{ID: action.ActionID, Name: action.Name, Input: action.Input})
		}
		request.Messages = append(request.Messages, proposalMessage)
		for _, action := range proposal.Actions {
			result := *action.Result
			if action.Name == "search_evidence" && result.Status == ActionSucceeded {
				result.Output, err = projectSearchEvidenceForModel(execution, result.Output, candidates)
				if err != nil {
					return models.ModelRequest{}, err
				}
			}
			checkpoint, err := NewActionResultCheckpoint(proposal.DecisionNo, action.Index, action.ActionID, result)
			if err != nil {
				return models.ModelRequest{}, err
			}
			request.Messages = append(request.Messages, models.ModelMessage{Role: models.RoleAction, Content: string(checkpoint.Payload), ActionCallID: action.ActionID})
		}
	}
	request.ActionDefinitions = cloneActionDefinitions(definitions)
	return request, nil
}

func (r *studioRuntime) outputContext(ctx context.Context, runID string) (studio.Kind, string, string, error) {
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return "", "", "", err
	}
	defer tx.Rollback(ctx)
	var kind studio.Kind
	var locale, definitionIdentity string
	var definitionVersion int
	if err := tx.QueryRow(ctx, `
		select output.kind,output.locale,run.definition_identity,run.definition_version
		from studio_outputs output join agent_runs run on run.id=output.root_agent_run_id where run.id=$1
	`, runID).Scan(&kind, &locale, &definitionIdentity, &definitionVersion); err != nil {
		return "", "", "", err
	}
	definition, ok := r.catalog.ResolveDefinition(agentcatalog.Reference{Identity: definitionIdentity, Version: definitionVersion})
	if !ok || len(definition.Prompts) != 1 {
		return "", "", "", errors.New("Studio prompt binding is invalid")
	}
	var promptReference agentcatalog.Reference
	for _, reference := range definition.Prompts {
		promptReference = reference
	}
	prompt, ok := productionPromptCatalog.Resolve(promptReference.Identity, promptReference.Version)
	if !ok {
		return "", "", "", errors.New("Studio prompt is missing")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", err
	}
	return kind, locale, prompt.Content, nil
}

func (r *studioRuntime) PublishFinal(ctx context.Context, attempt Attempt, draft models.FinalDraft) error {
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	traceCtx, traceScope, err := r.base.beginTraceScope(ctx)
	if err != nil {
		return err
	}
	if traceScope != nil {
		defer traceScope.Rollback()
	}
	if err := lockCheckpointAuthority(ctx, tx, attempt); err != nil {
		return err
	}
	checkpoints, err := loadRunCheckpoints(ctx, tx, attempt.RunID)
	if err != nil {
		return err
	}
	prefix, err := LoadCheckpointPrefix(ctx, checkpoints)
	if err != nil || prefix.Final == nil || strings.TrimSpace(prefix.Final.Text) != strings.TrimSpace(draft.Text) {
		return invalidCheckpoint("Studio publication Final Draft does not match accepted prefix")
	}
	var outputID string
	var kind studio.Kind
	var selectedCount int
	var contract agentcatalog.ContractVersion
	if err := tx.QueryRow(ctx, `
		select output.id,output.kind,output.selected_source_count,
			definition.result_contract_identity,definition.result_contract_version,contract.canonical_sha256
		from studio_outputs output
		join agent_runs run on run.id=output.root_agent_run_id
		join agent_definition_versions definition on definition.definition_identity=run.definition_identity and definition.definition_version=run.definition_version
		join agent_contract_versions contract on contract.contract_identity=definition.result_contract_identity and contract.contract_version=definition.result_contract_version
		where run.id=$1 and output.status='running'
	`, attempt.RunID).Scan(&outputID, &kind, &selectedCount, &contract.Identity, &contract.Version, &contract.SHA256); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		select evidence.source_id
		from agent_run_evidence_set evidence
		join source_sources source on source.id=evidence.source_id and source.state='ready'
		join source_evidence_revisions revision on revision.id=evidence.evidence_revision_id and revision.status='active'
		join retrieval_index_versions version on version.id=evidence.index_version_id and version.status='active'
		where evidence.run_id=$1 order by evidence.ordinal
	`, attempt.RunID)
	if err != nil {
		return err
	}
	sourceIDs := make([]string, 0, selectedCount)
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			rows.Close()
			return err
		}
		sourceIDs = append(sourceIDs, sourceID)
	}
	rows.Close()
	if len(sourceIDs) != selectedCount {
		return errors.New("Studio Source snapshot is no longer publishable")
	}
	artifact, err := studio.ValidateArtifact(kind, []byte(strings.TrimSpace(draft.Text)), sourceIDs)
	if err != nil {
		return fmt.Errorf("invalid Studio artifact: %w", err)
	}
	result, err := NewAgentResult("result_"+uuid.NewString(), attempt.RunID, contract, artifact.JSON)
	if err != nil {
		return err
	}
	if err := StoreAgentResultInTx(ctx, tx, result); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `
		update agent_trees set result_bytes_consumed=result_bytes_consumed+$2,updated_at=now()
		where id=(select tree_id from agent_runs where id=$1) and result_bytes_consumed+$2<=result_byte_limit
	`, attempt.RunID, result.PayloadBytes); err != nil {
		return err
	} else if tag.RowsAffected() != 1 {
		return errors.New("Studio Result budget exhausted")
	}
	if tag, err := tx.Exec(ctx, `
		update studio_outputs set title=$2,artifact=$3::jsonb,result_id=$4,status='completed',error_code=null,
			finished_at=now(),updated_at=now() where id=$1 and status='running'
	`, outputID, artifact.Title, string(artifact.JSON), result.ID); err != nil {
		return err
	} else if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	runTag, err := tx.Exec(ctx, `update agent_runs set status='completed',error_code=null,finished_at=now(),updated_at=now() where id=$1 and status='running'`, attempt.RunID)
	if err != nil {
		return err
	}
	jobTag, err := tx.Exec(ctx, `
		update agent_jobs set status='succeeded',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now()
		where id=$1 and status='running' and lease_token=$2::uuid
	`, attempt.JobID, attempt.LeaseToken)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 || jobTag.RowsAffected() != 1 {
		return errors.New("Studio publication did not terminalize Run and Job together")
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(traceCtx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "completed", SpanStatus: agentobs.StatusOK, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	publishCommittedTrace(traceCtx, traceScope)
	return nil
}
