package app_test

import (
	"context"
	"encoding/json"
	"net/http"
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

func TestStudioExecutorSearchesThenPublishesValidatedArtifact(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "studio-executor@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "studio-executor")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_studio_exec", "evr_studio_exec", "src_studio_exec_other", "evr_studio_exec_other")
	catalog := agentcatalog.MustLoadEmbedded()
	api.server = app.NewServer(app.Config{CookieSecure: false, AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@2")}, api.db)
	api.handler = api.server.Handler()
	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/studio-outputs", map[string]any{
		"kind": "report", "locale": "en", "source_ids": []string{"src_studio_exec"},
	}, owner, csrf, csrf.Value, "studio-executor-report")
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
		{Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: "search_evidence", Input: json.RawMessage(`{"query":"key facts","purpose":"build report"}`)}}}},
		{Final: &models.FinalDraft{Text: `{"title":"Source brief","summary":"A concise summary.","sections":[{"id":"overview","heading":"Overview","markdown":"Grounded content.","source_ids":["src_studio_exec"]}]}`}},
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
	if detail.Code != http.StatusOK || !containsString(detail.Body.String(), `"status":"completed"`) || !containsString(detail.Body.String(), `"title":"Source brief"`) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var resultCount int
	if err := api.db.Pool().QueryRow(context.Background(), `select count(*) from agent_run_results where producer_run_id=$1`, body.Output.RunID).Scan(&resultCount); err != nil || resultCount != 1 {
		t.Fatalf("result count=%d err=%v", resultCount, err)
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
