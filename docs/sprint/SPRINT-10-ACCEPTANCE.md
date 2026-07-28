# Sprint 10 Acceptance Evidence

## Result

- **Status:** Accepted.
- **Verified:** 2026-07-28
- **Authority:** `docs/sprint/SPRINT-10-PRD.md`
- **Automated gate:** `./scripts/test-go`
- **Gate result:** exit 0; formatting, every Go package including PostgreSQL/MinIO integrations (`internal/app` in 140.493s), `go vet`, and all production binary builds passed.
- **Additional race gates:** `go test -race -count=1 ./internal/agent ./internal/agentcatalog ./internal/models` and `go test -race -count=1 ./internal/app -run TestConfiguredLeaderSchedulesPinnedChildThroughMCPAndSuspends` with `NANO_TEST_DATABASE_URL` set to the repository test database.
- **Race result:** both commands exited 0; the configured parent/child MCP integration completed in 2.595s under the race detector.
- **Scope audit:** `git diff --name-only origin/main...HEAD` contains no Web, Studio, or UI surface. The embedded catalog contains only `chat.leader@1` and `research.source-discovery@1`; no audio, quiz, test, report, mind-map, flashcard, table, or other product Agent was added.
- **Protocol boundary:** configured children are generated tools invoked with the official `github.com/modelcontextprotocol/go-sdk v1.7.0-pre.3` over in-memory standard `tools/call`. The immediate scheduling receipt is Controller-internal. PostgreSQL Delegations, Runs, Jobs, Results, and Checkpoints own asynchronous lifecycle and parent wake-up; Nano neither claims MCP Task semantics nor adds a private Task wire format.
- **Migration boundary:** new admission is configured and product-neutral. Legacy Role state remains a read-only compatibility path until the database-owned drain view reports it is safe to contract; terminal history is not rewritten merely to rename concepts.
- **Done:** yes. All 40 Sprint 10 success criteria have direct implementation or verification evidence.

## Success-Criterion Evidence

| # | Status | Evidence |
| --- | --- | --- |
| 1 | Met | `internal/agentcatalog` embeds strict JSON Definitions, Policies, Contracts, and Releases, canonically hashes them, and the database registry stores immutable exact versions. |
| 2 | Met | `TestLoadFSRejectsUnknownFieldsTrailingJSONAndIdentityConflicts`, `TestCatalogRejectsUnresolvedAndCyclicConfiguration`, `TestValidateBindingsAllowsOnlyCapabilityNarrowing`, and readiness integration tests reject invalid catalogs and bindings. |
| 3 | Met | The typed catalog decoder accepts only the fixed declarative schema; validation contains no template, include, environment interpolation, entrypoint, URL, SQL, or control-flow surface. |
| 4 | Met | Definition validation requires one Executor, exact Policy/Prompt/Contract references, bounded tools/children, and local limits within the Executor ceiling. |
| 5 | Met | Model Policies pin Provider model, temperature, output tokens, and timeout; `TestModelAdapterRecordsPinnedPromptContractAndRequestedModel`, policy-hash tests, and Bifrost tests prove propagation. |
| 6 | Met | `ExecutorRegistry` dispatches configured Runs and owns capability ceilings; `RoleRegistry` remains reachable only for legacy compatibility. |
| 7 | Met | `TestConfiguredAgentRunRequiresNoRoleExecutorVersionOrChatOwnership` and `TestExecutionHostDispatchesConfiguredPinWithoutRoleOrExecutorVersion` prove the new path has no Role or executor-version dependency. |
| 8 | Met | `NANO_AGENT_RELEASE` selects an exact manifest; `TestAgentCatalogRegistersIdempotentlyAndSelectsExactRelease`, activation, retry, and transitive-pin tests prove immutable admission. |
| 9 | Met | `TestEmbeddedCatalogContainsOnlyMigratedProductionAgents` and a source audit show exactly `chat.leader@1` and `research.source-discovery@1`. |
| 10 | Met | MCP registry/Controller tests cover `calculate`, `current_time`, `search_evidence`, `web_search`, and generated `delegate.research.source-discovery.v1`; configured integration invokes the child through that MCP plane. |
| 11 | Met | `select_leader_route` remains a typed Models decision consumed by `ModelLeaderRouter`; it is not registered as an MCP Action. |
| 12 | Met | `TestMCPToolPlaneUsesOfficialInMemoryProtocolAndDefinitionScope` proves discovery is the intersection of the pinned Definition and registered ceiling. |
| 13 | Met | Embedded tool schemas contain bounded business input only; MCP authority arrives in process-local metadata and configured delegation builds child authority server-side. |
| 14 | Met | `MCPToolHost.OpenAttempt` binds an opaque Attempt handle and `CallTool` supplies stable `action_id`; MCP tests inspect the server-bound attempt and stable logical identity. |
| 15 | Met | MCP authority and configured delegation revalidate Lease fencing, Definition/hash, allowlist, product ownership, deadline, and tree budget before mutation. Lost-Lease and configured RLS integration tests fail closed. |
| 16 | Met | Attempt handles live only in the Host registry, are removed by `Close`, and are never stored; missing-handle/lost-Lease tests reject reuse. |
| 17 | Met | Ordinary tools retain at-least-once behavior. Delegation resolves by the database-unique stable `action_id`, and creation plus parent suspension is one transaction. |
| 18 | Met | `agent_run_checkpoints` Proposal/Result rows remain the logical ledger; migrations and source audit add no generic Tool Invocation table. |
| 19 | Met | Tool bindings declare `ordered_sync` or `exclusive_delegation`; `TestMCPToolPlaneMaterializesConfiguredChildAsExclusiveDelegation` rejects mixed proposals. |
| 20 | Met | `TestAttemptDispositionClassifiesRetryableTerminalAndAbandonedFailures` and MCP error tests prove infrastructure retry, Lease abandonment, and safe invariant termination without fabricated Results. |
| 21 | Met | Action adapters preserve bounded domain Results while `ToolCallError` classification, rather than MCP `isError`, controls lifecycle disposition. |
| 22 | Met | Parent Definitions hold exact child references; `TestGeneratedDelegationToolNameIsDeterministicAndBounded` and MCP materialization prove deterministic `delegate.<identity>.v<version>` names. |
| 23 | Met | Generated input accepts no target identity. The registered Definition relationship and current product ownership are revalidated inside `configuredDelegationAction.Invoke`. |
| 24 | Met | Catalog and Delegation Kernel validation enforce depth one, one total child, no child children, fan-out, join, recursion, or free-form Agent conversation. |
| 25 | Met | `configuredDelegationAction.Invoke` performs relationship resolution, child Run/Job creation, context charging, Delegation insertion, Proposal checkpoint, and parent waiting transition in one PostgreSQL transaction. |
| 26 | Met | `delegation_id` is the durable handle; migrations and source audit contain no MCP Task table, task-list endpoint, or Task polling worker. |
| 27 | Met | Child terminalization stores the outcome and atomically requeues the parent; normal Job claiming recovers independently of notification. The configured end-to-end integration proves the wake path. |
| 28 | Met | `TestAgentResultIsCanonicalTypedImmutableAndUniquePerProducer` and configured delegation integration prove one immutable typed Result and reference-only parent Checkpoint projection. |
| 29 | Met | The configured MCP integration verifies a bounded server-built context manifest, tree context-byte charging, and absence of caller-supplied authority or full parent context. |
| 30 | Met | `go list -m` resolves official Go SDK `v1.7.0-pre.3`; in-memory MCP discovery/call tests and the configured end-to-end test exercise only standard `tools/call`. |
| 31 | Met | The configured integration proves one accepted Proposal, no immediate Action Result or model-visible scheduling tool, parent Job `waiting` without a Lease, and Result checkpointing only after child completion. |
| 32 | Met | `chat_runs` owns Member/Message lifecycle; configured `agent_runs` are product-neutral, as proved by generic migration and admission integration tests. |
| 33 | Met | `agent_trees` own shared deadline/budgets; configured Delegations relate parent and child without Role columns. Deadline, budget, and configured delegation tests cover the invariant. |
| 34 | Met | Fresh configured Attempts load exact Definition/Policy pins, product-authorized input, and generic Checkpoints; the configured child writes no Role checkpoint. |
| 35 | Met | Context and result budgets charge atomically and fail closed; budget/projection tests prove required results and evidence are not silently discarded. |
| 36 | Met | Full Chat, Research, grounding, cancellation, retry, publication, citations, SSE, API, and legacy integration suites pass without changing Member-facing behavior. |
| 37 | Met | `TestModelResearchPlannerProposesConfiguredWebSearchAction` and the configured end-to-end integration prove one bounded MCP `web_search` Proposal and deterministic terminalization from its accepted Result. |
| 38 | Met | Legacy Profile/Role/Role-checkpoint paths remain readable for pre-existing Runs; legacy Research recovery and full regression suites continue to pass. |
| 39 | Met | The migration is additive, configured release activation handles new admission, and `agent_legacy_runtime_drain_status` gates later contraction on durable active-reference evidence. Retained history is not rewritten. |
| 40 | Met | Catalog, MCP, authority, scheduling, idempotency, recovery, drain, generic runtime, and unchanged Chat/Research journeys are deterministic and credential-free; the full gate and targeted race gates pass. |

## Delivery Commits

- `94057a3` — Specify configured agent framework.
- `3887e13` — Plan Sprint 10 agent framework implementation.
- `3d94a63` — Add immutable agent catalogs.
- `7a0ffe8` — Add configured executor registry.
- `26c2320` — Route synchronous agent tools through MCP.
- `afd981d` — Run research web search through MCP.
- `d88860e` — Add generic durable agent runtime.
- `ba64d5e` — Activate configured agent runtime.
- `b55d77c` — Preserve configured grounded chat ownership.
- `dacb375` — Retry configured chat runs through active release.
- `25606d5` — Apply pinned model invocation policies.
- `7495b3f` — Resolve configured delegation product ownership.
- `5fa8a42` — Record the initial gated acceptance evidence.
- `a2241e7` — Expose legacy agent drain readiness.
- `e1a4789` — Update drain acceptance evidence.
- `1b15646` — Document the superseded MCP Tasks SDK blocker.
- `92d6759` — Revise the configured delegation runtime boundary.
- `7d4c89d` — Update the delegation implementation plan.
- `0144c36` — Enable configured child delegation through MCP.

The later architecture commits supersede the earlier MCP Tasks gate without changing the durable PostgreSQL authority: standard MCP accepts the child work, while Nano owns suspension, execution, recovery, and wake-up.
