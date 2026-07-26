# Nano Notebook Sprint 9 PRD

## Document Status

- **Sprint:** Sprint 9
- **Status:** Implemented and verified
- **Date:** 2026-07-26
- **Theme:** Versioned Agent Configuration and Delegation Hardening
- **Delivery boundary:** Infrastructure governance and reliability for the existing Leader-to-Research path; no new Member-facing capability, Agent Role, or general workflow framework

## 1. Decision

Sprint 9 converts the Sprint 8 Leader-to-Research implementation from a Research-specific executor into a bounded, versioned Agent execution architecture.

The Sprint delivers:

1. a Git-owned immutable Prompt Catalog for all production model instructions;
2. typed Router and Research Planner contracts;
3. immutable Agent Prompt Sets, Agent Configuration Sets, and Role-specific Profiles;
4. an application-registered Role Registry;
5. a bounded Delegation Kernel for depth-one parent-child lifecycle;
6. Role-owned executors, Checkpoints, and outcomes;
7. uniform retry, cancellation, deadline, migration, and Trace behavior.

The Sprint retains the current product contract: an authorized explicit Source Discovery request may create one private Research child Run; the child creates candidates but cannot import a Source or publish a Chat answer; the Leader remains the only Member-facing Run.

## 2. Source Documents

This PRD derives from:

- `docs/superpowers/specs/2026-07-26-versioned-agent-configuration-and-delegation-hardening-design.md`
- `docs/technical-architecture/CONTEXT.md`
- `docs/technical-architecture/ARCHITECTURE.md`
- `docs/technical-architecture/adr/0012-bound-the-durable-runtime-to-product-jobs.md`
- `docs/technical-architecture/adr/0027-schedule-jobs-with-leases-and-workload-classes.md`
- `docs/technical-architecture/adr/0029-separate-operational-telemetry-from-durable-agent-traces.md`
- `docs/technical-architecture/adr/0030-cancel-cooperatively-and-publish-through-a-barrier.md`
- `docs/technical-architecture/adr/0040-delegate-source-discovery-through-child-runs.md`
- `docs/technical-architecture/adr/0041-build-a-bounded-agent-delegation-kernel.md`
- `docs/sprint/SPRINT-8-PRD.md`

ADR 0041 supersedes ADR 0040 for generic lifecycle storage, copied parent configuration, shared root deadline, recovery policy, and executor architecture. ADR 0040 remains authoritative for Source Discovery product behavior, privacy, authorization, and publication boundaries.

Sprint 10 Studio Outputs remain a separate scope and must not add speculative Roles or workflow requirements to Sprint 9.

## 3. Sprint Goal

Deliver these dependent slices in order:

1. **Prompt governance:** move every production instruction into immutable Markdown and register exact content and contracts by hash.
2. **Typed model control:** replace Router token parsing and Planner line parsing with required function-call contracts that fail closed.
3. **Pinned Role configuration:** admit a Leader Run against one immutable Configuration Set and materialize a separate Profile for its Research child.
4. **Bounded execution architecture:** dispatch through a fixed Role Registry and split Leader behavior, Research behavior, Job execution, and delegation lifecycle.
5. **Recoverable Research:** checkpoint the accepted plan and each accepted search result, then resume only missing work.
6. **Uniform reliability:** explicitly commit waiting, retry, terminal, cancellation, deadline, and parent wake-up outcomes.
7. **Migration and evidence:** migrate existing durable state, preserve compatibility, enrich Trace identity, and pass deterministic recovery and regression tests.

## 4. Success Criteria

Sprint 9 is complete only when all of the following are true:

1. All six production Prompt instructions reside in versioned Markdown files outside Go source.
2. Each Prompt Version has a stable Prompt identity, numeric version, output-contract identity, canonical SHA-256, and immutable durable registration.
3. Re-registering the same identity and hash is idempotent; the same identity with different content or contract fails startup; a registered Prompt Version is never overwritten or deleted.
4. Runtime Prompt resolution never uses `latest`, mutable aliases, remote content, or Member-supplied instructions.
5. The five Agent prompts form an immutable Agent Prompt Set; image normalization pins its Prompt Version through Source Processing Configuration.
6. Every Model Call Trace identifies its exact Prompt Version, Prompt Contract, requested/selected model, and Provider-reported usage.
7. Leader admission pins one Agent Configuration Set and one Leader Role Profile.
8. A Research child carries the same Configuration Set identity but resolves a Research-specific model, Prompt binding, tool/provider allowlist, local budget, and executor compatibility.
9. Infrastructure Attempts and continuation reuse pinned configuration; a user-created new Run may adopt a newer deployment-selected Set.
10. Worker readiness rejects an unsupported Configuration Set referenced by a non-terminal Run without terminalizing that Run.
11. Leader Router accepts only a required `select_leader_route` function call with valid route and bounded reason enums.
12. Research Planner accepts only a required `submit_research_queries` function call with one to three valid queries.
13. Invalid, missing, mixed, or inconsistent model contracts fail closed and never grant Web access or child creation.
14. Router context may resolve bounded recent references, but only the current Member turn can explicitly request new Source Discovery.
15. Evidence insufficiency, prior Research activity, or an ordinary request for current information does not independently create a child.
16. Durable route state distinguishes requested route, effective route, intent reason, and deterministic policy reason.
17. Delegation Policy rechecks membership role, Notebook authority, root Run validity, deadline, Provider availability, registered relationship, and active-child limits.
18. Application code registers only `leader` and `research`; duplicate or unknown Role and executor compatibility registrations fail safely.
19. Only `leader -> research` with ordinal zero is legal; delegation depth is one; Sprint 9 permits one total Research child per Leader and therefore at most one active child; a child cannot delegate.
20. Only the Leader is Member-visible and can publish an Assistant Message.
21. The Agent Worker execution host owns claim, Lease, heartbeat, disposition, and retry scheduling without interpreting Research outcomes.
22. Leader and Research use distinct Role Executors; neither writes generic delegation or Job lifecycle state directly.
23. The Delegation Kernel owns child creation, parent waiting, cancellation propagation, terminal handoff, parent wake-up, and outcome consumption.
24. `agent_run_delegations` becomes the sole parent-child lifecycle authority; Research Discovery outcomes remain in a Role-owned relation.
25. Consuming an outcome writes `consumed_at` without erasing its `succeeded`, `failed`, or `cancelled` terminal status.
26. Existing Research delegation data migrates without losing parent, child, terminal, error, or Discovery Session identity.
27. Expanded Research queries persist as a query-plan Checkpoint rather than generic delegation state.
28. Each Web Search result persists as an immutable Checkpoint keyed by stable query ordinal.
29. Recovery reuses the accepted query plan and completed result ordinals and executes only the first missing query.
30. Research makes at most three sequential Web Search calls per logical Run and retains at most ten normalized candidates.
31. A crash between Provider response and Checkpoint may repeat only the uncertain query; the runtime makes no exactly-once claim.
32. Every leased execution ends with a typed `completed`, `waiting`, `retryable`, `terminal`, or `abandoned` disposition when PostgreSQL is available; abandoned cause distinguishes Lease loss from cancellation.
33. Retryable failures deliberately clear the Lease, set bounded backoff through `available_at`, and retain the same Run, deadline, configuration, and Checkpoints.
34. Timeout, rate limit, unavailable, and classified transient network errors retry only within Role Profile and root deadline limits.
35. Non-retryable contract, configuration, Role, policy, authorization, and durable-state errors terminalize with bounded safe codes.
36. Lease expiry remains a recovery fallback for an execution host that cannot commit its disposition; it is not the normal retry mechanism.
37. Research success and failure both terminalize the child, update the delegation, requeue an active parent, and notify the Agent Job channel in one transaction.
38. Polling recovers a committed parent requeue even if notification delivery is lost.
39. Research failure continues to fail the Leader rather than silently degrading into ordinary Chat.
40. The child shares the Leader's absolute root deadline; creation and parent waiting never reset or pause elapsed time.
41. Leader cancellation atomically cancels the active child and Discovery work and prevents every late result from waking or publishing the parent.
42. Root deadline expiry terminalizes the depth-one tree without requeueing an expired parent.
43. Lease Token, active Run state, root deadline, and current authorization fence every Checkpoint, outcome, and publication commit.
44. Parent-child Trace links, Prompt identity, routes, policy reasons, Checkpoints, dispositions, backoff, wake-up, and consumption are reconstructable in Durable Agent Trace.
45. Standard Trace and logs contain no credentials, raw Provider envelope, full Prompt content, unrestricted Source content, or chain of thought.
46. Legacy active Runs remain recoverable through expand/activate/retire deployment compatibility.
47. The current and immediately previous compatible Configuration Sets remain executable until no non-terminal Run references the older Set.
48. Deterministic tests inject failure around every nondeterministic and terminal boundary and prove recovery, fencing, and idempotency.
49. Default automated tests require no live Model or Brave credential; credentialed smoke tests remain opt-in.
50. Sprint 1 through Sprint 8 authentication, Source, Chat, Agent, RAG, sharing, cancellation, observability, and deletion behavior remains green.
51. No new Member-facing API, SSE field, UI control, Agent Role, same-turn Web answer, or Studio Output ships in Sprint 9.

## 5. Canonical Terms

- **Prompt Catalog:** immutable application-owned production instructions identified by exact Prompt Version.
- **Prompt Contract:** typed model-facing input or output shape paired with a Prompt Version and validated by application code.
- **Agent Prompt Set:** immutable binding of the five Agent Prompt purposes to exact versions and contracts.
- **Agent Configuration Set:** immutable compatibility set fixed at Leader admission.
- **Agent Role Profile:** one Role's model, Prompt, tools, local limits, and executor compatibility inside a Configuration Set.
- **Agent Role:** application-registered execution responsibility, not a Prompt persona or plugin.
- **Role Executor:** code implementation of one Role's behavior; it does not own generic Job or delegation lifecycle.
- **Delegation Kernel:** bounded runtime mechanism for parent-child creation, waiting, cancellation, terminal handoff, and consumption.
- **Agent Delegation:** durable relationship between one parent Run and one child Run.
- **Delegation Outcome:** terminal child handoff interpreted by the parent Role.
- **Attempt Disposition:** explicit execution-host result controlling success, waiting, retry, terminalization, or lost authority.
- **Run Checkpoint:** immutable accepted nondeterministic boundary used for recovery.

## 6. Prompt Inventory And Contracts

The Prompt Catalog contains:

| Prompt identity | Owner | Output contract |
| --- | --- | --- |
| `agent.leader-router` | Leader Role | required `select_leader_route` |
| `agent.research-planner` | Research Role | required `submit_research_queries` |
| `agent.chat-composer-bare` | Agent Controller | text Final Draft |
| `agent.chat-composer-grounded` | Agent Controller | grounded text Final Draft |
| `agent.query-contextualizer` | Agent Controller | required existing `search_evidence` proposal |
| `source-processing.image-evidence-normalizer` | Source Processing | existing bounded JSON validation |

Markdown and adjacent schema files are the Git source of truth and are embedded into the application. PostgreSQL retains immutable copies so an old pinned Run is not dependent on the current working tree or a mutable deployment default.

## 7. Leader Route Contract

The Router returns exactly one required function call:

```json
{
  "route": "continue_chat | delegate_research",
  "reason_code": "ordinary_conversation | existing_source_work | ambiguous_discovery_intent | external_information_without_discovery_request | explicit_source_discovery"
}
```

`delegate_research` is valid only with `explicit_source_discovery`. The model does not receive or create execution authority.

Application policy converts requested intent into an effective route. The policy outcome is durable and independently testable. An unauthorized explicit request is distinguishable from an ordinary Chat decision in restricted Trace even when no child is created.

## 8. Research Planner Contract

The Planner returns exactly one required function call:

```json
{
  "queries": ["one to three bounded queries"]
}
```

Application code validates count, content, size, normalization, and duplication before accepting the plan Checkpoint. Provider selection, Web-call count, Candidate limit, URL validation, and authorization remain server-owned.

## 9. Agent Runtime Architecture

```text
Agent Worker Execution Host
  -> Role Registry
       -> Leader Role Executor
       -> Research Role Executor
  -> Prompt Runtime
  -> Delegation Kernel
  -> existing Agent Controller and domain services
```

The Kernel is a bounded product runtime, not a graph engine. The data model retains a child ordinal for possible later sequential children, but current policy permits only ordinal zero and one Research child in total.

## 10. Configuration And Deployment

Agent Configuration Sets are immutable records registered by the application. Admission selects one exact deployment-configured Set rather than a mutable `latest` alias. Prompt-only revisions can reuse an executor compatibility version; incompatible typed payload or behavior changes require a new version supported by the running Worker.

Deployment follows:

```text
expand support -> activate admission -> drain active references -> retire support
```

Sprint 9 supports controlled two-phase rollout or a maintenance window. It does not implement a scheduler that routes Jobs among arbitrary Worker capability versions.

## 11. Generic Delegation Lifecycle

The generic lifecycle states are:

```text
waiting -> succeeded
waiting -> failed
waiting -> cancelled
```

Parent consumption is a timestamp on a terminal outcome. It is not another terminal state.

Creation atomically produces the child Run, child Job, delegation, searching Discovery Session, Trace link, and parent waiting transition. Terminal handoff atomically records Role outcome, child terminal state, delegation terminal state, parent requeue, and notification.

## 12. Recovery And Retry

Research checkpoints:

1. accepted query plan;
2. accepted Web Search result for each ordinal.

Deterministic merge and validation may rerun. Searches remain sequential. External Web Search is bounded at-least-once, while product state and publication remain fenced and idempotent.

Retryable dispositions use explicit backoff. Role limits and the shared absolute deadline cap cost and duration. A failed child always creates a durable outcome that the parent can consume; no failure path can strand a waiting parent until a notification happens to arrive.

## 13. Cancellation, Deadline, And Security

- Only an authenticated Editor or Owner with current Source-maintenance authority can create the Research relationship.
- Role Registry permission never replaces Notebook Capability checks or PostgreSQL RLS.
- Parent cancellation owns child cancellation; the hidden child has no Member command surface.
- The shared root deadline bounds queue delay, Leader execution, child execution, parent waiting, and retry.
- Stale Leases and late Provider results cannot write Checkpoints, outcomes, Candidates, or Messages.
- Prompt registration and Configuration registration require trusted application startup or migration authority, not request-path authority.

## 14. Observability

Durable Trace must distinguish:

- what Prompt and contract was used;
- what route the model requested;
- what effective route policy permitted;
- what Role and executor version advanced the Run;
- which Checkpoints were accepted or reused;
- why an Attempt waited, retried, failed, or lost authority;
- when a child terminal outcome woke and was consumed by its parent.

These are control facts, not model reasoning. Chain of thought is never requested or retained.

## 15. Migration And Compatibility

Migration uses expand, backfill, cutover, and contract cleanup:

1. create new registry, profile, generic delegation, Research outcome, and retry scheduling structures;
2. register a legacy Prompt and Configuration Set matching current behavior;
3. backfill Run references;
4. migrate Research lifecycle and accepted expanded queries;
5. cut application reads and writes to the new authority;
6. verify recoverability and relational counts;
7. remove obsolete Research-specific lifecycle and duplicate relationship authority.

Long-term dual write is forbidden because it would make recovery and cancellation ambiguous.

## 16. Deterministic Acceptance Matrix

Required suites include:

- Prompt parsing, canonical hashing, immutable conflict, and registration idempotency;
- typed Router and Planner normalization and rejection;
- route intent versus policy authorization;
- Role registration and illegal topology;
- child creation and parent waiting transactionality;
- query-plan and per-search Checkpoint recovery;
- success, failure, cancellation, deadline, and authority-loss terminal handoff;
- transient retry, backoff, exhaustion, and Lease-expiry fallback;
- lost notification scan recovery;
- stale Attempt and late Provider-result fencing;
- legacy configuration and delegation migration;
- unsupported active-version readiness rejection;
- complete prior-Sprint regression.

The Sprint does not add a formal offline Agent Eval, live-model promotion gate, management UI, or online experiment system.

## 17. Delivery Plan

### Step 1: Freeze Contracts And Legacy Identity

- finalize Prompt IDs, versions, schemas, Role names, reason enums, relationship key, and legacy Configuration Set;
- add ADR 0041 and synchronize domain terminology.

### Step 2: Prompt Catalog And Typed Decisions

- add Markdown catalog and embedded registration;
- move all six production prompts;
- implement required Router and Planner function contracts;
- record per-call Prompt identity.

### Step 3: Configuration And Role Execution

- add immutable Prompt Sets, Configuration Sets, and Role Profiles;
- pin them at admission and child creation;
- add Role Registry and split Leader and Research executors.

### Step 4: Delegation Kernel And Data Migration

- create generic delegation lifecycle and Research-owned outcome;
- migrate current rows and remove duplicate lifecycle authority;
- centralize creation, waiting, cancellation, terminal handoff, wake-up, and consumption.

### Step 5: Checkpoint And Retry Hardening

- generalize the Checkpoint envelope;
- checkpoint Research plan and per-query results;
- add Attempt Disposition, `available_at`, backoff, and safe classification.

### Step 6: Compatibility, Trace, And Acceptance

- add expand/activate/retire readiness checks;
- complete Trace identities and retry links;
- run crash, cancellation, deadline, migration, and full regression acceptance.

## 18. Risks And Mitigations

| Risk | Mitigation |
| --- | --- |
| The Kernel becomes a hidden workflow framework | Enforce depth one, one active child, fixed relationships, no graph API, no plugins, and no child delegation |
| Database migration leaves two lifecycle authorities | Cut over once, verify, then remove Research-specific lifecycle and duplicate parent link |
| Role Profiles become mutable runtime configuration | Immutable IDs and hashes; deployment-selected exact Set; no `latest` or online editor |
| Retry multiplies Provider cost | Role-specific retry classification, Attempt cap, shared deadline, accepted Checkpoint reuse, sequential searches |
| Prompt changes silently alter old Runs | Admission pinning, immutable durable content, per-call identity, active-version readiness checks |
| Typed output is mistaken for authorization | Model output remains intent/data; deterministic policy and RLS authorize every effect |
| External framework migration is deferred too long | Re-evaluate on depth, fan-out, HITL, cross-service workflow, or managed multi-Agent triggers |

## 19. Explicitly Deferred

- formal Agent Eval and Prompt promotion gate;
- online Prompt management and A/B experimentation;
- new Agent Roles and all Sprint 10 Output roles;
- parallel or nested delegation;
- Agent memory shared across Runs;
- model-selected Providers, tools, budgets, or permissions;
- cost ledger and organization quota allocation;
- external Agent or workflow runtime adoption.

## 20. Definition Of Done

Sprint 9 is done when the approved design and ADR are reflected in implementation, schema, migration, Trace, and deterministic tests; every Success Criterion has evidence; all prior-Sprint tests pass; the existing Source Discovery journey remains compatible; and no deferred capability appears through code, API, database, configuration, or UI.

Implementation evidence is recorded in `docs/sprint/SPRINT-9-ACCEPTANCE.md`.
