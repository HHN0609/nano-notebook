package app_test

import (
	"context"
	"testing"
)

func TestMigrationsInstallGenericDelegationAuthorityAndRetireDuplicates(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	var genericExists, legacyExists, parentColumnExists bool
	if err := api.db.Pool().QueryRow(ctx, `select to_regclass('public.agent_run_delegations') is not null`).Scan(&genericExists); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select to_regclass('public.agent_research_delegations') is not null`).Scan(&legacyExists); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `
		select exists(select 1 from information_schema.columns
		where table_schema='public' and table_name='agent_runs' and column_name='parent_run_id')
	`).Scan(&parentColumnExists); err != nil {
		t.Fatal(err)
	}
	if !genericExists || legacyExists || parentColumnExists {
		t.Fatalf("generic=%t legacy=%t parent_column=%t", genericExists, legacyExists, parentColumnExists)
	}
	var states string
	if err := api.db.Pool().QueryRow(ctx, `
		select pg_get_constraintdef(oid) from pg_constraint
		where conrelid='agent_run_delegations'::regclass and conname='agent_run_delegations_state_check'
	`).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if states == "" {
		t.Fatal("delegation state constraint is missing")
	}
}
