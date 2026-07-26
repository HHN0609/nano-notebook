# Agent Job Legacy Migration Order

## Problem

An existing pre-Sprint 9 database has an `agent_jobs` table without the
`available_at` and `last_error_code` columns. The application migration tries
to create `agent_jobs_queued_idx` over `available_at` before the compatibility
section adds that column. PostgreSQL therefore aborts the migration with
SQLSTATE `42703`, and `scripts/start` exits before starting the application
processes.

Fresh databases do not expose the defect because `create table if not exists
agent_jobs` includes the new columns. Existing migration coverage also does not
remove the Sprint 9 columns when reconstructing a legacy schema.

## Decision

Preserve existing data and make the migration order valid for both fresh and
legacy databases.

The migration will not create `agent_jobs_queued_idx` immediately after the
`create table if not exists` statement. The compatibility section will remain
the single authority for the index and will perform these operations in order:

1. add `available_at` with its non-null default when missing;
2. add `last_error_code` when missing and install its constraint;
3. drop any legacy `agent_jobs_queued_idx` definition;
4. create the current partial index over `available_at`, `created_at`, and `id`.

This order is idempotent. A fresh database already has the columns, so the
column additions are no-ops. A legacy database receives the columns before any
statement references them. Existing rows receive the `available_at` default,
and no Agent Run or Job rows are deleted or recreated.

## Alternatives Rejected

- Raising the failure as an operator reset requirement would destroy local
  durable state and leave the upgrade defect unresolved.
- A conditional PL/pgSQL block could detect the column before creating the
  index, but it duplicates logic already owned by the compatibility section
  and adds unnecessary dynamic migration control flow.
- Keeping both index creation statements would preserve the ordering hazard
  and two competing authorities for the same index definition.

## Verification

An integration regression test will start from a migrated database, remove the
Sprint 9 scheduling columns and current queue index to reproduce the legacy
shape, and retain representative Job data. Before the fix, rerunning
`RunMigrations` must fail because `available_at` is absent. After the fix, the
same migration must succeed and prove that:

- the existing Job row remains present;
- `available_at` and `last_error_code` exist;
- `agent_jobs_queued_idx` exists with the current ordered columns;
- reapplying migrations remains successful.

The focused integration test and the full Go test suite must pass. Finally,
the real local migration must succeed against the existing development
database before the application stack is restarted.
