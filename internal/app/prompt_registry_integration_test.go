package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/app"
)

func TestPromptCatalogMigrationRegistersIdempotentlyAndGuardsMutation(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	var count int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_prompt_versions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("registered prompts=%d want=6", count)
	}
	if err := app.RunMigrations(ctx, api.db); err != nil {
		t.Fatalf("idempotent re-registration: %v", err)
	}
	if _, err := api.db.Pool().Exec(ctx, `delete from agent_prompt_versions where prompt_identity='agent.leader-router' and prompt_version=1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delete err=%v", err)
	}
	if err := app.VerifyEmbeddedPromptCatalog(ctx, api.db); err != nil {
		t.Fatalf("readiness: %v", err)
	}
}

func TestPromptCatalogMigrationFailsOnSameVersionWithDifferentHash(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	if _, err := api.db.Pool().Exec(ctx, `
		drop trigger agent_prompt_versions_immutable on agent_prompt_versions;
		update agent_prompt_versions set canonical_sha256=repeat('0',64)
		where prompt_identity='agent.leader-router' and prompt_version=1
	`); err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(ctx, api.db); err == nil || !strings.Contains(err.Error(), "immutable prompt conflict") {
		t.Fatalf("migration err=%v", err)
	}
}
