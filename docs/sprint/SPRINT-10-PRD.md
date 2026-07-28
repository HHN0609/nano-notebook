# Nano Notebook Sprint 10 PRD

## Document Status

- **Sprint:** Sprint 10
- **Status:** Implemented and accepted; configured delegation uses Nano-owned durable suspension through the MCP Tool Plane
- **Date:** 2026-07-27
- **Theme:** Configured Agent Framework and MCP Tool Plane
- **Delivery boundary:** Agent infrastructure only; no new Member-facing feature or new product Agent

## 1. Decision

Sprint 10 replaces Sprint 9's Role-shaped, hard-coded Agent configuration with a small configuration-driven framework:

1. every production Agent is represented by an immutable Git-owned Agent Definition;
2. one Executor Registry binds stable executor identities to Go implementations and code-owned capability ceilings;
3. every executable model tool is discovered and called through one in-process Nano Tools MCP Server;
4. configured child Agents appear to a parent as generated MCP delegation tools backed by the existing durable Delegation Kernel;
5. Agent Runs become product-neutral durable invocations, while Chat owns its Member-visible answer lifecycle;
6. existing Leader and Research behavior migrates to the framework without becoming a new Sprint 10 product capability.

The framework remains deliberately bounded. Sprint 10 does not create Studio, report, mind-map, flashcard, table, audio, quiz, or other feature Agents, and it does not introduce a general DAG or plugin platform.

## 2. Source Documents

- `docs/superpowers/specs/2026-07-27-configured-agent-framework-design.md`
- `docs/technical-architecture/CONTEXT.md`
- `docs/technical-architecture/adr/0041-build-a-bounded-agent-delegation-kernel.md`
- `docs/technical-architecture/adr/0042-invoke-configured-child-agents-through-mcp.md`
- `docs/technical-architecture/adr/0043-separate-chat-runs-from-agent-runs.md`
- `docs/technical-architecture/adr/0044-use-mcp-as-the-agent-tool-plane.md`
- `docs/technical-architecture/adr/0045-migrate-all-agents-to-an-embedded-definition-catalog.md`
- `docs/sprint/SPRINT-9-PRD.md`

ADRs 0042–0045 supersede ADR 0041 only for newly admitted Agent Runs' configuration, dispatch, tool transport, and product ownership. ADR 0041 remains authoritative for the bounded depth-one lifecycle and for legacy Sprint 9 recovery during migration.

## 3. Problem

Sprint 9 successfully extracted a bounded Delegation Kernel, but the runtime is still shaped around exactly two Roles:

- `AgentConfigurationSet` requires Leader and Research profiles;
- `RoleRegistry` duplicates executor identity and capability policy;
- `agent_role`, `parent_role`, and `child_role` are embedded in runtime storage;
- Research invokes Web Search outside the common Action path;
- ordinary Actions and delegated children use different invocation abstractions;
- `agent_runs` owns Chat-specific Message and Member fields, preventing reuse by a non-Chat product;
- adding a safe Agent requires coordinated identity branches across constructors, dispatch, policy, and persistence.

The next product capability should be expressible by adding reviewed configuration to a stable framework when its execution shape is already supported. It should not require copying orchestration code or weakening server-owned authorization.

## 4. Sprint Goal

Deliver these dependent slices in order:

1. **Definition governance:** strict embedded Agent Definition and Model Policy catalogs with immutable registration and exact references.
2. **Executor dispatch:** replace new-path Role dispatch with a single Executor Registry whose code-owned ceilings configuration can only narrow.
3. **MCP tool plane:** move existing executable Actions and Web Search behind one scoped in-process MCP Host/Server boundary.
4. **Configured delegation:** materialize exact child Definition references as generated MCP tools backed by the Delegation Kernel and Nano's durable Run lifecycle.
5. **Generic runtime:** separate Chat Run ownership from product-neutral Agent Runs, Agent Trees, Delegations, Results, Jobs, Checkpoints, and Trace.
6. **Migration:** admit new Leader and Research Runs through Definitions while legacy non-terminal Runs drain through a read-only compatibility path.
7. **Proof:** demonstrate deterministic configuration validation, recovery, fencing, idempotency, and unchanged Chat/Research behavior.

## 5. Success Criteria

Sprint 10 is complete only when all of the following are true:

1. Production Agent Definitions are strict JSON files embedded in the binary and registered immutably by `identity + version` and canonical SHA-256.
2. Unknown fields, duplicate identities, invalid generated tool names, unresolved references, and capability-expanding bindings fail Worker readiness.
3. Definitions support no templates, inheritance, includes, environment interpolation, arbitrary entrypoints, SQL, URLs, branches, loops, or mutable aliases.
4. A Definition binds exactly one registered Executor, one exact Model Policy, exact Prompt and Contract versions, allowed tools, allowed child Definitions, and local limits.
5. Exact Provider model routes and non-secret invocation parameters are versioned Model Policies; environment configuration supplies secrets and endpoints only.
6. One Executor Registry replaces Role Registry for new Runs. Executor code owns control flow, publication rights, tool classes, legal child executor classes, and maximum capability.
7. New Agent Definitions and Agent Runs contain no Role or executor-version field. A breaking Executor implementation change requires drain before replacement in Sprint 10.
8. The active deployment manifest selects exact Definition versions for new admission; an admitted Run never resolves `latest` or changes behavior after deployment configuration changes.
9. The catalog initially contains only migrated `chat.leader@1` and `research.source-discovery@1`; Sprint 10 adds no new product Agent.
10. Every executable model tool uses the Nano Tools MCP Server, including `calculate`, `current_time`, `search_evidence`, `web_search`, and generated `delegate.*` tools.
11. `select_leader_route` remains a Decision Contract in the Models Module because it describes structured model output rather than an executable capability.
12. Scoped MCP discovery exposes only the intersection of the pinned Definition allowlist and the selected Executor's code-owned ceiling.
13. Model-visible tool arguments contain bounded business inputs only and never accept Run, Member, Notebook, Definition, Lease, permission, budget, or credential authority.
14. The Host injects a process-local opaque Attempt Context Handle and stable logical `action_id` in non-model-visible MCP request metadata.
15. The MCP Server revalidates current Lease fencing, pinned Definition, scoped allowlist, product authorization, deadline, and budget before discovery and invocation.
16. Attempt Context Handles expire with Attempt authority and are never persisted or reused after recovery; a new Attempt receives a new handle.
17. Tool execution remains at-least-once. Read-only and pure tools tolerate repetition; state-changing delegation is idempotent by stable `action_id` in PostgreSQL.
18. Existing accepted Action Proposal and Action Result Checkpoints remain the durable logical invocation record; Sprint 10 adds no generic Tool Invocation ledger.
19. `calculate`, `current_time`, `search_evidence`, and `web_search` are sequential `ordered_sync` tools. A generated delegation tool is `exclusive_delegation` and must be proposed alone because an accepted call suspends the parent.
20. Transient infrastructure failure creates no model-visible Action Result and returns a retryable Attempt disposition; Lease loss abandons the Attempt; invariant or authorization failure terminates safely.
21. Only bounded contract-declared domain failures become model-visible Action Results. MCP `isError` is not allowed to choose Agent or product lifecycle state.
22. A parent Definition lists exact child Definition references; the runtime generates each callable name deterministically as `delegate.<identity>.v<version>`.
23. A delegation proposal cannot provide an arbitrary Definition identity. The Controller and Kernel revalidate the configured relationship and current product authority.
24. Sprint 10 preserves depth one, one child in total, no child delegation, no fan-out, no join, no recursive loop, and no Agent-to-Agent free-form conversation.
25. Delegation creation atomically records the relation, child Agent Run, child Job, parent waiting transition, and idempotent `action_id` binding.
26. `delegation_id` is the durable handle; PostgreSQL is the only lifecycle truth, and no MCP Task table, task list, or polling Worker is introduced.
27. Child terminalization atomically records the outcome and requeues an eligible parent. Notification remains advisory, and normal Job polling recovers missed notification.
28. A successful child stores one immutable, typed Agent Result. Delegation, parent Checkpoint, and Trace carry its reference and integrity metadata rather than copying its payload.
29. Child input is a bounded server-built context manifest and excludes full parent context, hidden reasoning, raw Provider payloads, and caller-supplied authority.
30. The first implementation uses the official MCP Go SDK over an in-memory transport. Generated delegation is a standard synchronous `tools/call` whose immediate server result is an internal scheduling receipt; Nano never labels that receipt as an MCP Task or exposes a private Task wire format.
31. The Controller interprets an accepted `exclusive_delegation` call as a suspension boundary: it persists the logical Proposal with delegation creation, returns no scheduling receipt to the model, releases the parent Lease, and resumes only after the child reaches a durable terminal state.
32. Product-neutral Agent Runs no longer require Chat Message or Member columns. A Chat Run owns the Chat input/output relationship and references one root Agent Run.
33. One Agent Tree owns the root and child shared absolute deadline and logical budgets. Generic Delegations relate parent and child Runs without Role columns.
34. A fresh Attempt reconstructs Provider-neutral context from pinned Definitions, authorized product input, and normalized Checkpoints without Provider sessions or in-memory conversation authority.
35. Required context that cannot fit its pinned budget fails with `context_budget_exhausted`; required contracts, evidence, and accepted Action Results are never silently dropped.
36. Existing Leader behavior, Research policy, Source Discovery privacy, cancellation, publication, and Member APIs remain unchanged.
37. New Research Runs propose exactly one MCP `web_search` Action containing one to three bounded queries, then deterministically complete from its accepted Result without another Model Call.
38. Existing Sprint 9 `submit_research_queries`, Role Profile, Role checkpoint, and `agent_role` state remains readable only for already admitted Runs during drain.
39. Migration follows expand, activate, drain, and contract. Historical records are not rewritten merely to adopt new terminology.
40. Deterministic tests cover catalog validation, scoped discovery, metadata authority, error mapping, scheduling, delegation idempotency, recovery boundaries, legacy drain, and unchanged Chat/Research journeys without live credentials.

## 6. Canonical Terms

- **Agent Definition:** immutable reusable configuration identified by exact `identity@version`.
- **Agent Executor:** registered Go implementation of a bounded execution strategy and capability ceiling.
- **Agent Run:** one product-neutral durable invocation of a pinned Agent Definition.
- **Agent Attempt:** one leased effort to advance an Agent Run.
- **Agent Job:** the durable delivery record for an Agent Run.
- **Agent Tree:** root and descendants sharing a deadline and logical budget.
- **Agent Result:** one immutable typed result produced by a successful child Agent Run.
- **Chat Run:** Member-visible lifecycle of one requested Chat answer; it owns Message relationships and references a root Agent Run.
- **Decision Contract:** typed model decision with no executable capability or Action Result.
- **MCP Tool Plane:** the scoped Host/Server boundary for every executable Agent tool.
- **Attempt Context Handle:** ephemeral server-side authority injected into MCP metadata.
- **Logical Action Identity:** stable `action_id` reused across infrastructure Attempts.

`Agent Execution Run` is not used: an Agent Run already denotes execution. `Role` is legacy Sprint 9 state, not a new framework concept.

## 7. Framework Boundary

```text
Chat admission
  -> active release manifest
  -> exact Agent Definition + Model Policy
  -> Chat Run + root Agent Run + Agent Tree + Job

Agent Worker
  -> Execution Host (Lease, heartbeat, disposition)
  -> Executor Registry
  -> Agent Controller
       -> Models Module for decisions
       -> MCP Host -> Nano Tools MCP Server
            -> ordinary tool adapters
            -> generated child delegation adapters
  -> Checkpoints / Results / Trace / Publication Barrier
```

Configuration chooses among reviewed capabilities. It never becomes executable code or authorization. The Controller remains the authority above MCP, and PostgreSQL remains the authority below it.

## 8. Agent Definition Contract

Each strict Definition contains only declarative bindings equivalent to:

```json
{
  "identity": "research.source-discovery",
  "version": 1,
  "executor": "research",
  "model_policy": "agent.research-default@1",
  "prompts": { "planner": "agent.research-planner@1" },
  "contracts": {
    "input": "research.discovery-task@1",
    "result": "research.discovery-result@1"
  },
  "tools": ["web_search"],
  "children": [],
  "limits": {
    "model_calls": 1,
    "actions": 1,
    "action_batch": 1,
    "context_bytes": 65536,
    "result_bytes": 262144,
    "attempts": 3
  },
  "delegation": {
    "description": "Discover bounded public web source candidates for an explicit source-discovery request"
  }
}
```

This is the Sprint 10 Definition shape. Root Definitions omit `delegation`; callable children require it. Schema changes require a new Definition version and documentation review. Every reference is exact, startup validation is strict, and configuration can only narrow the selected Executor's registered ceiling.

## 9. MCP And Delegation Semantics

The first Nano Tools MCP Server runs in the Agent Worker process over the official SDK's in-memory transport. This is still a real protocol boundary: scoped discovery, invocation, and result/error normalization pass through MCP rather than calling a parallel direct Action path.

Ordinary tools return their business result in the same call. A generated child tool instead completes the protocol call after one transaction has accepted the durable delegation:

- the Tool Server revalidates the pinned parent/child relationship, product authority, Lease, deadline, and budgets;
- the transaction resolves or creates by stable `action_id`, creates the child Run and Job, records the parent Proposal, moves the parent Job to `waiting`, and clears its Lease;
- the synchronous MCP result is an internal scheduling receipt consumed by the Controller, not a model-visible Action Result;
- the child terminal transaction stores the immutable Agent Result or safe error and requeues an eligible parent;
- the resumed parent resolves the Delegation from PostgreSQL and checkpoints the eventual Agent Result reference as the original Action Result;
- cancellation continues to map to the bounded Delegation Kernel rather than an MCP-specific task store.

The parent does not hold a Lease or run an MCP polling loop while waiting. MCP Tasks and A2A remain future interoperability adapters for remote generic clients or independently deployed Agents; neither is required inside Nano's single runtime and database authority boundary.

## 10. Storage And Migration

The target model contains:

- immutable Agent Definition and Model Policy registrations;
- release manifests selecting exact definitions for new admission;
- `chat_runs` for Message ownership and product retry/publication state;
- product-neutral `agent_runs` pinned to one Definition and one Agent Tree;
- `agent_jobs`, generic Run Checkpoints, Durable Agent Trace, and Run evidence linked to Agent Runs;
- generic Delegations without Role columns;
- immutable Agent Results referenced by delegations and parent Checkpoints.

Migration is additive first. New-path writes use Definitions and generic runtime state. Legacy columns, configuration sets, Role profiles, and Role-specific checkpoints remain read-only inputs for non-terminal Sprint 9 Runs. Contract migration occurs only after a durable query proves no active legacy Run depends on them.

## 11. Explicit Non-Goals

- Any Studio UI, Output persistence, renderer, or Output permission change.
- Report, mind-map, flashcard, data-table, audio, quiz, slide, infographic, or video Agents.
- Member-created, remotely installed, mutable, or hot-reloaded Agents.
- Dynamic code loading, executable configuration, workflow DSLs, or arbitrary DAGs.
- Parallel Action batches, child fan-out, join, recursion, quorum, or human-in-the-loop Tasks.
- MCP Sampling or one MCP Server per Agent.
- Remote MCP transport, third-party MCP marketplace, or OAuth design.
- Exactly-once tool RPC, generic tool-call ledger, cross-Agent memory, or raw transcript persistence.
- Executor multi-version dispatch before a real rolling-compatibility requirement exists.
- New Member API, SSE field, route, or visible product behavior.

## 12. Delivery Slices

1. Add strict catalogs, immutable registration, release manifest selection, and validation fixtures.
2. Add Executor Registry and new-path Agent Run admission; migrate Leader/Research configuration and dispatch.
3. Add the in-process MCP Host/Server, scoped tool discovery, metadata authority, scheduling, and error normalization; adapt existing synchronous tools.
4. Route Research Web Search through MCP while retaining legacy checkpoint recovery.
5. Generalize Chat Run and Agent Run storage through additive migration and compatibility projections.
6. Add Agent Tree and immutable Agent Result storage, then remove Role fields from new Delegations.
7. Add generated child tools and Nano-owned durable delegation suspension over standard `tools/call`.
8. Activate the new release manifest, drain legacy Runs, verify recovery and regression evidence, then contract obsolete paths in a later safe migration if any historical reader still needs them.
