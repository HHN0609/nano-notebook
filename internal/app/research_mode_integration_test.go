package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/skillcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

func TestResearchMessageAdmissionCreatesPlanningSessionAndPinnedRun(t *testing.T) {
	api := newTestAPI(t)
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@5"),
	}, api.db)
	api.handler = api.server.Handler()
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "research-admission@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)

	const messageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c701"
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": "Research open-source Agent Harness architectures and produce a decision report.",
		"mode": "research", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		MessageID         string `json:"message_id"`
		Mode              string `json:"mode"`
		ResearchSessionID string `json:"research_session_id"`
		RunID             string `json:"run_id"`
		Status            string `json:"status"`
	}
	decodeBody(t, response, &body)
	if body.MessageID != messageID || body.Mode != "research" || body.ResearchSessionID == "" || body.RunID == "" || body.Status != "planning" {
		t.Fatalf("body=%+v", body)
	}

	var sessionStatus, planningRunID, definitionIdentity, runStatus, jobStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select session.status,session.planning_run_id,run.definition_identity,run.status,job.status
		from research_sessions session
		join agent_runs run on run.id=session.planning_run_id
		join agent_jobs job on job.run_id=run.id
		where session.id=$1 and session.chat_id=$2 and session.input_message_id=$3
	`, body.ResearchSessionID, chatID, messageID).Scan(&sessionStatus, &planningRunID, &definitionIdentity, &runStatus, &jobStatus); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "planning" || planningRunID != body.RunID || definitionIdentity != "research.planner" || runStatus != "queued" || jobStatus != "queued" {
		t.Fatalf("session=%s planning=%s definition=%s run=%s job=%s", sessionStatus, planningRunID, definitionIdentity, runStatus, jobStatus)
	}
	snapshot := api.getWithCookie(t, "/api/v1/chats/"+chatID, sessionCookie)
	var chatBody struct {
		ResearchSessions []struct {
			ID             string `json:"id"`
			InputMessageID string `json:"input_message_id"`
			Status         string `json:"status"`
			PlanningRunID  string `json:"planning_run_id"`
		} `json:"research_sessions"`
	}
	decodeBody(t, snapshot, &chatBody)
	if len(chatBody.ResearchSessions) != 1 || chatBody.ResearchSessions[0].ID != body.ResearchSessionID || chatBody.ResearchSessions[0].InputMessageID != messageID || chatBody.ResearchSessions[0].Status != "planning" || chatBody.ResearchSessions[0].PlanningRunID != body.RunID {
		t.Fatalf("Research Session snapshot=%+v", chatBody.ResearchSessions)
	}
	cancelled := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+body.RunID+"/cancel", map[string]any{}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code, cancelled.Body.String())
	}
	researchSnapshot := api.getWithCookie(t, "/api/v1/research-sessions/"+body.ResearchSessionID, sessionCookie)
	if researchSnapshot.Code != http.StatusOK || !strings.Contains(researchSnapshot.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancelled Research snapshot status=%d body=%s", researchSnapshot.Code, researchSnapshot.Body.String())
	}
	retry := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+body.RunID+"/retry", map[string]any{}, sessionCookie, csrfCookie, csrfCookie.Value, "research-retry")
	if retry.Code != http.StatusConflict {
		t.Fatalf("Research retry status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestResearchPlannerTerminalFailureDoesNotLeaveSessionOrLeaseRunning(t *testing.T) {
	api := newTestAPI(t)
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@5"),
	}, api.db)
	api.handler = api.server.Handler()
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "research-planner-terminal@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c702", "content": "Research Agent Harness architectures.", "mode": "research",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	var admitted struct {
		ResearchSessionID string `json:"research_session_id"`
		RunID             string `json:"run_id"`
	}
	decodeBody(t, response, &admitted)
	queue := jobs.NewQueue(api.db.Pool())
	claimed, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != admitted.RunID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	resolution, err := queue.ResolveAttempt(context.Background(), claimed, agent.AttemptResolution{
		Disposition: agent.AttemptTerminal, ErrorCode: "model_invalid_response",
	})
	if err != nil || resolution.Disposition != agent.AttemptTerminal {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
	var sessionStatus, runStatus, jobStatus, sessionError, runError, jobError string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select session.status,run.status,job.status,session.error_code,run.error_code,job.last_error_code
		from research_sessions session
		join agent_runs run on run.id=session.planning_run_id
		join agent_jobs job on job.run_id=run.id
		where session.id=$1
	`, admitted.ResearchSessionID).Scan(&sessionStatus, &runStatus, &jobStatus, &sessionError, &runError, &jobError); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "failed" || runStatus != "failed" || jobStatus != "failed" ||
		sessionError != "model_invalid_response" || runError != sessionError || jobError != sessionError {
		t.Fatalf("terminal state=%s/%s/%s errors=%q/%q/%q", sessionStatus, runStatus, jobStatus, sessionError, runError, jobError)
	}
}

func TestResearchPlannerPublishesImmutablePlanWithoutChatAnswer(t *testing.T) {
	api := newTestAPI(t)
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@5")}, api.db)
	api.handler = api.server.Handler()
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "research-planner@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c703", "content": "Compare Agent Harness architectures for a platform team.", "mode": "research",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var admitted struct {
		ResearchSessionID string `json:"research_session_id"`
		RunID             string `json:"run_id"`
	}
	decodeBody(t, response, &admitted)
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != admitted.RunID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	plan := `{"title":"Agent Harness architecture research","objective":"Choose an architecture","scope":"Open-source harnesses","research_questions":["What are the control loops?"],"investigation_tracks":["Source code","Evaluations"],"source_strategy":["Primary repositories","Independent evaluations"],"analysis_method":["Shared comparison dimensions"],"deliverable_outline":["Executive summary","Comparison","Recommendation"],"completion_criteria":["Material claims are read-backed"],"clarifying_questions":[]}`
	var received struct {
		Messages []models.ModelMessage `json:"messages"`
		Tools    []any                 `json:"tools"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": plan}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 100, "total_tokens": 200},
		})
	}))
	defer upstream.Close()
	prompts := promptcatalog.MustLoadEmbedded()
	skills := skillcatalog.MustLoadEmbedded()
	runtime, err := agent.NewResearchPlanningRuntime(api.db.Pool(), prompts, skills)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agent.NewActionRegistry(agent.NewReadSkillAction(catalog, skills))
	if err != nil {
		t.Fatal(err)
	}
	model := models.NewBifrostClient(upstream.URL, upstream.Client(), 16384)
	if err := agent.NewController(runtime, model, registry).Execute(context.Background(), attemptFromClaim(claimed)); err != nil {
		t.Fatal(err)
	}
	if len(received.Messages) < 2 || !strings.Contains(received.Messages[0].Content, "Grill Me") || strings.Contains(received.Messages[0].Content, "Use this Skill only") || len(received.Tools) != 1 {
		t.Fatalf("progressive planner request messages=%+v tools=%d", received.Messages, len(received.Tools))
	}
	var status, runStatus, jobStatus, storedPlan string
	var assistants int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select session.status,run.status,job.status,plan.plan_json::text
		from research_sessions session
		join agent_runs run on run.id=session.planning_run_id
		join agent_jobs job on job.run_id=run.id
		join research_plan_versions plan on plan.session_id=session.id and plan.version=1
		where session.id=$1
	`, admitted.ResearchSessionID).Scan(&status, &runStatus, &jobStatus, &storedPlan); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(context.Background(), `select count(*) from chat_messages where chat_id=$1 and role='assistant'`, chatID).Scan(&assistants); err != nil {
		t.Fatal(err)
	}
	if status != "awaiting_confirmation" || runStatus != "completed" || jobStatus != "succeeded" || !strings.Contains(storedPlan, "Agent Harness") || assistants != 0 {
		t.Fatalf("session=%s run=%s job=%s plan=%s assistants=%d", status, runStatus, jobStatus, storedPlan, assistants)
	}
}

func TestMessageAdmissionRejectsUnknownMode(t *testing.T) {
	api := newTestAPI(t)
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "unknown-mode@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c702", "content": "Hello", "mode": "surprise",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResearchPlanCanBeEditedThenAcceptedIntoExecutionRun(t *testing.T) {
	api := newTestAPI(t)
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@5")}, api.db)
	api.handler = api.server.Handler()
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "research-plan-edit@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c704", "content": "Research Agent Harnesses.", "mode": "research",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	var admitted struct {
		ResearchSessionID string `json:"research_session_id"`
		RunID             string `json:"run_id"`
	}
	decodeBody(t, response, &admitted)
	basePlan := researchPlanFixture("Initial plan")
	basePlanJSON, _ := json.Marshal(basePlan)
	ctx := context.Background()
	if _, err := api.db.Pool().Exec(ctx, `
		insert into research_plan_versions(session_id,version,plan_json,producer_run_id,created_by)
		values($1,1,$2::jsonb,$3,'model')
	`, admitted.ResearchSessionID, string(basePlanJSON), admitted.RunID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update research_sessions set status='awaiting_confirmation' where id=$1`,
		`update agent_runs set status='completed',finished_at=now() where id=$1`,
		`update agent_jobs set status='succeeded',finished_at=now() where run_id=$1`,
	} {
		argument := admitted.RunID
		if strings.Contains(statement, "research_sessions") {
			argument = admitted.ResearchSessionID
		}
		if _, err := api.db.Pool().Exec(ctx, statement, argument); err != nil {
			t.Fatal(err)
		}
	}
	editedPlan := researchPlanFixture("Edited plan")
	editedPlan["scope"] = "Codex, Claude Code, DeepSeek and comparable open-source harnesses"
	edited := api.patchJSONWithCookieAndCSRF(t, "/api/v1/research-sessions/"+admitted.ResearchSessionID+"/plan", map[string]any{
		"plan": editedPlan,
	}, sessionCookie, csrfCookie, csrfCookie.Value)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	var editBody struct {
		Version int `json:"version"`
	}
	decodeBody(t, edited, &editBody)
	if editBody.Version != 2 {
		t.Fatalf("edit body=%+v", editBody)
	}
	started := api.postJSONWithCookieAndCSRF(t, "/api/v1/research-sessions/"+admitted.ResearchSessionID+"/start", map[string]any{
		"plan_version": 2, "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}
	var startBody struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	decodeBody(t, started, &startBody)
	if startBody.RunID == "" || startBody.Status != "queued" {
		t.Fatalf("start body=%+v", startBody)
	}
	var status, definition string
	var acceptedVersion int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select session.status,session.accepted_plan_version,run.definition_identity
		from research_sessions session join agent_runs run on run.id=session.execution_run_id
		where session.id=$1 and session.execution_run_id=$2
	`, admitted.ResearchSessionID, startBody.RunID).Scan(&status, &acceptedVersion, &definition); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || acceptedVersion != 2 || definition != "research.executor" {
		t.Fatalf("status=%s accepted=%d definition=%s", status, acceptedVersion, definition)
	}
	snapshot := api.getWithCookie(t, "/api/v1/research-sessions/"+admitted.ResearchSessionID, sessionCookie)
	if snapshot.Code != http.StatusOK || !strings.Contains(snapshot.Body.String(), "Edited plan") || !strings.Contains(snapshot.Body.String(), startBody.RunID) {
		t.Fatalf("snapshot status=%d body=%s", snapshot.Code, snapshot.Body.String())
	}
}

type researchSearchProvider struct{}

func (researchSearchProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	return []websearch.Candidate{
		{Title: "Harness source", URL: "https://example.com/harness", Description: "Primary source", Rank: 1},
		{Title: "Harness evaluation", URL: "https://example.org/eval", Description: "Independent evaluation", Rank: 2},
	}, nil
}

type researchReader struct{}

func (researchReader) Parse(_ context.Context, request webreader.Request) (webreader.Page, error) {
	return webreader.Page{Title: "Harness source", FinalURL: request.URL, Content: "# Architecture\n\nThe harness uses a checkpointed tool loop.", Engine: "readability", WordCount: 8}, nil
}

func TestResearchExecutorPersistsStepCapsulesEvidenceAndReportVersion(t *testing.T) {
	api := newTestAPI(t)
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@5")}, api.db)
	api.handler = api.server.Handler()
	sessionCookie, csrfCookie := api.registerWithCSRF(t, "research-executor@example.com")
	_, chatID := createNotebookAndChatForEvidenceSet(t, api, sessionCookie, csrfCookie)
	admission := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c705", "content": "Research Agent Harness architectures.", "mode": "research",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	var admitted struct {
		ResearchSessionID string `json:"research_session_id"`
		RunID             string `json:"run_id"`
	}
	decodeBody(t, admission, &admitted)
	planJSON, _ := json.Marshal(researchPlanFixture("Execution plan"))
	ctx := context.Background()
	if _, err := api.db.Pool().Exec(ctx, `insert into research_plan_versions(session_id,version,plan_json,producer_run_id,created_by) values($1,1,$2::jsonb,$3,'model')`, admitted.ResearchSessionID, string(planJSON), admitted.RunID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update research_sessions set status='awaiting_confirmation' where id=$1`,
		`update agent_runs set status='completed',finished_at=now() where id=$1`,
		`update agent_jobs set status='succeeded',finished_at=now() where run_id=$1`,
	} {
		argument := admitted.RunID
		if strings.Contains(statement, "research_sessions") {
			argument = admitted.ResearchSessionID
		}
		if _, err := api.db.Pool().Exec(ctx, statement, argument); err != nil {
			t.Fatal(err)
		}
	}
	started := api.postJSONWithCookieAndCSRF(t, "/api/v1/research-sessions/"+admitted.ResearchSessionID+"/start", map[string]any{"plan_version": 1}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	var startBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, started, &startBody)
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || claimed.RunID != startBody.RunID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		w.Header().Set("Content-Type", "application/json")
		switch modelCalls {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"search","type":"function","function":{"name":"web_search","arguments":"{\"queries\":[\"agent harness architecture\"]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"read","type":"function","function":{"name":"read_url","arguments":"{\"url\":\"https://example.com/harness\"}"}}]},"finish_reason":"tool_calls"}]}`))
		case 3:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"# Agent Harness report\n\n**Evidence Ledger status**: 99 successfully read sources.\n\n## Executive summary\n\nCheckpointed tool loops improve recovery. [Harness source](https://example.com/harness) An [unread lead](https://unread.example/lead) was not inspected.\n\n## Method and limitations\n\nPrimary source read through the URL reader."},"finish_reason":"stop"}]}`))
		default:
			t.Fatalf("unexpected model call %d", modelCalls)
		}
	}))
	defer upstream.Close()
	prompts := promptcatalog.MustLoadEmbedded()
	runtime, err := agent.NewResearchRuntime(api.db.Pool(), prompts)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agent.NewActionRegistry(
		agent.NewWebSearchAction(researchSearchProvider{}), agent.NewReadURLAction(researchReader{}), agent.NewSearchEvidenceAction(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	model := models.NewBifrostClient(upstream.URL, upstream.Client(), 16384)
	if err := agent.NewController(runtime, model, registry).Execute(ctx, attemptFromClaim(claimed)); err != nil {
		t.Fatal(err)
	}
	var sessionStatus, runStatus, jobStatus, report string
	var capsules, discovered, read, assistants int
	if err := api.db.Pool().QueryRow(ctx, `select session.status,run.status,job.status,report.content_markdown from research_sessions session join agent_runs run on run.id=session.execution_run_id join agent_jobs job on job.run_id=run.id join research_report_versions report on report.session_id=session.id and report.version=1 where session.id=$1`, admitted.ResearchSessionID).Scan(&sessionStatus, &runStatus, &jobStatus, &report); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from research_step_capsules where session_id=$1`, admitted.ResearchSessionID).Scan(&capsules); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) filter(where status='discovered'),count(*) filter(where status='read') from research_evidence_ledger where session_id=$1`, admitted.ResearchSessionID).Scan(&discovered, &read); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id=$1 and role='assistant'`, chatID).Scan(&assistants); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 3 || sessionStatus != "completed" || runStatus != "completed" || jobStatus != "succeeded" || capsules != 2 || discovered != 1 || read != 1 || assistants != 1 || !strings.Contains(report, "Harness source") || !strings.Contains(report, "1 successfully read") || strings.Contains(report, "99 successfully read") || strings.Contains(report, "https://unread.example/lead") || strings.Contains(report, "Publication note") {
		t.Fatalf("calls=%d session=%s run=%s job=%s capsules=%d discovered=%d read=%d assistants=%d report=%s", modelCalls, sessionStatus, runStatus, jobStatus, capsules, discovered, read, assistants, report)
	}
}

func researchPlanFixture(title string) map[string]any {
	return map[string]any{
		"title": title, "objective": "Produce a decision report", "scope": "Open-source Agent Harnesses",
		"research_questions": []string{"How do the loops differ?"}, "investigation_tracks": []string{"Code", "Evaluations"},
		"source_strategy": []string{"Primary repositories"}, "analysis_method": []string{"Shared dimensions"},
		"deliverable_outline": []string{"Summary", "Comparison", "Recommendation"},
		"completion_criteria": []string{"Important claims are read-backed"}, "clarifying_questions": []string{},
	}
}

func (api *testAPI) patchJSONWithCookieAndCSRF(t *testing.T, path string, payload map[string]any, cookie, csrfCookie *http.Cookie, csrfHeader string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrfHeader)
	request.AddCookie(cookie)
	request.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	return response
}
