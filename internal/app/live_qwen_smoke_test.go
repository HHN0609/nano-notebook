package app_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type liveChatDiscoveryProvider struct {
	requests []websearch.Request
}

func (*liveChatDiscoveryProvider) ResearchAvailable() bool { return true }

func (p *liveChatDiscoveryProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	return []websearch.Candidate{{
		Title: "Official reference for " + request.Query, URL: "https://example.com/reference/" + uuid.NewString(),
		DisplayURL: "example.com/reference", Description: "A public source candidate for user review.", Rank: 1,
	}}, nil
}

func TestLiveQwenThroughBifrostUsesBothSprint3ActionsAndPublishesOnce(t *testing.T) {
	if os.Getenv("NANO_QWEN_SMOKE") != "1" {
		t.Skip("set NANO_QWEN_SMOKE=1 through scripts/test-sprint3-qwen")
	}
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "live-qwen-sprint3@example.com")
	const inputMessageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c086"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": inputMessageID,
		"content": "You must use both available Actions before answering. First call current_time for UTC and Asia/Shanghai. " +
			"Then call calculate to divide 28800 by 3600. Finally answer in one concise sentence.",
		"time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status=%d", admitted.Code)
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("claim unavailable: ok=%t err=%v", ok, err)
	}
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	bifrostURL := os.Getenv("NANO_BIFROST_URL")
	if bifrostURL == "" {
		bifrostURL = "http://127.0.0.1:56666"
	}
	model := models.NewBifrostClient(bifrostURL, &http.Client{Timeout: 90 * time.Second}, 2048)
	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, func() string { return "msg_live_qwen_sprint3" })
	if err := agent.NewController(runtime, model, registry).Execute(ctx, attemptFromClaim(claimed)); err != nil {
		t.Fatalf("live Controller failed safely: %v", err)
	}

	rows, err := api.db.Pool().Query(ctx, `
		select action->>'name'
		from agent_run_checkpoints c,
			jsonb_array_elements(c.payload->'actions') with ordinality as item(action, action_order)
		where c.run_id = $1 and c.kind = 'action_proposal'
		order by c.sequence_no, action_order`, admittedBody.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actionNames := make([]string, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actionNames = append(actionNames, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	currentTimeIndex, calculateIndex := -1, -1
	for index, name := range actionNames {
		if name == "current_time" && currentTimeIndex < 0 {
			currentTimeIndex = index
		}
		if name == "calculate" && calculateIndex < 0 {
			calculateIndex = index
		}
	}
	if currentTimeIndex < 0 || calculateIndex <= currentTimeIndex {
		t.Fatalf("live model did not accept both Actions in required order; accepted names=%v", actionNames)
	}

	var runStatus, jobStatus string
	var assistants, finalDrafts int
	if err := api.db.Pool().QueryRow(ctx, `
		select r.status, j.status
		from agent_runs r join agent_jobs j on j.run_id = r.id
		where r.id = $1`, admittedBody.RunID).Scan(&runStatus, &jobStatus); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from chat_messages where chat_id = $1 and role = 'assistant'`, chatID).Scan(&assistants); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id = $1 and kind = 'final_draft'`, admittedBody.RunID).Scan(&finalDrafts); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || jobStatus != "succeeded" || assistants != 1 || finalDrafts != 1 {
		t.Fatalf("live terminal state=%s/%s assistants=%d final_drafts=%d", runStatus, jobStatus, assistants, finalDrafts)
	}
}

func TestLiveQwenRetrievalFirstChatProactivelySearchesTheWeb(t *testing.T) {
	if os.Getenv("NANO_QWEN_SMOKE") != "1" {
		t.Skip("set NANO_QWEN_SMOKE=1 through scripts/test-sprint3-qwen")
	}
	questions := []string{
		"What is the latest stable Go release?",
		"Why does PostgreSQL MVCC let readers avoid blocking writers?",
		"Compare Kafka and RabbitMQ for an event-driven backend.",
	}
	for index, question := range questions {
		t.Run(strings.ReplaceAll(question[:min(len(question), 18)], " ", "_"), func(t *testing.T) {
			api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "live-retrieval-first-"+uuid.NewString()+"@example.com")
			catalog, err := agentcatalog.LoadEmbedded()
			if err != nil {
				t.Fatal(err)
			}
			api.server = app.NewServer(app.Config{
				CookieSecure: false, AgentRun: agent.DefaultRunConfig("nano-interactive-v1"),
				AgentCatalog: catalog, AgentRelease: agentcatalog.MustParseReference("nano.default@25"),
			}, api.db)
			api.handler = api.server.Handler()

			messageID := []string{
				"0190cdd2-5f2d-7ad8-b3f5-1b588788e001",
				"0190cdd2-5f2d-7ad8-b3f5-1b588788e002",
				"0190cdd2-5f2d-7ad8-b3f5-1b588788e003",
			}[index]
			admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
				"id": messageID, "content": question, "time_zone": "Asia/Shanghai",
			}, sessionCookie, csrfCookie, csrfCookie.Value, "")
			if admitted.Code != http.StatusAccepted {
				t.Fatalf("admission status=%d body=%s", admitted.Code, admitted.Body.String())
			}
			var admission struct {
				RunID string `json:"run_id"`
			}
			decodeBody(t, admitted, &admission)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
			if err != nil || !ok || claimed.RunID != admission.RunID {
				t.Fatalf("claim=%+v ok=%t err=%v", claimed, ok, err)
			}
			provider := &liveChatDiscoveryProvider{}
			runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, func() string { return "msg_" + uuid.NewString() },
				agent.WithGroundingService(agent.NewGroundingService(api.db.Pool())))
			discover := agent.NewDiscoverSourcesAction(agent.NewPostgresDiscoverSourcesBackend(api.db.Pool(), provider), provider)
			actions := []agent.Action{
				agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil), discover,
				agent.NewRewriteTodoListAction(runtime), agent.NewSearchEvidenceAction(nil), agent.NewUpdateTodoStatusAction(runtime),
			}
			directRegistry, err := agent.NewActionRegistry(actions...)
			if err != nil {
				t.Fatal(err)
			}
			mcpRegistry, err := agent.NewMCPToolRegistry(
				agent.MCPToolRegistration{Action: actions[0], Scheduling: agentcatalog.ToolParallel},
				agent.MCPToolRegistration{Action: actions[1], Scheduling: agentcatalog.ToolParallel},
				agent.MCPToolRegistration{Action: actions[2], Scheduling: agentcatalog.ToolParallel},
				agent.MCPToolRegistration{Action: actions[3], Scheduling: agentcatalog.ToolOrderedSync},
				agent.MCPToolRegistration{Action: actions[4], Scheduling: agentcatalog.ToolParallel},
				agent.MCPToolRegistration{Action: actions[5], Scheduling: agentcatalog.ToolOrderedSync},
			)
			if err != nil {
				t.Fatal(err)
			}
			host, err := agent.NewMCPToolHost(catalog, mcpRegistry, runtime)
			if err != nil {
				t.Fatal(err)
			}
			bifrostURL := os.Getenv("NANO_BIFROST_URL")
			if bifrostURL == "" {
				bifrostURL = "http://127.0.0.1:56666"
			}
			model := models.NewBifrostClient(bifrostURL, &http.Client{Timeout: 90 * time.Second}, 2048)
			if err := agent.NewMCPController(runtime, model, directRegistry, host, agentcatalog.MustParseReference("chat.leader@6")).Execute(ctx, attemptFromClaim(claimed)); err != nil {
				t.Fatalf("retrieval-first live Controller failed: %v", err)
			}

			var discoverCalls, readySessions int
			if err := api.db.Pool().QueryRow(ctx, `
				select count(*) from agent_run_checkpoints proposal,
				jsonb_array_elements(proposal.payload->'actions') action
				where proposal.run_id=$1 and proposal.kind='action_proposal' and action->>'name'='discover_sources'
			`, admission.RunID).Scan(&discoverCalls); err != nil {
				t.Fatal(err)
			}
			if err := api.db.Pool().QueryRow(ctx, `select count(*) from source_discovery_sessions where agent_run_id=$1 and status='ready'`, admission.RunID).Scan(&readySessions); err != nil {
				t.Fatal(err)
			}
			if discoverCalls < 1 || len(provider.requests) < 1 || readySessions != 1 {
				t.Fatalf("question=%q discover_actions=%d provider_requests=%d ready_sessions=%d", question, discoverCalls, len(provider.requests), readySessions)
			}
		})
	}
}
