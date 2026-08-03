package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
)

func TestAgentConfigurationRegistersIdempotentlyAndRejectsIdentityMutation(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	run := agent.DefaultRunConfig("config-test-v1")
	prompts, configuration, err := agent.DefaultAgentConfigurationBundle("config-test-v1", "leader-model", "research-model", run)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterAgentConfiguration(ctx, api.db, prompts, configuration); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterAgentConfiguration(ctx, api.db, prompts, configuration); err != nil {
		t.Fatalf("idempotent registration: %v", err)
	}
	var roles int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_role_profiles where configuration_set_id=$1`, configuration.ID).Scan(&roles); err != nil || roles != 2 {
		t.Fatalf("roles=%d err=%v", roles, err)
	}
	_, mutated, err := agent.DefaultAgentConfigurationBundle("config-test-v1", "leader-model", "different-research-model", run)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterAgentConfiguration(ctx, api.db, prompts, mutated); err == nil || !strings.Contains(err.Error(), "immutable Agent Configuration Set conflict") {
		t.Fatalf("mutation err=%v", err)
	}
	if err := app.VerifyAgentConfigurationReady(ctx, api.db, configuration); err != nil {
		t.Fatalf("readiness: %v", err)
	}
}

func TestWorkerReadinessAcceptsOnlyCurrentAndImmediatelyPreviousCompatibleSet(t *testing.T) {
	api, _, _, chatID := newChatFixture(t, "configuration-window@example.com")
	ctx := context.Background()
	makeConfiguration := func(id string) agent.AgentConfigurationSet {
		run := agent.DefaultRunConfig(id)
		prompts, configuration, err := agent.DefaultAgentConfigurationBundle(id, "leader-model-"+id, "research-model-"+id, run)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.RegisterAgentConfiguration(ctx, api.db, prompts, configuration); err != nil {
			t.Fatal(err)
		}
		return configuration
	}
	unsupported := makeConfiguration("configuration-window-v0")
	previous := makeConfiguration("configuration-window-v1")
	current := makeConfiguration("configuration-window-v2")
	runID := admitLegacyRunForTest(t, api, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788c093", "run_configuration_window", "job_configuration_window")
	if _, err := api.db.Pool().Exec(ctx, `update agent_runs set agent_config_id=$2,executor_version=$3 where id=$1`, runID, previous.ID, previous.Profiles[agent.RoleLeader].ExecutorVersion); err != nil {
		t.Fatal(err)
	}
	if err := app.VerifyAgentConfigurationReady(ctx, api.db, current); err != nil {
		t.Fatalf("previous compatible Set was rejected: %v", err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_runs set agent_config_id=$2,executor_version=$3 where id=$1`, runID, unsupported.ID, unsupported.Profiles[agent.RoleLeader].ExecutorVersion); err != nil {
		t.Fatal(err)
	}
	if err := app.VerifyAgentConfigurationReady(ctx, api.db, current); err == nil || !strings.Contains(err.Error(), unsupported.ID) {
		t.Fatalf("older compatible Set readiness error=%v", err)
	}
}
