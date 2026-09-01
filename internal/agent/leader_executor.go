package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AttemptExecutor interface {
	Execute(context.Context, Attempt) error
}

// LeaderExecutor is the worker entry point for every Agent Run. Member-visible
// Leader Runs either continue through the normal controller or suspend while a
// durable, non-member-visible Research child gathers Source candidates.
type LeaderExecutor struct {
	pool               *pgxpool.Pool
	normal             AttemptExecutor
	planner            ResearchPlanner
	provider           websearch.Provider
	researchToolHost   *MCPToolHost
	researchDefinition agentcatalog.Reference
	researchResult     agentcatalog.ContractVersion
	candidateValidator sourcediscovery.CandidateValidator
	traceSink          TraceSink
	replayStager       ReplayStager
}

type LeaderExecutorOption func(*LeaderExecutor)

func WithLeaderTraceSink(sink TraceSink) LeaderExecutorOption {
	return func(executor *LeaderExecutor) { executor.traceSink = sink }
}

func WithLeaderReplayStager(stager ReplayStager) LeaderExecutorOption {
	return func(executor *LeaderExecutor) { executor.replayStager = stager }
}

func WithResearchCandidateValidator(validator sourcediscovery.CandidateValidator) LeaderExecutorOption {
	return func(executor *LeaderExecutor) { executor.candidateValidator = validator }
}

func WithResearchMCPToolPlane(host *MCPToolHost, definition agentcatalog.Reference) LeaderExecutorOption {
	return func(executor *LeaderExecutor) {
		executor.researchToolHost = host
		executor.researchDefinition = definition
	}
}

func WithResearchResultContract(contract agentcatalog.ContractVersion) LeaderExecutorOption {
	return func(executor *LeaderExecutor) { executor.researchResult = contract }
}

func NewLeaderExecutor(pool *pgxpool.Pool, normal AttemptExecutor, planner ResearchPlanner, provider websearch.Provider, options ...LeaderExecutorOption) *LeaderExecutor {
	executor := &LeaderExecutor{pool: pool, normal: normal, planner: planner, provider: provider, traceSink: DiscardTraceSink{}}
	for _, option := range options {
		option(executor)
	}
	return executor
}

type leaderRunContext struct {
	RuntimeKind        string
	Role               string
	ExecutorIdentity   string
	Definition         agentcatalog.Reference
	DefinitionSHA256   string
	UserID             string
	ChatID             string
	NotebookID         string
	Message            string
	Model              string
	ModelInvocation    models.ModelInvocationPolicy
	PromptVersion      string
	TimeZone           string
	MemberRole         string
	Status             string
	DeadlineAt         time.Time
	ExistingChildCount int
	DelegationState    *string
}

func (e *LeaderExecutor) Execute(ctx context.Context, attempt Attempt) error {
	return e.executeExpected(ctx, attempt, "", "")
}

func (e *LeaderExecutor) executeExpectedRole(ctx context.Context, attempt Attempt, expected AgentRole) error {
	return e.executeExpected(ctx, attempt, "legacy_role", string(expected))
}

func (e *LeaderExecutor) executeExpectedDefinition(ctx context.Context, attempt Attempt, expectedExecutor string) error {
	return e.executeExpected(ctx, attempt, "configured", expectedExecutor)
}

func (e *LeaderExecutor) executeExpected(ctx context.Context, attempt Attempt, expectedRuntimeKind, expectedIdentity string) error {
	if e == nil || e.pool == nil || e.normal == nil || e.planner == nil || e.provider == nil {
		return errors.New("Leader Executor dependencies are incomplete")
	}
	scope, err := NewTraceScope(e.traceSink, WithTraceScopeReplayStager(e.replayStager))
	if err != nil {
		return err
	}
	defer scope.Rollback()
	traceCtx := ContextWithTraceScope(ctx, scope)
	if err := e.execute(traceCtx, attempt, expectedRuntimeKind, expectedIdentity); err != nil {
		return err
	}
	scope.PublishAfterCommit(ctx)
	return nil
}

func (e *LeaderExecutor) execute(ctx context.Context, attempt Attempt, expectedRuntimeKind, expectedIdentity string) error {
	run, err := e.loadRun(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	if expectedRuntimeKind != "" && (run.RuntimeKind != expectedRuntimeKind || run.executionIdentity() != expectedIdentity) {
		return ErrInvalidLeaderRoute
	}
	if run.isResearch() {
		if run.RuntimeKind == "configured" {
			return e.executeConfiguredResearch(ctx, attempt, run)
		}
		return e.executeResearch(ctx, attempt, run)
	}
	if !run.isChatLeader() {
		return ErrInvalidLeaderRoute
	}
	if run.RuntimeKind == "legacy_role" && run.DelegationState != nil {
		return e.resumeDelegated(ctx, attempt, run)
	}
	return e.normal.Execute(ctx, attempt)
}

func (r leaderRunContext) executionIdentity() string {
	if r.RuntimeKind == "configured" {
		return r.ExecutorIdentity
	}
	return r.Role
}

func (r leaderRunContext) isResearch() bool {
	return (r.RuntimeKind == "configured" && r.ExecutorIdentity == "research") ||
		(r.RuntimeKind == "legacy_role" && AgentRole(r.Role) == RoleResearch)
}

func (r leaderRunContext) isChatLeader() bool {
	return (r.RuntimeKind == "configured" && r.ExecutorIdentity == "chat_leader") ||
		(r.RuntimeKind == "legacy_role" && AgentRole(r.Role) == RoleLeader)
}

func (e *LeaderExecutor) loadRun(ctx context.Context, runID string) (leaderRunContext, error) {
	var run leaderRunContext
	var delegationState *string
	var temperature *float64
	var maxOutputTokens, timeoutMS *int
	tx, err := e.workerTx(ctx)
	if err != nil {
		return leaderRunContext{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		select r.runtime_kind,coalesce(r.agent_role,''),coalesce(r.executor_identity,''),
			coalesce(r.definition_identity,''),coalesce(r.definition_version,0),coalesce(r.definition_sha256,''),
			coalesce(r.user_id,product.user_id),coalesce(r.chat_id,product.chat_id),c.notebook_id,m.content,
			coalesce(r.model,r.provider_model),
			coalesce(r.prompt_version,r.definition_identity||'@'||r.definition_version::text),
			coalesce(r.time_zone,r.parent_context_manifest->>'time_zone','UTC'),
			coalesce(member.role,''),r.status,coalesce(r.deadline_at,tree.absolute_deadline),
			policy.temperature,policy.max_output_tokens,policy.timeout_ms,
			(select count(*) from agent_run_delegations child_link where child_link.parent_run_id=tree.root_agent_run_id),
			delegation.state
		from agent_runs r
		left join agent_trees tree on tree.id=r.tree_id
		left join agent_model_policy_versions policy
			on policy.policy_identity=r.model_policy_identity and policy.policy_version=r.model_policy_version
			and policy.canonical_sha256=r.model_policy_sha256 and policy.provider_model=r.provider_model
		left join chat_runs product on product.root_agent_run_id=tree.root_agent_run_id
		join chat_chats c on c.id=coalesce(r.chat_id,product.chat_id)
		join chat_messages m on m.id=coalesce(r.input_message_id,product.input_message_id)
		left join notebook_memberships member on member.notebook_id=c.notebook_id and member.user_id=coalesce(r.user_id,product.user_id)
		left join agent_run_delegations relationship on relationship.child_run_id=r.id
		left join agent_run_delegations delegation on delegation.parent_run_id=tree.root_agent_run_id
		where r.id=$1
	`, runID).Scan(&run.RuntimeKind, &run.Role, &run.ExecutorIdentity,
		&run.Definition.Identity, &run.Definition.Version, &run.DefinitionSHA256,
		&run.UserID, &run.ChatID, &run.NotebookID, &run.Message, &run.Model,
		&run.PromptVersion, &run.TimeZone, &run.MemberRole, &run.Status, &run.DeadlineAt,
		&temperature, &maxOutputTokens, &timeoutMS, &run.ExistingChildCount, &delegationState)
	if err != nil {
		return leaderRunContext{}, err
	}
	if run.RuntimeKind == "configured" {
		if temperature == nil || maxOutputTokens == nil || timeoutMS == nil {
			return leaderRunContext{}, errors.New("configured Model Policy pin is invalid")
		}
		run.ModelInvocation = models.ModelInvocationPolicy{
			Temperature: temperature, MaxOutputTokens: *maxOutputTokens, Timeout: time.Duration(*timeoutMS) * time.Millisecond,
		}
	}
	run.DelegationState = delegationState
	if err := tx.Commit(ctx); err != nil {
		return leaderRunContext{}, err
	}
	return run, nil
}

func (e *LeaderExecutor) executeResearch(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	progress, err := e.loadResearchProgress(ctx, attempt)
	if err != nil {
		return err
	}
	if len(progress.Plan) == 0 {
		request := ResearchPlanRequest{Model: run.Model, UserMessage: run.Message, InvocationPolicy: run.ModelInvocation}
		var queries []string
		if traced, ok := e.planner.(TracedResearchPlanner); ok {
			traceContext, tracer, traceErr := e.modelTraceContext(ctx, attempt)
			if traceErr != nil {
				return traceErr
			}
			modelIdentity := TraceResearchPlanModelStartIdentity(attempt.RunID, attempt.AttemptNo)
			queries, err = traced.ExpandQueriesTraced(traceContext, tracer, request, ModelTraceOptions{
				StartIdentity: modelIdentity, RequestIdentity: modelIdentity + "/replay/request",
				DecisionIdentity: modelIdentity + "/replay/decision", ReplayStager: e.replayStager,
				Phase: ModelPhaseResearchQueryExpansion, Role: RoleResearch, Prompt: promptTraceRef("agent.research-planner", 1),
			})
		} else {
			queries, err = e.planner.ExpandQueries(ctx, request)
		}
		if err != nil {
			return err
		}
		queries = boundQueries(queries)
		if len(queries) == 0 {
			return ErrInvalidLeaderRoute
		}
		checkpoint, err := NewRoleCheckpoint(RoleResearch, ResearchStepQueryPlan, 0, ResearchQueryPlan{Queries: queries})
		if err != nil {
			return err
		}
		if err := e.appendResearchCheckpoint(ctx, attempt, checkpoint); err != nil {
			return err
		}
		progress.Plan = queries
	}
	var resultGroups [][]websearch.Candidate
	if e.researchToolHost != nil {
		resultGroups, err = e.searchResearchQueries(ctx, attempt, run, progress)
		if err != nil {
			return err
		}
		for ordinal, query := range progress.Plan {
			if _, accepted := progress.Results[ordinal]; accepted {
				continue
			}
			checkpoint, err := NewRoleCheckpoint(RoleResearch, ResearchStepSearchResult, ordinal, ResearchSearchResult{Query: query, Candidates: resultGroups[ordinal]})
			if err != nil {
				return err
			}
			if err := e.appendResearchCheckpoint(ctx, attempt, checkpoint); err != nil {
				return err
			}
		}
	} else {
		resultGroups = make([][]websearch.Candidate, len(progress.Plan))
		for ordinal, query := range progress.Plan {
			if accepted, ok := progress.Results[ordinal]; ok {
				resultGroups[ordinal] = accepted
				continue
			}
			results, searchErr := e.provider.Search(ctx, websearch.Request{Query: query, Count: 10})
			if searchErr != nil {
				return searchErr
			}
			checkpoint, err := NewRoleCheckpoint(RoleResearch, ResearchStepSearchResult, ordinal, ResearchSearchResult{Query: query, Candidates: results})
			if err != nil {
				return err
			}
			if err := e.appendResearchCheckpoint(ctx, attempt, checkpoint); err != nil {
				return err
			}
			resultGroups[ordinal] = results
		}
	}
	candidates := mergeResearchCandidates(resultGroups)
	if e.candidateValidator != nil {
		validated := make([]sourcediscovery.DiscoveredCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if e.candidateValidator.Validate(ctx, candidate.URL) {
				validated = append(validated, candidate)
			}
		}
		candidates = validated
	}
	return e.completeResearch(ctx, attempt, candidates)
}

func (e *LeaderExecutor) executeConfiguredResearch(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	if err := e.ensureConfiguredResearchSession(ctx, attempt, run); err != nil {
		return err
	}
	runtime := NewPostgresRuntime(e.pool, BareSystemPrompt, nil,
		WithTraceSink(e.traceSink), WithReplayStager(e.replayStager))
	prefix, err := runtime.LoadCheckpointPrefix(ctx, attempt)
	if err != nil {
		return err
	}
	if len(prefix.Proposals) == 0 {
		request := ResearchPlanRequest{Model: run.Model, UserMessage: run.Message, InvocationPolicy: run.ModelInvocation}
		var proposal models.ActionProposal
		if traced, ok := e.planner.(TracedConfiguredResearchPlanner); ok {
			traceContext, tracer, traceErr := e.modelTraceContext(ctx, attempt)
			if traceErr != nil {
				return traceErr
			}
			modelIdentity := TraceResearchPlanModelStartIdentity(attempt.RunID, attempt.AttemptNo)
			proposal, err = traced.PlanWebSearchTraced(traceContext, tracer, request, ModelTraceOptions{
				StartIdentity: modelIdentity, RequestIdentity: modelIdentity + "/replay/request",
				DecisionIdentity: modelIdentity + "/replay/decision", ReplayStager: e.replayStager,
				Phase: ModelPhaseResearchQueryExpansion, Role: RoleResearch, Prompt: promptTraceRef("agent.research-planner", 1),
			})
		} else if planner, ok := e.planner.(ConfiguredResearchPlanner); ok {
			proposal, err = planner.PlanWebSearch(ctx, request)
		} else {
			var queries []string
			queries, err = e.planner.ExpandQueries(ctx, request)
			queries = boundQueries(queries)
			if err == nil && len(queries) == 0 {
				err = ErrInvalidLeaderRoute
			}
			if err == nil {
				proposal.Name = "web_search"
				proposal.Input, err = json.Marshal(webSearchInput{Queries: queries})
			}
		}
		if err != nil || proposal.Name != "web_search" {
			if err != nil {
				return err
			}
			return ErrInvalidLeaderRoute
		}
		checkpoint, err := NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{proposal}})
		if err != nil {
			return err
		}
		if _, err := runtime.AppendCheckpoint(ctx, attempt, checkpoint); err != nil {
			return err
		}
		prefix, err = runtime.LoadCheckpointPrefix(ctx, attempt)
		if err != nil {
			return err
		}
	}
	if prefix.Final != nil || len(prefix.Proposals) != 1 || len(prefix.Proposals[0].Actions) != 1 ||
		prefix.Proposals[0].Actions[0].Name != "web_search" {
		return ErrCheckpointInvalid
	}
	action := prefix.Proposals[0].Actions[0]
	result := action.Result
	if result == nil {
		if e.researchToolHost == nil {
			return errors.New("Research MCP Tool Plane is unavailable")
		}
		session, err := e.researchToolHost.OpenAttempt(ctx, AttemptToolScope{
			Definition: run.Definition, Attempt: attempt, DefaultTimeZone: run.TimeZone,
			RemainingActions: 1, Deadline: run.DeadlineAt,
		})
		if err != nil {
			return err
		}
		called, callErr := session.CallTool(ctx, "web_search", action.Input, action.ActionID)
		closeErr := session.Close()
		if callErr != nil {
			return callErr
		}
		if closeErr != nil {
			return closeErr
		}
		called = enrichActionDomainError(called)
		checkpoint, err := NewActionResultCheckpoint(1, 0, action.ActionID, called)
		if err != nil {
			return err
		}
		if _, err := runtime.AppendCheckpoint(ctx, attempt, checkpoint); err != nil {
			return err
		}
		result = &called
	}
	if result.Status != ActionSucceeded {
		return errors.New("configured Research Web Search did not succeed")
	}
	var output webSearchOutput
	if json.Unmarshal(result.Output, &output) != nil {
		return websearch.ErrInvalidResponse
	}
	groups := make([][]websearch.Candidate, 0, len(output.Results))
	for _, item := range output.Results {
		group := make([]websearch.Candidate, 0, len(item.Candidates))
		for _, candidate := range item.Candidates {
			group = append(group, websearch.Candidate{
				Title: candidate.Title, URL: candidate.URL, DisplayURL: candidate.DisplayURL,
				Description: candidate.Description, Rank: candidate.Rank,
			})
		}
		groups = append(groups, group)
	}
	return e.completeResearch(ctx, attempt, mergeResearchCandidates(groups))
}

func (e *LeaderExecutor) ensureConfiguredResearchSession(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.requireLease(ctx, tx, attempt, "research"); err != nil {
		return err
	}
	var notebookID, userID, chatID, message string
	if err := tx.QueryRow(ctx, `
		select chat.notebook_id,product.user_id,product.chat_id,input.content
		from agent_runs child
		join agent_trees tree on tree.id=child.tree_id
		join chat_runs product on product.root_agent_run_id=tree.root_agent_run_id
		join chat_chats chat on chat.id=product.chat_id
		join chat_messages input on input.id=product.input_message_id
		join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=product.user_id
		where child.id=$1 and child.runtime_kind='configured' and child.executor_identity='research'
		  and member.role in ('owner','editor')
	`, attempt.RunID).Scan(&notebookID, &userID, &chatID, &message); err != nil {
		return err
	}
	sessionID := "dss_" + uuid.NewString()
	stored, err := sourcediscovery.NewStore(tx).EnsureResearchSession(ctx, sourcediscovery.ResearchSessionCommand{
		ID: sessionID, NotebookID: notebookID, UserID: userID, OriginChatID: chatID,
		ResearchRunID: attempt.RunID, Query: truncateLeaderRunes(message, 500),
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update agent_runs set discovery_session_id=$2,updated_at=now()
		where (id=$1 or id=(select root_agent_run_id from agent_trees where id=agent_runs.tree_id))
		  and discovery_session_id is null
	`, attempt.RunID, stored.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) searchResearchQueries(ctx context.Context, attempt Attempt, run leaderRunContext, progress ResearchProgress) ([][]websearch.Candidate, error) {
	groups := make([][]websearch.Candidate, len(progress.Plan))
	missing := false
	for ordinal, accepted := range progress.Results {
		if ordinal >= 0 && ordinal < len(groups) {
			groups[ordinal] = append([]websearch.Candidate(nil), accepted...)
		}
	}
	for ordinal := range progress.Plan {
		if _, accepted := progress.Results[ordinal]; !accepted {
			missing = true
		}
	}
	if !missing {
		return groups, nil
	}
	if e == nil || e.researchToolHost == nil {
		return nil, errors.New("Research MCP Tool Plane is unavailable")
	}
	session, err := e.researchToolHost.OpenAttempt(ctx, AttemptToolScope{
		Definition: e.researchDefinition, Attempt: attempt, DefaultTimeZone: run.TimeZone,
		RemainingActions: 1, Deadline: run.DeadlineAt,
	})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	input, err := json.Marshal(webSearchInput{Queries: progress.Plan})
	if err != nil {
		return nil, err
	}
	result, err := session.CallTool(ctx, "web_search", input, "research:web_search:0")
	if err != nil {
		return nil, err
	}
	var output webSearchOutput
	if result.Status != ActionSucceeded || json.Unmarshal(result.Output, &output) != nil || len(output.Results) != len(progress.Plan) {
		return nil, websearch.ErrInvalidResponse
	}
	for ordinal, item := range output.Results {
		if item.Query != progress.Plan[ordinal] {
			return nil, websearch.ErrInvalidResponse
		}
		if _, accepted := progress.Results[ordinal]; accepted {
			continue
		}
		group := make([]websearch.Candidate, 0, len(item.Candidates))
		for _, candidate := range item.Candidates {
			group = append(group, websearch.Candidate{
				Title: candidate.Title, URL: candidate.URL, DisplayURL: candidate.DisplayURL,
				Description: candidate.Description, Rank: candidate.Rank,
			})
		}
		groups[ordinal] = group
	}
	return groups, nil
}

func (e *LeaderExecutor) loadResearchProgress(ctx context.Context, attempt Attempt) (ResearchProgress, error) {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return ResearchProgress{}, err
	}
	defer tx.Rollback(ctx)
	progress, err := LoadResearchProgressInTx(ctx, tx, attempt)
	if err != nil {
		return ResearchProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ResearchProgress{}, err
	}
	return progress, nil
}

func (e *LeaderExecutor) appendResearchCheckpoint(ctx context.Context, attempt Attempt, checkpoint RoleCheckpoint) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := AppendRoleCheckpointInTx(ctx, tx, attempt, checkpoint); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mergeResearchCandidates(groups [][]websearch.Candidate) []sourcediscovery.DiscoveredCandidate {
	type retained struct {
		candidate websearch.Candidate
		identity  string
		domain    string
	}
	interleaved := make([]retained, 0, 30)
	seen := make(map[string]struct{}, 30)
	for ordinal := 0; ; ordinal++ {
		found := false
		for _, group := range groups {
			if ordinal >= len(group) {
				continue
			}
			found = true
			candidate := group[ordinal]
			identity, err := source.CanonicalURLIdentity(candidate.URL)
			if err != nil || strings.TrimSpace(candidate.Title) == "" {
				continue
			}
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			seen[identity] = struct{}{}
			parsed, _ := url.Parse(candidate.URL)
			interleaved = append(interleaved, retained{candidate: candidate, identity: identity, domain: strings.ToLower(parsed.Hostname())})
		}
		if !found {
			break
		}
	}
	preferred := make([]retained, 0, 10)
	overflow := make([]retained, 0, len(interleaved))
	domainCounts := make(map[string]int)
	for _, item := range interleaved {
		if item.domain != "" && domainCounts[item.domain] >= 2 {
			overflow = append(overflow, item)
			continue
		}
		preferred = append(preferred, item)
		domainCounts[item.domain]++
		if len(preferred) == 10 {
			break
		}
	}
	for _, item := range overflow {
		if len(preferred) == 10 {
			break
		}
		preferred = append(preferred, item)
	}
	result := make([]sourcediscovery.DiscoveredCandidate, 0, len(preferred))
	for _, item := range preferred {
		result = append(result, sourcediscovery.DiscoveredCandidate{
			ID: "dscand_" + uuid.NewString(), Title: item.candidate.Title, URL: item.candidate.URL,
			DisplayURL: item.candidate.DisplayURL, Snippet: item.candidate.Description, ProviderRank: item.candidate.Rank,
		})
	}
	return result
}

func (e *LeaderExecutor) completeResearch(ctx context.Context, attempt Attempt, candidates []sourcediscovery.DiscoveredCandidate) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.requireLease(ctx, tx, attempt, "research"); err != nil {
		return err
	}
	var stillAuthorized bool
	if err := tx.QueryRow(ctx, `
		select exists(
			select 1 from agent_runs child
			left join agent_trees tree on tree.id=child.tree_id
			left join chat_runs product on product.root_agent_run_id=tree.root_agent_run_id
			join chat_chats chat on chat.id=coalesce(child.chat_id,product.chat_id)
			join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=coalesce(child.user_id,product.user_id)
			where child.id=$1 and member.role in ('owner','editor')
		)
	`, attempt.RunID).Scan(&stillAuthorized); err != nil {
		return err
	}
	if !stillAuthorized {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return err
		}
		return e.failResearch(ctx, attempt, "research_authority_lost")
	}
	sessionID, err := sourcediscovery.NewStore(tx).CompleteResearchSession(ctx, attempt.RunID, "", candidates)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_runs set discovery_session_id=$2,updated_at=now() where id=$1 and status='running'`, attempt.RunID, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_research_outcomes(delegation_id,discovery_session_id)
		select id,$2 from agent_run_delegations where child_run_id=$1 and state='waiting'
	`, attempt.RunID, sessionID); err != nil {
		return err
	}
	if e.researchResult.Reference().Identity != "" {
		payloadCandidates := make([]map[string]string, 0, len(candidates))
		for _, candidate := range candidates {
			payloadCandidates = append(payloadCandidates, map[string]string{
				"url": candidate.URL, "title": candidate.Title, "snippet": candidate.Snippet,
			})
		}
		payload, err := json.Marshal(map[string]any{"candidates": payloadCandidates})
		if err != nil {
			return err
		}
		result, err := NewAgentResult("result_"+uuid.NewString(), attempt.RunID, e.researchResult, payload)
		if err != nil {
			return err
		}
		if err := StoreAgentResultInTx(ctx, tx, result); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			update agent_run_delegations set result_id=$2,updated_at=now()
			where child_run_id=$1 and state='waiting' and result_id is null
		`, attempt.RunID, result.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Research delegation did not accept its Agent Result")
		}
		tag, err = tx.Exec(ctx, `
			update agent_trees
			set result_bytes_consumed=result_bytes_consumed+$2,updated_at=now()
			where id=(select tree_id from agent_runs where id=$1)
			  and result_bytes_consumed+$2<=result_byte_limit
		`, attempt.RunID, result.PayloadBytes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Agent Tree Result budget exhausted")
		}
	}
	if err := (DelegationKernel{}).TerminalizeInTx(ctx, tx, attempt, DelegationSucceeded, ""); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(ctx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "completed", SpanStatus: agentobs.StatusOK, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) failResearch(ctx context.Context, attempt Attempt, errorCode string) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.requireLease(ctx, tx, attempt, "research"); err != nil {
		return err
	}
	if err := sourcediscovery.NewStore(tx).FailResearchSession(ctx, attempt.RunID, errorCode); err != nil {
		return err
	}
	if err := (DelegationKernel{}).TerminalizeInTx(ctx, tx, attempt, DelegationFailed, errorCode); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(ctx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "failed", SpanStatus: agentobs.StatusError, ErrorCode: errorCode, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) resumeDelegated(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.requireLease(ctx, tx, attempt, run.executionIdentity()); err != nil {
		return err
	}
	var state string
	var sessionID, errorCode *string
	if err := tx.QueryRow(ctx, `
		select delegation.state,outcome.discovery_session_id,delegation.error_code
		from agent_run_delegations delegation
		left join agent_research_outcomes outcome on outcome.delegation_id=delegation.id
		where delegation.parent_run_id=$1 for update of delegation
	`, attempt.RunID).Scan(&state, &sessionID, &errorCode); err != nil {
		return err
	}
	if state == string(DelegationFailed) || state == string(DelegationCancelled) {
		code := "research_failed"
		if errorCode != nil {
			code = *errorCode
		}
		return e.failLeaderInTx(ctx, tx, attempt, code)
	}
	if state != string(DelegationSucceeded) || sessionID == nil {
		return errors.New("Research delegation is not ready")
	}
	messageID := "msg_" + uuid.NewString()
	content := "I found relevant source material. Review the search results and import what you need."
	if containsHan(run.Message) {
		content = "已找到相关资料，请在来源搜索结果中选择需要的内容并导入。"
	}
	if _, err := tx.Exec(ctx, `insert into chat_messages(id,chat_id,role,content) values($1,$2,'assistant',$3)`, messageID, run.ChatID, content); err != nil {
		return err
	}
	runTag, err := tx.Exec(ctx, `
		update agent_runs set status='completed',output_message_id=$2,discovery_session_id=$3,finished_at=now(),updated_at=now()
		where id=$1 and status='running'
	`, attempt.RunID, messageID, *sessionID)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if err := (DelegationKernel{}).CompleteParentJobInTx(ctx, tx, attempt); err != nil {
		return err
	}
	if err := (DelegationKernel{}).ConsumeInTx(ctx, tx, attempt.RunID, DelegationSucceeded); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(ctx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "completed", SpanStatus: agentobs.StatusOK, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) failLeaderInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, errorCode string) error {
	var childRunID string
	if err := tx.QueryRow(ctx, `select child_run_id from agent_run_delegations where parent_run_id=$1`, attempt.RunID).Scan(&childRunID); err != nil {
		return err
	}
	if err := FailResearchPayloadInTx(ctx, tx, childRunID, errorCode); err != nil {
		return err
	}
	if err := (DelegationKernel{}).FailParentInTx(ctx, tx, attempt, errorCode); err != nil {
		return err
	}
	var terminal DelegationState
	if err := tx.QueryRow(ctx, `select state from agent_run_delegations where parent_run_id=$1`, attempt.RunID).Scan(&terminal); err != nil {
		return err
	}
	if err := (DelegationKernel{}).ConsumeInTx(ctx, tx, attempt.RunID, terminal); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(ctx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "failed", SpanStatus: agentobs.StatusError, ErrorCode: errorCode, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) requireLease(ctx context.Context, tx pgx.Tx, attempt Attempt, role string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from agent_runs r join agent_jobs j on j.run_id=r.id
		left join agent_trees tree on tree.id=r.tree_id
		where r.id=$1 and ((r.runtime_kind='legacy_role' and r.agent_role=$2) or (r.runtime_kind='configured' and r.executor_identity=$2))
		and r.status='running' and coalesce(r.deadline_at,tree.absolute_deadline)>now() and j.id=$3 and j.status='running'
		and j.lease_token=$4::uuid and j.lease_expires_at>now())
	`, attempt.RunID, role, attempt.JobID, attempt.LeaseToken).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrLeaseLost
	}
	return nil
}

func (e *LeaderExecutor) workerTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func (e *LeaderExecutor) modelTraceContext(ctx context.Context, attempt Attempt) (context.Context, *agentobs.Tracer, error) {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return ctx, nil, err
	}
	defer tx.Rollback(ctx)
	recorder, err := NewRunTraceRecorder(ctx, tx, attempt.RunID)
	if err != nil {
		return ctx, nil, err
	}
	attemptSpan, err := recorder.SpanContextByIdentity(ctx, TraceAttemptStartIdentity(attempt.RunID, attempt.AttemptNo))
	if err != nil {
		return ctx, nil, err
	}
	tracer, err := agentobs.NewTracer(agentobs.TracerConfig{
		Recorder: recorder, SemanticConventionVersion: TraceSemanticConventionVersion,
	})
	if err != nil {
		return ctx, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ctx, nil, err
	}
	return agentobs.ContextWithSpanContext(ctx, attemptSpan), tracer, nil
}

func boundQueries(input []string) []string {
	queries := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, query := range input {
		query = strings.TrimSpace(query)
		if query == "" || utf8.RuneCountInString(query) > 500 {
			continue
		}
		key := strings.ToLower(query)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
		if len(queries) == 3 {
			break
		}
	}
	return queries
}

func truncateLeaderRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func containsHan(value string) bool {
	for _, current := range value {
		if unicode.Is(unicode.Han, current) {
			return true
		}
	}
	return false
}
