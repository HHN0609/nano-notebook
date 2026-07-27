package app_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
)

func TestAgentCatalogRegistersIdempotentlyAndSelectsExactRelease(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	catalog := testAgentCatalog(t, "provider/model")
	if err := app.RegisterAgentCatalog(ctx, api.db, catalog); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterAgentCatalog(ctx, api.db, catalog); err != nil {
		t.Fatalf("idempotent registration: %v", err)
	}
	manifest, err := app.VerifyAgentCatalogReady(ctx, api.db, catalog, agentcatalog.MustParseReference("nano.test@1"))
	if err != nil || manifest.Roots["test"].String() != "agent.test@1" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	for table, want := range map[string]int{
		"agent_definition_versions":   1,
		"agent_model_policy_versions": 1,
		"agent_contract_versions":     2,
		"agent_release_manifests":     1,
	} {
		var count int
		if err := api.db.Pool().QueryRow(ctx, "select count(*) from "+table+" where source_path like 'definitions/%' or source_path like 'model-policies/%' or source_path like 'contracts/%' or source_path like 'releases/%'").Scan(&count); err != nil || count < want {
			t.Fatalf("%s count=%d want-at-least=%d err=%v", table, count, want, err)
		}
	}

	mutated := testAgentCatalog(t, "provider/changed")
	if err := app.RegisterAgentCatalog(ctx, api.db, mutated); err == nil || !strings.Contains(err.Error(), "immutable model policy conflict") {
		t.Fatalf("mutation err=%v", err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_definition_versions set executor='changed' where definition_identity='agent.test' and definition_version=1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("database mutation err=%v", err)
	}
}

func TestAgentCatalogReadinessRejectsUnregisteredOrMutableReleaseSelector(t *testing.T) {
	api := newTestAPI(t)
	catalog := testAgentCatalog(t, "provider/model")
	if _, err := app.VerifyAgentCatalogReady(context.Background(), api.db, catalog, agentcatalog.MustParseReference("nano.test@1")); err == nil || !strings.Contains(err.Error(), "registered release") {
		t.Fatalf("unregistered release err=%v", err)
	}
	if _, err := agentcatalog.ParseReference("nano.test@latest"); err == nil {
		t.Fatal("mutable release selector was accepted")
	}
}

func testAgentCatalog(t *testing.T, providerModel string) agentcatalog.Catalog {
	t.Helper()
	files := fstest.MapFS{
		"definitions/agent.test.v1.json":       catalogMapFile(`{"identity":"agent.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"tools":["tool"],"children":[],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1}}`),
		"model-policies/model.test.v1.json":    catalogMapFile(`{"identity":"model.test","version":1,"provider_model":"` + providerModel + `","temperature":0,"max_output_tokens":128,"timeout_ms":1000}`),
		"contracts/input.test.v1.schema.json":  catalogMapFile(`{"identity":"input.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"contracts/result.test.v1.schema.json": catalogMapFile(`{"identity":"result.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"releases/nano.test.v1.json":           catalogMapFile(`{"identity":"nano.test","version":1,"roots":{"test":"agent.test@1"}}`),
	}
	catalog, err := agentcatalog.LoadFS(files)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func catalogMapFile(value string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(value), Mode: fs.FileMode(0o644)}
}
