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
