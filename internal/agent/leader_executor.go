package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
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
	pool      *pgxpool.Pool
	normal    AttemptExecutor
	router    LeaderRouter
	planner   ResearchPlanner
	provider  websearch.Provider
	traceSink TraceSink
}

type LeaderExecutorOption func(*LeaderExecutor)

func WithLeaderTraceSink(sink TraceSink) LeaderExecutorOption {
	return func(executor *LeaderExecutor) { executor.traceSink = sink }
}

func NewLeaderExecutor(pool *pgxpool.Pool, normal AttemptExecutor, router LeaderRouter, planner ResearchPlanner, provider websearch.Provider, options ...LeaderExecutorOption) *LeaderExecutor {
	executor := &LeaderExecutor{pool: pool, normal: normal, router: router, planner: planner, provider: provider, traceSink: DiscardTraceSink{}}
	for _, option := range options {
		option(executor)
	}
	return executor
}

type leaderRunContext struct {
	Role            string
	UserID          string
	ChatID          string
	NotebookID      string
	Message         string
	Model           string
	PromptVersion   string
	TimeZone        string
	MemberRole      string
	ExistingRoute   *LeaderRoute
	DelegationState *string
}

func (e *LeaderExecutor) Execute(ctx context.Context, attempt Attempt) error {
	if e == nil || e.pool == nil || e.normal == nil || e.router == nil || e.planner == nil || e.provider == nil {
		return errors.New("Leader Executor dependencies are incomplete")
	}
	scope, err := NewTraceScope(e.traceSink)
	if err != nil {
		return err
	}
	defer scope.Rollback()
	traceCtx := ContextWithTraceScope(ctx, scope)
	if err := e.execute(traceCtx, attempt); err != nil {
		return err
	}
	scope.PublishAfterCommit(ctx)
	return nil
}

func (e *LeaderExecutor) execute(ctx context.Context, attempt Attempt) error {
	run, err := e.loadRun(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	if run.Role == "research" {
		return e.executeResearch(ctx, attempt, run)
	}
	if run.Role != "leader" {
		return ErrInvalidLeaderRoute
	}
	if run.ExistingRoute == nil {
		route, err := e.router.DecideRoute(ctx, LeaderRouteRequest{Model: run.Model, UserMessage: run.Message})
		if err != nil {
			return err
		}
		// Viewing a notebook never grants permission to discover or import Sources.
		if route == LeaderDelegateResearch && run.MemberRole != "owner" && run.MemberRole != "editor" {
			route = LeaderContinueChat
		}
		if route == LeaderContinueChat {
			if err := e.persistRoute(ctx, attempt, route); err != nil {
				return err
			}
			return e.normal.Execute(ctx, attempt)
		}
		return e.delegate(ctx, attempt, run)
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
			coalesce(member.role,''),route.route,delegation.state
		from agent_runs r
		join chat_chats c on c.id=r.chat_id
		join chat_messages m on m.id=r.input_message_id
		left join notebook_memberships member on member.notebook_id=c.notebook_id and member.user_id=r.user_id
		left join agent_run_routes route on route.run_id=case when r.agent_role='leader' then r.id else r.parent_run_id end
		left join agent_research_delegations delegation on delegation.parent_run_id=case when r.agent_role='leader' then r.id else r.parent_run_id end
		where r.id=$1
	`, runID).Scan(&run.Role, &run.UserID, &run.ChatID, &run.NotebookID, &run.Message, &run.Model,
		&run.PromptVersion, &run.TimeZone, &run.MemberRole, &route, &delegationState)
	if err != nil {
		return leaderRunContext{}, err
	}
	if route != nil {
		parsed := LeaderRoute(*route)
		run.ExistingRoute = &parsed
	}
	run.DelegationState = delegationState
	if err := tx.Commit(ctx); err != nil {
		return leaderRunContext{}, err
	}
	return run, nil
}

func (e *LeaderExecutor) persistRoute(ctx context.Context, attempt Attempt, route LeaderRoute) error {
	tx, err := e.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		insert into agent_run_routes(run_id,route)
		select r.id,$3 from agent_runs r join agent_jobs j on j.run_id=r.id
		where r.id=$1 and r.agent_role='leader' and r.status='running'
		  and j.id=$2 and j.status='running' and j.lease_token=$4::uuid and j.lease_expires_at>now()
		on conflict(run_id) do nothing
	`, attempt.RunID, attempt.JobID, route, attempt.LeaseToken); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) delegate(ctx context.Context, attempt Attempt, run leaderRunContext) error {
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
	if _, err := tx.Exec(ctx, `insert into agent_run_routes(run_id,route) values($1,'delegate_research')`, attempt.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_runs(
			id,user_id,chat_id,input_message_id,status,model,prompt_version,agent_config_id,time_zone,
			deadline_at,action_decision_limit,final_decision_limit,action_limit,action_batch_limit,
			action_result_byte_limit,action_results_byte_limit,agent_role,parent_run_id
		)
		select $2,user_id,chat_id,input_message_id,'queued',model,prompt_version,agent_config_id,time_zone,
			deadline_at,action_decision_limit,final_decision_limit,action_limit,action_batch_limit,
			action_result_byte_limit,action_results_byte_limit,'research',id
		from agent_runs where id=$1
	`, attempt.RunID, childRunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into agent_jobs(id,kind,run_id,status) values($1,'agent_run',$2,'queued')`, childJobID, childRunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_research_delegations(parent_run_id,child_run_id,state) values($1,$2,'waiting')
	`, attempt.RunID, childRunID); err != nil {
		return err
	}
	if err := StartRunTraceInTx(ctx, tx, childRunID, run.Model, run.PromptVersion, nil); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		update agent_jobs set status='waiting',lease_token=null,lease_expires_at=null,updated_at=now()
		where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid
	`, attempt.JobID, attempt.RunID, attempt.LeaseToken)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if err := RecordAttemptWaitingInTx(ctx, tx, attempt.RunID, attempt.JobID, attempt.AttemptNo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_jobs',$1)`, childJobID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *LeaderExecutor) executeResearch(ctx context.Context, attempt Attempt, run leaderRunContext) error {
	queries, err := e.planner.ExpandQueries(ctx, ResearchPlanRequest{Model: run.Model, UserMessage: run.Message})
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
	encodedQueries, err := json.Marshal(queries)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update agent_research_delegations set expanded_queries=$2::jsonb,updated_at=now()
		where child_run_id=$1 and state='waiting'
	`, attempt.RunID, string(encodedQueries)); err != nil {
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
	return e.completeResearch(ctx, attempt, mergeResearchCandidates(resultGroups))
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
	if _, err := tx.Exec(ctx, `update agent_runs set status='completed',discovery_session_id=$2,finished_at=now(),updated_at=now() where id=$1 and status='running'`, attempt.RunID, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs set status='succeeded',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid`, attempt.JobID, attempt.RunID, attempt.LeaseToken); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_research_delegations set state='ready',discovery_session_id=$2,completed_at=now(),updated_at=now() where child_run_id=$1 and state='waiting'`, attempt.RunID, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_runs parent set status='queued',updated_at=now() from agent_research_delegations d where d.child_run_id=$1 and parent.id=d.parent_run_id and parent.status='running'`, attempt.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs parent_job set status='queued',updated_at=now() from agent_research_delegations d where d.child_run_id=$1 and parent_job.run_id=d.parent_run_id and parent_job.status='waiting'`, attempt.RunID); err != nil {
		return err
	}
	if err := RecordRunTerminalInTx(ctx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "completed", SpanStatus: agentobs.StatusOK, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_jobs',parent_run_id) from agent_research_delegations where child_run_id=$1`, attempt.RunID); err != nil {
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
	if _, err := tx.Exec(ctx, `update agent_runs set status='failed',error_code=$2,finished_at=now(),updated_at=now() where id=$1 and status='running'`, attempt.RunID, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs set status='failed',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where id=$1 and status='running' and lease_token=$2::uuid`, attempt.JobID, attempt.LeaseToken); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_research_delegations set state='failed',error_code=$2,completed_at=now(),updated_at=now() where child_run_id=$1 and state='waiting'`, attempt.RunID, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_runs parent set status='queued',updated_at=now() from agent_research_delegations d where d.child_run_id=$1 and parent.id=d.parent_run_id and parent.status='running'`, attempt.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs parent_job set status='queued',updated_at=now() from agent_research_delegations d where d.child_run_id=$1 and parent_job.run_id=d.parent_run_id and parent_job.status='waiting'`, attempt.RunID); err != nil {
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
		select state,discovery_session_id,error_code from agent_research_delegations
		where parent_run_id=$1 for update
	`, attempt.RunID).Scan(&state, &sessionID, &errorCode); err != nil {
		return err
	}
	if state == "failed" {
		code := "research_failed"
		if errorCode != nil {
			code = *errorCode
		}
		return e.failLeaderInTx(ctx, tx, attempt, code)
	}
	if state != "ready" || sessionID == nil {
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
	if _, err := tx.Exec(ctx, `update agent_research_delegations set state='consumed',updated_at=now() where parent_run_id=$1 and state='ready'`, attempt.RunID); err != nil {
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
	if _, err := tx.Exec(ctx, `update agent_runs set status='failed',error_code=$2,finished_at=now(),updated_at=now() where id=$1 and status='running'`, attempt.RunID, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_jobs set status='failed',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid`, attempt.JobID, attempt.RunID, attempt.LeaseToken); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update agent_research_delegations set state='consumed',updated_at=now() where parent_run_id=$1`, attempt.RunID); err != nil {
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
