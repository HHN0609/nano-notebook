package app

import (
	"strings"
	"testing"
)

func TestResearchModeMigrationInstallsProductAndProjectionTables(t *testing.T) {
	for _, required := range []string{
		"create table if not exists research_sessions",
		"create table if not exists research_plan_versions",
		"create table if not exists research_report_versions",
		"create table if not exists research_evidence_ledger",
		"alter table research_evidence_ledger add column if not exists media_type text",
		"alter table research_evidence_ledger add column if not exists page_count integer",
		"alter table research_evidence_ledger add column if not exists document_handle text",
		"alter table research_evidence_ledger add column if not exists failure_reason text",
		"create table if not exists research_step_capsules",
		"create table if not exists research_rollups",
		"status text not null check (status in ('planning','awaiting_confirmation','queued','running','publishing','completed','failed','cancelled'))",
		"primary key (session_id,version)",
		"unique (run_id,decision_no)",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}

func TestResearchThresholdCompactionMigrationInstallsAppendOnlyArtifactsAndBoundedFailures(t *testing.T) {
	for _, required := range []string{
		"create table if not exists research_archival_capsules",
		"capsule_json jsonb not null",
		"capsule_bytes integer not null check (capsule_bytes between 1 and 8192)",
		"source_checkpoint_sha256 text not null",
		"unique (run_id,decision_no)",
		"create table if not exists research_task_memories",
		"memory_bytes integer not null check (memory_bytes between 1 and 32768)",
		"create table if not exists research_compaction_failures",
		"reason_code text not null check (reason_code ~ '^[a-z][a-z0-9_]{2,63}$')",
		"create trigger research_archival_capsules_immutable",
		"create trigger research_task_memories_immutable",
		"alter table research_archival_capsules enable row level security",
		"alter table research_task_memories enable row level security",
		"alter table research_compaction_failures enable row level security",
		"grant select, insert on research_archival_capsules,research_task_memories,research_compaction_failures to nano_worker",
		"create policy research_archival_capsules_worker",
		"create policy research_task_memories_worker",
		"create policy research_compaction_failures_worker",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("Research threshold Compaction migration is missing %q", required)
		}
	}
}

func TestResearchSourceImportMigrationKeepsLifecycleAuthorityInSourceTables(t *testing.T) {
	for _, required := range []string{
		"create table if not exists research_source_imports",
		"session_id text not null references research_sessions(id) on delete cascade",
		"run_id text not null references agent_runs(id) on delete cascade",
		"action_id text not null",
		"requested_url text not null",
		"final_url_identity text",
		"source_id text references source_sources(id) on delete set null",
		"processing_job_id text references source_processing_jobs(id) on delete set null",
		"barrier_observed_attempt_no integer",
		"retrieval_error_code text",
		"unique (run_id, action_id)",
		"alter table research_source_imports enable row level security",
		"grant select, insert, update, delete on research_source_imports to nano_worker",
		"create unique index if not exists source_sources_notebook_url_pdf_hash_idx",
		"create policy source_sources_worker_insert on source_sources",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("Research Source import migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"research_source_imports.source_state",
		"research_source_imports.job_status",
		"research_source_imports.searchable",
	} {
		if strings.Contains(migrationsSQL, forbidden) {
			t.Fatalf("Research import relation copied lifecycle authority %q", forbidden)
		}
	}
}

func TestSourceMapMigrationInstallsImmutableAuthorizedDerivedArtifacts(t *testing.T) {
	for _, required := range []string{
		"create table if not exists source_maps",
		"id text primary key check (id ~ '^smap_[0-9a-f]{32}$')",
		"source_id text not null references source_sources(id) on delete cascade",
		"revision_id text not null references source_evidence_revisions(id) on delete cascade",
		"original_sha256 text not null",
		"artifact_object_key text not null unique",
		"artifact_sha256 text not null",
		"parser_identity text not null",
		"parser_version text not null",
		"parser_policy_id text not null",
		"navigation_kind text not null check (navigation_kind in ('embedded_outline','inferred_sections','page_samples'))",
		"confidence text not null check (confidence in ('high','medium','low'))",
		"unique (revision_id,parser_policy_id)",
		"create trigger source_maps_immutable",
		"alter table source_maps enable row level security",
		"grant select on source_maps to nano_app",
		"grant select, insert on source_maps to nano_worker",
		"create policy source_maps_app_read on source_maps",
		"create policy source_maps_worker on source_maps",
	} {
		if !strings.Contains(migrationsSQL, required) {
			t.Fatalf("Source Map migration is missing %q", required)
		}
	}
}
