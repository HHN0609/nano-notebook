# Sprint 10 Agent Framework Implementation Plan

**Date:** 2026-07-27

**Source:** `docs/superpowers/specs/2026-07-27-configured-agent-framework-design.md`

**Execution:** Directly on `main`, test-driven, atomic behavior commits, no new product Agent.

## Slice 1: Baseline And MCP Dependency Gate

1. Run the existing full Go/PostgreSQL test gate and record baseline failures separately from Sprint 10 regressions.
2. Inspect the official MCP Go SDK version compatible with protocol 2026-07-28, in-memory transport, per-request metadata, and standard Tool discovery/invocation.
3. Add a focused compile/conformance spike proving the selected standard SDK path before production architecture depends on it.
4. Update `go.mod`/`go.sum` and the declared Go version only when the spike proves the required surface.

## Slice 2: Embedded Catalogs

1. Write failing tests for strict Agent Definition, Model Policy, and Contract decoding, canonical references, hashing, duplicates, unknown fields, and conflicts.
2. Add `internal/agentcatalog` with embedded definitions, policies, contracts, and strict schemas.
3. Write failing cross-registry tests for Executor/tool/Prompt/Contract/child resolution, local limit ceilings, topology, and generated MCP name validation.
4. Add only `chat.leader@1` and `research.source-discovery@1` and their required policies/contracts.
5. Add immutable database registration and release-manifest selection tests and implementation.
6. Run focused tests, downstream compile tests, and commit.

## Slice 3: Executor Registry

1. Write failing tests for duplicate/unknown Executor rejection and Definition capability narrowing.
2. Replace new-path `RoleRegistry` resolution with one `ExecutorRegistry` carrying code-owned ceilings and one implementation per identity.
3. Add Definition-pinned Attempt loading and new admission without Role/executor-version dispatch.
4. Retain a read-only legacy resolver for non-terminal Sprint 9 Runs.
5. Route existing Leader and Research behavior through `chat_leader` and `research` registrations.
6. Run focused and downstream tests and commit.

## Slice 4: Synchronous MCP Tool Plane

1. Write failing in-memory Host/Server tests for scoped discovery and invocation using the official SDK.
2. Add a Tool Registry with typed adapters, scheduling classes, canonical materialized definitions, and hashes.
3. Add process-local Attempt Context Handles and server-side Lease/Definition/authorization/budget validation.
4. Add stable `action_id` metadata, exhaustive Tool Error classification, and at-least-once Checkpoint behavior.
5. Adapt `calculate`, `current_time`, and `search_evidence`, then make Controller execution cross MCP exclusively.
6. Keep accepted legacy Checkpoint payloads readable, remove migrated direct registry callers, verify, and commit.

## Slice 5: Research Web Search

1. Write failing tests for one `web_search` proposal containing one to three bounded queries.
2. Add the Web Search MCP adapter over the existing Provider boundary.
3. Change new Research execution to use one standard Action Proposal/Result and deterministically produce its terminal outcome.
4. Preserve legacy query-plan and per-query result recovery for already admitted Runs.
5. Verify Source Discovery integration and commit.

## Slice 6: Generic Durable Runtime

1. Write migration and repository tests for additive `chat_runs`, `agent_trees`, generic Definition-pinned `agent_runs`, Delegation `action_id`, and immutable `agent_run_results`.
2. Backfill existing Chat ownership and tree state while preserving public Run identifiers and terminal history.
3. Route new Chat admission, retry, cancellation, publication, Job, Checkpoint, Trace, and evidence ownership through the separated model.
4. Add Agent Result canonical payload, hash, size, uniqueness, and reference-only projection tests.
5. Stop new Delegation writes from storing parent/child Roles while legacy readers remain available.
6. Run migration, integration, and recovery suites and commit.

## Slice 7: Configured Delegation MCP Tools

1. Write failing materialization tests for exact configured children and deterministic names.
2. Add a generated delegation adapter that cannot accept a Definition identity from model input.
3. Use standard `tools/call` to atomically accept the child work and return a Controller-internal scheduling receipt; do not claim SEP-2663 semantics.
4. Keep `delegation_id` as the PostgreSQL lifecycle handle; add no Task table or polling Worker.
5. Make child creation and repeated `action_id` resolution transactional and idempotent.
6. Store one immutable Agent Result, wake parent atomically, and checkpoint only its reference and integrity metadata on resume.
7. Run conformance, crash-injection, cancellation, authorization, and missed-notification tests and commit.

## Slice 8: Activate And Drain

1. Switch fresh Chat admission to the exact release manifest and new runtime path.
2. Prove new rows do not depend on Role/Profile/executor-version fields.
3. Remove the legacy Action Registry, Role Registry, direct Research Provider path, and duplicate constructors from production new-path wiring.
4. Keep legacy decoders/readers until a durable no-active-reference query permits later contract removal.
5. Run all existing Chat/Research/API/publication regression tests and commit.

## Slice 9: Acceptance

1. Run formatting, full Go/PostgreSQL tests, targeted race tests, vet, production builds, and MCP conformance.
2. Create a 40-row evidence matrix for every Sprint 10 success criterion.
3. Audit the repository for forbidden Role writes, direct executable tool paths, concrete new Agents, Studio product changes, and obsolete runtime authority.
4. Mark the PRD/design implemented only when every criterion has direct evidence.
5. Commit acceptance evidence and report every commit plus verification command.
