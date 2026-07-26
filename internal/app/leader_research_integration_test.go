package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs/semconv"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type fixedLeaderRouter struct{ route agent.LeaderRoute }

func (r fixedLeaderRouter) DecideRoute(context.Context, agent.LeaderRouteRequest) (agent.LeaderRouteDecision, error) {
	reason := agent.LeaderReasonOrdinaryConversation
	if r.route == agent.LeaderDelegateResearch {
		reason = agent.LeaderReasonExplicitSourceDiscovery
	}
	return agent.LeaderRouteDecision{Route: r.route, ReasonCode: reason}, nil
}

type fixedResearchPlanner struct{ queries []string }

func (p fixedResearchPlanner) ExpandQueries(context.Context, agent.ResearchPlanRequest) ([]string, error) {
	return append([]string(nil), p.queries...), nil
}

type recordingNormalExecutor struct{ calls int }

func (e *recordingNormalExecutor) Execute(context.Context, agent.Attempt) error {
	e.calls++
	return nil
}

type recordingResearchProvider struct {
	requests []websearch.Request
	results  map[string][]websearch.Candidate
}

type callbackResearchProvider struct {
	search func(context.Context, websearch.Request) ([]websearch.Candidate, error)
}

type tracedResearchModel struct{ calls int }

func (m *tracedResearchModel) Decide(_ context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
	m.calls++
	name := "select_leader_route"
	payload := json.RawMessage(`{"route":"delegate_research","reason_code":"explicit_source_discovery"}`)
	if m.calls == 2 {
		name = "submit_research_queries"
		payload = json.RawMessage(`{"queries":["traced research"]}`)
	}
	input, output, total := int64(7), int64(2), int64(9)
	cost := 0.001
	return models.ModelOutcome{
		ModelDecision: models.ModelDecision{Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: name, Input: payload}}}},
		Metadata: models.ModelCallMetadata{
			RequestedModel: request.Model, SelectedProvider: "test-provider", SelectedModel: "trace-model",
			ResultKind: models.ModelResultActionProposal, FinishReason: "tool_calls",
			InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
			Cost: models.ModelCost{Known: true, Amount: &cost, Currency: "USD", Source: "test"},
		},
	}, nil
}

func (p callbackResearchProvider) Search(ctx context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	return p.search(ctx, request)
}

func (p *recordingResearchProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	return p.results[request.Query], nil
}

func TestLeaderDelegatesDurableResearchChildAndResumesWithPrivateDiscovery(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "leader-research@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-research")
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "leader-research-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	messageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c1"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": messageID, "content": "帮我搜集电影制作流程的资料", "time_zone": "Asia/Shanghai",
	}, owner, csrf, csrf.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admit status=%d body=%s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)

	provider := &recordingResearchProvider{results: map[string][]websearch.Candidate{
		"电影制作流程":   {{Title: "Film workflow", URL: "https://example.com/workflow?utm_source=one", DisplayURL: "example.com/workflow", Description: "Workflow", Rank: 1}},
		"剧本 拍摄 后期": {{Title: "Film workflow duplicate", URL: "https://example.com/workflow#part", DisplayURL: "example.com/workflow", Description: "Duplicate", Rank: 1}, {Title: "Editing", URL: "https://example.org/editing", DisplayURL: "example.org/editing", Description: "Editing", Rank: 2}},
	}}
	normal := &recordingNormalExecutor{}
	validator := &researchCandidateValidator{accepted: map[string]bool{"https://example.com/workflow?utm_source=one": true}}
	executor := agent.NewLeaderExecutor(
		api.db.Pool(), normal, fixedLeaderRouter{route: agent.LeaderDelegateResearch},
		fixedResearchPlanner{queries: []string{"电影制作流程", "剧本 拍摄 后期"}}, provider,
		agent.WithResearchCandidateValidator(validator),
	)
	queue := jobs.NewQueue(api.db.Pool())

	parentJob, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || parentJob.RunID != admittedBody.RunID {
		t.Fatalf("parent claim=%+v ok=%v err=%v", parentJob, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: parentJob.ID, RunID: parentJob.RunID, AttemptNo: parentJob.AttemptNo, LeaseToken: parentJob.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	var parentJobStatus, parentLease, route, childRunID, childRole string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select parent_job.status,coalesce(parent_job.lease_token::text,''),route.route,child.id,child.agent_role
		from agent_runs parent
		join agent_jobs parent_job on parent_job.run_id=parent.id
		join agent_run_routes route on route.run_id=parent.id
		join agent_run_delegations delegation on delegation.parent_run_id=parent.id
		join agent_runs child on child.id=delegation.child_run_id
		where parent.id=$1
	`, admittedBody.RunID).Scan(&parentJobStatus, &parentLease, &route, &childRunID, &childRole); err != nil {
		t.Fatal(err)
	}
	if parentJobStatus != "waiting" || parentLease != "" || route != "delegate_research" || childRole != "research" || normal.calls != 0 {
		t.Fatalf("parent job=%q lease=%q route=%q child=%q normal=%d", parentJobStatus, parentLease, route, childRole, normal.calls)
	}
	var parentSessionID, childSessionID *string
	var searchingSessionStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select parent.discovery_session_id,child.discovery_session_id,coalesce(session.status,'')
		from agent_runs parent
		join agent_run_delegations delegation on delegation.parent_run_id=parent.id
		join agent_runs child on child.id=delegation.child_run_id
		left join source_discovery_sessions session on session.id=parent.discovery_session_id
		where parent.id=$1
	`, admittedBody.RunID).Scan(&parentSessionID, &childSessionID, &searchingSessionStatus); err != nil {
		t.Fatal(err)
	}
	if parentSessionID == nil || childSessionID == nil || *parentSessionID != *childSessionID || searchingSessionStatus != "searching" {
		t.Fatalf("delegated Session parent=%v child=%v status=%q", parentSessionID, childSessionID, searchingSessionStatus)
	}
	searchingProjection := api.getWithCookie(t, "/api/v1/chats/"+chatBody.Chat.ID, owner)
	if searchingProjection.Code != http.StatusOK || !strings.Contains(searchingProjection.Body.String(), *parentSessionID) {
		t.Fatalf("searching projection status=%d body=%s", searchingProjection.Code, searchingProjection.Body.String())
	}

	childJob, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || childJob.RunID != childRunID {
		t.Fatalf("child claim=%+v ok=%v err=%v", childJob, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: childJob.ID, RunID: childJob.RunID, AttemptNo: childJob.AttemptNo, LeaseToken: childJob.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 || provider.requests[0].Count != 10 || provider.requests[1].Count != 10 {
		t.Fatalf("provider requests=%+v", provider.requests)
	}
	var sessionID, childStatus, requeuedStatus string
	var candidateCount int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select child.discovery_session_id,child.status,parent_job.status,
		  (select count(*) from source_discovery_candidates where session_id=child.discovery_session_id)
		from agent_runs child
		join agent_run_delegations delegation on delegation.child_run_id=child.id
		join agent_jobs parent_job on parent_job.run_id=delegation.parent_run_id
		where child.id=$1
	`, childRunID).Scan(&sessionID, &childStatus, &requeuedStatus, &candidateCount); err != nil {
		t.Fatal(err)
	}
	if sessionID == "" || childStatus != "completed" || requeuedStatus != "queued" || candidateCount != 1 {
		t.Fatalf("child=%q parent job=%q session=%q candidates=%d", childStatus, requeuedStatus, sessionID, candidateCount)
	}
	if len(validator.urls) != 2 {
		t.Fatalf("validated Research URLs=%+v", validator.urls)
	}
	if sessionID != *parentSessionID {
		t.Fatalf("completed Session=%q, want delegated Session=%q", sessionID, *parentSessionID)
	}

	resumed, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || resumed.RunID != admittedBody.RunID {
		t.Fatalf("resumed claim=%+v ok=%v err=%v", resumed, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: resumed.ID, RunID: resumed.RunID, AttemptNo: resumed.AttemptNo, LeaseToken: resumed.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	var assistantText, leaderStatus string
	var memberRunCount, childMessageCount int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select message.content,leader.status,
		  (select count(*) from agent_runs where chat_id=leader.chat_id and agent_role='leader'),
		  (select count(*) from chat_messages where id=(select output_message_id from agent_runs where id=$2))
		from agent_runs leader join chat_messages message on message.id=leader.output_message_id
		where leader.id=$1
	`, admittedBody.RunID, childRunID).Scan(&assistantText, &leaderStatus, &memberRunCount, &childMessageCount); err != nil {
		t.Fatal(err)
	}
	if leaderStatus != "completed" || childMessageCount != 0 || strings.Contains(assistantText, "2") || strings.Contains(assistantText, "two") || !strings.Contains(assistantText, "资料") {
		t.Fatalf("leader status=%q text=%q child messages=%d member runs=%d", leaderStatus, assistantText, childMessageCount, memberRunCount)
	}
	projection := api.getWithCookie(t, "/api/v1/chats/"+chatBody.Chat.ID, owner)
	if projection.Code != http.StatusOK || !strings.Contains(projection.Body.String(), sessionID) || strings.Contains(projection.Body.String(), childRunID) {
		t.Fatalf("member projection status=%d body=%s", projection.Code, projection.Body.String())
	}
}

type researchCandidateValidator struct {
	accepted map[string]bool
	urls     []string
}

func (v *researchCandidateValidator) Validate(_ context.Context, rawURL string) bool {
	v.urls = append(v.urls, rawURL)
	return v.accepted[rawURL]
}

func TestLeaderAndResearchModelUsageAndReplayAppearInTheirRunTraces(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "leader-research-trace@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-research-trace")
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "leader-research-trace-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c8", "content": "帮我搜索可观测性资料", "time_zone": "Asia/Shanghai",
	}, owner, csrf, csrf.Value, "")
	var admission struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admission)

	sink := &capturingDirectTraceSink{}
	keyProvider, err := replay.NewDevelopmentKeyProvider("leader-replay-test-key", bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := replay.NewSealer(keyProvider)
	if err != nil {
		t.Fatal(err)
	}
	replayStager, err := replay.NewObjectStager(sealer, objectstore.NewMemoryStore(), replay.StagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	queue := jobs.NewQueueWithTraceSink(api.db.Pool(), sink)
	model := &tracedResearchModel{}
	executor := agent.NewLeaderExecutor(
		api.db.Pool(), &recordingNormalExecutor{}, agent.NewModelLeaderRouter(model),
		agent.NewModelResearchPlanner(model), &recordingResearchProvider{}, agent.WithLeaderTraceSink(sink),
		agent.WithLeaderReplayStager(replayStager),
	)
	parent, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || parent.RunID != admission.RunID {
		t.Fatalf("parent claim=%+v ok=%v err=%v", parent, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: parent.ID, RunID: parent.RunID, AttemptNo: parent.AttemptNo, LeaseToken: parent.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	child, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("child claim=%+v ok=%v err=%v", child, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: child.ID, RunID: child.RunID, AttemptNo: child.AttemptNo, LeaseToken: child.LeaseToken}); err != nil {
		t.Fatal(err)
	}

	modelCalls := map[string]agentobs.Record{}
	for _, envelope := range sink.envelopes {
		if envelope.Record.Kind == agentobs.RecordSpanEnded && envelope.Record.Name == semconv.ModelCall {
			modelCalls[envelope.Trace.RunID] = envelope.Record
		}
	}
	for _, runID := range []string{parent.RunID, child.RunID} {
		record, found := modelCalls[runID]
		if !found || traceAttribute(record, semconv.ModelNameKey) != "trace-model" || traceAttribute(record, semconv.TokenTotalKey) != "9" {
			t.Fatalf("Run %s model call=%+v all envelopes=%+v", runID, record, sink.envelopes)
		}
		var replayClasses []replay.Class
		for _, envelope := range sink.envelopes {
			if envelope.Trace.RunID == runID && envelope.Record.Name == semconv.ModelCall {
				for _, attachment := range envelope.Attachments {
					replayClasses = append(replayClasses, attachment.Class)
				}
			}
		}
		if len(replayClasses) != 2 || replayClasses[0] != replay.ClassModelRequest || replayClasses[1] != replay.ClassModelDecision {
			t.Fatalf("Run %s Replay classes=%v, want model request and decision", runID, replayClasses)
		}
	}
}

func TestLeaderContinueChatNeverCallsWebSearch(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "leader-continue@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-continue")
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "leader-continue-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c2", "content": "解释一下蒙太奇", "time_zone": "Asia/Shanghai",
	}, owner, csrf, csrf.Value, "")
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &body)
	job, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", job, ok, err)
	}
	normal := &recordingNormalExecutor{}
	provider := &recordingResearchProvider{}
	executor := agent.NewLeaderExecutor(api.db.Pool(), normal, fixedLeaderRouter{route: agent.LeaderContinueChat}, fixedResearchPlanner{}, provider)
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: job.ID, RunID: job.RunID, AttemptNo: job.AttemptNo, LeaseToken: job.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	if normal.calls != 1 || len(provider.requests) != 0 {
		t.Fatalf("normal=%d web=%d", normal.calls, len(provider.requests))
	}
	var route string
	if err := api.db.Pool().QueryRow(context.Background(), `select route from agent_run_routes where run_id=$1`, body.RunID).Scan(&route); err != nil || route != "continue_chat" {
		t.Fatalf("route=%q err=%v", route, err)
	}
}

func TestCancellingWaitingLeaderAlsoCancelsResearchChild(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "leader-cancel@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-cancel")
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "leader-cancel-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c3", "content": "帮我搜索资料", "time_zone": "Asia/Shanghai",
	}, owner, csrf, csrf.Value, "")
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	queue := jobs.NewQueue(api.db.Pool())
	job, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", job, ok, err)
	}
	executor := agent.NewLeaderExecutor(api.db.Pool(), &recordingNormalExecutor{}, fixedLeaderRouter{route: agent.LeaderDelegateResearch}, fixedResearchPlanner{queries: []string{"资料"}}, &recordingResearchProvider{})
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: job.ID, RunID: job.RunID, AttemptNo: job.AttemptNo, LeaseToken: job.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	cancelled := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+admittedBody.RunID+"/cancel", map[string]any{}, owner, csrf, csrf.Value, "")
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code, cancelled.Body.String())
	}
	var parentStatus, parentJobStatus, childStatus, childJobStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select parent.status,parent_job.status,child.status,child_job.status
		from agent_runs parent join agent_jobs parent_job on parent_job.run_id=parent.id
		join agent_run_delegations delegation on delegation.parent_run_id=parent.id
		join agent_runs child on child.id=delegation.child_run_id join agent_jobs child_job on child_job.run_id=child.id
		where parent.id=$1
	`, admittedBody.RunID).Scan(&parentStatus, &parentJobStatus, &childStatus, &childJobStatus); err != nil {
		t.Fatal(err)
	}
	if parentStatus != "cancelled" || parentJobStatus != "cancelled" || childStatus != "cancelled" || childJobStatus != "cancelled" {
		t.Fatalf("parent=%s/%s child=%s/%s", parentStatus, parentJobStatus, childStatus, childJobStatus)
	}
}

func TestViewerCannotTriggerResearchDelegation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "leader-viewer-owner@example.com")
	viewer, csrf := api.registerWithCSRF(t, "leader-viewer@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-viewer")
	viewerID := sourceTestUserID(t, api, "leader-viewer@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'viewer')`, notebookID, viewerID); err != nil {
		t.Fatal(err)
	}
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, viewer, csrf, csrf.Value, "leader-viewer-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c4", "content": "帮我搜索外部资料", "time_zone": "Asia/Shanghai",
	}, viewer, csrf, csrf.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admit status=%d body=%s", admitted.Code, admitted.Body.String())
	}
	job, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", job, ok, err)
	}
	normal := &recordingNormalExecutor{}
	provider := &recordingResearchProvider{}
	executor := agent.NewLeaderExecutor(api.db.Pool(), normal, fixedLeaderRouter{route: agent.LeaderDelegateResearch}, fixedResearchPlanner{queries: []string{"external"}}, provider)
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: job.ID, RunID: job.RunID, AttemptNo: job.AttemptNo, LeaseToken: job.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	var childCount int
	if err := api.db.Pool().QueryRow(context.Background(), `select count(*) from agent_run_delegations where parent_run_id=$1`, job.RunID).Scan(&childCount); err != nil {
		t.Fatal(err)
	}
	if normal.calls != 1 || len(provider.requests) != 0 || childCount != 0 {
		t.Fatalf("normal=%d web=%d children=%d", normal.calls, len(provider.requests), childCount)
	}
}

func TestRoleDowngradePreventsLateResearchCandidatePublication(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "leader-downgrade-owner@example.com")
	editor, csrf := api.registerWithCSRF(t, "leader-downgrade-editor@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "leader-downgrade")
	editorID := sourceTestUserID(t, api, "leader-downgrade-editor@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'editor')`, notebookID, editorID); err != nil {
		t.Fatal(err)
	}
	chatResponse := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, editor, csrf, csrf.Value, "leader-downgrade-chat")
	var chatBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, chatResponse, &chatBody)
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatBody.Chat.ID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c0c5", "content": "帮我搜索外部资料", "time_zone": "Asia/Shanghai",
	}, editor, csrf, csrf.Value, "")
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	queue := jobs.NewQueue(api.db.Pool())
	parentJob, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("parent claim=%+v ok=%v err=%v", parentJob, ok, err)
	}
	provider := callbackResearchProvider{search: func(ctx context.Context, _ websearch.Request) ([]websearch.Candidate, error) {
		if _, err := api.db.Pool().Exec(ctx, `update notebook_memberships set role='viewer' where notebook_id=$1 and user_id=$2`, notebookID, editorID); err != nil {
			return nil, err
		}
		return []websearch.Candidate{{Title: "Late result", URL: "https://example.com/late", DisplayURL: "example.com/late", Rank: 1}}, nil
	}}
	executor := agent.NewLeaderExecutor(api.db.Pool(), &recordingNormalExecutor{}, fixedLeaderRouter{route: agent.LeaderDelegateResearch}, fixedResearchPlanner{queries: []string{"external"}}, provider)
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: parentJob.ID, RunID: parentJob.RunID, AttemptNo: parentJob.AttemptNo, LeaseToken: parentJob.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	childJob, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok {
		t.Fatalf("child claim=%+v ok=%v err=%v", childJob, ok, err)
	}
	if err := executor.Execute(context.Background(), agent.Attempt{JobID: childJob.ID, RunID: childJob.RunID, AttemptNo: childJob.AttemptNo, LeaseToken: childJob.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	var childStatus, sessionStatus, parentJobStatus string
	var candidateCount int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select child.status,session.status,parent_job.status,(select count(*) from source_discovery_candidates where session_id=session.id)
		from agent_runs child join source_discovery_sessions session on session.research_run_id=child.id
		join agent_run_delegations delegation on delegation.child_run_id=child.id
		join agent_jobs parent_job on parent_job.run_id=delegation.parent_run_id where child.id=$1
	`, childJob.RunID).Scan(&childStatus, &sessionStatus, &parentJobStatus, &candidateCount); err != nil {
		t.Fatal(err)
	}
	if childStatus != "failed" || sessionStatus != "failed" || parentJobStatus != "queued" || candidateCount != 0 {
		t.Fatalf("child=%s session=%s parent=%s candidates=%d", childStatus, sessionStatus, parentJobStatus, candidateCount)
	}
}
