# Sprint 10 Acceptance Evidence

## Result

- **Status:** Partial delivery: configured activation is implemented; configured child delegation remains SDK-gated.
- **Verified:** 2026-07-28
- **Authority:** `docs/sprint/SPRINT-10-PRD.md`
- **Automated gate:** `./scripts/test-go`
- **Gate result:** exit 0 after the final drain-readiness patch. The gate passed formatting, all Go packages and PostgreSQL/MinIO integrations (`internal/app` in 135.991s), `go vet`, and production binary builds. `go test -race -count=1 ./internal/agent ./internal/agentcatalog ./internal/models` also passed.
- **Scope audit:** the delivery diff contains no Web/Studio/UI surface and the embedded catalog contains only `chat.leader@1` and `research.source-discovery@1`; no audio, quiz, test, or other product Agent was added.
- **Protocol gate:** official `github.com/modelcontextprotocol/go-sdk v1.7.0-pre.3` cannot publicly encode the SEP-2663 polymorphic `tools/call` Task result and does not permit replacing its registered `tools/call` codec. A 2026-07-28 check found no newer published prerelease, official `main` commit `bc72835f62eb` still exposes no public Task-result surface, and the maintainer-authored [Go SDK issue #942](https://github.com/modelcontextprotocol/go-sdk/issues/942) explicitly records that Tasks have not been implemented. Criteria 22–30 therefore remain gated rather than using a private wire format; this is the fail-closed behavior required by criterion 31.
- **Done:** no. Criteria 10, 17, 19, 22–30, 37, 39, and the corresponding portion of 40 are not fully met.

## Success-Criterion Evidence

| # | Status | Evidence |
| --- | --- | --- |
| 1 | Met | Strict JSON Definitions, Policies, Contracts, and Releases are embedded under `internal/agentcatalog/`, canonically hashed, and immutably registered. |
| 2 | Met | Catalog, binding, registry, and readiness tests reject unknown fields, conflicts, dangling references, invalid generated names, and expanded capabilities. |
| 3 | Met | The typed catalog accepts only the fixed declarative schema; no executable configuration or mutable resolution surface exists. |
| 4 | Met | Each Definition binds one Executor, exact Policy/Prompt/Contract references, bounded tools/children, and local limits. |
| 5 | Met | Model Policies pin Provider model, temperature, output-token limit, and timeout; runtime, Bifrost, Trace hash, and Replay use those exact non-secret settings. |
| 6 | Met | `ExecutorRegistry` owns new-path dispatch and code-owned capability ceilings; `RoleRegistry` is isolated to legacy drain. |
| 7 | Met | Configured admissions persist Definition/Executor/Policy pins and the deferred neutrality constraint rejects Role, executor-version, or product ownership on committed Agent Runs. |
| 8 | Met | `NANO_AGENT_RELEASE` selects one exact release; admission and retry pin the resolved transitive configuration. |
| 9 | Met | The catalog contains only `chat.leader@1` and `research.source-discovery@1`. |
| 10 | Partial / gated | `calculate`, `current_time`, `search_evidence`, and `web_search` cross the official in-memory MCP plane. Generated `delegate.*` tools cannot ship until the Tasks gate opens. |
| 11 | Met | `select_leader_route` remains a typed Models decision contract, not an executable MCP capability. |
| 12 | Met | MCP discovery intersects the pinned Definition allowlist with the registered tool/executor ceiling. |
| 13 | Met | Tool schemas expose bounded business inputs only; authority is absent from model-visible arguments. |
| 14 | Met | The Host injects an opaque Attempt Context Handle and stable `action_id` in non-model-visible MCP metadata. |
| 15 | Met | Discovery/call paths revalidate Lease, Definition/hash, allowlist, ownership, deadline, and remaining budget server-side. |
| 16 | Met | Attempt handles are process-local, removed on close/authority loss, and never persisted. |
| 17 | Partial / gated | Synchronous tools retain at-least-once semantics and stable Action identities; Delegation has a durable `action_id` uniqueness boundary but no Task adapter ships. |
| 18 | Met | Accepted Proposal/Result Checkpoints remain the logical invocation ledger; no generic Tool-call table was added. |
| 19 | Partial / gated | All shipped tools are `ordered_sync`; `exclusive_task` is intentionally absent until generated delegation tools are protocol-conformant. |
| 20 | Met | Tool/model errors map to retryable, abandoned, or terminal Attempt outcomes without fabricating accepted Results. |
| 21 | Met | Only bounded domain failures become model-visible Results; MCP `isError` does not control lifecycle state. |
| 22 | Gated | Exact child references and deterministic generated names validate in the catalog, but no callable delegation tool is materialized. |
| 23 | Gated | Relationship and authority validation exists in catalog/kernel boundaries; no arbitrary target can enter a shipped tool call because the adapter is disabled. |
| 24 | Met | The durable Kernel and Definition validation retain depth one, one total child, no child delegation, fan-out, join, or recursion. |
| 25 | Gated | Legacy Kernel transactions remain atomic; the configured Task-creation transaction awaits the official Task result surface. |
| 26 | Gated | Durable Delegation identity is ready to serve as `task_id`; no Task table/list/polling worker was introduced. |
| 27 | Met for kernel / gated for MCP | Child terminalization and parent wake-up remain durable and notification-independent; Task projection is not shipped. |
| 28 | Met for storage / gated for MCP | Immutable typed Agent Results, integrity metadata, Delegation references, and Tree charging are implemented; Task projection awaits the gate. |
| 29 | Met for storage / gated for MCP | Configured admission stores a bounded canonical context manifest without caller authority; child Task handoff is not shipped. |
| 30 | Gated | Official SDK and protocol version are pinned, but the SDK cannot emit the required public Task result. No deprecated/private format is used. |
| 31 | Met | The gate fails closed exactly as specified; the rest of the framework landed without claiming Tasks conformance. |
| 32 | Met | `chat_runs` owns Member/Message lifecycle; committed configured Agent Runs are product-neutral. |
| 33 | Met | `agent_trees` own shared deadlines/budgets; new Delegation rows permit Role-free relationships and stable `action_id`. |
| 34 | Met | Attempts reconstruct Provider-neutral context from product ownership, exact pins, and normalized Checkpoints. |
| 35 | Met | Context/result budgets charge atomically and fail closed; required evidence/results are not silently discarded. |
| 36 | Met | Configured cancellation, retry, grounded publication, citations, SSE, Chat projection, and legacy Research regression tests preserve product behavior. |
| 37 | Partial / gated | Research Web Search is one bounded MCP Action and completion is deterministic from accepted results; configured Research admission is blocked with delegation. |
| 38 | Met | Sprint 9 Profiles, Roles, Role Checkpoints, and planner/search recovery remain readable only through the legacy path. |
| 39 | Partial / drain pending | Expand and activate are complete. `agent_legacy_runtime_drain_status` now provides database-owned, fail-closed contract readiness evidence, but legacy readers/columns remain until the deployed state reports no active legacy Run or Job. |
| 40 | Partial / gated | Catalog, registry, MCP, metadata, error, recovery, generic runtime, release, retry, grounding, and legacy tests are deterministic and credential-free; Tasks conformance cannot run until the SDK gate opens. |

## Delivery Commits

- `94057a3` — accepted design, PRD, context, and ADRs.
- `3887e13` — implementation plan and acceptance routing.
- `3d94a63` — immutable Agent catalogs and exact release registration.
- `7a0ffe8` — configured Executor Registry and pinned dispatch.
- `26c2320` — official-SDK synchronous MCP tool plane.
- `afd981d` — Research Web Search through MCP.
- `d88860e` — product-neutral durable runtime, Agent Trees, and Results.
- `ba64d5e` — configured release activation and legacy drain path.
- `b55d77c` — configured grounded Chat ownership and citations.
- `dacb375` — configured retry through the active release.
- `25606d5` — exact Model Policy invocation settings.
- `7495b3f` — configured Delegation ownership through the product-neutral RLS boundary.
- `a2241e7` — database-owned legacy Agent drain readiness.

This document records the honest SDK-gated acceptance boundary; it does not claim configured delegation or legacy contract cleanup is complete.
