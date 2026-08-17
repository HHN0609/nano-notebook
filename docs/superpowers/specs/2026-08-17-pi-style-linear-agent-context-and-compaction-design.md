# Pi-style linear Agent context and compaction

## Document status

- **Status:** Implemented
- **Date:** 2026-08-17; implementation completed 2026-08-18
- **Scope:** Requirements, design, runtime contract, and acceptance criteria
- **Reference behavior:** Pi durable AgentHarness and coding-agent compaction

## Decision

Nano will adopt Pi's context-management semantics for Chat-backed Leader Agents:

> A Chat owns one linear, append-only causal history. Every User Message and every accepted Agent Step across its Agent Runs remains available to later model calls until an append-only Compaction replaces an older prefix in the model projection with a summary and retains an exact recent suffix.

An Agent Step is one Model Decision together with the complete Action batch it requests and every corresponding Action Result. Agent Run, Agent Attempt, Worker lease, user Retry, and process restart are execution or recovery boundaries; none is a model-context isolation boundary.

Nano deliberately does **not** adopt Pi's session tree. There is no context fork, `parentId`, active branch, branch summary, or branch-selection UI. Each Chat has exactly one causal lane.

## Source documents

- `docs/technical-architecture/CONTEXT.md`
- `docs/technical-architecture/ARCHITECTURE.md`
- `docs/technical-architecture/adr/0043-separate-chat-runs-from-agent-runs.md`
- `docs/sprint/SPRINT-3-PRD.md`
- `internal/agent/context_builder.go`
- `internal/agent/controller.go`
- `internal/agent/checkpoint_prefix.go`
- `internal/agent/postgres_runtime.go`
- `internal/agentcatalog/model-policies/agent.chat-default.v1.json`
- `internal/promptcatalog/prompts/agent.chat-composer-bare.v2.md`
- `internal/promptcatalog/prompts/agent.chat-composer-grounded.v3.md`
- Pi compaction: `https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/compaction.md`
- Pi durable harness: `https://github.com/earendil-works/pi/blob/main/packages/agent/docs/harness-v2.md`
- Qwen Plus model limits: `https://help.aliyun.com/zh/model-studio/qwen-plus`

If this document conflicts with the Sprint 3 statement that Retry inherits no Checkpoint, the precise interpretation here wins: a Retry still receives no Checkpoint rows from the source Run and resumes none of its execution state, but the source Run's closed Agent Steps remain part of the Chat's later model context.

## Current behavior and gap

`PostgresRuntime.Build` currently loads at most 20 `chat_messages` through the current input Message. Those rows contain only Member-visible User Messages and published Assistant Messages. `BuildDecisionRequest` then appends completed Proposal/Result Checkpoints from only the current Agent Run.

This produces valid current-Run Tool history, because the Controller resumes an incomplete Action batch before asking the model for another decision. It does not produce Pi-style Chat history:

- prior Agent Run Tool calls and Tool Results are absent;
- the fixed 20-Message window is a message-count truncation rather than a token budget;
- there is no durable Compaction boundary or summary;
- a later User Message sees prior published finals, but not the Tool work that caused them;
- context overflow has no Pi-style compact-and-retry path.

## Goal

Build a deterministic, Provider-neutral Agent Context Projection that:

1. reconstructs the current Chat's complete linear causal history across Agent Runs;
2. preserves complete Tool-call/Tool-result batches;
3. respects the active model's real token budget;
4. summarizes old history only through an append-only Chat-scoped Compaction;
5. remains recoverable from PostgreSQL without an in-memory or Provider-owned thread;
6. exposes enough durable state and telemetry to explain recovery and token decisions in an interview or incident review.

## Non-goals

- Session trees, branching, forks, alternate leaves, or active-branch selection.
- Deleting, rewriting, or moving old Messages or Run Checkpoints after Compaction.
- Treating Compaction as long-term semantic memory, RAG indexing, or user-editable notes.
- Persisting a Provider conversation/thread as runtime authority.
- Preserving hidden reasoning or Provider-specific internal state.
- Optimizing answer quality beyond preserving Pi's context semantics.
- Adding a context-management UI in the first implementation.
- Changing Tool scheduling, Tool parallelism, or Agent delegation policy except where required to close a partial Action batch.

## Canonical context sequence

For a Chat containing two user inputs, where the first answer requires two model/tool rounds, the logical lane is:

```text
User Message 1
  Assistant Action Proposal: [A, B]
  Action Result A
  Action Result B
  Assistant Action Proposal: [C]
  Action Result C
  Assistant Final 1
User Message 2
  Assistant Action Proposal: [D]
  Action Result D
  Assistant Final 2
```

The model call after `Action Result D` receives the full sequence through that result unless Compaction has replaced an older prefix. Agent Run boundaries are not inserted as Provider messages.

When a User Message is retried, the User Message appears once. Runs created for that input contribute their accepted, closed Agent Steps in creation order. A published Assistant final is projected once even though nano durably represents it as both a `final_draft` Checkpoint and a `chat_messages` publication.

## Requirements

### R1. One Chat-scoped linear lane

1. A Chat owns exactly one causal context lane.
2. Every admitted User Message is ordered by the Chat's durable Message order.
3. Every Chat Run for that input Message is ordered deterministically by durable creation identity.
4. Every Run Checkpoint is ordered by `(run_id, sequence_no)` within its owning Run and mapped into the Chat lane at that Run's position.
5. Infrastructure Attempts do not add duplicate context entries.
6. No request may select or omit history through a branch identifier because branching does not exist.

The ordering contract must not depend on goroutine completion order, wall-clock precision alone, or a process-local slice.

### R2. Durable sources and projection mapping

The projection uses existing durable authorities rather than copying a second raw transcript:

- User content comes from authorized `chat_messages` rows.
- Assistant Action calls and Action Results come from accepted `agent_run_checkpoints`.
- Assistant final content comes from the accepted `final_draft` Checkpoint when available.
- The published Assistant `chat_messages` row is the Member-visible publication of that same final and must not be projected a second time.
- A bounded compatibility path may project a legacy Assistant Message only when its historical Run has no equivalent Final Checkpoint.
- Current Agent configuration, System Prompt, Tool definitions, Model Policy, and selected evidence bindings remain pinned request configuration; they are not reconstructed from old Provider payloads.

Every projection must be reproducible from PostgreSQL after process loss. Provider response IDs, in-memory message arrays, and gateway thread state are never authoritative.

### R3. Complete Agent Steps and Tool batches

1. The model must never receive an Assistant Tool Call without exactly one following model-visible Action Result for every call in that proposal batch.
2. Successful, failed, rejected, timed-out, and interrupted Actions all produce explicit Results.
3. Parallel Actions may commit Results in completion order, but projection emits Results in the original proposal order, matching Pi's stable Tool-result ordering.
4. An incomplete current-Run batch is resumed or reconciled before another model call.
5. A Run may become terminal with a partial batch only after the unresolved calls have durable closing Results.
6. A Final-only decision is a complete Agent Step with no Actions.

Closing an unresolved call follows the durable-harness order:

1. reuse an already accepted real Result;
2. if the Tool contract explicitly permits crash replay, execute it and accept the Result once;
3. otherwise append a failed Result with a stable interruption error such as `action_interrupted`.

The runtime must never guess that an external side effect did or did not occur. Unknown completion is represented as interruption, not silent success and not blind replay.

### R4. Cross-Run projection

Before every model call, the Context Builder loads the Chat lane through the current Run's latest closed Agent Step. It includes:

- all earlier User Messages;
- accepted closed Steps from their original and Retry Runs;
- prior Assistant finals exactly once;
- the current User Message exactly once;
- every closed Step already accepted in the current Run.

A Retry Run starts with zero inherited Checkpoint rows and its own Decision numbering. This preserves Run authority and idempotent recovery. Nevertheless, its first model call sees the source Run's closed Steps because those Steps belong to the Chat lane's historical projection.

A failed or cancelled Run with no accepted outcomes adds no synthetic conversational answer. If it accepted Tool work, that closed Tool work remains visible so a later Run can reason from what happened.

### R5. Historical Tool Result reconstruction

Tool Results must preserve their original accepted meaning across later Runs:

- ordinary Results use their accepted Checkpoint payload;
- a Result stored as a durable reference, including `search_evidence`, is resolved using the originating Run's pinned Source/Evidence identities and revisions;
- current-Run configuration must not reinterpret a historical Result under a newer evidence revision or Tool schema;
- missing required historical authority fails closed with a stable context-projection error rather than silently dropping the Result;
- Compaction may later summarize that Result, but only when the complete Agent Step lies before the Compaction cut.

Large old Tool Results may be truncated only in the temporary input used to generate a Compaction summary, following Pi's initial per-Result serialization cap of approximately 2,000 characters. The accepted durable Result remains unchanged.

### R6. Token budget

Every model request has a model-aware input budget derived from two separate,
versioned inputs:

- a **Provider Model Capability** records Provider facts: resolved model
  snapshot, context window, maximum input, maximum output, tokenizer identity
  and version, and any invocation-mode-specific limits such as thinking versus
  non-thinking;
- an **Agent Model Context Policy** records nano's product choices for that
  capability: pinned invocation output limit, soft input limit, estimation
  safety margin, exact-suffix target, Compaction-summary output limit, and
  overflow-recovery limit.

The Provider capability is not an Agent Definition budget and the Agent policy
must not claim that the Provider supports more than its capability. A mutable
Provider alias may select a model at admission, but admission must resolve and
pin the concrete capability identity and policy hash. Agent Attempts, Worker
reclaims, process restarts, and model calls inside that Run reuse the same
pinned values. A later Run may select a different model and therefore a
different Context Policy; an existing Run never changes limits mid-execution
because a Provider alias moved.

The effective budgets are:

```text
hardInputBudget = min(
  providerMaxInputTokens,
  contextWindowTokens - pinnedMaxOutputTokens
)

safeInputBudget = hardInputBudget - estimationSafetyTokens

compactionTrigger = min(
  safeInputBudget,
  softInputLimitTokens
)
```

All values must be positive and validated together when the immutable model
configuration is registered and again when it is pinned at admission. The
pinned output limit must not exceed the Provider maximum. The safety margin
protects estimation and Provider-serialization uncertainty; it is distinct
from output reservation. Invalid or missing capability metadata fails
configuration validation instead of falling back to a generic model window.

The first nano profile is deliberately smaller than Pi's generic defaults. As
of this design, `aliyun/qwen-plus` resolves to the Qwen Plus 2025-07-28
capability published with a 1,000,000-token context window, 997,952 maximum
input, and 32,768 maximum output. Nano's pinned Chat Model Policy still limits
one completion to 2,048 tokens. Its initial Context Policy is:

```text
pinnedMaxOutputTokens  = 2048
softInputLimitTokens   = 98304
estimationSafetyTokens = 4096
keepRecentTokens       = 12288
summaryMaxOutputTokens = 2048
overflowRetryLimit     = 1
```

These are Qwen Plus product-policy values, not cross-model defaults. The soft
limit keeps normal Chat requests below the Provider's 128,000-token pricing
tier with room for estimation variance, even though the hard model limit is
much larger. Selecting a different model resolves a different validated
profile.

The current static Chat request baseline is small relative to that policy. A
Qwen3 tokenizer measurement over the exact pinned prompt contents, excluding
front matter, gives approximately:

| Chat mode | System message | available Tool definitions | static total |
| --- | ---: | ---: | ---: |
| Bare | 307 tokens | 223 tokens | 530 tokens |
| Grounded | 661 tokens | 317 tokens | 978 tokens |

These measurements justify reducing Pi's output reserve and retained suffix;
they are design evidence, not accounting constants. Runtime accounting always
tokenizes or estimates the actual serialized request because Prompt versions,
available Tools, Provider envelopes, and evidence vary.

The settings are configurable and validated against the selected model's context window. Invalid settings fail configuration validation; they are not silently reduced per request.

Budget accounting includes every token sent to the Provider:

- System Prompt and other pinned instructions;
- Compaction summary, if present;
- exact historical suffix;
- current User Message and current-Run Steps;
- Tool definitions and structured-output contracts;
- projected evidence and other request attachments.

Message-count limits and rune/byte limits remain defense caps only. They must not be reported or treated as token budgets.

Token estimation follows Pi's hierarchy:

1. anchor on the latest valid Provider-reported input/cache usage for the same context lineage when available;
2. estimate only the entries appended after that anchor;
3. otherwise estimate the whole request with a deterministic tokenizer or Pi-compatible `characters / 4` fallback;
4. record that the value is estimated rather than Provider-observed.

### R7. Automatic Compaction

Automatic Compaction is evaluated at a stable checkpoint before the next model request:

```text
estimatedInputTokens > compactionTrigger
```

Compaction:

1. selects an older prefix of the Chat lane;
2. summarizes that prefix;
3. keeps an exact recent suffix targeting `keepRecentTokens`;
4. appends a durable Chat-scoped Compaction record;
5. leaves every original Message and Run Checkpoint unchanged;
6. makes later projections use the latest valid Compaction summary plus its exact suffix.

Compaction is rolling rather than one-shot. Context appended after an accepted
Compaction remains exact at the tail and counts toward every later request. If
the resulting `latest summary + exact suffix + newly appended context` again
exceeds the active Run's pinned `compactionTrigger`, a successor Compaction
summarizes the previous summary together with the newly old prefix and retains
a new exact suffix. Post-Compaction work is therefore initially uncompressed,
but it has no permanent exemption from a later Compaction.

The summary call uses the pinned Context Policy's
`summaryMaxOutputTokens`. After Compaction, the rebuilt request must fit
`safeInputBudget`; otherwise the pending decision fails with a stable
context-budget error without sending a predictably oversized Provider request.

The summary is a Provider-neutral context entry. The initial Provider adaptation mirrors Pi by emitting it as a User-role message wrapped in `<summary>...</summary>` immediately before the retained exact suffix; it is not a Member-authored Chat Message.

The cut must occur at an Agent Step boundary. It may not begin at an Action Result, split one proposal from any Result, or split sibling Results in a batch. If one recent user turn itself exceeds the retention target, split-turn summary handling may summarize an older portion only while preserving a valid Tool-call/Result sequence.

The summary contract preserves at least:

- user goal and current request state;
- constraints and explicit preferences;
- decisions already made;
- important facts and identifiers learned from Tools;
- completed work and accepted failures;
- unresolved work and next steps.

It must not claim a Tool succeeded when its accepted Result is failed or interrupted. It must not contain hidden reasoning.

### R8. Compaction authority and idempotency

A Compaction is append-only and uniquely identifies:

- its Chat;
- the prior Compaction it supersedes, if any;
- the inclusive summarized-through boundary;
- the exact suffix start boundary;
- summary text and schema version;
- summarizer model and prompt version;
- Provider Capability and Agent Model Context Policy identities and hashes;
- tokenizer identity and version;
- hard, safe, soft-trigger, suffix, summary-output, and before/after token values;
- creation time and deterministic/idempotency identity.

Concurrent Workers must converge on at most one accepted Compaction for the same predecessor and cut boundary. A failed summary call appends no valid boundary and leaves the previous projection usable.

Compaction is projection authority, not Message or Run authority. Deleting a Compaction must never be required to understand what originally happened.

### R9. Overflow recovery

The runtime handles Provider-confirmed context overflow separately from ordinary transient model retry:

1. retain the failed call in operational Trace;
2. exclude the overflow error Assistant payload from the next model-visible context;
3. create one Compaction from the last valid closed checkpoint;
4. retry the interrupted model decision once;
5. fail with a stable context-budget error if Compaction cannot make the request fit or the retry also overflows.

A successful model call that leaves the context above the threshold is not repeated. The runtime compacts at the next checkpoint before making another call.

### R10. Cancellation, crash, and Retry semantics

- Worker or lease retry inside the same Agent Run reloads the same accepted Checkpoint prefix and resumes its first incomplete Action.
- User Retry creates a new Chat Run and Agent Run with no copied Checkpoints.
- Stop/cancellation does not remove accepted Steps.
- Before later context passes a cancelled or crashed partial batch, reconciliation closes every unresolved Tool Call as defined in R3.
- A later User Message remains legal after a failed/cancelled Run and sees all closed historical Steps.
- Retry remains restricted by the product's existing latest-input and concurrency rules; this design changes context visibility, not Retry authorization.

### R11. Provider adaptation and prompt-cache behavior

The Provider-neutral lane is converted immediately before each model call. Adapters may translate roles and Tool syntax but must preserve ordering and call/result identity.

Provider adaptation uses the capability pinned at Run admission. A mutable
Provider alias, a later catalog activation, or a Provider-reported replacement
model may not silently change an active Run's context window, tokenizer, output
limit, or Compaction Policy. If the Provider cannot serve the pinned compatible
model, the call fails through the existing model-availability boundary rather
than rebuilding the Run under different limits.

Within the exact suffix, new context is appended only at the tail. Mid-history mutation is forbidden because it changes meaning and invalidates Provider prompt caches. Compaction is the one deliberate cache-breaking operation.

Provider cache reads/writes and cached-token usage are optimization and telemetry, never context authority.

### R12. Observability

Each model-call Trace records, without copying sensitive content:

- context window, Provider maximum input/output, pinned output limit, and
  estimation safety margin;
- Provider Capability and Agent Model Context Policy identities;
- hard input budget, safe input budget, and soft Compaction trigger;
- estimated versus observed input tokens;
- exact-suffix token estimate;
- Compaction ID and summarized-through boundary, when used;
- Compaction trigger reason: threshold or Provider overflow;
- estimated tokens before and after Compaction;
- overflow recovery attempt number;
- Provider cached input tokens where available.

Each Compaction has a traceable start/end outcome. Trace delivery remains best-effort diagnostic; PostgreSQL Compaction and Checkpoint state remain authority.

### R13. Authorization and retention

Context assembly may read only Messages, Runs, Checkpoints, Evidence, and Compactions authorized through the current Chat's ownership boundary. Existing PostgreSQL RLS and Worker authority rules must extend to Compaction records.

Compaction does not weaken deletion semantics. Deleting a Chat removes its Messages, Chat Runs, Agent Runs, Checkpoints, and Compactions through the same ownership lifecycle.

## Acceptance scenarios

### A1. Multiple Tool rounds survive the next User Message

Given User Message 1 causes two Action proposal batches and a Final, when User Message 2 begins, its first model call contains Message 1, both complete Action batches and Results, Final 1, and Message 2 in order.

### A2. No duplicate published final

Given a successful Run has both a Final Checkpoint and an Assistant `chat_messages` publication, the next model request contains that final exactly once.

### A3. Retry is a new Run but not an empty conversation

Given a failed source Run has closed Tool Steps, when the user retries it, the new Run has zero Checkpoint rows but its first model call includes the source Run's closed Steps.

### A4. Parallel Result ordering is stable

Given Actions B, C, and A commit in that completion order for a proposal `[A, B, C]`, model projection emits the proposal followed by Results A, B, and C.

### A5. Tool failures remain causal history

Given a Tool returns an accepted error, the error Result is included in the next model call and in later Runs until summarized by Compaction.

### A6. Crash does not create an orphan Tool Call

Given a Worker crashes after a proposal and before one non-replay-safe Tool Result is known, recovery appends an interrupted Result before any later model call.

### A7. Threshold Compaction preserves raw authority

Given estimated input tokens exceed the pinned model profile's
`compactionTrigger`, the next model call uses a summary plus an exact suffix,
while queries over the original Messages and Checkpoints return unchanged rows.

### A8. A cut never splits an Agent Step

Given the retention target falls inside a multi-Action batch, the cut moves to a valid Step boundary or uses valid split-turn summary handling; no orphan Action Result is produced.

### A9. Overflow retries once

Given the Provider returns context overflow, the runtime compacts and retries the same pending decision once. A second overflow terminates with a stable error and does not loop.

### A10. Historical evidence stays pinned

Given an old `search_evidence` Result references Evidence Revision X and the Source later advances to Y, a later model projection reconstructs X until the Step is summarized.

### A11. No branching surface exists

No schema, API, context-builder input, or UI introduces a branch ID, parent entry, fork, alternate leaf, or active-branch selector.

### A12. Token telemetry distinguishes estimates

Traces distinguish Provider-observed usage from runtime estimates and show why Compaction occurred without recording raw prompts or Tool outputs.

### A13. Model selection changes policy without changing an active Run

Given two Agent Model Policies select models with different Provider
Capabilities, new Runs derive different hard, safe, soft-trigger, output, and
suffix limits. A Run admitted under either policy retains its pinned capability
and Context Policy across Worker reclaim and process restart, even if a mutable
Provider alias or active Release changes afterward.

## Test requirements

- Pure projection tests over multiple User Messages, Runs, retries, and Compactions.
- Property tests that every projected Assistant Action Call has exactly one later matching Result before the next Assistant/User message.
- Checkpoint-order tests for parallel completion and deterministic proposal-order projection.
- Integration tests for Retry with zero copied Checkpoints and non-empty cross-Run context.
- Crash/lease-loss tests for replay-safe and non-replay-safe incomplete Actions.
- Token-estimator tests covering system prompt, Tool schemas, evidence, summary, and suffix.
- Model-capability and Context-Policy validation tests covering different
  context windows, output limits, thinking modes, tokenizers, and missing or
  contradictory metadata.
- Admission and recovery tests proving the selected capability and policy are
  pinned per Run and are not changed by alias or Release movement.
- Compaction cut-point tests, including a single oversized turn.
- Idempotency tests for concurrent Compaction attempts.
- Overflow compact-and-retry-once tests.
- RLS tests proving Compactions cannot cross Chat ownership.
- Regression tests proving existing Message/Checkpoint history is not rewritten.

## Delivery sequence recommendation

This document does not authorize implementation, but the lowest-risk implementation order is:

1. Introduce a pure Chat-lane projection model and golden tests while preserving current behavior.
2. Load and project historical Run Checkpoints, including pinned historical Tool Results and final de-duplication.
3. Add terminal partial-batch reconciliation.
4. Add the versioned Provider Capability and Agent Model Context Policy
   catalogs, admission-time pinning, model-aware token estimation, and
   telemetry.
5. Add append-only Compaction persistence and summary generation.
6. Add threshold triggering and one-shot overflow recovery.
7. Remove the fixed 20-Message behavior after parity tests pass.

Each phase should keep accepted Messages and Checkpoints readable by the previous runtime until the new projection path is proven.

## Superseded behavior

Once implemented, this design supersedes only these context-related behaviors:

- `PostgresRuntime.Build` limiting conversational history to the latest 20 `chat_messages`;
- `BuildDecisionRequest` appending Checkpoints from only the current Agent Run;
- interpreting Sprint 3's “Retry inherits no Checkpoint” as “prior Run Tool history is invisible.”

It does not supersede Run-local Checkpoint identity, Publication Barrier, Retry authorization, deadlines, leases, action budgets, or Agent Tree delegation semantics.
