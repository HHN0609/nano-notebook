package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/skillcatalog"
)

func TestReadSkillActionDisclosesOnlyPinnedAllowedSkill(t *testing.T) {
	definitions := readSkillTestAgentCatalog(t)
	skills := skillcatalog.MustLoadEmbedded()
	action := NewReadSkillAction(definitions, skills)
	result, err := action.Execute(context.Background(), ActionRequest{
		Definition: agentcatalog.MustParseReference("research.test@1"),
		Input:      json.RawMessage(`{"skill":"skill.grill-me@1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded {
		t.Fatalf("result=%+v", result)
	}
	var output struct {
		Skill        string `json:"skill"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Skill != "skill.grill-me@1" || output.Name != "Grill Me" || output.Description == "" || output.Instructions == "" || len(output.SHA256) != 64 {
		t.Fatalf("output=%+v", output)
	}
}

func TestReadSkillActionRejectsUnpinnedSkillAsRecoverableResult(t *testing.T) {
	action := NewReadSkillAction(readSkillTestAgentCatalog(t), skillcatalog.MustLoadEmbedded())
	result, err := action.Execute(context.Background(), ActionRequest{
		Definition: agentcatalog.MustParseReference("chat.test@1"),
		Input:      json.RawMessage(`{"skill":"skill.grill-me@1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionDomainError || result.ErrorCode != "skill_not_allowed" {
		t.Fatalf("result=%+v", result)
	}
}

func readSkillTestAgentCatalog(t *testing.T) agentcatalog.Catalog {
	t.Helper()
	files := fstest.MapFS{
		"definitions/research.test.v1.json":            mapFileForSkillTest(`{"identity":"research.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"skills":["skill.grill-me@1"],"tools":["read_skill"],"children":[],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1}}`),
		"definitions/chat.test.v1.json":                mapFileForSkillTest(`{"identity":"chat.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"tools":["read_skill"],"children":[],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1}}`),
		"model-policies/model.test.v1.json":            mapFileForSkillTest(`{"identity":"model.test","version":1,"provider_model":"provider/model","temperature":0,"max_output_tokens":128,"timeout_ms":1000}`),
		"provider-capabilities/provider.model.v1.json": mapFileForSkillTest(`{"identity":"provider.model","version":1,"provider_model":"provider/model","resolved_model":"model-1","context_window_tokens":1000,"max_input_tokens":900,"max_output_tokens":200,"tokenizer_identity":"test-tokenizer","tokenizer_version":"1","invocation_mode":"non_thinking"}`),
		"model-context-policies/context.test.v1.json":  mapFileForSkillTest(`{"identity":"context.test","version":1,"invocation_model_policy":"model.test@1","provider_capability":"provider.model@1","pinned_max_output_tokens":128,"soft_input_limit_tokens":700,"estimation_safety_tokens":100,"keep_recent_tokens":200,"summary_max_output_tokens":100,"overflow_retry_limit":1}`),
		"contracts/input.test.v1.schema.json":          mapFileForSkillTest(`{"identity":"input.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"contracts/result.test.v1.schema.json":         mapFileForSkillTest(`{"identity":"result.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"releases/nano.test.v1.json":                   mapFileForSkillTest(`{"identity":"nano.test","version":1,"roots":{"research":"research.test@1","chat":"chat.test@1"}}`),
	}
	catalog, err := agentcatalog.LoadFS(files)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func mapFileForSkillTest(value string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(value), Mode: fs.FileMode(0o644)}
}
