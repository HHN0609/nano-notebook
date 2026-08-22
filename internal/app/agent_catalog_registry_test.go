package app

import (
	"strings"
	"testing"
)

func TestAgentCatalogMigrationInstallsImmutableRegistries(t *testing.T) {
	for _, required := range []string{
		"create table if not exists agent_skill_versions",
		"create table if not exists agent_contract_versions",
		"create table if not exists agent_model_policy_versions",
		"create table if not exists agent_definition_versions",
		"create table if not exists agent_release_manifests",
		"primary key (definition_identity,definition_version)",
		"skill_allowlist jsonb not null",
		"create trigger agent_skill_versions_immutable",
		"create trigger agent_definition_versions_immutable",
		"raise exception 'Agent catalog records are immutable'",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}
