# Versioned Agent Configuration And Delegation Hardening Design

**Date:** 2026-07-26

**Status:** Approved

**Scope:** Sprint 9 Agent infrastructure governance and reliability; no new Member-facing capability

## 1. Outcome

Sprint 9 turns the Sprint 8 Leader-to-Research path into a small, explicit Agent execution architecture:

1. all six production model instructions are immutable Git-owned Prompt Versions rather than inline Go strings;
2. every Agent Run pins an immutable Agent Configuration Set whose Role Profiles may select different models, prompts, tools, and local limits for Leader and Research;
3. Leader routing and Research planning use typed required function-call contracts rather than free-form token or line parsing;
4. a bounded Delegation Kernel owns generic parent-child lifecycle while Role Executors own Agent behavior and domain outcomes;
5. a uniform Attempt Disposition makes retry, waiting, terminalization, and Lease loss explicit;
6. Research resumes from accepted query-plan and per-query search-result Checkpoints;
7. existing Source Discovery, authorization, publication, Trace, and Member API guarantees remain unchanged.

The delivery is deliberately not a general multi-Agent framework. Sprint 9 introduces no new Role, graph, plugin, fan-out, same-turn Web answer, or UI.

## 2. Current-State Findings

The current implementation proved durable child Runs but concentrates unrelated responsibilities in `LeaderExecutor`:

- role dispatch;
- Leader routing and policy;
- child creation and parent waiting;
- Research planning and Web Search;
- Discovery Session completion;
- child failure, parent wake-up, and publication.

The current database similarly mixes generic lifecycle and Research payload in `agent_research_delegations`. The child copies its parent's model, prompt version, configuration, deadline, and Action budgets. Research writes expanded queries but re-invokes the planner and every Web Search after a crash. Success notifies the parent Worker channel, while the direct Research failure path only commits the requeue and relies on the periodic scan.

Production instructions currently exist in six code locations:

1. Leader Router;
2. Research Planner;
3. source-less Chat composer;
4. grounded Chat composer;
5. query contextualizer;
6. image-evidence normalizer used by Source Processing.

The existing Run-level `prompt_version` cannot identify which instruction and contract each Model Call actually used. Invalid Router and Planner outputs are parsed from free-form text. Job errors also have inconsistent behavior: some terminalize immediately, some wait for Lease expiry, and transient Web Search failures are terminal.

## 3. Goals

- Make Prompt content and model-facing contracts immutable, reviewable, and addressable by exact identity.
- Make Role behavior independently configurable without permitting runtime-installed Roles.
- Separate Agent behavior from reliable Job and delegation mechanics.
- Preserve one PostgreSQL source of truth for Runs, Jobs, Checkpoints, delegation, and publication.
- Resume Research after the last accepted nondeterministic boundary.
- Make intent, policy, retry, and terminal handoff auditable without recording chain of thought.
- Preserve the existing Sprint 8 product journey and authorization boundaries.

## 4. Non-Goals

- New Agent Roles or Member-selectable Agents.
- Dynamic Roles, plugins, model-created Agents, or user-defined tools.
- Arbitrary DAGs, loops, recursive delegation, parallel fan-out, join, quorum, or partial-success orchestration.
- Agent-to-Agent free-form messaging.
- Parallel Research Web Search.
- Google ADK, LangGraph, Temporal, or another runtime migration.
- Online Prompt editing, mutable aliases, hot switching, remote Prompt consoles, or online A/B tests.
- A formal Agent Eval system or Configuration promotion gate.
- A cross-Agent token or cost ledger.
- Same-turn Web answers, automatic Source import, or any Studio Output work.
- New Member API, SSE, or UI capability.

## 5. Alternatives Considered

### 5.1 Keep Research-specific orchestration

Moving prompts to files and fixing individual bugs would minimize the diff, but every future fixed Role would copy waiting, cancellation, Trace, terminal handoff, and retry logic. The database would remain a Research-specific lifecycle authority. This does not satisfy the confirmed engineering boundary.

### 5.2 Build a bounded in-repository kernel

This is the selected approach. Nano already has PostgreSQL Jobs, leases, Checkpoints, RLS, Durable Agent Trace, Publication Barrier, and product-specific Source Discovery authority. A small Kernel extracts the shared lifecycle while retaining those established guarantees.

### 5.3 Adopt an external Agent or workflow runtime

Google ADK supports Go, multi-Agent composition, dynamic routing, evaluation, deployment, and graph workflows: <https://adk.dev/>. LangGraph provides low-level state graphs, persistence, subgraphs, and durable execution in its Python and TypeScript ecosystem: <https://docs.langchain.com/oss/python/langgraph/overview>. Temporal provides a mature durable Workflow and Activity control plane: <https://docs.temporal.io/>.

Those are credible options when their orchestration model is itself a product requirement. In Sprint 9 they would duplicate or replace Nano's established PostgreSQL authority and require re-proving RLS, cancellation, publication, Trace, Source, and Run semantics for one depth-one relationship. Re-evaluation is appropriate only when graph/HITL orchestration, managed multi-Agent deployment, or cross-service long-running workflows becomes first-class scope.

## 6. Runtime Components

```text
Chat admission
  | pin Agent Configuration Set
  | create Leader Run + Job
  v
Execution Host
  | claim / lease / heartbeat / disposition / backoff
  v
Agent Role Registry
  +-- leader   -> Leader Role Executor
  `-- research -> Research Role Executor
                         |
       +-----------------+------------------+
       v                                    v
Prompt Runtime                      Delegation Kernel
version + contract                  parent/child lifecycle
model invocation                    waiting/resume/cancel
checkpoint metadata                 terminal handoff
       |                                    |
       `----------> Domain Services <-------'
                    Agent Controller
                    Source Discovery
                    Web Search Provider
                    Durable Trace
```

### 6.1 Execution Host

The Agent Worker remains the Job execution host. It claims and heartbeats a leased Attempt, loads the Run's pinned configuration, resolves a Role Executor, and commits the returned Attempt Disposition. It does not interpret Research outcomes or Leader route semantics.

### 6.2 Agent Role Registry

The Registry is immutable application code. Each Role definition identifies:

- Role name and supported executor compatibility versions;
- Member visibility;
- publication authority;
- legal parent and child relationships;
- whether the Role may delegate;
- Role Profile decoder and executor factory.

Sprint 9 registers only `leader` and `research`. Only `leader -> research` is legal. The Leader is the only visible and publishing Role; Research cannot delegate.

### 6.3 Role Executors

A Role Executor advances the Role's bounded behavior and returns a typed Attempt Disposition. It does not directly implement generic Job retry or delegation state transitions.

The Leader Role Executor:

- obtains a typed Route Decision;
- applies deterministic Delegation Policy;
- enters the existing Agent Controller for `continue_chat`;
- requests one Research child for an authorized `delegate_research`;
- interprets a terminal Delegation Outcome after resume.

The Research Role Executor:

- obtains and checkpoints a typed query plan;
- executes missing Web Search steps sequentially;
- checkpoints each accepted Provider-neutral result;
- deterministically merges, deduplicates, and validates candidates;
- commits a Role-owned Discovery outcome.

### 6.4 Delegation Kernel

The Kernel owns:

- relationship validation against the Role Registry;
- child Run and Job creation;
- parent `waiting` transition and Lease release;
- maximum depth and active-child enforcement;
- root deadline and cancellation propagation;
- child terminal handoff;
- parent requeue and notification;
- terminal-outcome consumption.

It does not call models, parse Research queries, decide whether child failure should fail the Leader, or publish Chat content.

## 7. Prompt Catalog

### 7.1 Repository layout

```text
prompts/
|-- agent/
|   |-- leader-router/v1.md
|   |-- research-planner/v1.md
|   |-- chat-composer-bare/v1.md
|   |-- chat-composer-grounded/v1.md
|   `-- query-contextualizer/v1.md
|-- source-processing/
|   `-- image-evidence-normalizer/v1.md
`-- contracts/
    |-- select-leader-route/v1.schema.json
    `-- submit-research-queries/v1.schema.json
```

Every Prompt Markdown file has fixed front matter:

```yaml
---
prompt_id: agent.leader-router
version: 1
output_contract: select-leader-route/v1
---
```

Git is the authoring source of truth. `go:embed` packages the catalog with the application. Startup parses and canonicalizes the metadata, content, and adjacent schema before serving work.

### 7.2 Immutable registration

`model_prompt_versions` stores one durable row per `prompt_id + version` with canonical content, output-contract identity and canonical schema, SHA-256, and registration metadata. Registering the same identity and hash is idempotent. Registering the same identity with different content or contract hash fails startup. Registered Prompt Versions are never overwritten or deleted; retiring Agent Configuration support does not delete their records.

The database copy is required for recovery and rolling compatibility; it is not a remote editing surface. Runtime calls never resolve `latest` and do not accept Prompt content from flags, user input, or a mutable database alias.

### 7.3 Prompt Sets

An Agent Prompt Set is an immutable mapping of the five Agent Prompt purposes to exact Prompt Versions and Contracts. Source Processing pins the image normalizer independently through its Source Processing Configuration while reusing the same Prompt Catalog.

Each Model Call records the actual Prompt identity, version, hash, and contract identity. Standard Trace stores identities and outcomes, not full Prompt content.

## 8. Typed Model Contracts

### 8.1 Leader Router

The Router must call `select_leader_route`:

```json
{
  "route": "continue_chat | delegate_research",
  "reason_code": "ordinary_conversation | existing_source_work | ambiguous_discovery_intent | external_information_without_discovery_request | explicit_source_discovery"
}
```

Only `explicit_source_discovery` is valid with `delegate_research`. Any missing call, wrong tool, unknown enum, mixed final text, or inconsistent pair fails closed and cannot create a child.

The Router may receive the current turn plus a bounded maximum of three recent completed conversation pairs to resolve references. The current turn must itself clearly ask to search, find, collect, research, or add new external Source material. History cannot create a sticky Research mode. Evidence insufficiency and an ordinary request for current information do not independently grant Web Source Discovery.

### 8.2 Delegation Policy

The model returns requested intent only. Application code derives the effective route after checking:

- Member is still Editor or Owner;
- root Run and deadline are active;
- `leader -> research` is registered;
- no conflicting child exists;
- Web Search Provider is configured;
- Notebook and Source-maintenance authority remain valid.

Durable route state preserves requested route, effective route, intent reason, and a bounded policy reason such as `allowed`, `member_not_authorized`, `active_child_exists`, `provider_unavailable`, or `root_not_active`.

### 8.3 Research Planner

The Planner must call `submit_research_queries`:

```json
{
  "queries": ["one to three non-empty bounded query strings"]
}
```

Go validates count, length, Unicode normalization, duplicates, and forbidden empty values. The Planner cannot answer the user, submit URLs as outcomes, select a Provider, raise query count, or authorize an external call.

### 8.4 Other production prompts

- bare and grounded Chat composers continue to return text under their existing publication contracts;
- query contextualization continues to use the required `search_evidence` tool contract;
- image evidence normalization retains its existing bounded JSON validation for Sprint 9 and gains Prompt Version governance only.

## 9. Agent Configuration Sets

Leader admission pins one immutable Agent Configuration Set. A Set contains shared root policy and one Agent Role Profile per registered Role. A Profile identifies:

- exact model and bounded model parameters;
- Agent Prompt Set bindings used by that Role;
- allowed tools and Providers;
- local model-call, search, result-size, and Attempt limits;
- executor compatibility version.

The Research child carries the same Configuration Set identity but materializes the Research Profile. It does not copy the Leader's model or local budgets. The root absolute deadline, Notebook, Member, Chat, and authorization context remain shared.

Infrastructure retry and continuation use the pinned Set and Profiles. A user-requested new Run may use the deployment-selected newer Set.

## 10. Deployment Compatibility

Configuration deployment follows `expand -> activate -> retire`:

1. **Expand:** a release registers new Prompt and Configuration versions while preserving support for active versions.
2. **Activate:** admission selects a new Set only after the Worker release supports its executor compatibility versions.
3. **Retire:** code support may be removed only after PostgreSQL proves no non-terminal Run references that Set.

Worker readiness queries non-terminal Runs and refuses readiness if their Configuration or executor compatibility is unsupported. It does not fail those Runs. Prompt-only changes normally do not increase executor compatibility; typed payload or behavior incompatibility does.

Sprint 9 does not implement capability-aware scheduling for arbitrary mixed Worker fleets. Controlled two-phase deployment or a maintenance window is acceptable under the target operating profile. At least the current and immediately previous compatible Set remain supported until the active-reference check passes.

## 11. Delegation Data Model

`agent_run_delegations` becomes the sole parent-child lifecycle authority:

| Field | Contract |
| --- | --- |
| `id` | Opaque delegation identity |
| `parent_run_id` | One Leader Run |
| `child_run_id` | One internal child Run; globally unique |
| `relationship_key` | Registered relationship identity |
| `ordinal` | Stable child order; Sprint 9 permits only zero |
| `state` | `waiting`, `succeeded`, `failed`, or `cancelled` |
| `safe_error_code` | Bounded terminal error for failure |
| `terminal_at` | Required for terminal state |
| `consumed_at` | Parent consumption timestamp; does not replace terminal state |

`agent_research_outcomes` is Role-owned and maps a successful delegation to exactly one private Discovery Session. Expanded queries move to Research Checkpoints.

`parent_run_id` is removed from `agent_runs` after migration so the system does not retain two relationship authorities. Run role remains on `agent_runs`. Database constraints and application policy enforce one child per delegation, unique `(parent_run_id, ordinal)`, one active child per parent, and only the currently registered Sprint 9 relationship.

The schema retains ordinal for possible future sequential children, but the Sprint 9 Role Profile and policy allow only one total Research child.

## 12. Delegation Transactions

### 12.1 Creation

One transaction locks and revalidates the parent Run and Job, applies Delegation Policy, creates the child Run and Job, resolves the Research Role Profile, creates the lifecycle row and searching Discovery Session, writes Trace linkage, and moves the parent Job to `waiting` without a Lease.

### 12.2 Child terminal handoff

One transaction revalidates child Lease and root authority, commits the Role outcome or safe error, terminalizes child Run and Job, terminalizes the delegation, requeues a still-valid parent Job, and sends `pg_notify` after the state writes. Both success and failure paths use the same Kernel operation.

The Kernel delivers a `succeeded`, `failed`, or `cancelled` outcome. The parent Role owns the product policy. Sprint 9 preserves the current Research behavior: Research failure fails the Leader; it does not silently degrade to ordinary Chat.

### 12.3 Consumption

The parent locks the terminal delegation, validates its Role-owned outcome, performs its Role policy, and writes `consumed_at`. Consumption is idempotent and does not erase `succeeded`, `failed`, or `cancelled`.

## 13. Attempt Disposition And Retry

Role execution produces one disposition:

- `completed`: terminal success committed;
- `waiting`: parent released its Lease for a child outcome;
- `retryable`: the same Job should be deliberately requeued;
- `terminal`: safe terminal failure should be committed;
- `abandoned`: the Attempt must stop without further effect, with bounded cause `lease_lost` or `cancelled`.

For `retryable`, the execution host conditionally clears the current Lease, returns the Job to `queued`, records `last_error_code`, and sets `available_at` using bounded exponential backoff with jitter. The same Run, Configuration Set, root deadline, and Checkpoints remain authoritative.

Timeout, rate limit, Provider unavailable, and classified transient network failures are retryable within the Role Profile. Provider-not-configured, invalid Prompt Contract, unknown Role, policy loss, and invalid durable state are terminal. If PostgreSQL is unavailable and active requeue cannot commit, Lease expiry remains the final recovery path.

Role Attempt count and the root deadline both cap retries. Exhausting a child Attempt policy creates a failed Delegation Outcome and wakes the parent. No error path waits on Lease expiry by design when a disposition can be committed.

## 14. Run Checkpoints And Research Recovery

The shared immutable Checkpoint envelope contains:

- Run and sequence identity;
- stable logical identity key;
- Role and step type;
- optional step ordinal;
- payload version, canonical payload, and SHA-256.

Existing Controller proposal, result, and final-draft Checkpoints migrate into this envelope. Role code owns payload decoding and validation; the envelope is not a workflow DSL.

Research step types are:

- `research.query-plan`;
- `research.web-search-result` per query ordinal.

Recovery loads the accepted plan, skips every accepted search ordinal, and executes the first missing query. Each Provider-neutral result is checkpointed immediately. Candidate merge, deduplication, ordering, and URL validation are deterministic and may be recomputed.

Web Search is bounded at-least-once. If a process fails after receiving a Provider response but before its Checkpoint commits, that query may repeat unless the Provider offers an idempotency contract. Sprint 9 does not claim exactly-once external calls and does not create a child Agent or Job per query. Searches remain sequential.

## 15. Deadline, Cancellation, And Authority

The Leader admission deadline is the root end-to-end deadline. A child inherits the same absolute instant; waiting never pauses or extends it. Research adds Role-local limits for planning, one-to-three searches, result bytes, and Attempts.

Cancelling the Leader atomically cancels the active child Run and Job, terminalizes the delegation as cancelled, fails or cancels the searching Discovery Session, and prevents later parent wake or publication. Members cannot independently operate the hidden child.

When the shared root deadline expires, the runtime terminalizes the depth-one tree without requeueing an already expired parent. Every Checkpoint, terminal outcome, and publication boundary revalidates current Lease Token, Run state, root deadline, and authorization. Provider cancellation remains best effort; late results cannot create product effects.

## 16. Observability

Every Model Call records Role, phase, logical call identity, Prompt identity/version/hash, Prompt Contract, requested and selected model, Provider, latency, Provider-reported usage, validation outcome, and retry links.

Delegation Trace records parent and child links, relationship and ordinal, requested and effective route, bounded intent and policy reasons, waiting, terminal handoff, parent wake, consumption, Attempt Disposition, backoff, and recovery exhaustion.

Standard Trace excludes chain of thought, credentials, raw Provider envelopes, complete Prompt content, and unrestricted user or Source content. Existing encrypted, audited, retention-bounded Replay remains the content-level debugging path.

## 17. Migration

1. Add Prompt, Prompt Set, Configuration Set, Role Profile, generic delegation, Research outcome, and retry scheduling structures.
2. Move the six production prompts into Markdown without changing their accepted behavior, then register a legacy Prompt and Configuration Set representing the current deployment.
3. Backfill existing Runs and Model Call identities.
4. Migrate current Research lifecycle rows into generic delegation and Research outcome rows; migrate expanded query state into Research Checkpoints when present.
5. Move application reads and writes to the new authority and verify counts, uniqueness, terminal states, and non-terminal recoverability.
6. Remove the Research-specific lifecycle table, old relationship column, and obsolete Run-level prompt alias after verification. Do not retain long-term dual writes.

Migration follows schema expand, data backfill, application cutover, and contract cleanup. Unrelated Sprint 10 documents and existing worktree changes remain untouched.

## 18. Deterministic Acceptance

Sprint 9 deliberately defers a formal Agent Eval. Completion still requires deterministic evidence:

- Prompt front matter, schema, canonical hash, duplicate registration, and immutable-conflict tests;
- typed Router and Planner contract tests for wrong tool, mixed text, invalid enum, missing field, empty queries, duplicates, and over-limit queries;
- deterministic intent/policy tests proving ordinary Chat and unauthorized Members cannot reach Web Search;
- Registry tests for duplicate Role, unknown compatibility, illegal relationship, depth, active-child, and publication authority;
- integration tests for atomic child creation, waiting, success, failure, consumption, cancellation, root deadline, and authority loss;
- crash injection before and after plan, each search result, terminal handoff, parent requeue, and notification;
- retry/backoff, retry exhaustion, Lease fallback, stale Attempt fencing, and scan recovery after a lost notification;
- migration tests for legacy Runs and Research outcomes;
- readiness tests for unsupported active Configuration Sets;
- complete Sprint 1 through Sprint 8 regression.

Default automated tests use fake models and Web Search Providers. A credentialed smoke test remains opt-in and cannot be the acceptance authority.

## 19. Delivery Sequence

1. Freeze terminology, ADR, Prompt inventory, typed contracts, and legacy configuration identity.
2. Add Prompt Catalog registration and move all six prompts without behavioral changes.
3. Pin Agent Configuration Sets and resolve Role-specific Profiles.
4. Split Execution Host, Role Registry, Leader Role Executor, and Research Role Executor.
5. Migrate to generic delegation lifecycle and Research-owned outcome.
6. Add uniform Attempt Disposition, `available_at`, and bounded retry classification.
7. Generalize Checkpoint envelope and add Research plan/result recovery.
8. Complete cancellation, deadline, terminal wake-up, Trace, migration, and readiness tests.
9. Run the full regression and verify every non-goal remains absent.

## 20. Re-evaluation Triggers

Reconsider an external runtime or a broader Kernel only when measured product requirements include at least one of:

- multiple new registered Roles with independent ownership;
- depth greater than one;
- parallel child fan-out and join;
- durable human approval interrupts across long periods;
- cross-service workflows that outgrow PostgreSQL Job ownership;
- a need for managed multi-Agent deployment or framework-native graph tooling.

Until then, extending the bounded Role Registry and Kernel is preferred to introducing a second execution authority.
