package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/jackc/pgx/v5"
)

type capturingLeaderRouter struct {
	request agent.LeaderRouteRequest
}

func (r *capturingLeaderRouter) DecideRoute(_ context.Context, request agent.LeaderRouteRequest) (agent.LeaderRouteDecision, error) {
	r.request = request
	return agent.LeaderRouteDecision{Route: agent.LeaderContinueChat, ReasonCode: agent.LeaderReasonOrdinaryConversation}, nil
}

func TestLegacyAdmissionDualWritesChatRunAndAgentTreeOwnership(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "generic-runtime@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c110")
	var chatRunID, rootRunID, treeID, runtimeKind string
	err := api.db.Pool().QueryRow(context.Background(), `
		select chat_run.id,chat_run.root_agent_run_id,run.tree_id,run.runtime_kind
		from chat_runs chat_run join agent_runs run on run.id=chat_run.root_agent_run_id
		where chat_run.id=$1
	`, runID).Scan(&chatRunID, &rootRunID, &treeID, &runtimeKind)
	if err != nil || chatRunID != runID || rootRunID != runID || treeID == "" || runtimeKind != "legacy_role" {
		t.Fatalf("chat=%s root=%s tree=%s kind=%s err=%v", chatRunID, rootRunID, treeID, runtimeKind, err)
	}
	var treeRoot string
	if err := api.db.Pool().QueryRow(context.Background(), `select root_agent_run_id from agent_trees where id=$1`, treeID).Scan(&treeRoot); err != nil || treeRoot != runID {
		t.Fatalf("tree root=%s err=%v", treeRoot, err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `update agent_runs set status='running',started_at=now() where id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	var productStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `select status from chat_runs where id=$1`, runID).Scan(&productStatus); err != nil || productStatus != "running" {
		t.Fatalf("Chat Run status=%q err=%v", productStatus, err)
	}
}

func TestConfiguredAgentRunRequiresNoRoleExecutorVersionOrChatOwnership(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	_, err := api.db.Pool().Exec(ctx, `insert into agent_trees(
		id,absolute_deadline,model_call_limit,action_limit,context_byte_limit,result_byte_limit
	) values('tree_generic',now()+interval '10 minutes',5,8,65536,262144)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.db.Pool().Exec(ctx, `insert into agent_runs(
		id,status,runtime_kind,tree_id,definition_identity,definition_version,definition_sha256,
		executor_identity,model_policy_identity,model_policy_version,model_policy_sha256,provider_model
	) select 'run_generic','queued','configured','tree_generic',definition_identity,definition_version,definition.canonical_sha256,
		executor,model_policy_identity,model_policy_version,policy.canonical_sha256,policy.provider_model
	from agent_definition_versions definition
	join agent_model_policy_versions policy on policy.policy_identity=definition.model_policy_identity and policy.policy_version=definition.model_policy_version
	where definition_identity='chat.leader' and definition_version=1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_trees set root_agent_run_id='run_generic' where id='tree_generic'`); err != nil {
		t.Fatal(err)
	}
	var role, executorVersion, userID, chatID, messageID *string
	if err := api.db.Pool().QueryRow(ctx, `select agent_role,executor_version,user_id,chat_id,input_message_id from agent_runs where id='run_generic'`).Scan(&role, &executorVersion, &userID, &chatID, &messageID); err != nil {
		t.Fatal(err)
	}
	if role != nil || executorVersion != nil || userID != nil || chatID != nil || messageID != nil {
		t.Fatalf("configured Run retained legacy ownership role=%v executor=%v user=%v chat=%v message=%v", role, executorVersion, userID, chatID, messageID)
	}
}

func TestLegacyRuntimeDrainStatusBlocksOnlyActiveLegacyRuns(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "legacy-drain-status@example.com")
	legacyRunID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c122")
	ctx := context.Background()
	readStatus := func() (active, retained int64, ready bool) {
		t.Helper()
		if err := api.db.Pool().QueryRow(ctx, `
			select active_legacy_runs,retained_legacy_runs,ready_to_contract
			from agent_legacy_runtime_drain_status
		`).Scan(&active, &retained, &ready); err != nil {
			t.Fatal(err)
		}
		return active, retained, ready
	}
	active, retained, ready := readStatus()
	if active != 1 || retained != 1 || ready {
		t.Fatalf("active=%d retained=%d ready=%t want=1/1/false", active, retained, ready)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_runs set status='completed',finished_at=now(),updated_at=now() where id=$1`, legacyRunID); err != nil {
		t.Fatal(err)
	}
	active, retained, ready = readStatus()
	if active != 1 || retained != 1 || ready {
		t.Fatalf("terminal Run with active Job: active=%d retained=%d ready=%t want=1/1/false", active, retained, ready)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		update agent_jobs set status='succeeded',lease_token=null,lease_expires_at=null,updated_at=now()
		where run_id=$1
	`, legacyRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_trees(
			id,absolute_deadline,model_call_limit,action_limit,context_byte_limit,result_byte_limit
		) values('tree_drain_configured',now()+interval '10 minutes',5,8,65536,262144)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_runs(
			id,status,runtime_kind,tree_id,definition_identity,definition_version,definition_sha256,
			executor_identity,model_policy_identity,model_policy_version,model_policy_sha256,provider_model
		) select 'run_drain_configured','queued','configured','tree_drain_configured',
			definition_identity,definition_version,definition.canonical_sha256,executor,
			model_policy_identity,model_policy_version,policy.canonical_sha256,policy.provider_model
		from agent_definition_versions definition
		join agent_model_policy_versions policy
		  on policy.policy_identity=definition.model_policy_identity
		 and policy.policy_version=definition.model_policy_version
		where definition_identity='chat.leader' and definition_version=1
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_trees set root_agent_run_id='run_drain_configured' where id='tree_drain_configured'`); err != nil {
		t.Fatal(err)
	}
	active, retained, ready = readStatus()
	if active != 0 || retained != 1 || !ready {
		t.Fatalf("active=%d retained=%d ready=%t want=0/1/true", active, retained, ready)
	}
}

func TestExactAgentReleaseActivatesConfiguredAdmissionForNewChatRuns(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-release-admission@example.com")
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	messageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788c114"
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": "Use the released Agent.", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	var runtimeKind, definition, executor, modelPolicy string
	var role, executorVersion, userID *string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select runtime_kind,definition_identity||'@'||definition_version::text,executor_identity,
			model_policy_identity||'@'||model_policy_version::text,agent_role,executor_version,user_id
		from agent_runs where id=$1
	`, body.RunID).Scan(&runtimeKind, &definition, &executor, &modelPolicy, &role, &executorVersion, &userID); err != nil {
		t.Fatal(err)
	}
	if runtimeKind != "configured" || definition != "chat.leader@1" || executor != "chat_leader" ||
		modelPolicy != "agent.chat-default@1" || role != nil || executorVersion != nil || userID != nil {
		t.Fatalf("kind=%s definition=%s executor=%s policy=%s role=%v version=%v user=%v", runtimeKind, definition, executor, modelPolicy, role, executorVersion, userID)
	}
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != body.RunID {
		t.Fatalf("claim=%+v ok=%t err=%v", claimed, ok, err)
	}
	normal := &recordingNormalExecutor{}
	runtime := agent.NewLeaderExecutor(
		api.db.Pool(), normal, fixedLeaderRouter{route: agent.LeaderDelegateResearch},
		fixedResearchPlanner{queries: []string{"unused"}}, &recordingResearchProvider{},
	)
	resolution := agent.NewChatLeaderDefinitionExecutor(runtime).ExecuteAttempt(context.Background(), attemptFromClaim(claimed))
	if resolution.Disposition != agent.AttemptCompleted || normal.calls != 1 {
		t.Fatalf("resolution=%+v normal calls=%d", resolution, normal.calls)
	}
	var requestedRoute, effectiveRoute, policyReason string
	if err := api.db.Pool().QueryRow(context.Background(), `select requested_route,effective_route,policy_reason_code from agent_run_routes where run_id=$1`, body.RunID).Scan(&requestedRoute, &effectiveRoute, &policyReason); err != nil || requestedRoute != "delegate_research" || effectiveRoute != "continue_chat" || policyReason != "relationship_unregistered" {
		t.Fatalf("route=%q/%q policy=%q err=%v", requestedRoute, effectiveRoute, policyReason, err)
	}
	postgresRuntime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, func() string { return "msg_configured_answer" })
	draft := appendFinalDraft(t, postgresRuntime, attemptFromClaim(claimed), "Configured Agent completed.")
	if err := postgresRuntime.PublishFinal(context.Background(), attemptFromClaim(claimed), draft); err != nil {
		t.Fatal(err)
	}
	var runStatus, productStatus, outputMessageID, resultContract string
	var resultPayload []byte
	var resultPayloadBytes, modelCallsConsumed, contextBytesConsumed, resultBytesConsumed int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select run.status,product.status,product.output_message_id,
			result.contract_identity||'@'||result.contract_version::text,result.payload,result.payload_bytes,
			tree.model_calls_consumed,tree.context_bytes_consumed,tree.result_bytes_consumed
		from agent_runs run join chat_runs product on product.root_agent_run_id=run.id
		join agent_trees tree on tree.id=run.tree_id
		join agent_run_results result on result.producer_run_id=run.id
		where run.id=$1
	`, body.RunID).Scan(&runStatus, &productStatus, &outputMessageID, &resultContract, &resultPayload, &resultPayloadBytes,
		&modelCallsConsumed, &contextBytesConsumed, &resultBytesConsumed); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" || productStatus != "completed" || outputMessageID != "msg_configured_answer" ||
		resultContract != "chat.answer@1" || !strings.Contains(string(resultPayload), "Configured Agent completed.") ||
		resultBytesConsumed != resultPayloadBytes || modelCallsConsumed != 2 || contextBytesConsumed < 1 {
		t.Fatalf("terminal run=%s product=%s output=%s result=%s payload=%s consumed=%d", runStatus, productStatus, outputMessageID, resultContract, resultPayload, resultBytesConsumed)
	}
	events := api.getWithCookie(t, "/api/v1/agent-runs/"+body.RunID+"/events", sessionCookie)
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"status":"completed"`) ||
		!strings.Contains(events.Body.String(), "Configured Agent completed.") {
		t.Fatalf("events status=%d body=%s", events.Code, events.Body.String())
	}
	chatProjection := api.getWithCookie(t, "/api/v1/chats/"+chatID, sessionCookie)
	if chatProjection.Code != http.StatusOK || !strings.Contains(chatProjection.Body.String(), body.RunID) {
		t.Fatalf("Chat projection status=%d body=%s", chatProjection.Code, chatProjection.Body.String())
	}
}

func TestConfiguredAgentTreeDeadlineExpiresBeforeJobClaim(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-deadline@example.com")
	catalog, _ := agentcatalog.LoadEmbedded()
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c115", "content": "Expire this configured run.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	if _, err := api.db.Pool().Exec(context.Background(), `
		update agent_trees set absolute_deadline=now()-interval '1 second'
		where id=(select tree_id from agent_runs where id=$1)
	`, body.RunID); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || ok {
		t.Fatalf("claim=%+v ok=%t err=%v", claimed, ok, err)
	}
	var runStatus, jobStatus, productStatus, errorCode string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select run.status,job.status,product.status,run.error_code
		from agent_runs run join agent_jobs job on job.run_id=run.id
		join chat_runs product on product.root_agent_run_id=run.id
		where run.id=$1
	`, body.RunID).Scan(&runStatus, &jobStatus, &productStatus, &errorCode); err != nil {
		t.Fatal(err)
	}
	if runStatus != "failed" || jobStatus != "failed" || productStatus != "failed" || errorCode != "run_deadline_exceeded" {
		t.Fatalf("terminal=%s/%s/%s error=%s", runStatus, jobStatus, productStatus, errorCode)
	}
}

func TestConfiguredAttemptFailureTerminalizesWithoutAgentRole(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-terminal@example.com")
	catalog, _ := agentcatalog.LoadEmbedded()
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c116", "content": "Fail this configured attempt.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	queue := jobs.NewQueue(api.db.Pool())
	claimed, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || claimed.RunID != body.RunID {
		t.Fatalf("claim=%+v ok=%t err=%v", claimed, ok, err)
	}
	resolution, err := queue.ResolveAttempt(context.Background(), claimed, agent.AttemptResolution{
		Disposition: agent.AttemptTerminal, ErrorCode: "agent_execution_failed",
	})
	if err != nil || resolution.Disposition != agent.AttemptTerminal {
		t.Fatalf("resolution=%+v err=%v", resolution, err)
	}
	var runStatus, jobStatus, productStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select run.status,job.status,product.status
		from agent_runs run join agent_jobs job on job.run_id=run.id
		join chat_runs product on product.root_agent_run_id=run.id where run.id=$1
	`, body.RunID).Scan(&runStatus, &jobStatus, &productStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "failed" || jobStatus != "failed" || productStatus != "failed" {
		t.Fatalf("terminal=%s/%s/%s", runStatus, jobStatus, productStatus)
	}
}

func TestConfiguredChatRunCanBeCancelledThroughProductOwnership(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-cancel@example.com")
	catalog, _ := agentcatalog.LoadEmbedded()
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c117", "content": "Cancel this configured run.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	cancelled := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+body.RunID+"/cancel", map[string]any{}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if cancelled.Code != http.StatusOK || !strings.Contains(cancelled.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code, cancelled.Body.String())
	}
	var productStatus string
	if err := api.db.Pool().QueryRow(context.Background(), `select status from chat_runs where root_agent_run_id=$1`, body.RunID).Scan(&productStatus); err != nil || productStatus != "cancelled" {
		t.Fatalf("product status=%q err=%v", productStatus, err)
	}
	retry := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+body.RunID+"/retry", map[string]any{
		"time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "configured-retry")
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	var retryBody struct {
		Run agent.RunSnapshot `json:"run"`
	}
	decodeBody(t, retry, &retryBody)
	var runtimeKind, definition string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select runtime_kind,definition_identity||'@'||definition_version::text from agent_runs where id=$1
	`, retryBody.Run.ID).Scan(&runtimeKind, &definition); err != nil {
		t.Fatal(err)
	}
	if retryBody.Run.ID == body.RunID || runtimeKind != "configured" || definition != "chat.leader@1" {
		t.Fatalf("retry=%+v kind=%s definition=%s", retryBody.Run, runtimeKind, definition)
	}
	replayed := api.postJSONWithCookieAndCSRF(t, "/api/v1/agent-runs/"+body.RunID+"/retry", map[string]any{
		"time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "configured-retry")
	var replayedBody struct {
		Run agent.RunSnapshot `json:"run"`
	}
	decodeBody(t, replayed, &replayedBody)
	if replayed.Code != http.StatusAccepted || replayedBody.Run.ID != retryBody.Run.ID {
		t.Fatalf("replayed status=%d run=%+v", replayed.Code, replayedBody.Run)
	}
}

func TestConfiguredDelegationRLSResolvesParentProductOwnership(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-delegation-rls@example.com")
	catalog, _ := agentcatalog.LoadEmbedded()
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c121", "content": "Create a configured root.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission=%d %s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	ctx := context.Background()
	var userID string
	if err := api.db.Pool().QueryRow(ctx, `select user_id from chat_runs where root_agent_run_id=$1`, body.RunID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_runs(
			id,user_id,chat_id,input_message_id,status,error_code,model,prompt_version,
			agent_config_id,executor_version,agent_role,finished_at
		)
		select 'run_configured_rls_child',product.user_id,product.chat_id,product.input_message_id,
			'failed','fixture_failed','aliyun/qwen-plus',$2,'fixture-config','fixture-executor','research',now()
		from chat_runs product where product.root_agent_run_id=$1
	`, body.RunID, agent.BarePromptVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_run_delegations(
			id,parent_run_id,child_run_id,parent_role,child_role,child_ordinal,depth,state,error_code,completed_at,action_id
		) values(
			'delegation_configured_rls',$1,'run_configured_rls_child',null,null,0,1,'failed','fixture_failed',now(),'delegate.research.source-discovery.v1'
		)
	`, body.RunID); err != nil {
		t.Fatal(err)
	}
	tx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_app`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `select set_config('app.principal_id',$1,true)`, userID); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx, `select count(parent_run_id) from agent_run_delegations where parent_run_id=$1`, body.RunID).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 1 {
		t.Fatalf("configured Delegations visible=%d want=1", visible)
	}
	result, err := tx.Exec(ctx, `
		update agent_run_delegations
		set error_code='fixture_updated',updated_at=now()
		where parent_run_id=$1
	`, body.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("configured Delegations updated=%d want=1", result.RowsAffected())
	}
}

func TestConfiguredChatAdmissionPinsTransitiveDefinitionAndPolicy(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "configured-admission@example.com")
	legacyRunID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c112")
	ctx := context.Background()
	var userID, inputMessageID string
	if err := api.db.Pool().QueryRow(ctx, `select user_id,input_message_id from agent_runs where id=$1`, legacyRunID).Scan(&userID, &inputMessageID); err != nil {
		t.Fatal(err)
	}
	catalog, _ := agentcatalog.LoadEmbedded()
	definition, _ := catalog.ResolveDefinition(agentcatalog.MustParseReference("chat.leader@1"))
	policy, _ := catalog.ResolveModelPolicy(definition.ModelPolicy)
	command := agent.ConfiguredChatAdmission{
		RunID: "run_configured_admission", UserID: userID, ChatID: chatID, InputMessageID: inputMessageID,
		Definition: definition, ModelPolicy: policy, DeadlineAt: definitionAdmissionDeadline(),
		ContextManifest: json.RawMessage(`{"time_zone":"Asia/Shanghai"}`),
	}
	traceScope, err := agent.NewTraceScope(agent.DiscardTraceSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer traceScope.Rollback()
	ctx = agent.ContextWithTraceScope(ctx, traceScope)
	if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		store := agent.NewStore(tx)
		if err := store.CreateConfiguredChatQueued(ctx, command); err != nil {
			return fmt.Errorf("create run: %w", err)
		}
		if err := store.PinEvidenceSet(ctx, command.RunID, userID, nil); err != nil {
			return fmt.Errorf("pin evidence: %w", err)
		}
		if err := jobs.NewStore(tx).CreateAgentRun(ctx, "job_configured_admission", command.RunID); err != nil {
			return fmt.Errorf("create job: %w", err)
		}
		var temporaryOwner, principal string
		if err := tx.QueryRow(ctx, `select user_id,current_setting('app.principal_id',true) from agent_runs where id=$1`, command.RunID).Scan(&temporaryOwner, &principal); err != nil {
			return fmt.Errorf("read temporary owner: %w", err)
		}
		if temporaryOwner != userID || principal != userID {
			return fmt.Errorf("temporary owner=%q principal=%q", temporaryOwner, principal)
		}
		if err := agent.StartRunTraceInTx(ctx, tx, command.RunID, policy.ProviderModel, definition.Reference().String(), nil); err != nil {
			return fmt.Errorf("start trace: %w", err)
		}
		return store.FinalizeConfiguredChatOwnership(ctx, command.RunID)
	}); err != nil {
		t.Fatal(err)
	}
	_ = traceScope.PublishAfterCommit(ctx)
	var runtimeKind, definitionHash, policyHash, providerModel, executor string
	var role, executorVersion *string
	if err := api.db.Pool().QueryRow(ctx, `
		select runtime_kind,definition_sha256,model_policy_sha256,provider_model,executor_identity,agent_role,executor_version
		from agent_runs where id=$1
	`, command.RunID).Scan(&runtimeKind, &definitionHash, &policyHash, &providerModel, &executor, &role, &executorVersion); err != nil {
		t.Fatal(err)
	}
	if runtimeKind != "configured" || definitionHash != definition.SHA256 || policyHash != policy.SHA256 || providerModel != policy.ProviderModel || executor != definition.Executor || role != nil || executorVersion != nil {
		t.Fatalf("kind=%s definition=%s policy=%s model=%s executor=%s role=%v version=%v", runtimeKind, definitionHash, policyHash, providerModel, executor, role, executorVersion)
	}
}

func definitionAdmissionDeadline() time.Time {
	return time.Now().UTC().Add(10 * time.Minute)
}

func TestPostgresRuntimeLoadsConfiguredChatRootWithoutLegacyFields(t *testing.T) {
	api, _, _, chatID := newChatFixture(t, "configured-runtime-load@example.com")
	ctx := context.Background()
	var userID string
	if err := api.db.Pool().QueryRow(ctx, `select creator_user_id from chat_chats where id=$1`, chatID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	catalog, _ := agentcatalog.LoadEmbedded()
	definition, _ := catalog.ResolveDefinition(agentcatalog.MustParseReference("chat.leader@1"))
	policy, _ := catalog.ResolveModelPolicy(definition.ModelPolicy)
	deadline := definitionAdmissionDeadline()
	command := agent.ConfiguredChatAdmission{
		RunID: "run_configured_load", UserID: userID, ChatID: chatID, InputMessageID: "msg_configured_load",
		Definition: definition, ModelPolicy: policy, DeadlineAt: deadline,
		ContextManifest: json.RawMessage(`{"time_zone":"Asia/Shanghai"}`),
	}
	traceScope, err := agent.NewTraceScope(agent.DiscardTraceSink{})
	if err != nil {
		t.Fatal(err)
	}
	defer traceScope.Rollback()
	ctx = agent.ContextWithTraceScope(ctx, traceScope)
	if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into chat_messages(id,chat_id,role,content) values($1,$2,'user',$3)`, command.InputMessageID, chatID, "Use configured runtime."); err != nil {
			return err
		}
		if err := agent.NewStore(tx).CreateConfiguredChatQueued(ctx, command); err != nil {
			return fmt.Errorf("create configured run: %w", err)
		}
		if err := jobs.NewStore(tx).CreateAgentRun(ctx, "job_configured_load", command.RunID); err != nil {
			return fmt.Errorf("create configured job: %w", err)
		}
		if err := agent.StartRunTraceInTx(ctx, tx, command.RunID, policy.ProviderModel, definition.Reference().String(), nil); err != nil {
			return fmt.Errorf("start configured trace: %w", err)
		}
		return agent.NewStore(tx).FinalizeConfiguredChatOwnership(ctx, command.RunID)
	}); err != nil {
		t.Fatal(err)
	}
	_ = traceScope.PublishAfterCommit(ctx)
	const leaseToken = "0190cdd2-5f2d-7ad8-b3f5-1b588788c113"
	if _, err := api.db.Pool().Exec(ctx, `update agent_jobs set status='running',attempt_no=1,lease_token=$2,lease_expires_at=now()+interval '1 minute' where id=$1`, "job_configured_load", leaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_runs set status='running',started_at=now() where id=$1`, command.RunID); err != nil {
		t.Fatal(err)
	}
	attempt := agent.Attempt{JobID: "job_configured_load", RunID: command.RunID, AttemptNo: 1, LeaseToken: leaseToken}
	execution, err := agent.NewPostgresRuntime(api.db.Pool(), "", nil).Load(ctx, attempt)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ChatID != chatID || execution.UserID != userID || execution.InputMessageID != command.InputMessageID ||
		execution.Model != policy.ProviderModel || execution.PromptVersion != agent.BarePromptVersion ||
		execution.AgentConfigID != definition.Reference().String() || execution.TimeZone != "Asia/Shanghai" {
		t.Fatalf("configured execution=%+v", execution)
	}
	if execution.ActionDecisionLimit != definition.Limits.ModelCalls-1 || execution.FinalDecisionLimit != 1 ||
		execution.ActionLimit != definition.Limits.Actions || execution.ActionBatchLimit != definition.Limits.ActionBatch ||
		execution.ActionResultByteLimit != definition.Limits.ResultBytes || execution.ActionResultsByteLimit != definition.Limits.ResultBytes {
		t.Fatalf("configured limits=%+v definition=%+v", execution, definition.Limits)
	}
	if execution.ModelInvocation.Temperature == nil || *execution.ModelInvocation.Temperature != policy.Temperature ||
		execution.ModelInvocation.MaxOutputTokens != policy.MaxOutputTokens ||
		execution.ModelInvocation.Timeout != time.Duration(policy.TimeoutMS)*time.Millisecond {
		t.Fatalf("configured invocation=%+v policy=%+v", execution.ModelInvocation, policy)
	}
	if !execution.DeadlineAt.Equal(deadline) {
		t.Fatalf("deadline=%s want=%s", execution.DeadlineAt, deadline)
	}
	normal := &recordingNormalExecutor{}
	router := &capturingLeaderRouter{}
	runtime := agent.NewLeaderExecutor(
		api.db.Pool(), normal, router,
		fixedResearchPlanner{queries: []string{"unused"}}, &recordingResearchProvider{},
	)
	resolution := agent.NewChatLeaderDefinitionExecutor(runtime).ExecuteAttempt(ctx, attempt)
	if resolution.Disposition != agent.AttemptCompleted || normal.calls != 1 {
		t.Fatalf("resolution=%+v normal calls=%d", resolution, normal.calls)
	}
	if router.request.InvocationPolicy.Temperature == nil || *router.request.InvocationPolicy.Temperature != policy.Temperature ||
		router.request.InvocationPolicy.MaxOutputTokens != policy.MaxOutputTokens ||
		router.request.InvocationPolicy.Timeout != time.Duration(policy.TimeoutMS)*time.Millisecond {
		t.Fatalf("router invocation=%+v policy=%+v", router.request.InvocationPolicy, policy)
	}
}

func TestAgentResultIsCanonicalTypedImmutableAndUniquePerProducer(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "agent-result@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c111")
	catalog, _ := agentcatalog.LoadEmbedded()
	contract, _ := catalog.ResolveContract(agentcatalog.MustParseReference("chat.answer@1"))
	result, err := agent.NewAgentResult("result_one", runID, contract, json.RawMessage(`{ "content": "answer" }`))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := api.db.Pool().Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.StoreAgentResultInTx(context.Background(), tx, result); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result.PayloadSHA256 == "" || result.PayloadBytes != len(result.Payload) || string(result.Payload) != `{"content":"answer"}` {
		t.Fatalf("result=%+v", result)
	}
	mutated, _ := agent.NewAgentResult("result_two", runID, contract, json.RawMessage(`{"content":"changed"}`))
	tx, _ = api.db.Pool().Begin(context.Background())
	if err := agent.StoreAgentResultInTx(context.Background(), tx, mutated); err == nil || !strings.Contains(err.Error(), "immutable Agent Result conflict") {
		t.Fatalf("mutation err=%v", err)
	}
	_ = tx.Rollback(context.Background())
	if _, err := api.db.Pool().Exec(context.Background(), `update agent_run_results set payload='{}' where id='result_one'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("database mutation err=%v", err)
	}
}
