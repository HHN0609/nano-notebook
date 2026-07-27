package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/jackc/pgx/v5"
)

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
	if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		return agent.NewStore(tx).CreateConfiguredChatQueued(ctx, command)
	}); err != nil {
		t.Fatal(err)
	}
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
