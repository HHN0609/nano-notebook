package app

import (
	"strings"
	"testing"
)

func TestGenericAgentRuntimeMigrationIsAdditiveAndProductNeutral(t *testing.T) {
	for _, required := range []string{
		"create table if not exists agent_trees",
		"create table if not exists chat_runs",
		"create table if not exists agent_run_results",
		"alter table agent_runs add column if not exists definition_identity",
		"alter table agent_runs alter column user_id drop not null",
		"runtime_kind in ('legacy_role','configured')",
		"insert into agent_trees",
		"insert into chat_runs",
		"create trigger agent_run_results_immutable",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}
