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
