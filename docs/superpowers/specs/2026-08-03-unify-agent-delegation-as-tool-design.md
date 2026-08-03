# Unify agent delegation into a single genuine tool-call protocol

## Context

Nano's leader agent can invoke three structurally different kinds of capability today, even though from a product standpoint they're all "the leader does something":

1. **Plain tools** (`search_evidence`, `web_search`, `current_time`) — registered MCP tools. `Execute()` runs synchronously. The model genuinely discovers them in `tools/list` and chooses to call them.
2. **`legacy_role` research delegation** — bypasses the MCP tool plane entirely. `LeaderExecutor.Execute()` runs a dedicated structured-output classifier (`e.router.DecideRoute()`) plus `EvaluateDelegationPolicy` (`internal/agent/delegation_policy.go`) to decide whether to delegate, then calls `DelegationKernel.CreateInTx` directly (`internal/agent/leader_executor.go:371`). The model never sees this as a tool call.
3. **`configured` child delegation** — *looks* like a tool (`configuredDelegationAction`, registered as `delegate.<identity>.v<version>`) but `Available(Execution) bool` is hardcoded to `false` (`internal/agent/configured_delegation_action.go:89`), so it never appears in `tools/list`. `LeaderExecutor.delegateConfigured()` (`leader_executor.go:408`) manufactures the tool call itself after the same router/policy decision as lane 2 — the "tool" shape exists only to reuse checkpoint/trace plumbing.

Additionally, whether a chat's leader run is even represented as a `configured` (catalog-driven) or `legacy_role` (hardcoded-enum) run depends on a runtime branch in `internal/app/server.go` (`if s.chatAgent == nil { ... } else { ... }`), which itself depends on whether `cfg.AgentRelease` is configured.

**Investigation finding, corrected mid-brainstorming**: production already defaults `AgentRelease` to `nano.default@2` (see `cmd/control-plane/main.go`, `cmd/worker/main.go`, and their tests), so ordinary chat already runs on the `configured` path today. The `legacy_role` fallback in `server.go` is dormant in production but still reachable by configs that don't set `AgentRelease` (some test setups). Separately, the catalog Definitions for both roles already exist and are already cross-referenced: `internal/agentcatalog/definitions/chat.leader.v1.json` declares `"children": ["research.source-discovery@1"]`, and `research.source-discovery.v1.json` already carries `"delegation"` metadata — everything `NewConfiguredDelegationToolRegistrations` needs to generate the `delegate.research.source-discovery.v1` MCP tool. The only thing not wired up is the *creation call site* for a research child, which still bypasses the catalog machinery.

## Goal

Every capability the leader model can invoke — plain tool or delegation to a child agent — goes through one mechanism: a real MCP tool the model discovers in `tools/list` and chooses to call, gated only by a real `Available()` policy check, the same way `search_evidence` already works. No dedicated router/classifier deciding delegation on the model's behalf. No `runtime_kind`/`agent_role` enum branching in the *creation* path for new runs. This design was validated during brainstorming against `github.com/earendil-works/pi` (a public agent harness where sub-agents are registered as ordinary, freely-callable tools with no special protocol).

## Non-goals

- Not touching `DelegationKernel`'s internals (`CreateInTx`/`TerminalizeInTx`/`ConsumeInTx`/etc.) — the durable child-run state machine is correct and out of scope.
- Not backfilling historical `legacy_role` rows already in the database. Old completed/terminal runs stay as they are; read paths that need to tolerate old data may still reference `legacy_role` for that reason (see Sub-goal C).
- Not implementing concurrent/parallel child fan-out (multiple children per parent) — a separate, previously-discussed topic, explicitly out of scope here. This design keeps the existing single-active-child constraint.
- Not changing `configuredDelegationAction.Execute()`'s internal logic (validate input → check relationship/authorization → dedupe by `action_id` → create child `agent_runs`/`agent_jobs` rows → suspend parent → notify). That logic is correct and stays as-is; only how it gets *reached* changes.

## Design

Three sub-goals, done as sequential steps in one continuous piece of work (not separate PRs), in this order: **A → C → B**. A and C both retire fallback/duplicate paths and are low-risk, behavior-preserving refactors verified against existing tests. B is the actual model-facing behavior change, and should land last so there's exactly one `Available()` implementation and one creation path to get right.

### Sub-goal A: route research-child creation through the catalog/MCP shell

`internal/agent/leader_executor.go:371` currently calls `DelegationKernel.CreateInTx` directly, building the child from `agent_role_profiles` (keyed by the `agent_role` enum). Since `research.source-discovery.v1` already exists as a catalog Definition and is already declared as `chat.leader.v1`'s child, this call site should instead go through the same path `configuredDelegationAction.schedule()` uses (catalog `agent_definition_versions`/`agent_model_policy_versions` lookups, not `agent_role_profiles`).

No new catalog Definition needs authoring. This step does not change `EvaluateDelegationPolicy`/`DecideRoute()` — the decision to delegate is still made the same way as today; only *how the child gets created once the decision is made* changes. Verify with existing tests (`delegation_kernel_test.go`, `delegation_policy_test.go`, `leader_test.go`) — behavior should be identical before and after.

### Sub-goal C: retire the `legacy_role` chat-run creation fallback

`internal/app/server.go` has two `if s.chatAgent == nil { legacy } else { configured }` branches: the send-message path (~1379-1398) and the retry path (~1113-1125). Since production already configures `AgentRelease` (defaulting to `nano.default@2`) and therefore already takes the `configured` branch, delete the `nil` branch and `CreateQueued`'s role as a chat-run creator — `CreateConfiguredChatQueued` becomes the only way a chat's leader run is created. `AgentRelease` becomes an unconditional startup requirement instead of an optional feature flag (`cmd/control-plane/main.go` and `cmd/worker/main.go` already fail closed via `VerifyAgentCatalogReady` if the release is misconfigured, so this mostly formalizes an already-true production invariant).

Caveat, not a contradiction of the goal: `internal/agent/store.go`'s read paths (e.g. the `(runtime_kind='legacy_role' and agent_role=X) or (runtime_kind='configured' and executor_identity=X)` pattern, ~10+ occurrences) most likely still need to tolerate reading *historical* rows created before this change, or from `internal/rageval`'s eval-harness callers of `CreateQueued` (`retrieval_live_executor.go:233`, `live_product_executor.go:236` — these are test/eval infrastructure, not production traffic; leave them on `CreateQueued` unless a follow-up decides otherwise, out of scope here). So `legacy_role` as a string value and as a thing read-queries handle does not disappear from the codebase entirely — only from the *live creation path* for real chat traffic.

### Sub-goal B: make delegation a real, model-chosen tool

1. **`ActionAvailability` interface** changes from `Available(Execution) bool` to `Available(Execution) (ok bool, reasonCode string)`. Exactly two implementers exist today (confirmed by grep): `configuredDelegationAction` (configured_delegation_action.go:89) and `searchEvidenceAction` (search_evidence_action.go:36, real logic: `execution.SelectedSourceCount > 0` — update its return shape but no logic change, `reasonCode` can be a static string like `"no_sources_selected"`).

2. **`configuredDelegationAction.Available()`** goes from hardcoded `false` to a real policy check, sourcing `EvaluateDelegationPolicy`'s checks as follows:
   - `MemberRole`, `ExistingChildCount` → two new fields on `Execution` (`internal/agent/runtime_types.go:10-28`), populated by two additions to `PostgresRuntime.Load()`'s existing query (`internal/agent/postgres_runtime.go:136-206`) — `Load()` already joins `notebook_memberships m` but doesn't select `m.role`; adding a `count(*)` subquery against `agent_run_delegations` mirrors the pattern already used in the (now-being-retired) `loadRun()`.
   - `RootActive`, `DeadlineValid` → no new representation needed. `Load()` already fails closed on both (`where r.status='running'`, and the explicit `deadlineValid` check) before `Execution` is ever returned, so by the time any `Available()` runs these are already guaranteed true.
   - `ProviderAvailable` (today's `e.provider.(interface{ ResearchAvailable() bool })` type-assertion) → a constructor-injected dependency on `configuredDelegationAction` (it already holds `pool`/`catalog` as fields; add a provider-health checker the same way).
   - `RelationshipRegistered` (catalog parent/child check) → already available via `configuredDelegationAction`'s own `catalog`/`child` fields at construction; no `Execution` involvement needed.

3. **Silent-filter → traced filter.** `internal/agent/mcp_tool_plane.go:358` (`ActionDefinitions()`) and its non-MCP twin `internal/agent/actions.go:124` currently `continue` silently when `Available()` returns false. Change both to record a new trace/span event carrying the tool name and `reasonCode` whenever `reasonCode` is non-empty, before continuing. This is a generic capability — any tool with conditional availability benefits, not just delegation — and it's what preserves the audit value `EvaluateDelegationPolicy`'s typed reject reasons currently provide via `agent_run_routes`.

4. **Delete the router entirely.** `e.router.DecideRoute()`, `EvaluateDelegationPolicy` (`internal/agent/delegation_policy.go`), `LeaderExecutor.delegateConfigured()`'s synthetic-call construction (`leader_executor.go:408-446`), the `agent_run_routes` table, and `RecordLeaderRouteInTx` (`internal/agent/delegation_trace.go:13`) all get deleted. Investigation confirmed `agent_run_routes` has exactly one reader (`leader_executor.go:243`, inside `loadRun()`), used only to avoid re-deciding "delegate vs. continue chat" when an attempt is retried after a crash. Once delegation is a normal tool call inside the standard `Controller` loop, this idempotency need is already subsumed by the existing checkpoint/`firstIncompleteAction` resumption mechanism — nothing else needs to replace it.

5. **Net effect**: the leader model always runs through the normal `Controller`/`Execution` loop (what `e.normal.Execute()` already does for "continue chat" today). `delegate.research.source-discovery.v1` appears in its `tools/list` whenever `Available()` says yes, and the model decides — via ordinary tool-choice reasoning, no dedicated classifier — whether to call it, exactly like `search_evidence`.

## Accepted risks (explicitly chosen by the user during brainstorming)

- **Routing precision**: a general tool-calling model choosing among tools is likely less precise than today's dedicated `DecideRoute()` classifier at deciding "should I delegate." No compensating pre-filter, soft prompt hint, or cost-gated partial classifier — pure trust in the model's tool-choice judgment, matching Pi's design. If this proves too imprecise in practice, revisit with real usage data rather than pre-guessing a fix.
- **`agent_run_routes` audit trail is deleted, not migrated.** The new tool-level "filtered with reason" trace event (item 3 above) is the replacement signal, but it's a different granularity (per tool-list-build, not per run) and does not reconstruct old `agent_run_routes` history.

## Testing plan

- **Sub-goal A**: no new tests required beyond existing coverage; the point is that `delegation_kernel_test.go`, `delegation_policy_test.go`, and `leader_test.go` continue to pass unmodified, proving behavior didn't change.
- **Sub-goal C**: update/remove tests that construct `app.Config{}` without `AgentRelease` and expect the legacy branch; add a startup-config test asserting the server refuses to start (or panics, matching existing `panic()` conventions in that code) without a valid `AgentRelease`.
- **Sub-goal B**:
  - Unit tests for `configuredDelegationAction.Available()`'s new policy logic (one case per `EvaluateDelegationPolicy` reason it now needs to reproduce: membership denied, existing-child-limit, provider unavailable, relationship unregistered).
  - Unit tests for the new `(ok bool, reasonCode string)` signature on `searchEvidenceAction.Available()`.
  - `mcp_tool_plane_test.go`/`actions_test.go`: assert the new "filtered with reason" trace event fires with the right reason and does not fire when a tool is simply absent for unrelated reasons.
  - `leader_test.go`: the bulk of existing router/policy-decision test cases get deleted (the code they test is deleted); replace with `controller_test.go`-style cases where the model chooses to call `delegate.research.source-discovery.v1` from the tool list, verified end-to-end through `Controller.Execute()`.
  - `postgres_runtime.go` tests: verify the two new `Execution` fields (`MemberRole`, `ExistingChildCount`) populate correctly from `Load()`.

## Rollout

All three sub-goals land as sequential commits on one branch (not separate PRs, per explicit preference) in order A → C → B, each verified before starting the next. No feature flag — this is a straight cutover once B lands, matching how `AgentRelease` is already treated as an immutable, fail-closed startup config rather than a runtime toggle.
