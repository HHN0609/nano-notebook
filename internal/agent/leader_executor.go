package agent

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
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
	router             LeaderRouter
	planner            ResearchPlanner
	provider           websearch.Provider
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

func NewLeaderExecutor(pool *pgxpool.Pool, normal AttemptExecutor, router LeaderRouter, planner ResearchPlanner, provider websearch.Provider, options ...LeaderExecutorOption) *LeaderExecutor {
	executor := &LeaderExecutor{pool: pool, normal: normal, router: router, planner: planner, provider: provider, traceSink: DiscardTraceSink{}}
	for _, option := range options {
		option(executor)
	}
	return executor
}

type leaderRunContext struct {
	Role               string
	UserID             string
	ChatID             string
	NotebookID         string
	Message            string
	Model              string
	PromptVersion      string
	TimeZone           string
	MemberRole         string
	Status             string
	DeadlineAt         time.Time
	ExistingChildCount int
	RecentPairs        []LeaderConversationPair
	ExistingRoute      *LeaderRoute
	DelegationState    *string
}

func (e *LeaderExecutor) Execute(ctx context.Context, attempt Attempt) error {
	return e.executeExpectedRole(ctx, attempt, "")
}

func (e *LeaderExecutor) executeExpectedRole(ctx context.Context, attempt Attempt, expected AgentRole) error {
	if e == nil || e.pool == nil || e.normal == nil || e.router == nil || e.planner == nil || e.provider == nil {
		return errors.New("Leader Executor dependencies are incomplete")
	}
	scope, err := NewTraceScope(e.traceSink, WithTraceScopeReplayStager(e.replayStager))
	if err != nil {
		return err
	}
	defer scope.Rollback()
	traceCtx := ContextWithTraceScope(ctx, scope)
	if err := e.execute(traceCtx, attempt, expected); err != nil {
		return err
	}
	scope.PublishAfterCommit(ctx)
	return nil
}

func (e *LeaderExecutor) execute(ctx context.Context, attempt Attempt, expected AgentRole) error {
	run, err := e.loadRun(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	if expected != "" && AgentRole(run.Role) != expected {
		return ErrInvalidLeaderRoute
	}
	if AgentRole(run.Role) == RoleResearch {
		return e.executeResearch(ctx, attempt, run)
	}
	if AgentRole(run.Role) != RoleLeader {
		return ErrInvalidLeaderRoute
	}
	if run.ExistingRoute == nil {
		request := LeaderRouteRequest{Model: run.Model, UserMessage: run.Message, RecentPairs: run.RecentPairs}
		var decision LeaderRouteDecision
		if traced, ok := e.router.(TracedLeaderRouter); ok {
			traceContext, tracer, traceErr := e.modelTraceContext(ctx, attempt)
			if traceErr != nil {
				return traceErr
			}
			modelIdentity := TraceLeaderRouteModelStartIdentity(attempt.RunID, attempt.AttemptNo)
			decision, err = traced.DecideRouteTraced(traceContext, tracer, request, ModelTraceOptions{
				StartIdentity: modelIdentity, RequestIdentity: modelIdentity + "/replay/request",
				DecisionIdentity: modelIdentity + "/replay/decision", ReplayStager: e.replayStager,
				Phase: ModelPhaseLeaderRouting,
			})
		} else {
			decision, err = e.router.DecideRoute(ctx, request)
		}
		if err != nil {
			return err
		}
		providerAvailable := true
		if availability, ok := e.provider.(interface{ ResearchAvailable() bool }); ok {
			providerAvailable = availability.ResearchAvailable()
		}
		policy := EvaluateDelegationPolicy(decision, DelegationPolicyContext{
			MemberRole: run.MemberRole, NotebookAuthorized: run.MemberRole != "", RootActive: run.Status == "running",
			DeadlineValid: time.Now().Before(run.DeadlineAt), ProviderAvailable: providerAvailable,
			RelationshipRegistered: true, ExistingChildCount: run.ExistingChildCount,
		})
		if policy.EffectiveRoute == LeaderContinueChat {
			if err := e.persistRoute(ctx, attempt, policy); err != nil {
				return err
			}
			return e.normal.Execute(ctx, attempt)
		}
		return e.delegate(ctx, attempt, run, policy)
	}
	if *run.ExistingRoute == LeaderContinueChat {
		return e.normal.Execute(ctx, attempt)
	}
	return e.resumeDelegated(ctx, attempt, run)
}

func (e *LeaderExecutor) loadRun(ctx context.Context, runID string) (leaderRunContext, error) {
	var run leaderRunContext
	var route, delegationState *string
	tx, err := e.workerTx(ctx)
	if err != nil {
		return leaderRunContext{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		select r.agent_role,r.user_id,r.chat_id,c.notebook_id,m.content,r.model,r.prompt_version,r.time_zone,
			coalesce(member.role,''),r.status,r.deadline_at,
			(select count(*) from agent_run_delegations child_link where child_link.parent_run_id=case when r.agent_role='leader' then r.id else relationship.parent_run_id end),
			route.effective_route,delegation.state
		from agent_runs r
		join chat_chats c on c.id=r.chat_id
		join chat_messages m on m.id=r.input_message_id
		left join notebook_memberships member on member.notebook_id=c.notebook_id and member.user_id=r.user_id
		left join agent_run_routes route on route.run_id=case when r.agent_role='leader' then r.id else relationship.parent_run_id end
		left join agent_run_delegations relationship on relationship.child_run_id=r.id
		left join agent_run_delegations delegation on delegation.parent_run_id=case when r.agent_role='leader' then r.id else relationship.parent_run_id end
		where r.id=$1
	`, runID).Scan(&run.Role, &run.UserID, &run.ChatID, &run.NotebookID, &run.Message, &run.Model,
		&run.PromptVersion, &run.TimeZone, &run.MemberRole, &run.Status, &run.DeadlineAt, &run.ExistingChildCount, &route, &delegationState)
	if err != nil {
		return leaderRunContext{}, err
	}
	if route != nil {
		parsed := LeaderRoute(*route)
		run.ExistingRoute = &parsed
	}
	run.DelegationState = delegationState
	if AgentRole(run.Role) == RoleLeader {
		rows, err := tx.Query(ctx, `
			select input.content,output.content
			from agent_runs prior
			join chat_messages input on input.id=prior.input_message_id and input.role='user'
			join chat_messages output on output.id=prior.output_message_id and output.role='assistant'
			join agent_runs current on current.id=$1
			where prior.chat_id=current.chat_id and prior.status='completed'
			  and (input.created_at,input.id)<(
				select created_at,id from chat_messages where id=current.input_message_id
			  )
			order by input.created_at desc,input.id desc limit 3
		`, runID)
		if err != nil {
			return leaderRunContext{}, err
		}
		newest := make([]LeaderConversationPair, 0, 3)
		for rows.Next() {
			var pair LeaderConversationPair
			if err := rows.Scan(&pair.User, &pair.Assistant); err != nil {
				rows.Close()
				return leaderRunContext{}, err
			}
			newest = append(newest, pair)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return leaderRunContext{}, err
		}
		rows.Close()
		for index := len(newest) - 1; index >= 0; index-- {
			run.RecentPairs = append(run.RecentPairs, newest[index])
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return leaderRunContext{}, err
	}
	return run, nil
}

func (e *LeaderExecutor) persistRoute(ctx context.Context, attempt Attempt, policy LeaderRoutePolicy) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		insert into agent_run_routes(run_id,route,requested_route,effective_route,intent_reason_code,policy_reason_code)
		select r.id,$3,$4,$3,$5,$6 from agent_runs r join agent_jobs j on j.run_id=r.id
		where r.id=$1 and r.agent_role='leader' and r.status='running'
		  and j.id=$2 and j.status='running' and j.lease_token=$7::uuid and j.lease_expires_at>now()
		on conflict(run_id) do nothing
	`, attempt.RunID, attempt.JobID, policy.EffectiveRoute, policy.RequestedRoute, policy.IntentReason, policy.PolicyReason, attempt.LeaseToken); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) delegate(ctx context.Context, attempt Attempt, run leaderRunContext, policy LeaderRoutePolicy) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid bool
	if err := tx.QueryRow(ctx, `
		select exists(
			select 1 from agent_runs r join agent_jobs j on j.run_id=r.id
			where r.id=$1 and r.agent_role='leader' and r.status='running'
			  and j.id=$2 and j.status='running' and j.lease_token=$3::uuid and j.lease_expires_at>now()
		)
	`, attempt.RunID, attempt.JobID, attempt.LeaseToken).Scan(&valid); err != nil || !valid {
		if err != nil {
			return err
		}
		return ErrLeaseLost
	}
	childRunID := "run_" + uuid.NewString()
	childJobID := "job_" + uuid.NewString()
	sessionID := "dss_" + uuid.NewString()
	if _, err := tx.Exec(ctx, `
		insert into agent_run_routes(run_id,route,requested_route,effective_route,intent_reason_code,policy_reason_code)
		values($1,$2,$3,$2,$4,$5)
	`, attempt.RunID, policy.EffectiveRoute, policy.RequestedRoute, policy.IntentReason, policy.PolicyReason); err != nil {
		return err
	}
	if err := (DelegationKernel{}).CreateInTx(ctx, tx, CreateDelegationCommand{
		ParentAttempt: attempt, ChildRunID: childRunID, ChildJobID: childJobID,
		ChildRole: RoleResearch, ChildPromptVersion: "agent.research-planner@1",
	}); err != nil {
		return err
	}
	if _, err := sourcediscovery.NewStore(tx).EnsureResearchSession(ctx, sourcediscovery.ResearchSessionCommand{
		ID: sessionID, NotebookID: run.NotebookID, UserID: run.UserID, OriginChatID: run.ChatID,
		ResearchRunID: childRunID, Query: truncateLeaderRunes(run.Message, 500),
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update agent_runs set discovery_session_id=$3,updated_at=now()
		where id in ($1,$2) and discovery_session_id is null
	`, attempt.RunID, childRunID, sessionID); err != nil {
		return err
	}
	var childModel, childPromptVersion string
	if err := tx.QueryRow(ctx, `select model,prompt_version from agent_runs where id=$1`, childRunID).Scan(&childModel, &childPromptVersion); err != nil {
		return err
	}
	if err := StartRunTraceInTx(ctx, tx, childRunID, childModel, childPromptVersion, nil); err != nil {
		return err
	}
	if err := RecordAttemptWaitingInTx(ctx, tx, attempt.RunID, attempt.JobID, attempt.AttemptNo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) executeResearch(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	request := ResearchPlanRequest{Model: run.Model, UserMessage: run.Message}
	var queries []string
	var err error
	if traced, ok := e.planner.(TracedResearchPlanner); ok {
		traceContext, tracer, traceErr := e.modelTraceContext(ctx, attempt)
		if traceErr != nil {
			return traceErr
		}
		modelIdentity := TraceResearchPlanModelStartIdentity(attempt.RunID, attempt.AttemptNo)
		queries, err = traced.ExpandQueriesTraced(traceContext, tracer, request, ModelTraceOptions{
			StartIdentity: modelIdentity, RequestIdentity: modelIdentity + "/replay/request",
			DecisionIdentity: modelIdentity + "/replay/decision", ReplayStager: e.replayStager,
			Phase: ModelPhaseResearchQueryExpansion,
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
	sessionID := "dss_" + uuid.NewString()
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := e.requireLease(ctx, tx, attempt, "research"); err != nil {
		return err
	}
	if _, err := sourcediscovery.NewStore(tx).EnsureResearchSession(ctx, sourcediscovery.ResearchSessionCommand{
		ID: sessionID, NotebookID: run.NotebookID, UserID: run.UserID, OriginChatID: run.ChatID,
		ResearchRunID: attempt.RunID, Query: truncateLeaderRunes(run.Message, 500),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	resultGroups := make([][]websearch.Candidate, 0, len(queries))
	for _, query := range queries {
		results, searchErr := e.provider.Search(ctx, websearch.Request{Query: query, Count: 10})
		if searchErr != nil {
			return e.failResearch(ctx, attempt, sourcediscovery.SafeProviderError(searchErr))
		}
		resultGroups = append(resultGroups, results)
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
			join chat_chats chat on chat.id=child.chat_id
			join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=child.user_id
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
	if err := e.requireLease(ctx, tx, attempt, "leader"); err != nil {
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
	jobTag, err := tx.Exec(ctx, `
		update agent_jobs set status='succeeded',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now()
		where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid
	`, attempt.JobID, attempt.RunID, attempt.LeaseToken)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 || jobTag.RowsAffected() != 1 {
		return ErrLeaseLost
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
	if _, err := tx.Exec(ctx, `
		update source_discovery_sessions
		set status='failed',error_code=$2,completed_at=now(),updated_at=now()
		where research_run_id=$1 and origin='research_agent' and status='searching'
	`, childRunID, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_runs set status='failed',error_code=$2,finished_at=now(),updated_at=now() where id=$1 and status='running'`, attempt.RunID, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs set status='failed',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid`, attempt.JobID, attempt.RunID, attempt.LeaseToken); err != nil {
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
		where r.id=$1 and r.agent_role=$2 and r.status='running' and j.id=$3 and j.status='running'
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
