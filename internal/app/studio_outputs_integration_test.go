package app_test

import (
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
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

func TestStudioOutputAdmissionPinsConfiguredRootAndListsDurably(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "studio-owner@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-output")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio", "evr_studio", "src_studio_other", "evr_studio_other")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@2"),
	}, api.db)
	api.handler = api.server.Handler()

	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "report", "locale": "zh", "source_ids": []string{"src_studio"},
	}, owner, csrf, csrf.Value, "studio-report-one")
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var body struct {
		Output struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			Status      string `json:"status"`
			SourceCount int    `json:"source_count"`
			RunID       string `json:"run_id"`
		} `json:"output"`
	}
	decodeBody(t, created, &body)
	if body.Output.ID == "" || body.Output.RunID == "" || body.Output.Kind != "report" || body.Output.Status != "queued" || body.Output.SourceCount != 1 {
		t.Fatalf("output=%+v", body.Output)
	}

	var runtimeKind, definition, executor string
	var role, userID, chatID *string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select runtime_kind,definition_identity||'@'||definition_version::text,executor_identity,agent_role,user_id,chat_id
		from agent_runs where id=$1
	`, body.Output.RunID).Scan(&runtimeKind, &definition, &executor, &role, &userID, &chatID); err != nil {
		t.Fatal(err)
	}
	if runtimeKind != "configured" || definition != "studio.report@1" || executor != "studio_structured_output" || role != nil || userID != nil || chatID != nil {
		t.Fatalf("run kind=%s definition=%s executor=%s role=%v user=%v chat=%v", runtimeKind, definition, executor, role, userID, chatID)
	}

	retried := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "report", "locale": "zh", "source_ids": []string{"src_studio"},
	}, owner, csrf, csrf.Value, "studio-report-one")
	if retried.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
	}
	var retriedBody struct {
		Output struct {
			ID string `json:"id"`
		} `json:"output"`
	}
	decodeBody(t, retried, &retriedBody)
	if retriedBody.Output.ID != body.Output.ID {
		t.Fatalf("idempotent output=%q want=%q", retriedBody.Output.ID, body.Output.ID)
	}

	listed := api.getWithCookie(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", owner)
	if listed.Code != http.StatusOK || !containsJSONID(listed.Body.Bytes(), body.Output.ID) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
}

func TestStudioOutputViewerCanReadButCannotMutate(t *testing.T) {
	api := newTestAPI(t)
	owner, ownerCSRF := api.registerWithCSRF(t, "studio-authority-owner@example.com")
	viewer, viewerCSRF := api.registerWithCSRF(t, "studio-authority-viewer@example.com")
	intruder := api.register(t, "studio-authority-intruder@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-authority")
	viewerID := sourceTestUserID(t, api, "studio-authority-viewer@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'viewer')`, notebookID, viewerID); err != nil {
		t.Fatal(err)
	}
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio_authority", "evr_studio_authority", "", "")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@2")}, api.db)
	api.handler = api.server.Handler()
	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "data_table", "locale": "en", "source_ids": []string{"src_studio_authority"},
	}, owner, ownerCSRF, ownerCSRF.Value, "studio-authority-table")
	var body struct {
		Output struct {
			ID string `json:"id"`
		} `json:"output"`
	}
	decodeBody(t, created, &body)

	if listed := api.getWithCookie(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", viewer); listed.Code != http.StatusOK || !containsJSONID(listed.Body.Bytes(), body.Output.ID) {
		t.Fatalf("viewer list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if detail := api.getWithCookie(t, "/api/v1/studio-outputs/"+body.Output.ID, viewer); detail.Code != http.StatusOK {
		t.Fatalf("viewer detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	forbiddenCreate := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "report", "locale": "en", "source_ids": []string{"src_studio_authority"},
	}, viewer, viewerCSRF, viewerCSRF.Value, "studio-viewer-forbidden")
	if forbiddenCreate.Code != http.StatusForbidden {
		t.Fatalf("viewer create status=%d body=%s", forbiddenCreate.Code, forbiddenCreate.Body.String())
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/studio-outputs/"+body.Output.ID, nil)
	request.AddCookie(viewer)
	request.AddCookie(viewerCSRF)
	request.Header.Set("X-CSRF-Token", viewerCSRF.Value)
	deleted := httptest.NewRecorder()
	api.handler.ServeHTTP(deleted, request)
	if deleted.Code != http.StatusForbidden {
		t.Fatalf("viewer delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if detail := api.getWithCookie(t, "/api/v1/studio-outputs/"+body.Output.ID, intruder); detail.Code != http.StatusNotFound {
		t.Fatalf("intruder detail status=%d body=%s", detail.Code, detail.Body.String())
	}
}

func TestStudioOutputSSEReconnectBeginsFromDurableState(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "studio-sse@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-sse")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio_sse", "evr_studio_sse", "", "")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@2")}, api.db)
	api.handler = api.server.Handler()
	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "mind_map", "locale": "en", "source_ids": []string{"src_studio_sse"},
	}, owner, csrf, csrf.Value, "studio-sse-map")
	var body struct {
		Output struct {
			ID    string `json:"id"`
			RunID string `json:"run_id"`
		} `json:"output"`
	}
	decodeBody(t, created, &body)

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer, done := startSSE(t, api, requestCtx, "/api/v1/studio-outputs/"+body.Output.ID+"/events", owner)
	waitForSSEFlush(t, writer, done)
	if !strings.HasPrefix(writer.header.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(writer.body(), "event: studio-output") || !strings.Contains(writer.body(), `"id":"`+body.Output.ID+`"`) ||
		!strings.Contains(writer.body(), `"status":"queued"`) {
		t.Fatalf("headers=%v body=%s", writer.header, writer.body())
	}
	if _, err := api.db.Pool().Exec(context.Background(), `update agent_runs set status='running',started_at=now(),updated_at=now() where id=$1`, body.Output.RunID); err != nil {
		t.Fatal(err)
	}
	api.server.NotifyRun(body.Output.RunID)
	waitForSSEFlush(t, writer, done)
	if !strings.Contains(writer.body(), `"status":"running"`) {
		t.Fatalf("updated body=%s", writer.body())
	}
	cancel()
	waitForSSEStop(t, done)
}

func TestStudioExecutorNormalizesNumericFlashcardIDsBeforePublishing(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "studio-executor@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-executor")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio_exec", "evr_studio_exec", "src_studio_exec_other", "evr_studio_exec_other")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@2")}, api.db)
	api.handler = api.server.Handler()
	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "flashcards", "locale": "en", "source_ids": []string{"src_studio_exec"},
	}, owner, csrf, csrf.Value, "studio-executor-flashcards")
	var body struct {
		Output struct {
			ID    string `json:"id"`
			RunID string `json:"run_id"`
		} `json:"output"`
	}
	decodeBody(t, created, &body)
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != body.Output.RunID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	attempt := agent.Attempt{JobID: claimed.ID, RunID: claimed.RunID, AttemptNo: claimed.AttemptNo, LeaseToken: claimed.LeaseToken}
	model := &sequenceDecisionModel{decisions: []models.ModelDecision{
		{Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: "search_evidence", Input: json.RawMessage(`{"query":"key facts","purpose":"build flashcards"}`)}}}},
		{Final: &models.FinalDraft{Text: `{"title":"Source cards","cards":[{"id":1,"front":"Question 1","back":"Answer 1","source_ids":["src_studio_exec"]},{"id":2,"front":"Question 2","back":"Answer 2","source_ids":["src_studio_exec"]},{"id":3,"front":"Question 3","back":"Answer 3","source_ids":["src_studio_exec"]},{"id":4,"front":"Question 4","back":"Answer 4","source_ids":["src_studio_exec"]},{"id":5,"front":"Question 5","back":"Answer 5","source_ids":["src_studio_exec"]}]}`}},
	}}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil)
	search := agent.NewSearchEvidenceAction(emptyEvidenceBackend{result: retrieval.SearchResult{CompleteEmpty: true}})
	actions, err := agent.NewActionRegistry(search)
	if err != nil {
		t.Fatal(err)
	}
	mcpRegistry, err := agent.NewMCPToolRegistry(agent.MCPToolRegistration{Action: search, Scheduling: agentcatalog.ToolOrderedSync})
	if err != nil {
		t.Fatal(err)
	}
	host, err := agent.NewMCPToolHost(catalog, mcpRegistry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agent.NewStudioDefinitionExecutor(api.db.Pool(), runtime, model, actions, host, catalog)
	if err != nil {
		t.Fatal(err)
	}
	resolution := executor.ExecuteAttempt(context.Background(), attempt)
	if resolution.Disposition != agent.AttemptCompleted {
		t.Fatalf("resolution=%+v", resolution)
	}
	if len(model.requests) != 2 || model.requests[0].RequiredActionName != "search_evidence" || model.requests[1].RequiredActionName != "" {
		t.Fatalf("requests=%+v", model.requests)
	}
	detail := api.getWithCookie(t, "/api/v1/studio-outputs/"+body.Output.ID, owner)
	if detail.Code != http.StatusOK || !containsString(detail.Body.String(), `"status":"completed"`) ||
		!containsString(detail.Body.String(), `"title":"Source cards"`) || !containsString(detail.Body.String(), `"id":"1"`) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var resultCount int
	if err := api.db.Pool().QueryRow(context.Background(), `select count(*) from agent_run_results where producer_run_id=$1`, body.Output.RunID).Scan(&resultCount); err != nil || resultCount != 1 {
		t.Fatalf("result count=%d err=%v", resultCount, err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `update studio_outputs set artifact='{"title":"tampered"}'::jsonb where id=$1`, body.Output.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("completed artifact mutation err=%v", err)
	}
}

func TestStudioTerminalDispositionFailsOutputAndJob(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "studio-terminal@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-terminal")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio_terminal", "evr_studio_terminal", "src_studio_terminal_other", "evr_studio_terminal_other")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@2")}, api.db)
	api.handler = api.server.Handler()
	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "mind_map", "locale": "en", "source_ids": []string{"src_studio_terminal"},
	}, owner, csrf, csrf.Value, "studio-terminal-mind-map")
	var body struct {
		Output struct {
			ID    string `json:"id"`
			RunID string `json:"run_id"`
		} `json:"output"`
	}
	decodeBody(t, created, &body)
	queue := jobs.NewQueue(api.db.Pool())
	claimed, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != body.Output.RunID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	resolution, err := queue.ResolveAttempt(context.Background(), claimed, agent.AttemptResolution{
		Disposition: agent.AttemptTerminal, ErrorCode: "agent_execution_failed",
	})
	if err != nil || resolution.Disposition != agent.AttemptTerminal {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
	var outputStatus, outputError, runStatus, runError, jobStatus, jobError string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select o.status,o.error_code,r.status,r.error_code,j.status,j.last_error_code
		from studio_outputs o join agent_runs r on r.id=o.root_agent_run_id
		join agent_jobs j on j.run_id=r.id where o.id=$1
	`, body.Output.ID).Scan(&outputStatus, &outputError, &runStatus, &runError, &jobStatus, &jobError); err != nil {
		t.Fatal(err)
	}
	if outputStatus != "failed" || runStatus != "failed" || jobStatus != "failed" ||
		outputError != "agent_execution_failed" || runError != outputError || jobError != outputError {
		t.Fatalf("terminal states=%s/%s/%s errors=%s/%s/%s", outputStatus, runStatus, jobStatus, outputError, runError, jobError)
	}
}

func containsJSONID(payload []byte, id string) bool {
	return string(payload) != "" && len(id) > 0 && string(payload) != `{}` && string(payload) != `null` &&
		containsString(string(payload), `"id":"`+id+`"`)
}

func containsString(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
