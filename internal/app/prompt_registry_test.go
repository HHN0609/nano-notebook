package app

import (
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
)

func TestValidateRegisteredPromptIsIdempotentOnlyForExactImmutableDefinition(t *testing.T) {
	definition := promptcatalog.PromptVersion{
		Identity: "agent.test", Version: 1, Contract: "contract.v1", Content: "content\n",
		SHA256: strings.Repeat("a", 64), SourcePath: "prompts/agent.test.v1.md",
	}
	stored := registeredPromptVersion{Contract: definition.Contract, Content: definition.Content, SHA256: definition.SHA256}
	if err := validateRegisteredPrompt(definition, stored); err != nil {
		t.Fatalf("exact re-registration: %v", err)
	}
	for _, mutation := range []registeredPromptVersion{
		{Contract: "contract.v2", Content: definition.Content, SHA256: definition.SHA256},
		{Contract: definition.Contract, Content: "changed\n", SHA256: definition.SHA256},
		{Contract: definition.Contract, Content: definition.Content, SHA256: strings.Repeat("b", 64)},
	} {
		if err := validateRegisteredPrompt(definition, mutation); err == nil || !strings.Contains(err.Error(), "immutable prompt conflict") {
			t.Fatalf("stored=%+v err=%v", mutation, err)
		}
	}
}

func TestPromptRegistryMigrationInstallsImmutableStorageGuard(t *testing.T) {
	for _, required := range []string{
		"create table if not exists agent_prompt_versions",
		"primary key (prompt_identity,prompt_version)",
		"create trigger agent_prompt_versions_immutable",
		"raise exception 'agent_prompt_versions is immutable'",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}
