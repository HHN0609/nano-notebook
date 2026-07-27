package agentcatalog

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedCatalogContainsOnlyMigratedProductionAgents(t *testing.T) {
	catalog, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	definitions := catalog.Definitions()
	if got, want := len(definitions), 2; got != want {
		t.Fatalf("definitions=%d want=%d", got, want)
	}
	want := map[string]struct {
		executor string
		model    string
		tools    []string
		children []string
	}{
		"chat.leader@1": {
			executor: "chat_leader", model: "agent.chat-default@1",
			tools: []string{"calculate", "current_time", "search_evidence"}, children: []string{"research.source-discovery@1"},
		},
		"research.source-discovery@1": {
			executor: "research", model: "agent.research-default@1",
			tools: []string{"web_search"},
		},
	}
	for _, definition := range definitions {
		key := definition.Reference().String()
		expected, ok := want[key]
		if !ok {
			t.Fatalf("unexpected Definition %s", key)
		}
		if definition.Executor != expected.executor || definition.ModelPolicy.String() != expected.model {
			t.Fatalf("definition=%+v", definition)
		}
		if strings.Join(definition.Tools, ",") != strings.Join(expected.tools, ",") {
			t.Fatalf("tools for %s=%v want=%v", key, definition.Tools, expected.tools)
		}
		children := make([]string, 0, len(definition.Children))
		for _, child := range definition.Children {
			children = append(children, child.String())
		}
		if strings.Join(children, ",") != strings.Join(expected.children, ",") {
			t.Fatalf("children for %s=%v want=%v", key, children, expected.children)
		}
		if len(definition.SHA256) != 64 || definition.SourcePath == "" {
			t.Fatalf("immutable identity missing for %s: %+v", key, definition)
		}
	}
	if got, want := len(catalog.ModelPolicies()), 2; got != want {
		t.Fatalf("model policies=%d want=%d", got, want)
	}
	if got, want := len(catalog.Contracts()), 4; got != want {
		t.Fatalf("contracts=%d want=%d", got, want)
	}
	manifest, ok := catalog.ResolveRelease(MustParseReference("nano.default@1"))
	if !ok || manifest.Roots["chat"].String() != "chat.leader@1" || len(manifest.SHA256) != 64 {
		t.Fatalf("manifest=%+v ok=%v", manifest, ok)
	}
}

func TestReferenceRejectsMutableOrNonCanonicalSelectors(t *testing.T) {
	for _, value := range []string{"", "latest", "chat.leader", "chat.leader@latest", "Chat.leader@1", "chat.leader@0", "chat.leader@01", "chat/leader@1", " chat.leader@1"} {
		if _, err := ParseReference(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	reference, err := ParseReference("chat.leader@12")
	if err != nil || reference.Identity != "chat.leader" || reference.Version != 12 || reference.String() != "chat.leader@12" {
		t.Fatalf("reference=%+v err=%v", reference, err)
	}
}

func TestLoadFSRejectsUnknownFieldsTrailingJSONAndIdentityConflicts(t *testing.T) {
	valid := minimalCatalogFS()
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{
			name: "unknown definition field",
			mutate: func(files fstest.MapFS) {
				files["definitions/agent.test.v1.json"] = mapFile(`{"identity":"agent.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"tools":["tool"],"children":[],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1},"unknown":true}`)
			},
			want: "unknown field",
		},
		{
			name: "trailing JSON",
			mutate: func(files fstest.MapFS) {
				files["model-policies/model.test.v1.json"] = mapFile(`{"identity":"model.test","version":1,"provider_model":"provider/model","temperature":0,"max_output_tokens":128,"timeout_ms":1000}{}`)
			},
			want: "trailing",
		},
		{
			name: "path identity conflict",
			mutate: func(files fstest.MapFS) {
				files["definitions/agent.test.v2.json"] = files["definitions/agent.test.v1.json"]
			},
			want: "path",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := cloneMapFS(valid)
			test.mutate(files)
			if _, err := LoadFS(files); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("err=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestCatalogRejectsUnresolvedAndCyclicConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   string
	}{
		{
			name: "missing model policy",
			mutate: func(files fstest.MapFS) {
				delete(files, "model-policies/model.test.v1.json")
			},
			want: "model policy",
		},
		{
			name: "missing contract",
			mutate: func(files fstest.MapFS) {
				delete(files, "contracts/result.test.v1.schema.json")
			},
			want: "contract",
		},
		{
			name: "self cycle",
			mutate: func(files fstest.MapFS) {
				files["definitions/agent.test.v1.json"] = mapFile(`{"identity":"agent.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"tools":["tool"],"children":["agent.test@1"],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1},"delegation":{"description":"call test agent"}}`)
			},
			want: "cycle",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := cloneMapFS(minimalCatalogFS())
			test.mutate(files)
			if _, err := LoadFS(files); err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("err=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateBindingsAllowsOnlyCapabilityNarrowing(t *testing.T) {
	catalog, err := LoadFS(minimalCatalogFS())
	if err != nil {
		t.Fatal(err)
	}
	bindings := Bindings{
		Prompts: map[Reference]bool{MustParseReference("prompt.test@1"): true},
		Tools:   map[string]ToolCapability{"tool": {Scheduling: ToolOrderedSync}},
		Executors: map[string]ExecutorCapability{
			"test": {
				PromptPurposes: map[string]bool{"main": true}, Tools: map[string]bool{"tool": true},
				Contracts: map[Reference]bool{MustParseReference("input.test@1"): true, MustParseReference("result.test@1"): true},
				MaxLimits: Limits{ModelCalls: 2, Actions: 2, ActionBatch: 2, ContextBytes: 2048, ResultBytes: 2048, Attempts: 2},
			},
		},
	}
	if err := catalog.ValidateBindings(bindings); err != nil {
		t.Fatal(err)
	}

	bad := bindings
	bad.Executors = map[string]ExecutorCapability{
		"test": {
			PromptPurposes: map[string]bool{"main": true}, Tools: map[string]bool{"other": true},
			Contracts: map[Reference]bool{MustParseReference("input.test@1"): true, MustParseReference("result.test@1"): true},
			MaxLimits: Limits{ModelCalls: 1, Actions: 1, ActionBatch: 1, ContextBytes: 1024, ResultBytes: 1024, Attempts: 1},
		},
	}
	if err := catalog.ValidateBindings(bad); err == nil || !strings.Contains(strings.ToLower(err.Error()), "tool") {
		t.Fatalf("capability expansion err=%v", err)
	}
}

func TestGeneratedDelegationToolNameIsDeterministicAndBounded(t *testing.T) {
	reference := MustParseReference("research.source-discovery@1")
	name, err := DelegationToolName(reference)
	if err != nil {
		t.Fatal(err)
	}
	if name != "delegate.research.source-discovery.v1" {
		t.Fatalf("name=%q", name)
	}
	if _, err := DelegationToolName(Reference{Identity: strings.Repeat("a", 120), Version: 1}); err == nil {
		t.Fatal("accepted overlong generated MCP Tool name")
	}
}

func minimalCatalogFS() fstest.MapFS {
	return fstest.MapFS{
		"definitions/agent.test.v1.json":       mapFile(`{"identity":"agent.test","version":1,"executor":"test","model_policy":"model.test@1","prompts":{"main":"prompt.test@1"},"contracts":{"input":"input.test@1","result":"result.test@1"},"tools":["tool"],"children":[],"limits":{"model_calls":1,"actions":1,"action_batch":1,"context_bytes":1024,"result_bytes":1024,"attempts":1},"delegation":{"description":"call test agent"}}`),
		"model-policies/model.test.v1.json":    mapFile(`{"identity":"model.test","version":1,"provider_model":"provider/model","temperature":0,"max_output_tokens":128,"timeout_ms":1000}`),
		"contracts/input.test.v1.schema.json":  mapFile(`{"identity":"input.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"contracts/result.test.v1.schema.json": mapFile(`{"identity":"result.test","version":1,"schema":{"type":"object","additionalProperties":false}}`),
		"releases/nano.test.v1.json":           mapFile(`{"identity":"nano.test","version":1,"roots":{"test":"agent.test@1"}}`),
	}
}

func mapFile(value string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(value), Mode: fs.FileMode(0o644)}
}

func cloneMapFS(source fstest.MapFS) fstest.MapFS {
	cloned := make(fstest.MapFS, len(source))
	for path, file := range source {
		copy := *file
		copy.Data = append([]byte(nil), file.Data...)
		cloned[path] = &copy
	}
	return cloned
}
