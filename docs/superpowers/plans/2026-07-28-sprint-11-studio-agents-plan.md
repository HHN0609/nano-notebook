# Sprint 11 Studio Agents Implementation Plan

**Date:** 2026-07-28

**Sources:** `docs/sprint/SPRINT-11-PRD.md`, `docs/superpowers/specs/2026-07-28-sprint-11-studio-structured-outputs-design.md`, ADR 0046

**Execution:** Directly on `main` at the user's request, test-driven, atomic behavior commits. The user pre-approved design and written-review gates. Quiz is excluded.

## Slice 1: Baseline And Catalog Red Gate

1. Run focused catalog and Web tests to establish the existing baseline without touching the user's unrelated environment-file change.
2. Extend `internal/agentcatalog/catalog_test.go` first with failing assertions for exactly six Definitions, four Studio bindings, immutable `nano.default@1`, exact `nano.default@2` roots, and strict prompt/contract/tool/limit resolution.
3. Add the four Definition JSON files, common input contract, four result contracts, one Studio model policy, four Prompt catalog entries, and `nano.default.v2.json`.
4. Extend Executor capability tests to prove the shared Executor rejects every unreviewed prompt, contract, tool, child, and limit expansion.
5. Run focused catalog/configuration tests and commit the independently reviewable catalog slice.

## Slice 2: Studio Artifact Domain

1. Add failing table tests in a new `internal/studio` package for kind parsing and every Report, Flashcards, Mind Map, and Data Table boundary.
2. Implement strict JSON decoding with unknown-field/trailing-data rejection, result-size limits, unique identifiers, pinned Source references, tree topology/depth validation, and exact table cardinality.
3. Add deterministic title/source-reference projection helpers used by both publication and HTTP responses.
4. Run focused tests and race tests for the pure domain package, then commit.

## Slice 3: Durable Product Storage And Authorization

1. Add failing migration/source assertions and PostgreSQL integration tests for `studio_outputs`, indexes, lifecycle projection, RLS, run ownership, membership loss, Source invalidation, Notebook deletion, and active deletion.
2. Extend `internal/app/db.go` additively with the product table, constraints, triggers, policies, ownership helper branches, and lifecycle cleanup while preserving generic `agent_runs` neutrality.
3. Add `internal/studio/store.go` for list/detail/admission/delete operations under existing app/worker transaction roles.
4. Prove completed artifacts are immutable, non-completed rows contain no artifact, and deletion cancels active Run/Job before product visibility disappears.
5. Run focused PostgreSQL integration tests and commit.

## Slice 4: Idempotent Studio Admission

1. Write failing application integration tests for Editor/Owner admission, Viewer rejection, CSRF, `Idempotency-Key`, canonical mismatch conflicts, allowed kinds, one-to-fifty ordered Ready Sources, duplicates, cross-Notebook Sources, and missing Evidence/index state.
2. Add a Studio admission service that maps browser kind to one exact release root, pins the source snapshot, and atomically creates Output, Agent Tree, configured root Run, Evidence Set, Job, Trace, and idempotency response.
3. Extend server startup/readiness to resolve all four exact Studio roots from the selected release.
4. Prove no HTTP field can select Definition, Executor, Prompt, Policy, or Contract identities.
5. Run focused integration tests and commit.

## Slice 5: Shared Structured-Output Executor

1. Add failing controller/runtime tests proving the first decision must be one `search_evidence` proposal, the second must be final JSON, and the run cannot exceed two calls, one logical action, batch size one, or create children.
2. Add a Studio execution type and register `studio_structured_output` in `internal/agent/executor_registry.go` for the four exact Definitions.
3. Implement a Studio runtime adapter that delegates generic checkpoint, lease, Trace, replay, and MCP authority methods to `PostgresRuntime`, while owning prompt construction, recovery context, kind-specific decoding, and publication.
4. Generalize configured trace prompt/contract identity so Studio does not inherit Chat Leader semantic labels or introduce a new Role.
5. Atomically publish immutable Agent Result, Studio artifact/status, Run/Job terminal state, budget, and Trace; delegate safe failure projection to the existing attempt disposition path.
6. Wire the Executor in `cmd/worker/main.go`, run focused controller/executor/recovery/race tests, and commit.

## Slice 6: Studio HTTP And Events

1. Add failing handler/integration tests for newest-first list, detail sharing, artifact visibility, safe errors, authorized idempotent delete, and response data minimization.
2. Add `POST/GET /api/v1/notebooks/{id}/studio-outputs`, `GET/DELETE /api/v1/studio-outputs/{id}`, and the Notebook Output SSE endpoint in `internal/app/server.go` and focused handler files.
3. Reuse the durable-first realtime pattern: send current bounded projection on connect, listen only as an invalidation hint, and keep polling/refetch recovery viable.
4. Run app and realtime integration tests and commit.

## Slice 7: NotebookLM-Referenced Studio Web UI

1. Replace `studio-panel.tsx` tests first with failing assertions for exactly four enabled actions, no Quiz/media/Add Note placeholder, Viewer permissions, selected-Ready-Source admission, running/failed/completed rows, deletion, refresh restoration, and opening details.
2. Add a typed `studio-outputs.ts` client/controller following existing Query/SSE/CSRF/idempotency conventions and connect it to the selected Source state in `notebook-workspace.tsx`.
3. Replace the nine placeholder actions in `App.tsx` with localized Report, Flashcards, Mind Map, and Data Table copy.
4. Implement the compact 2-by-2 tinted grid, Recent list, optimistic queued state, status/error treatments, and authorized overflow deletion in `studio-panel.tsx` and `styles.css`.
5. Run focused Vitest tests, lint, and build, then commit.

## Slice 8: Four Focused Viewers

1. Add failing component tests for safe Report Markdown, flashcard flip/navigation/shuffle/restart, Mind Map connected hierarchy/branch toggles/zoom, semantic Data Table overflow, Source chips, close/focus behavior, and reduced motion.
2. Add one focused Output dialog with kind-specific viewer components under `web/src/components/workspace/`.
3. Reuse the existing Source opening path for valid references and unavailable handling for deleted/missing Sources.
4. Add responsive styles for `1440x900` and `390x844`, preserving existing three-panel and tab behavior.
5. Run focused UI tests, accessibility checks, lint, build, and commit.

## Slice 9: Acceptance And Delivery

1. Run `gofmt`, focused Go suites, `go test -race` for Studio/catalog/executor code, `go vet ./...`, `./scripts/test-go`, focused Web tests, `./scripts/test-web`, and production builds.
2. Start the local stack with deterministic test fixtures and capture inspected Studio screenshots at `1440x900` and `390x844` for empty, running/recent, and each focused viewer state where practical.
3. Audit Definitions and source paths to prove no Quiz/media Agent, no mutation of release v1, no direct Retrieval/Provider tool bypass, and no new Role/product columns on `agent_runs`.
4. Write `docs/sprint/SPRINT-11-ACCEPTANCE.md` mapping all 58 PRD criteria to test, source, API, or screenshot evidence.
5. Commit acceptance evidence, verify only intended Sprint 11 files are staged, and report commit hashes plus exact verification commands.
