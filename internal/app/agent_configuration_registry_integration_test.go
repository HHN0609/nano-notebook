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
