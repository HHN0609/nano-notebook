package promptcatalog

import (
	"strings"
	"testing"
)

func TestEmbeddedCatalogContainsEveryProductionPrompt(t *testing.T) {
	catalog, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"agent.leader-router":                         "select_leader_route.v1",
		"agent.research-planner":                      "submit_research_queries.v1",
		"agent.chat-composer-bare":                    "final_draft_text.v1",
		"agent.chat-composer-grounded":                "grounded_final_draft_text.v1",
		"agent.query-contextualizer":                  "search_evidence.v1",
		"agent.studio-report":                         "studio_report_result.v1",
		"agent.studio-flashcards":                     "studio_flashcards_result.v1",
		"agent.studio-mind-map":                       "studio_mind_map_result.v1",
		"agent.studio-data-table":                     "studio_data_table_result.v1",
		"source-processing.image-evidence-normalizer": "image_evidence_regions.v1",
	}
	const extraVersions = 3 // agent.chat-composer-grounded@2/@3 and agent.chat-composer-bare@2, alongside their still-required @1s (legacy AgentPromptSet binding)
	if got := len(catalog.Versions()); got != len(want)+extraVersions {
		t.Fatalf("versions=%d want=%d", got, len(want)+extraVersions)
	}
	for identity, contract := range want {
		prompt, ok := catalog.Resolve(identity, 1)
		if !ok {
			t.Fatalf("missing %s@1", identity)
		}
		if prompt.Contract != contract || prompt.SHA256 == "" || strings.TrimSpace(prompt.Content) == "" {
			t.Fatalf("prompt=%+v", prompt)
		}
		if !strings.HasSuffix(prompt.SourcePath, ".md") {
			t.Fatalf("source path=%q", prompt.SourcePath)
		}
	}
	for _, version := range []int{2, 3} {
		grounded, ok := catalog.Resolve("agent.chat-composer-grounded", version)
		if !ok {
			t.Fatalf("missing agent.chat-composer-grounded@%d", version)
		}
		if grounded.Contract != "grounded_final_draft_text.v1" || grounded.SHA256 == "" || strings.TrimSpace(grounded.Content) == "" {
			t.Fatalf("prompt=%+v", grounded)
		}
	}
	bare, ok := catalog.Resolve("agent.chat-composer-bare", 2)
	if !ok {
		t.Fatal("missing agent.chat-composer-bare@2")
	}
	if bare.Contract != "final_draft_text.v1" || bare.SHA256 == "" || strings.TrimSpace(bare.Content) == "" {
		t.Fatalf("prompt=%+v", bare)
	}
}

func TestCanonicalSHA256NormalizesLineEndingsAndTerminalNewline(t *testing.T) {
	left := PromptVersion{Identity: "agent.test", Version: 7, Contract: "contract.v1", Content: "alpha\r\nbeta"}
	right := PromptVersion{Identity: "agent.test", Version: 7, Contract: "contract.v1", Content: "alpha\nbeta\n"}
	leftHash, err := CanonicalSHA256(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := CanonicalSHA256(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("left=%s right=%s", leftHash, rightHash)
	}
}

func TestCatalogRejectsSameIdentityAndVersionWithDifferentCanonicalDefinition(t *testing.T) {
	_, err := New([]PromptVersion{
		{Identity: "agent.test", Version: 1, Contract: "contract.v1", Content: "alpha", SourcePath: "one.md"},
		{Identity: "agent.test", Version: 1, Contract: "contract.v1", Content: "beta", SourcePath: "two.md"},
	})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("err=%v", err)
	}
}

func TestCatalogRejectsInvalidMetadataAndMutableVersionSelectors(t *testing.T) {
	tests := []PromptVersion{
		{Identity: "agent.test", Version: 0, Contract: "contract.v1", Content: "content"},
		{Identity: "latest", Version: 1, Contract: "contract.v1", Content: "content"},
		{Identity: "agent.test", Version: 1, Contract: "latest", Content: "content"},
		{Identity: "agent.test", Version: 1, Contract: "contract.v1", Content: " "},
	}
	for _, definition := range tests {
		if _, err := New([]PromptVersion{definition}); err == nil {
			t.Fatalf("accepted %+v", definition)
		}
	}
}

func TestMarkdownParserRejectsDuplicateMetadata(t *testing.T) {
	_, err := parseMarkdown("duplicate.md", "---\nidentity: agent.test\nidentity: agent.other\nversion: 1\ncontract: contract.v1\n---\ncontent\n")
	if err == nil {
		t.Fatal("accepted duplicate identity metadata")
	}
}
