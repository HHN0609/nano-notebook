# Sprint 9 Acceptance Evidence

## Result

- **Status:** Accepted
- **Verified:** 2026-07-26
- **Authority:** `docs/sprint/SPRINT-9-PRD.md`
- **Automated gate:** `./scripts/test-go`
- **Gate result:** all Go tests, PostgreSQL and observability integration tests, `go vet`, and production command builds passed.

## Success-Criterion Evidence

| # | Evidence |
| --- | --- |
| 1 | Six production instructions are Git-owned Markdown under `internal/promptcatalog/prompts/`. |
| 2 | `internal/promptcatalog/catalog.go`, `internal/app/prompt_registry.go`, and immutable schema registration validate identity, numeric version, contract, and canonical SHA-256. |
| 3 | Prompt registry unit/integration tests prove exact idempotency, conflict rejection, and the immutable database trigger. |
| 4 | Catalog resolution requires exact identity/version; tests reject missing versions and definition conflicts. |
| 5 | `AgentPromptSet` binds five Agent prompts; Source Processing resolves the exact image-normalizer version. |
| 6 | Model Trace tests assert Prompt identity/version/hash/contract, requested/selected model, provider metadata, and usage without content. |
| 7 | admission persists the immutable Configuration Set, Leader Profile model, executor version, and budgets on the Run. |
| 8 | Delegation Kernel materializes the Research Profile from the same Configuration Set and copies the root absolute deadline. |
| 9 | retry and continuation retain the same Run/configuration; new admission uses the deployment-selected Set. |
| 10 | readiness integration rejects unsupported active Sets without changing their Run state. |
| 11 | Router tests require the exact `select_leader_route` function call and bounded enums. |
| 12 | Planner tests require the exact `submit_research_queries` function call with one to three unique bounded queries. |
| 13 | malformed, text, mixed, wrong-function, extra-field, and inconsistent contracts fail closed in Agent tests. |
| 14 | Router reference context is bounded to three completed pairs; only current-turn content drives the explicit discovery reason. |
| 15 | deterministic policy tests keep ordinary/current-information/evidence-insufficiency paths on `continue_chat`. |
| 16 | `agent_run_routes` stores requested/effective route and bounded intent/policy reasons separately. |
| 17 | Delegation Policy tests cover membership, Notebook authority, active root, deadline, Provider, relationship, and child limit. |
| 18 | Role Registry tests accept only Leader and Research, reject duplicates/unknown Roles, and enforce executor compatibility. |
| 19 | Registry and Kernel enforce only Leader-to-Research, ordinal zero, depth one, one total child, and no child delegation. |
| 20 | only Leader is member-visible/publish-capable; Research integration proves no child Assistant Message is published. |
| 21 | Worker owns claim, Lease, heartbeat, disposition, and retry; jobs/worker contain no Research outcome interpretation. |
| 22 | distinct typed Leader/Research Role Executors dispatch through the Registry; Job and delegation transitions use runtime/kernel APIs. |
| 23 | Delegation Kernel owns child creation, waiting, cancellation, terminal handoff, wake-up, and consumption. |
| 24 | migration retains `agent_run_delegations` as sole relationship lifecycle authority and `agent_research_outcomes` as Role-owned output. |
| 25 | lifecycle tests prove `consumed_at` does not erase the stable terminal state. |
| 26 | migration backfills parent, child, state, error, and Discovery Session identity before removing legacy lifecycle structures. |
| 27 | legacy expanded queries backfill to the generic Research query-plan Checkpoint. |
| 28 | immutable Role Checkpoints key each Provider-neutral result by Research step and ordinal. |
| 29 | PostgreSQL retry integration proves accepted plan/results are loaded and only the first missing search repeats. |
| 30 | planner contract and execution loop enforce one-to-three sequential searches; merge retains ten normalized candidates. |
| 31 | Checkpoint commits after Provider response; documentation and recovery tests make only an at-least-once uncertain-query claim. |
| 32 | Role Executor and Worker tests cover completed, waiting, retryable, terminal, and abandoned with lease-lost/cancelled causes. |
| 33 | retry integration proves Lease clearing, safe last error, `available_at`, and retained Run/deadline/config/checkpoints. |
| 34 | classifier tests cover Model/Web timeout, rate limit, unavailable, attempt caps, and root deadline caps. |
| 35 | terminal classification uses bounded safe codes for invalid contracts, configuration/Role/durable state, policy, and authority. |
| 36 | active failures commit dispositions; lease integration retains expiry/recovery only for uncommitted host failure. |
| 37 | Kernel success/failure transactions terminalize child and delegation, wake parent, and notify the Agent Job channel. |
| 38 | Worker notification listener always retains periodic scan fallback; committed queued parents are claimable without notification. |
| 39 | failed child outcome is consumed by Leader as terminal failure; it never degrades to ordinary Chat. |
| 40 | parent/child deadline integration and Kernel SQL prove the child inherits the same absolute deadline. |
| 41 | cancellation integration atomically cancels child/delegation/Discovery work; Lease/deadline/authorization fences reject late effects. |
| 42 | deadline integration terminalizes active depth-one Runs and does not requeue an expired parent. |
| 43 | Checkpoint, terminal outcome, and publication SQL fence Lease token, active state, root deadline, and current membership authority. |
| 44 | Durable Trace records Prompt, Role/config, route/policy, parent-child links, Checkpoints, dispositions, backoff, wake, and consumption. |
| 45 | Trace tests reject raw Prompt/response content; classifiers/logging retain safe codes rather than Provider envelopes or credentials. |
| 46 | re-entrant migration tests cover expand/backfill/retire compatibility and recover legacy active Runs. |
| 47 | readiness integration accepts current plus only the immediately previous Prompt/executor-compatible Configuration Set. |
| 48 | deterministic unit/integration tests inject contract, Provider, checkpoint, retry, Lease, cancellation, deadline, and authority failures. |
| 49 | default tests use local stubs and PostgreSQL/MinIO containers; no live Model or Brave credential is required. |
| 50 | the complete `scripts/test-go` prior-Sprint regression gate passed. |
| 51 | API/SSE/UI schemas are unchanged; Role Registry still contains only Leader and Research and no same-turn Web answer or Studio Output. |

## Delivery Commits

- `75729e9` — Sprint 9 PRD, design, ADR, and architecture boundary.
- `5b50faf` — immutable Prompt Catalog and typed Router/Planner contracts.
- `46df1ea` — pinned configuration, Role Profiles, Registry, and dispatch.
- `8cdc9c4` — generic bounded Delegation Kernel and lifecycle migration.
- `a5432ce` — deterministic Research Checkpoints and recovery.
- `039c5f9` — typed Attempt disposition, active retry, terminalization, compatibility, and Trace hardening.
