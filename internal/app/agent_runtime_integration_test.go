package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/jackc/pgx/v5"
)

func TestWorkerClaimsBuildsContextAndPublishesOneAnswer(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "worker-happy@example.com")
	const messageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c005"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id":      messageID,
		"content": "Why is a publication barrier useful?",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status = %d, body = %s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)

	ctx := context.Background()
	queue := jobs.NewQueue(api.db.Pool())
	claimed, ok, err := queue.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || claimed.RunID != admittedBody.RunID {
		t.Fatalf("claimed = %+v ok=%v, want run %q", claimed, ok, admittedBody.RunID)
	}

	var modelRequest struct {
		Model               string                `json:"model"`
		Messages            []models.ModelMessage `json:"messages"`
		Stream              bool                  `json:"stream"`
		MaxCompletionTokens int                   `json:"max_completion_tokens"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("Bifrost request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&modelRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"It makes provisional output durable exactly once."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}
		}`))
	}))
	defer upstream.Close()
	model := models.NewBifrostClient(upstream.URL, upstream.Client(), 2048)
	runtime := agent.NewPostgresRuntime(
		api.db.Pool(),
		"System prompt for the bare agent.",
		func() string { return "msg_worker_answer" },
		agent.WithGroundingService(agent.NewGroundingService(api.db.Pool())),
	)
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.NewController(runtime, model, registry).Execute(ctx, attemptFromClaim(claimed)); err != nil {
		t.Fatal(err)
	}
	if modelRequest.Model != "aliyun/qwen-plus" || modelRequest.Stream || modelRequest.MaxCompletionTokens != 2048 {
		t.Fatalf("model request = %+v", modelRequest)
	}
	if len(modelRequest.Messages) != 3 || modelRequest.Messages[0].Role != "system" || modelRequest.Messages[1].Role != "user" || modelRequest.Messages[1].Content != "Why is a publication barrier useful?" ||
		!isAgentStatusMessage(string(modelRequest.Messages[2].Role), modelRequest.Messages[2].Content) {
		t.Fatalf("model context = %+v", modelRequest.Messages)
	}

	var runStatus, jobStatus, outputMessageID, role, content string
	if err := api.db.Pool().QueryRow(ctx, `
		select status, output_message_id
		from agent_runs where id = $1`, admittedBody.RunID).
		Scan(&runStatus, &outputMessageID); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select status from agent_jobs where run_id = $1`, admittedBody.RunID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select role, content from chat_messages where id = $1`, outputMessageID).Scan(&role, &content); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || jobStatus != "succeeded" || outputMessageID != "msg_worker_answer" {
		t.Fatalf("terminal state run=%s job=%s output=%s", runStatus, jobStatus, outputMessageID)
	}
	if role != "assistant" || content != "It makes provisional output durable exactly once." {
		t.Fatalf("published message role=%q content=%q", role, content)
	}
}

func TestControllerExecutesBifrostActionBatchAndPublishesFinal(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "controller-actions@example.com")
	const messageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c072"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": "Calculate two values, then summarize.", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status = %d, body = %s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("claim = %+v ok=%t err=%v", claimed, ok, err)
	}

	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch modelCalls {
		case 1:
			if len(request.Tools) != 2 || request.Tools[0].Function.Name != "calculate" || request.Tools[1].Function.Name != "current_time" || len(request.Messages) != 3 ||
				!isAgentStatusMessage(request.Messages[2].Role, request.Messages[2].Content) {
				t.Fatalf("first model request = %+v", request)
			}
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[
					{"id":"provider-a","type":"function","function":{"name":"calculate","arguments":"{\"operation\":\"add\",\"operands\":[\"12.5\",\"3.2\"]}"}},
					{"id":"provider-b","type":"function","function":{"name":"calculate","arguments":"{\"operation\":\"multiply\",\"operands\":[\"4\",\"5\"]}"}}
				]},"finish_reason":"tool_calls"}]
			}`))
		case 2:
			if len(request.Messages) != 6 || len(request.Messages[2].ToolCalls) != 2 ||
				request.Messages[2].ToolCalls[0].ID != "decision:1/action:0" ||
				request.Messages[2].ToolCalls[1].ID != "decision:1/action:1" ||
				request.Messages[3].Role != "tool" || request.Messages[3].ToolCallID != "decision:1/action:0" ||
				request.Messages[4].Role != "tool" || request.Messages[4].ToolCallID != "decision:1/action:1" ||
				!isAgentStatusMessage(request.Messages[5].Role, request.Messages[5].Content) {
				t.Fatalf("reconstructed model request = %+v", request.Messages)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"12.5 + 3.2 = 15.7, and 4 × 5 = 20."},"finish_reason":"stop"}]}`))
		default:
			t.Fatalf("unexpected model call %d", modelCalls)
		}
	}))
	defer upstream.Close()

	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, func() string { return "msg_controller_actions" })
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewController(runtime, models.NewBifrostClient(upstream.URL, upstream.Client(), 2048), registry)
	if err := controller.Execute(ctx, attemptFromClaim(claimed)); err != nil {
		t.Fatal(err)
	}

	var runStatus, jobStatus, outputID, content string
	var checkpointKinds []string
	if err := api.db.Pool().QueryRow(ctx, `
		select r.status, j.status, r.output_message_id, m.content
		from agent_runs r
		join agent_jobs j on j.run_id = r.id
		join chat_messages m on m.id = r.output_message_id
		where r.id = $1`, admittedBody.RunID).Scan(&runStatus, &jobStatus, &outputID, &content); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `
		select array_agg(kind order by sequence_no)
		from agent_run_checkpoints where run_id = $1`, admittedBody.RunID).Scan(&checkpointKinds); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || jobStatus != "succeeded" || outputID != "msg_controller_actions" || content != "12.5 + 3.2 = 15.7, and 4 × 5 = 20." {
		t.Fatalf("terminal state = %s/%s/%s/%q", runStatus, jobStatus, outputID, content)
	}
	wantKinds := []string{"action_proposal", "action_result", "action_result", "final_draft"}
	if len(checkpointKinds) != len(wantKinds) {
		t.Fatalf("checkpoint kinds = %v", checkpointKinds)
	}
	for index := range wantKinds {
		if checkpointKinds[index] != wantKinds[index] {
			t.Fatalf("checkpoint kinds = %v, want %v", checkpointKinds, wantKinds)
		}
	}
}

func TestLeaderLoopUsesCheckpointBackedTodoAndInjectsCurrentAgentStatus(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "controller-todo-status@example.com")
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	latestServer := app.NewServer(app.Config{
		CookieSecure: false, AgentRun: agent.DefaultRunConfig("nano-interactive-v1"),
		AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@15"),
	}, api.db)
	api.server = latestServer
	api.handler = latestServer.Handler()

	const messageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c172"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": "Plan the work, calculate 12.5 + 3.2, and verify the result.", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status = %d, body = %s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || claimed.RunID != admittedBody.RunID {
		t.Fatalf("claim=%+v ok=%t err=%v", claimed, ok, err)
	}

	fixedNow := time.Date(2026, 8, 31, 7, 24, 10, 0, time.UTC)
	statuses := make([]string, 0, 6)
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) == 0 || request.Messages[len(request.Messages)-1].Role != "user" || !strings.HasPrefix(request.Messages[len(request.Messages)-1].Content, `<agent_status version="1">`) {
			t.Fatalf("model call %d has no final user Agent Status: %+v", modelCalls, request.Messages)
		}
		status := request.Messages[len(request.Messages)-1].Content
		statuses = append(statuses, status)
		toolNames := make(map[string]bool, len(request.Tools))
		for _, tool := range request.Tools {
			toolNames[tool.Function.Name] = true
		}
		if !toolNames["rewrite_todo_list"] || !toolNames["update_todo_status"] {
			t.Fatalf("model call %d tools=%v", modelCalls, toolNames)
		}
		w.Header().Set("Content-Type", "application/json")
		switch modelCalls {
		case 1:
			if strings.Contains(status, "TODO List") || !strings.Contains(status, "Generated at: 2026-08-31T15:24:10+08:00") {
				t.Fatalf("initial status=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"todo-rewrite","type":"function","function":{"name":"rewrite_todo_list","arguments":"{\"items\":[\"Plan the calculation\",\"Calculate 12.5 + 3.2\",\"Verify the result\"]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			if !strings.Contains(status, "TODO List (revision=1):") || !strings.Contains(status, "[todo_1] pending") || !strings.Contains(status, "rewrite_todo_list: 1") {
				t.Fatalf("status after rewrite=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"todo-start","type":"function","function":{"name":"update_todo_status","arguments":"{\"revision\":1,\"updates\":[{\"id\":\"todo_1\",\"status\":\"completed\"},{\"id\":\"todo_2\",\"status\":\"in_progress\"}]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 3:
			if !strings.Contains(status, "TODO List (revision=2):") || !strings.Contains(status, "[todo_2] in_progress") || !strings.Contains(status, "update_todo_status: 1") {
				t.Fatalf("status after first update=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"calculate","type":"function","function":{"name":"calculate","arguments":"{\"operation\":\"add\",\"operands\":[\"12.5\",\"3.2\"]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 4:
			if !strings.Contains(status, "calculate: 1") || !strings.Contains(status, "TODO List (revision=2):") {
				t.Fatalf("status after calculate=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"todo-verify","type":"function","function":{"name":"update_todo_status","arguments":"{\"revision\":2,\"updates\":[{\"id\":\"todo_2\",\"status\":\"completed\"},{\"id\":\"todo_3\",\"status\":\"in_progress\"}]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 5:
			if !strings.Contains(status, "TODO List (revision=3):") || !strings.Contains(status, "[todo_3] in_progress") || !strings.Contains(status, "update_todo_status: 2") {
				t.Fatalf("status during verification=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"todo-complete","type":"function","function":{"name":"update_todo_status","arguments":"{\"revision\":3,\"updates\":[{\"id\":\"todo_3\",\"status\":\"completed\"}]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 6:
			if !strings.Contains(status, "TODO List (revision=4):") || strings.Contains(status, "in_progress") || !strings.Contains(status, "update_todo_status: 3") {
				t.Fatalf("completed status=%s", status)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"12.5 + 3.2 = 15.7, and the TODO plan was verified."},"finish_reason":"stop"}]}`))
		default:
			t.Fatalf("unexpected model call %d", modelCalls)
		}
	}))
	defer upstream.Close()

	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, func() string { return "msg_todo_status_answer" }, agent.WithRuntimeClock(func() time.Time { return fixedNow }))
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	if execution.PlanMutationLimit != 12 || execution.ActionDecisionLimit != 4 || execution.ActionLimit != 8 {
		t.Fatalf("pinned budgets plan/actions/decisions=%d/%d/%d", execution.PlanMutationLimit, execution.ActionLimit, execution.ActionDecisionLimit)
	}
	rewrite := agent.NewRewriteTodoListAction(runtime)
	update := agent.NewUpdateTodoStatusAction(runtime)
	calculate := agent.NewCalculateAction()
	currentTime := agent.NewCurrentTimeAction(nil)
	searchEvidence := agent.NewSearchEvidenceAction(nil)
	directRegistry, err := agent.NewActionRegistry(rewrite, update, calculate, currentTime, searchEvidence)
	if err != nil {
		t.Fatal(err)
	}
	mcpRegistry, err := agent.NewMCPToolRegistry(
		agent.MCPToolRegistration{Action: calculate, Scheduling: agentcatalog.ToolParallel, CrashReplaySafe: true},
		agent.MCPToolRegistration{Action: currentTime, Scheduling: agentcatalog.ToolParallel, CrashReplaySafe: true},
		agent.MCPToolRegistration{Action: rewrite, Scheduling: agentcatalog.ToolOrderedSync, CrashReplaySafe: true},
		agent.MCPToolRegistration{Action: searchEvidence, Scheduling: agentcatalog.ToolParallel, CrashReplaySafe: true},
		agent.MCPToolRegistration{Action: update, Scheduling: agentcatalog.ToolOrderedSync, CrashReplaySafe: true},
		agent.MCPToolRegistration{Action: todoDelegationStub{}, Scheduling: agentcatalog.ToolExclusiveDelegation},
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := agent.NewMCPToolHost(catalog, mcpRegistry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewMCPController(runtime, models.NewBifrostClient(upstream.URL, upstream.Client(), 2048), directRegistry, host, agentcatalog.MustParseReference("chat.leader@4"))
	if err := controller.Execute(ctx, attemptFromClaim(claimed)); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 6 || len(statuses) != 6 {
		t.Fatalf("model calls/statuses=%d/%d", modelCalls, len(statuses))
	}

	var runStatus, jobStatus, content string
	var checkpointCount, todoResultCount, persistedStatusCount int
	if err := api.db.Pool().QueryRow(ctx, `
		select r.status,j.status,message.content
		from agent_runs r join agent_jobs j on j.run_id=r.id join chat_messages message on message.id=r.output_message_id
		where r.id=$1`, admittedBody.RunID).Scan(&runStatus, &jobStatus, &content); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id=$1`, admittedBody.RunID).Scan(&checkpointCount); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `
		select count(*) from agent_run_checkpoints result
		join agent_run_checkpoints proposal on proposal.run_id=result.run_id and proposal.decision_no=result.decision_no and proposal.kind='action_proposal'
		where result.run_id=$1 and result.kind='action_result'
		  and (proposal.payload::text like '%rewrite_todo_list%' or proposal.payload::text like '%update_todo_status%')
		  and result.payload->>'status'='succeeded'`, admittedBody.RunID).Scan(&todoResultCount); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `
		select
			(select count(*) from chat_messages where chat_id=$1 and content like '%<agent_status%')+
			(select count(*) from agent_run_checkpoints where run_id=$2 and payload::text like '%<agent_status%')
	`, chatID, admittedBody.RunID).Scan(&persistedStatusCount); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || jobStatus != "succeeded" || content != "12.5 + 3.2 = 15.7, and the TODO plan was verified." || checkpointCount != 11 || todoResultCount != 4 || persistedStatusCount != 0 {
		t.Fatalf("durable state run/job=%s/%s content=%q checkpoints=%d TODO results=%d persisted statuses=%d", runStatus, jobStatus, content, checkpointCount, todoResultCount, persistedStatusCount)
	}
}

func TestTodoStatusSurvivesRuntimeRestartAndRetryButNotANewInputMessage(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "todo-retry-scope@example.com")
	const inputMessageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c173"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": inputMessageID, "content": "Plan this retry-scoped task.", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status=%d body=%s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	ctx := context.Background()
	firstClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || firstClaim.RunID != admittedBody.RunID {
		t.Fatalf("first claim=%+v ok=%t err=%v", firstClaim, ok, err)
	}
	firstAttempt := attemptFromClaim(firstClaim)
	firstRuntime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil)
	proposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "rewrite_todo_list", Input: json.RawMessage(`{"items":["inspect retry state","finish retry state"]}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstRuntime.AppendCheckpoint(ctx, firstAttempt, proposal); err != nil {
		t.Fatal(err)
	}
	result, err := agent.NewRewriteTodoListAction(firstRuntime).Execute(ctx, agent.ActionRequest{
		ActionID: "decision:1/action:0", Input: proposalInput(t, proposal), Attempt: firstAttempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultCheckpoint, err := agent.NewActionResultCheckpoint(1, 0, "decision:1/action:0", result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstRuntime.AppendCheckpoint(ctx, firstAttempt, resultCheckpoint); err != nil {
		t.Fatal(err)
	}
	if err := firstRuntime.Fail(ctx, firstAttempt, "retry_scope_fixture"); err != nil {
		t.Fatal(err)
	}

	retry := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+admittedBody.RunID+"/retry", map[string]any{
		"time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "todo-retry-scope")
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	var retryBody struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	decodeBody(t, retry, &retryBody)
	retryClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || retryClaim.RunID != retryBody.Run.ID {
		t.Fatalf("retry claim=%+v ok=%t err=%v", retryClaim, ok, err)
	}
	// A fresh runtime instance simulates a worker restart. Its empty Retry Run
	// must reconstruct TODO authority from the prior same-input checkpoint.
	restartedRuntime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil,
		agent.WithRuntimeClock(func() time.Time { return time.Date(2026, 8, 31, 7, 30, 0, 0, time.UTC) }))
	retryExecution, err := restartedRuntime.Load(ctx, attemptFromClaim(retryClaim))
	if err != nil {
		t.Fatal(err)
	}
	retryPrefix, err := restartedRuntime.LoadCheckpointPrefix(ctx, attemptFromClaim(retryClaim))
	if err != nil {
		t.Fatal(err)
	}
	retryRequest, err := restartedRuntime.BuildDecisionRequest(ctx, retryExecution, retryPrefix, nil)
	if err != nil {
		t.Fatal(err)
	}
	retryStatus := retryRequest.Messages[len(retryRequest.Messages)-1].Content
	if !strings.Contains(retryStatus, "TODO List (revision=1):") ||
		!strings.Contains(retryStatus, "inspect retry state") || !strings.Contains(retryStatus, "rewrite_todo_list: 1") {
		t.Fatalf("restarted Retry status=%s", retryStatus)
	}
	if err := restartedRuntime.Fail(ctx, attemptFromClaim(retryClaim), "new_input_scope_fixture"); err != nil {
		t.Fatal(err)
	}

	const newMessageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c174"
	newMessage := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": newMessageID, "content": "This is a new input scope.", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if newMessage.Code != http.StatusAccepted {
		t.Fatalf("new input status=%d body=%s", newMessage.Code, newMessage.Body.String())
	}
	var newBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, newMessage, &newBody)
	newClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || newClaim.RunID != newBody.RunID {
		t.Fatalf("new input claim=%+v ok=%t err=%v", newClaim, ok, err)
	}
	newRuntime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil)
	newExecution, err := newRuntime.Load(ctx, attemptFromClaim(newClaim))
	if err != nil {
		t.Fatal(err)
	}
	newPrefix, err := newRuntime.LoadCheckpointPrefix(ctx, attemptFromClaim(newClaim))
	if err != nil {
		t.Fatal(err)
	}
	newRequest, err := newRuntime.BuildDecisionRequest(ctx, newExecution, newPrefix, nil)
	if err != nil {
		t.Fatal(err)
	}
	newStatus := newRequest.Messages[len(newRequest.Messages)-1].Content
	if strings.Contains(newStatus, "TODO List") || strings.Contains(newStatus, "rewrite_todo_list") {
		t.Fatalf("new input inherited prior status=%s", newStatus)
	}
}

func proposalInput(t *testing.T, proposal agent.PendingCheckpoint) json.RawMessage {
	t.Helper()
	var payload struct {
		Actions []struct {
			Input json.RawMessage `json:"input"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(proposal.Payload, &payload); err != nil || len(payload.Actions) != 1 {
		t.Fatalf("proposal payload=%s err=%v", proposal.Payload, err)
	}
	return payload.Actions[0].Input
}

type todoDelegationStub struct{}

func (todoDelegationStub) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name: "delegate.research.source-discovery.v1", Description: "Unused configured delegation fixture.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["request"],"properties":{"request":{"type":"string"}}}`),
	}
}

func (todoDelegationStub) ValidateInput(json.RawMessage) error { return nil }

func (todoDelegationStub) Execute(context.Context, agent.ActionRequest) (agent.ActionResult, error) {
	return agent.ActionResult{}, errors.New("unexpected delegation")
}

func TestReclaimedControllerResumesTheFirstMissingActionOnTheSameRunAndJob(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "controller-reclaim-resume@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c088")
	ctx := context.Background()
	queue := jobs.NewQueue(api.db.Pool())
	first, ok, err := queue.ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("first claim=%+v ok=%t err=%v", first, ok, err)
	}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), "Recovery system prompt.", func() string { return "msg_reclaimed_controller" })
	proposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{
		{Name: "recovery_record", Input: json.RawMessage(`{"value":"already-accepted"}`)},
		{Name: "recovery_record", Input: json.RawMessage(`{"value":"resume-here"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(first), proposal); err != nil {
		t.Fatal(err)
	}
	firstResult, err := agent.NewActionResultCheckpoint(1, 0, "decision:1/action:0", agent.ActionResult{
		Status: agent.ActionSucceeded, Output: json.RawMessage(`{"recorded":"already-accepted"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(first), firstResult); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_jobs set lease_expires_at = now() - interval '1 second' where id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, ok, err := queue.ClaimNext(ctx)
	if err != nil || !ok || second.ID != first.ID || second.RunID != runID || second.AttemptNo != 2 {
		t.Fatalf("second claim=%+v ok=%t err=%v", second, ok, err)
	}
	action := &recoveryRecordingAction{}
	registry, err := agent.NewActionRegistry(action)
	if err != nil {
		t.Fatal(err)
	}
	model := &recordingModelClient{result: models.ModelDecision{Final: &models.FinalDraft{Text: "Recovered from the first incomplete Action."}}}
	if err := agent.NewController(runtime, model, registry).Execute(ctx, attemptFromClaim(second)); err != nil {
		t.Fatal(err)
	}
	if len(action.calls) != 1 || action.calls[0] != "resume-here" || model.calls != 1 {
		t.Fatalf("recovered Action/model calls=%v/%d", action.calls, model.calls)
	}
	if len(model.request.Messages) != 6 || model.request.Messages[2].Role != models.RoleAssistant || len(model.request.Messages[2].ActionCalls) != 2 ||
		model.request.Messages[3].ActionCallID != "decision:1/action:0" || model.request.Messages[4].ActionCallID != "decision:1/action:1" ||
		!isAgentStatusMessage(string(model.request.Messages[5].Role), model.request.Messages[5].Content) {
		t.Fatalf("reconstructed request=%+v", model.request.Messages)
	}
	var jobID, runStatus, jobStatus, outputID string
	var attemptNo, checkpoints, assistants int
	if err := api.db.Pool().QueryRow(ctx, `
		select j.id, r.status, j.status, r.output_message_id, j.attempt_no
		from agent_runs r join agent_jobs j on j.run_id = r.id where r.id = $1`, runID).
		Scan(&jobID, &runStatus, &jobStatus, &outputID, &attemptNo); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id = $1`, runID).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id = $1 and role = 'assistant'`, chatID).Scan(&assistants); err != nil {
		t.Fatal(err)
	}
	if jobID != first.ID || runStatus != "completed" || jobStatus != "succeeded" || outputID != "msg_reclaimed_controller" || attemptNo != 2 || checkpoints != 4 || assistants != 1 {
		t.Fatalf("recovered durable state job=%q run/job=%s/%s output=%q attempt=%d checkpoints=%d assistants=%d", jobID, runStatus, jobStatus, outputID, attemptNo, checkpoints, assistants)
	}
}

func TestWorkerPersistsTerminalInvalidBifrostResponseWithoutAssistantMessage(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "worker-failure@example.com")
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id":      "0190cdd2-5f2d-7ad8-b3f5-1b588788c006",
		"content": "This provider call will fail.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status = %d, body = %s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)

	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || claimed.RunID != admittedBody.RunID {
		t.Fatalf("claim = %+v ok=%v err=%v", claimed, ok, err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()
	model := models.NewBifrostClient(upstream.URL, upstream.Client(), 2048)
	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil)
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.NewController(runtime, model, registry).Execute(ctx, attemptFromClaim(claimed)); err == nil {
		t.Fatal("failed Bifrost call returned nil error")
	}

	var runStatus, jobStatus, errorCode string
	var outputMessageID *string
	if err := api.db.Pool().QueryRow(ctx, `select status, output_message_id, error_code from agent_runs where id = $1`, claimed.RunID).Scan(&runStatus, &outputMessageID, &errorCode); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select status from agent_jobs where run_id = $1`, claimed.RunID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	var assistantCount int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id = $1 and role = 'assistant'`, chatID).Scan(&assistantCount); err != nil {
		t.Fatal(err)
	}
	if runStatus != "failed" || jobStatus != "failed" || errorCode != "model_invalid_response" || outputMessageID != nil || assistantCount != 0 {
		t.Fatalf("failure state run=%q job=%q code=%q output=%v assistants=%d", runStatus, jobStatus, errorCode, outputMessageID, assistantCount)
	}

	next := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id":      "0190cdd2-5f2d-7ad8-b3f5-1b588788c007",
		"content": "A new turn can now be admitted.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if next.Code != http.StatusAccepted {
		t.Fatalf("admission after terminal failure = %d, body = %s", next.Code, next.Body.String())
	}
}

func TestContextBuilderProjectsAllDurableUserMessagesThroughTheCurrentRun(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "context-window@example.com")
	ctx := context.Background()
	for i := 1; i <= 25; i++ {
		if _, err := api.db.Pool().Exec(ctx, `
			insert into chat_messages(id, chat_id, role, content, created_at)
			values($1, $2, 'user', $3, timestamp with time zone '2026-07-14 00:00:00+00' + ($4 * interval '1 second'))`,
			messageIDForIndex(i), chatID, messageContentForIndex(i), i); err != nil {
			t.Fatal(err)
		}
	}
	currentMessageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788c107"
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, currentMessageID)
	if _, err := api.db.Pool().Exec(ctx, `
		insert into chat_messages(id, chat_id, role, content, created_at)
		values('msg_context_later', $1, 'user', 'must-not-enter-earlier-run', clock_timestamp() + interval '1 day')`, chatID); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || claimed.RunID != runID {
		t.Fatalf("claim = %+v ok=%t err=%v", claimed, ok, err)
	}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), "Bounded system prompt.", nil)
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := runtime.LoadCheckpointPrefix(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtime.BuildDecisionRequest(ctx, execution, prefix, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 28 || request.Messages[0].Role != "system" || request.Messages[0].Content != "Bounded system prompt." ||
		!isAgentStatusMessage(string(request.Messages[27].Role), request.Messages[27].Content) {
		t.Fatalf("context size/system = %d/%+v", len(request.Messages), request.Messages[0])
	}
	if request.Messages[1].Content != "message-01" || request.Messages[25].Content != "message-25" || request.Messages[26].Content == "must-not-enter-earlier-run" {
		t.Fatalf("context bounds = %q ... %q", request.Messages[1].Content, request.Messages[20].Content)
	}
}

func isAgentStatusMessage(role, content string) bool {
	return role == "user" && strings.HasPrefix(content, `<agent_status version="1">`) && strings.HasSuffix(content, `</agent_status>`)
}

func TestPublicationRejectsAnExpiredAttemptAfterTheJobIsReclaimed(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "publish-fence@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c022")
	ctx := context.Background()
	queue := jobs.NewQueue(api.db.Pool())
	first, ok, err := queue.ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("first claim = %+v ok=%v err=%v", first, ok, err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_jobs set lease_expires_at = now() - interval '1 second' where id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, ok, err := queue.ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("reclaim = %+v ok=%v err=%v", second, ok, err)
	}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), "System prompt.", func() string { return "msg_fenced_answer" })
	draft := appendFinalDraft(t, runtime, attemptFromClaim(second), "Only the current attempt may publish.")
	if err := runtime.PublishFinal(ctx, attemptFromClaim(first), draft); !errors.Is(err, agent.ErrLeaseLost) {
		t.Fatalf("stale publish error = %v, want ErrLeaseLost", err)
	}
	if err := runtime.Fail(ctx, attemptFromClaim(first), "model_unavailable"); !errors.Is(err, agent.ErrLeaseLost) {
		t.Fatalf("stale failure error = %v, want ErrLeaseLost", err)
	}
	if err := runtime.PublishFinal(ctx, attemptFromClaim(second), draft); err != nil {
		t.Fatal(err)
	}
	var assistantCount int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id = $1 and role = 'assistant'`, chatID).Scan(&assistantCount); err != nil {
		t.Fatal(err)
	}
	if assistantCount != 1 {
		t.Fatalf("assistant count = %d, want exactly one for run %q", assistantCount, runID)
	}
}

func TestPublicationAcknowledgementLossReconcilesCommittedSuccess(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "publish-reconcile@example.com")
	admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c025")
	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("claim = %+v ok=%v err=%v", claimed, ok, err)
	}
	draft := appendFinalDraft(t, agent.NewPostgresRuntime(api.db.Pool(), "System prompt.", nil), attemptFromClaim(claimed), "Committed exactly once.")
	ackLost := errors.New("simulated commit acknowledgement loss")
	firstCommit := true
	runtime := agent.NewPostgresRuntime(
		api.db.Pool(),
		"System prompt.",
		func() string { return "msg_reconciled_answer" },
		agent.WithCommitFunc(func(ctx context.Context, tx pgx.Tx) error {
			err := tx.Commit(ctx)
			if firstCommit && err == nil {
				firstCommit = false
				return ackLost
			}
			return err
		}),
	)
	if err := runtime.PublishFinal(ctx, attemptFromClaim(claimed), draft); err != nil {
		t.Fatalf("reconciled publication = %v", err)
	}
	var runStatus string
	var assistants int
	if err := api.db.Pool().QueryRow(ctx, `select status from agent_runs where id=$1`, claimed.RunID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id=$1 and role='assistant'`, chatID).Scan(&assistants); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || assistants != 1 {
		t.Fatalf("reconciled state run=%q assistants=%d", runStatus, assistants)
	}
}

func TestPostgresRuntimeFailPersistsJobAndRunErrorCode(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "fail-code-persist@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, uuid.NewString())
	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("claim = %+v ok=%v err=%v", claimed, ok, err)
	}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), "System prompt.", nil)
	if err := runtime.Fail(ctx, attemptFromClaim(claimed), "tool_execution_failed"); err != nil {
		t.Fatal(err)
	}

	var runStatus, jobStatus, runCode, jobCode string
	if err := api.db.Pool().QueryRow(ctx, `select status, coalesce(error_code,'') from agent_runs where id=$1`, runID).
		Scan(&runStatus, &runCode); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select status, coalesce(last_error_code,'') from agent_jobs where run_id=$1`, runID).
		Scan(&jobStatus, &jobCode); err != nil {
		t.Fatal(err)
	}
	if runStatus != "failed" || jobStatus != "failed" || runCode != "tool_execution_failed" || jobCode != "tool_execution_failed" {
		t.Fatalf("terminal state run=%s/%s job=%s/%s, want both failed with tool_execution_failed", runStatus, runCode, jobStatus, jobCode)
	}
}

func attemptFromClaim(job jobs.ClaimedJob) agent.Attempt {
	return agent.Attempt{JobID: job.ID, RunID: job.RunID, AttemptNo: job.AttemptNo, LeaseToken: job.LeaseToken}
}

func messageIDForIndex(index int) string {
	return "msg_context_" + messageContentForIndex(index)
}

func messageContentForIndex(index int) string {
	if index < 10 {
		return "message-0" + string(rune('0'+index))
	}
	return "message-" + string(rune('0'+index/10)) + string(rune('0'+index%10))
}

type recoveryRecordingAction struct {
	calls []string
}

func (*recoveryRecordingAction) CrashReplaySafe() bool { return true }

func (*recoveryRecordingAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "recovery_record",
		Description: "Record a deterministic value for recovery integration tests.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
	}
}

func (*recoveryRecordingAction) ValidateInput(input json.RawMessage) error {
	var decoded struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(input, &decoded); err != nil || decoded.Value == "" {
		return errors.New("recovery_record requires a value")
	}
	return nil
}

func (a *recoveryRecordingAction) Execute(ctx context.Context, request agent.ActionRequest) (agent.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return agent.ActionResult{}, err
	}
	var input struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return agent.ActionResult{}, err
	}
	a.calls = append(a.calls, input.Value)
	output, err := json.Marshal(map[string]string{"recorded": input.Value})
	if err != nil {
		return agent.ActionResult{}, err
	}
	return agent.ActionResult{Status: agent.ActionSucceeded, Output: output}, nil
}
