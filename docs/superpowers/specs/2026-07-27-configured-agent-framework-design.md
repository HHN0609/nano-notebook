# Configured Agent Framework and MCP Tool Plane Design

**Date:** 2026-07-27

**Status:** Approved; configured activation implemented, MCP Tasks delegation gated on official SDK support

**Scope:** Sprint 10 Agent infrastructure only; no new Member-facing feature or product Agent

## 1. Outcome

Sprint 10 converts Nano Notebook's working but Leader/Research-shaped runtime into a bounded Agent framework:

1. strict Git-owned Agent Definitions replace hard-coded Role Profiles for newly admitted Runs;
2. a single Executor Registry binds those Definitions to code-owned behavior and capability ceilings;
3. all executable Agent tools cross one internal MCP boundary;
4. exact child Agent references become generated MCP delegation tools backed by the durable Delegation Kernel;
5. Chat product lifecycle is separated from reusable Agent invocation lifecycle;
6. migrated Leader and Research prove compatibility, recovery, and behavior without adding a new product capability.

The design deliberately stops before a general workflow platform. It retains a depth-one, one-child topology and one PostgreSQL lifecycle authority. Adding a JSON file can instantiate an already-reviewed execution shape, but it cannot create code, permissions, publication authority, or a new graph.

## 2. Confirmed Scope

### 2.1 In scope

- Embedded Agent Definition and Model Policy catalogs.
- Immutable durable registration and exact release-manifest selection.
- Definition-to-Executor dispatch with no new-path Role concept.
- A unified Nano Tools MCP Server for ordinary tools and child delegation.
- An MCP Host/Client in the Agent Worker using official-SDK in-memory transport.
- Scoped discovery, non-model-visible Attempt authority, tool scheduling, error mapping, and at-least-once recovery.
- Generic Agent Runs, Agent Trees, Delegations, Results, Checkpoints, Jobs, and Trace.
- A Chat Run that owns Message relationships and references one root Agent Run.
- Migration of existing Leader and Research behavior to the new path.
- Read-only legacy recovery until all already admitted Sprint 9 Runs drain.

### 2.2 Out of scope

- Studio and every Output feature shown in its product concept: report, mind map, flashcards, data table, audio, quiz, slides, infographic, and video.
- New Director, Builder, specialist, or Member-selectable Agents.
- UI, API, SSE, permission, or publication changes visible to Members.
- Dynamic plugins, remote installation, hot reload, user-authored Agents, and mutable aliases.
- A workflow DSL, general DAG, recursion, fan-out, join, parallel child execution, quorum, or human-in-the-loop task state.
- MCP Sampling, third-party MCP Servers, remote transport, marketplace discovery, and OAuth.
- Exactly-once RPC, a generic tool invocation ledger, persistent Provider threads, and cross-Agent memory.
- Concurrent Executor implementation versions before operational evidence requires them.

## 3. Current-State Findings

Sprint 9 provides the right reliability primitives:

- one durable `agent_jobs` delivery record per Run;
- leased Attempts with fencing and typed dispositions;
- immutable Prompt registration;
- normalized Action and Role Checkpoints;
- a generic Delegation lifecycle with waiting and parent wake-up;
- cancellation, root deadline, Durable Agent Trace, and Publication Barrier.

The remaining coupling is architectural rather than a missing feature:

- `NewAgentConfigurationSet` requires exactly Leader and Research profiles;
- `validProfilePrompts` and `validProfileTools` contain Role-specific branches;
- `RoleRegistry` duplicates behavior identity, executor version, visibility, publication, and delegation policy;
- `agent_runs` requires Chat, Message, Member, model, prompt, executor-version, and Role fields in one record;
- `agent_run_delegations` stores fixed parent and child Roles;
- Research uses a typed planning decision and then calls its Web Search Provider directly rather than using the common Action path;
- ordinary Actions use `ActionRegistry`, while child creation calls `DelegationKernel` directly;
- durable Research results are split between Role checkpoints and domain rows rather than exposed as one reusable child result contract.

These constraints mean another Agent would need Go branching across configuration, admission, dispatch, tools, persistence, and delegation even if it used an existing safe execution shape.

## 4. Alternatives Considered

### 4.1 Put MCP in front of the Sprint 9 Role runtime

This would expose a standard-looking child tool but keep `RoleRegistry`, exact-two profiles, Role columns, direct Provider calls, and Chat-bound Agent Runs underneath. It reduces no real hard-coding and leaves two tool systems. Rejected.

### 4.2 Migrate to a small configured framework

This is selected. Definitions bind exact reviewed capabilities, Executors retain code-owned control flow, MCP becomes the common invocation plane, and the existing PostgreSQL runtime remains authority. It makes the next conforming Agent primarily configuration work without permitting executable configuration.

### 4.3 Build a generic DAG or adopt a workflow runtime

A graph runtime would require node/edge semantics, joins, state projection, retries at multiple layers, versioned workflow migration, and a larger authorization model. Nano currently has one depth-one relationship and established database lifecycle guarantees. This is deferred until a real product needs topology that the bounded Executor model cannot express.

## 5. Canonical Model

```text
Agent Definition  reusable immutable configuration
       |
       | selected by exact reference
       v
Agent Run         one durable invocation
       |
       +-- Agent Job       delivery to a Worker
       +-- Agent Attempt   one fenced leased effort
       +-- Checkpoints     accepted durable working state
       +-- Agent Result    one immutable successful child result
       `-- Delegation      parent/child lifecycle edge

Agent Tree        root + descendants sharing deadline and budgets
Chat Run          product lifecycle owning Messages and root Agent Run
```

`Agent` is an informal umbrella term. Durable APIs and schemas use the precise nouns above. `Agent Execution Run` is not introduced because `Run` already denotes an invocation.

`Role` does not survive in the new model. Sprint 9 Role is a compatibility discriminator for old records, not a new registration layer. The registered Executor already identifies the implementation whose code can exercise authority.

## 6. Agent Catalog

### 6.1 Authored format

Each Agent Definition is one strict JSON file under an embedded catalog. The initial directory is:

```text
internal/agentcatalog/
|-- definitions/
|   |-- chat.leader/v1.json
|   `-- research.source-discovery/v1.json
|-- model-policies/
|   |-- agent.chat-default/v1.json
|   `-- agent.research-default/v1.json
|-- contracts/
|   |-- research.discovery-task/v1.schema.json
|   `-- research.discovery-result/v1.schema.json
|-- schema/
|   |-- agent-definition.schema.json
|   `-- model-policy.schema.json
`-- catalog.go
```

The exact package may change to fit existing package boundaries, but the files and immutable identities do not become constructor defaults or environment-generated content.

An Agent Definition has this conceptual shape:

```json
{
  "identity": "research.source-discovery",
  "version": 1,
  "executor": "research",
  "model_policy": "agent.research-default@1",
  "prompts": {
    "planner": "agent.research-planner@1"
  },
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

A root-only Definition omits `delegation`. An allowlisted child must include it because the registry uses its bounded description and exact input/result contracts to materialize the parent-visible tool.

Input and Result Contracts are strict embedded JSON Schemas registered by exact identity and version. Prompt output Contracts remain owned by the existing Prompt Catalog; an Agent Definition references the Prompt Version rather than duplicating that schema.

A Model Policy has the fixed Sprint 10 shape:

```json
{
  "identity": "agent.research-default",
  "version": 1,
  "provider_model": "qwen-plus",
  "temperature": 0,
  "max_output_tokens": 2048,
  "timeout_ms": 30000
}
```

### 6.2 Strictness

Decoding rejects unknown fields and trailing documents. Registration rejects:

- empty, malformed, duplicate, or conflicting `identity + version`;
- a non-canonical reference or mutable `latest` alias;
- unresolved Executor, Model Policy, Prompt, Contract, tool, or child;
- a tool or child outside the Executor ceiling;
- invalid or colliding generated MCP names;
- a local limit above the Executor maximum or below the minimum needed by its state machine;
- cyclic child references, depth above one, or more than one child;
- task/result contract incompatibility;
- templates, inheritance, includes, environment interpolation, arbitrary entrypoints, SQL, endpoints, retry algorithms, permission rules, publication behavior, or control-flow expressions.

Accepted JSON is decoded into a typed value, normalized with deterministic ordering, encoded canonically, hashed with SHA-256, and registered immutably. Registering the same identity and hash is idempotent; the same identity with different content fails startup.

### 6.3 Model Policy

A Model Policy binds an exact Provider route and non-secret settings such as temperature, maximum output tokens, and call timeout. A Definition references an exact Policy version. Admission pins Policy identity, hash, and resolved Provider model so retries and continuations cannot change behavior.

Provider endpoints, credentials, and secrets remain environment configuration. Chat and Research model environment variables stop selecting production behavior after the legacy Profile adapter drains.

### 6.4 Release manifest

The existing Agent Configuration Set evolves into a deployment release manifest selecting exact root and child Definition references. It no longer copies model, Prompt, tool, budget, or Executor fields. A new admission pins everything transitively; runtime never asks for the current or latest Definition.

Sprint 10 registers only:

- `chat.leader@1` using Executor `chat_leader`;
- `research.source-discovery@1` using Executor `research`.

No new product Agent is hidden in the framework delivery.

## 7. Executor Registry

### 7.1 Responsibility

One immutable application registry maps a stable identity to:

- one Go `AgentExecutor` implementation;
- permitted Prompt purposes and Contract shapes;
- allowed ordinary tool names and scheduling classes;
- allowed child Executor identities;
- maximum local limits and topology;
- Member visibility and publication capability where relevant;
- supported Checkpoint decoders and recovery invariants.

The Definition selects an Executor and narrows these sets. It cannot replace a function, widen a set, change a scheduling class, grant publication, or supply policy code.

### 7.2 Initial executors

`chat_leader` owns the existing fixed Leader control flow:

1. obtain a typed route decision;
2. apply deterministic product policy;
3. enter the existing Controller loop for ordinary Chat; or
4. request its exact configured Research child and later interpret the terminal result;
5. publish only through the existing Chat Publication Barrier.

`research` owns the migrated Research control flow:

1. make one model decision proposing one `web_search` Action with one to three queries;
2. let the Controller invoke and checkpoint it through MCP;
3. deterministically normalize candidates and store one Research Agent Result;
4. terminalize the child through the Delegation Kernel.

Legacy Research Runs with accepted `query_plan` or `search_result` Role Checkpoints continue through the compatibility executor. Their durable payloads are read, not rewritten.

### 7.3 Versioning

Definitions do not carry an Executor implementation version in Sprint 10. Compatible code must understand every active Definition and Checkpoint it claims. A breaking implementation change follows expand and drain before replacement. Multiple concurrent Executor versions are deferred rather than designed speculatively.

## 8. Unified MCP Tool Plane

### 8.1 Boundary

```text
Agent Controller
  | validates proposal, budgets, scheduling, authorization
  v
MCP Host/Client
  | injects Attempt Context Handle + action_id metadata
  v
Nano Tools MCP Server
  | scoped tools/list and tools/call
  +-- calculate adapter
  +-- current_time adapter
  +-- search_evidence adapter
  +-- web_search adapter
  `-- generated delegate.<identity>.v<version> adapter
```

The first Host and Server are in the Agent Worker process over official-SDK in-memory transport. The boundary still uses MCP discovery, invocation, content/error normalization, and Tasks contracts. There is no alternate direct invocation path once migration completes.

The Server is an adapter layer. It has no independent queue, scheduler, Agent state machine, retry loop, authorization policy, Model gateway, or publication authority.

### 8.2 Discovery and authority

For each Model Call, the Controller computes:

```text
visible tools = Definition allowlist
              intersect Executor capability ceiling
              intersect current product authorization
              intersect remaining runtime budget
```

The Host creates a process-local opaque Attempt Context Handle bound to the leased Attempt, fencing token, pinned Definition, visible-tool set, product authorization snapshot/reference, deadline, and remaining logical budget. It injects the handle into MCP request metadata, never into the tool schema or model context.

The Server resolves and revalidates this handle for both `tools/list` and `tools/call`. Losing the Lease, cancellation, deadline expiry, or authorization revocation invalidates it. Recovery creates a new handle; handles are not durable bearer credentials.

The Controller records the canonical name, schema, Contract identity, and SHA-256 of each exposed tool for the Model Call. Invocation must resolve the same materialized definition and hash or fail closed.

### 8.3 Logical Action identity

An accepted Provider-neutral Action Proposal receives a stable `action_id`. The Host supplies it as non-model-visible metadata on `tools/call`. It remains stable across infrastructure Attempts while the Attempt Context Handle changes.

Tool execution is at-least-once:

- a crash after an external call but before accepted Checkpoint commit may repeat that call;
- pure and read-only tools must tolerate repetition;
- delegation is state-changing and must resolve-or-create by `action_id` transactionally;
- only the currently fenced Attempt may append an Action Result;
- repeated physical calls appear in Trace but do not consume another logical Action budget.

The accepted Proposal/Result Checkpoint stream remains the logical ledger. Sprint 10 does not add a second generic invocation table or claim exactly-once RPC.

### 8.4 Scheduling classes

Each tool adapter registers a class in code:

- `ordered_sync`: may appear in a bounded batch and executes strictly in proposal order;
- `exclusive_task`: must be the only proposal because it suspends the Agent Run.

`calculate`, `current_time`, `search_evidence`, and `web_search` are `ordered_sync`. Generated child tools are `exclusive_task`. Definitions may reduce batch limits but cannot alter a class. There is no parallel execution or hidden fan-out.

### 8.5 Decision Contracts

A Provider may use function-call syntax to return typed decision data, but syntax does not create an executable tool. `select_leader_route` remains a versioned Decision Contract inside the Models Module because it has no application side effect and produces no Action Result.

New Research removes the `submit_research_queries` decision followed by an executor-owned direct Provider call. Its model instead proposes one actual MCP `web_search` Action. That Action takes one to three bounded queries, returns normalized candidates, and is checkpointed through the same Controller path as other tools.

### 8.6 Error mapping

The Controller maps every outcome into one of four classes:

| Class | Durable effect | Model-visible |
| --- | --- | --- |
| Contract-declared domain error | accepted Action Result; consumes logical budget | bounded code and safe fields |
| Transient infrastructure error | no Result; retryable Attempt with same `action_id` | no |
| Lost Lease/fencing authority | abandoned Attempt | no |
| Authorization, schema, Definition, or invariant failure | safe terminal Run failure | no |

MCP `isError` is a transport representation. A tool adapter or Server cannot choose Agent Run, Job, Chat Run, or publication status.

## 9. Configured Delegation Through MCP Tasks

### 9.1 Generated tool

For an exact child reference `research.source-discovery@1`, the Tool Registry derives:

```text
delegate.research.source-discovery.v1
```

The tool's description comes from the child Definition's bounded delegation metadata. Its input and output schemas come from exact Contracts. The parent supplies task business input only; it cannot supply child identity, definition version, Run IDs, credentials, budgets, or authority.

### 9.2 Creation

After Controller validation, `tools/call` performs one transaction that:

1. revalidates Attempt fencing and current product authorization;
2. resolves the exact parent and child Definitions and relationship;
3. enforces depth one and the one-child total limit;
4. resolves an existing Delegation by `action_id` or creates a new identity;
5. creates the child Agent Run and Agent Job under the same Agent Tree;
6. stores the server-built Child Context Manifest reference;
7. records the parent Action/Delegation link;
8. moves the parent Job to `waiting`, clears its Lease, and notifies child availability.

The model cannot cause two children by retrying the same logical Action.

### 9.3 Task projection

Nano targets the `io.modelcontextprotocol/tasks` extension described by final SEP-2663 for protocol version 2026-07-28:

- Tasks capability is negotiated per request;
- `tools/call` can return a polymorphic Task result;
- `task_id` is the Delegation ID;
- `tasks/get` projects Delegation and child Run state and includes terminal result/error;
- `tasks/cancel` maps to Kernel cancellation;
- `tasks/update` is handled according to the extension, but Nano never emits `input_required`, so there are no application input responses to consume.

Nano does not implement the older experimental Task contract, `tasks/result`, `tasks/list`, or an `input_required` lifecycle. PostgreSQL is the only durable truth; there is no MCP Task table.

The protocol sources are:

- <https://modelcontextprotocol.io/extensions/tasks/overview>
- <https://modelcontextprotocol.io/seps/2663-tasks-extension>

As of the design date, official Go SDK v1.7.0 is still a prerelease targeting protocol version 2026-07-28, requires Go 1.25, and its published release notes do not establish complete SEP-2663 support. The implementation begins with a dependency spike: select an official SDK version and toolchain, then prove negotiation plus Task behavior before delegation is enabled. If first-class extension helpers are absent, a narrow adapter may use the SDK's official custom-method surface with exact SEP-2663 schemas; it may not invent or vary the wire contract. Production release should prefer stable v1.7 when available.

### 9.4 Completion and resume

Child success creates one immutable Agent Result and atomically terminalizes the child, Delegation, and child Job. Failure and cancellation record only safe error codes. An eligible parent is requeued in the same transaction and receives an advisory PostgreSQL notification.

The parent holds no Lease while waiting and does not poll Tasks. On its next leased Attempt it calls `tasks/get` once, validates the terminal Result reference or safe error, and checkpoints that projection as the original Action Result. Existing Leader policy continues to fail the Chat path when Research fails; Sprint 10 does not add degradation behavior.

## 10. Context and Result Boundaries

### 10.1 Context projection

Before every Model Call, a Context Builder reconstructs a Provider-neutral projection from:

- the pinned Agent Definition and Model Policy;
- exact Prompt and Contract versions;
- authorized Chat or child task input;
- the fixed Run evidence set;
- accepted Proposal and Result Checkpoints;
- deterministic policy outcomes required by the Executor.

Provider threads, MCP session history, raw Provider requests/responses, hidden reasoning, and Worker memory are disposable. If required state does not fit the pinned byte/item budget, the Run fails with `context_budget_exhausted`. The builder never silently drops required Contracts, evidence, or accepted Results.

### 10.2 Child Context Manifest

The parent supplies only bounded business task input. The server constructs an immutable manifest containing authorized product and evidence references, pinned child configuration, remaining tree budget, shared deadline, and result contract. It does not copy the parent's full context or reasoning.

### 10.3 Agent Result

A successful child creates one immutable row with:

- Result ID and producer Run ID;
- exact Contract identity, version, and hash;
- canonical JSON payload;
- payload SHA-256 and byte size;
- creation timestamp.

The producer Run has at most one successful Result. Delegation, Task projection, parent Checkpoint, and Trace store only the reference, Contract identity, hash, and size. Authorized context or product code resolves the payload. Result-byte budget is charged once at creation.

Research's normalized Discovery outcome becomes the first Result type. Existing Discovery Session and candidate records remain the product/domain projection needed by current Source Discovery behavior; the Agent Result is the typed child handoff and does not replace authorization or Source import rules.

## 11. Product-Neutral Runtime Storage

### 11.1 Target ownership

```text
chat_runs
  id, user_id, chat_id, input_message_id, output_message_id,
  root_agent_run_id, product status/error, retry/publication timestamps

agent_trees
  id, root_agent_run_id, absolute deadline,
  logical budget limits/reservations/consumption

agent_runs
  id, tree_id, definition_id/version/hash, executor identity,
  status/error, parent context reference, timestamps

agent_jobs
  id, run_id, delivery/Lease/retry state

agent_run_delegations
  id, parent_run_id, child_run_id, action_id, ordinal, depth,
  state, result_id or safe error, consumed_at

agent_run_results
  id, producer_run_id, contract identity/version/hash,
  canonical payload, payload hash/size, created_at
```

Existing normalized Checkpoints, Trace, Evidence, and Model Call records continue to reference Agent Runs. Chat publication resolves through Chat Run ownership and retains the existing fencing and authorization barrier.

### 11.2 Why Chat Run is introduced now

The current `agent_runs` record is a Chat product record and an internal invocation at once. Configuration alone cannot make the runtime reusable while Message and Member columns remain mandatory. Introducing `chat_runs` is the smallest proven separation. Sprint 10 does not add a generic nullable `product_runs` table or speculative records for future Studio work.

### 11.3 Agent Tree budget

The tree owns the shared absolute deadline and logical model-call, Action, context-byte, and result-byte limits. Each Run also has Definition-local limits and can consume only the smaller remaining allowance. Accepted logical work is not charged again after recovery; every physical Provider attempt and reported usage remains observable. Provider monetary cost is telemetry, not exact authorization state.

## 12. Migration

Migration follows four operational phases.

### 12.1 Expand

- Add catalog, Model Policy, release-manifest, Chat Run, Agent Tree, generic Definition reference, Result, and Delegation `action_id` storage.
- Register new Definitions and Executors alongside legacy Role configuration.
- Backfill Chat Run and tree ownership for existing rows without changing public IDs or terminal history.
- Deploy readers capable of both legacy and new Checkpoint shapes.

### 12.2 Activate

- New Chat admission pins `chat.leader@1` and creates Chat Run plus generic root Agent Run.
- New Leader delegation uses exact `research.source-discovery@1` through MCP.
- New ordinary tools and Research Web Search use the MCP path.
- New rows do not write Role, Role Profile, executor-version, or Chat ownership fields on Agent Runs.

### 12.3 Drain

- Existing non-terminal Sprint 9 Runs continue under their pinned Configuration Set and Role Profile.
- Legacy Leader/Research executors and Role Checkpoint decoders are read-only compatibility code.
- Worker readiness supports both paths until a durable query proves no non-terminal legacy Run remains.

### 12.4 Contract

- Retire Role Registry, Action Registry, default two-profile constructors, direct Research Web Search path, and obsolete write code.
- Drop obsolete columns or tables only when no active or required historical reader depends on them.
- Preserve immutable Prompt, configuration, Checkpoint, and Trace records needed for historical explanation.

The deployment must never reinterpret an admitted Run using the new active manifest, and unsupported active configuration must fail Worker readiness rather than terminalizing customer work.

## 13. Security and Reliability Invariants

- Definitions grant no authority; Controller policy, current product authorization, PostgreSQL RLS, and publication barriers remain authoritative.
- The model never supplies Agent identity, Run identity, credentials, permissions, budget, Lease, or Attempt handle as tool input.
- Only the current fenced Attempt can accept a Checkpoint, Result, lifecycle transition, or publication.
- Cancellation and absolute deadline cover the entire Agent Tree, including queue delay and parent waiting.
- Parent cancellation owns child cancellation; a child has no Member command surface.
- Child Results and errors are bounded, typed, and integrity checked before parent use.
- Standard logs and Trace exclude credentials, full Prompts, raw Provider envelopes, unrestricted Source content, and chain of thought.
- PostgreSQL state is authoritative; notifications and MCP sessions are disposable projections.

## 14. Testing Strategy

Implementation follows test-driven development. Each behavior is first expressed as a failing test, then minimally implemented and refactored only while green.

### 14.1 Catalog tests

- strict unknown-field and trailing-document rejection;
- canonical hashing and idempotent registration;
- duplicate/conflicting identity rejection;
- exact reference resolution and no `latest` behavior;
- capability ceiling, limit, topology, and generated-name validation;
- environment cannot override pinned model behavior.

### 14.2 MCP tests

- scoped `tools/list` never exposes an unallowlisted tool;
- business schemas contain no authority fields;
- missing, expired, stale-Lease, wrong-Definition, or wrong-hash context fails closed;
- scheduling rejects a batched delegation and preserves ordered synchronous execution;
- error classes map to the correct Attempt and Checkpoint effects;
- crash boundaries prove at-least-once behavior and stable `action_id` reuse;
- official SDK conformance proves discovery, call, Task negotiation, get, and cancel behavior.

### 14.3 Runtime and migration tests

- Chat Run and root Agent Run ownership remain stable across retry and publication;
- child creation is transactionally idempotent by `action_id`;
- parent waiting releases its Lease and child terminalization requeues once;
- polling recovers a missed notification;
- cancellation, deadline, and late-result fencing cover root and child;
- Agent Result writes once and all projections preserve its integrity metadata;
- context reconstructs from Checkpoints on a different Worker;
- legacy Role/Profile and Research Checkpoints remain recoverable during drain;
- newly admitted Runs never write the legacy path;
- existing Chat, Source Discovery, authorization, and publication suites remain green.

Default tests require no live Model, Brave, or remote MCP credentials. Credentialed smoke tests remain opt-in.

## 15. Delivery Order

The implementation should use small independently reviewable slices:

1. strict catalog and Model Policy types, fixtures, hashing, registration, and release manifest;
2. Executor Registry and Definition-pinned new-path dispatch;
3. in-process MCP transport, scoped registry, context handles, tool hashing, scheduling, and error mapper;
4. ordinary Action adapters and Controller cutover;
5. Research `web_search` MCP behavior plus legacy recovery adapter;
6. additive Chat Run, Agent Tree, generic Agent Run, Delegation, and Agent Result migrations;
7. generated child tools and official Tasks extension conformance;
8. Leader/Research new-path activation, drain evidence, and removal of obsolete writers;
9. full regression, migration, race, and acceptance verification.

The MCP Tasks SDK gate may delay slice 7. It must not cause a private wire implementation or conceal partial conformance. Slices that do not depend on Tasks may land independently if they leave production admission on a coherent supported path.

## 16. Acceptance Boundary

Sprint 10 is accepted when a fresh Chat Leader and its optional Research child run entirely through pinned Agent Definitions, Executor Registry, generic runtime records, and the MCP Tool Plane; they recover across injected crashes without widening authority; legacy Sprint 9 Runs still drain safely; and every Member-visible behavior remains unchanged.

Success is not measured by how many new Agent identities exist. It is measured by whether the next approved Agent that fits an existing Executor shape can be added through strict configuration without adding identity branches to runtime infrastructure.
