# Agent Status and TODO recitation

## Document status

- **Status:** Approved for implementation planning
- **Date:** 2026-08-31
- **Scope:** Leader Chat Agent loop only
- **Primary purpose:** Keep the current task plan in the model's recent attention through a code-generated, ephemeral tail reminder

## Decision

Nano will add a small Agent Status mechanism centered on TODO recitation.

For a complex task, the model owns the semantic plan through two dedicated tools:

- `rewrite_todo_list`
- `update_todo_status`

The runtime owns validation, durable recovery, timestamps, tool-call counts, safe error observations, rendering, and request placement. Before every Leader model decision, the runtime rebuilds the latest status and appends it as the final temporary `user`-role message in the Provider request.

The status is not a Member Message, Agent Step, Action Result, or Compaction input. It is a derived observation that exists only in the current Provider request.

## Motivation

Long Agent loops can make the model over-focus on the latest local action and lose the original objective or remaining work. Repeating the current TODO list at the tail of the request brings the global plan back into the model's recent attention and makes the plan an external working memory.

Nano already preserves accepted Model Decisions, Action calls, Action Results, and Finals through append-only Run checkpoints. This design does not duplicate that trajectory into a second status transcript. It adds only the mutable plan reminder, a current timestamp, and compact tool-call counts.

## Reference implementations

- Manus, [Context Engineering for AI Agents: Lessons from Building Manus](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus): the official source for TODO recitation as attention steering. It describes repeated `todo.md` rewriting but does not publish the two Tool names used here.
- AI Agents in Depth companion code, [System-Hint Agent](https://github.com/bojieli/ai-agent-book/blob/main/chapter2/system-hint/agent.py): the direct source for `rewrite_todo_list`, `update_todo_status`, temporary tail `user` injection, Tool counters, and timestamp examples.
- Pydantic AI Harness, [Planning capability](https://github.com/pydantic/pydantic-ai-harness/blob/main/pydantic_ai_harness/planning/_capability.py): a current implementation of model-owned structured planning with an ephemeral, cache-safe tail reminder.
- LangChain, [TodoListMiddleware](https://github.com/langchain-ai/langchain/blob/master/libs/langchain_v1/langchain/agents/middleware/todo.py): a full-snapshot TODO update design that rejects ambiguous parallel plan writes.

These sources inform the interaction pattern. Nano's checkpoint authority, Retry scope, budgets, and error boundaries remain Nano-specific.

## Goals

1. Let the model create and revise a structured plan for a complex current task.
2. Make the latest plan visible at the end of every subsequent model request.
3. Preserve TODO state across Worker restart, Attempt retry, and user Retry Runs for the same input Message.
4. Keep plan mutation deterministic, validated, bounded, and replay-safe.
5. Show compact per-tool call counts so repeated behavior is visible to the model.
6. Give the model safe, actionable detail for ordinary Tool domain failures.
7. Preserve Nano's append-only Chat lane, complete Agent Step, Compaction, and token-budget invariants.

## Non-goals

- A Member-facing TODO panel or progress UI.
- A general sandbox status display.
- Working directory, operating system, Shell, Python version, or geographic location.
- A second event timeline containing every Message and checkpoint timestamp.
- Raw Go stack traces, internal paths, secrets, or unredacted infrastructure errors in model context.
- Treating TODO completion as independent proof that the user's request was satisfied.
- Replacing Agent Run checkpoints, Trace, Replay, or Compaction.

## Status shape

Every Leader model decision receives a final Status message. Before a plan or Tool call exists, the message contains only its generated timestamp and time zone. Once state exists, it is shaped like:

```text
<agent_status version="1">
Generated at: 2026-08-31T15:24:10+08:00
Time zone: Asia/Shanghai

TODO List:
- [todo_1] completed | Inspect the current implementation | created_at=2026-08-31T15:20:01+08:00 | updated_at=2026-08-31T15:21:04+08:00
- [todo_2] in_progress | Implement the selected change | created_at=2026-08-31T15:20:01+08:00 | updated_at=2026-08-31T15:21:04+08:00
- [todo_3] pending | Run focused verification | created_at=2026-08-31T15:20:01+08:00 | updated_at=2026-08-31T15:20:01+08:00

Tool Calls:
- read_url: 3
- rewrite_todo_list: 1
- identical read_url input repeated: 2
</agent_status>
```

Rendering rules:

1. `Generated at` is observed from an injected runtime Clock and rendered in the Run-pinned `execution.TimeZone` using RFC 3339.
2. Each TODO exposes its durable `created_at` and `updated_at` in the same time zone.
3. Tool names are sorted lexicographically for deterministic serialization.
4. Exact-repeat notices use `Tool name + canonical JSON input`; they appear only when the same pair occurs at least twice.
5. Empty TODO and Tool-call sections are omitted, but the timestamp-only Agent Status is still appended.
6. The opening and closing tags are fixed. The body contains observations, not instructions or a new Member request.

## Request placement and context behavior

The Provider request order is:

```text
System Prompt
Compaction summary, when active
Durable exact Chat-lane suffix
Current Run's accepted closed Agent Steps
Recovery reminder, when retrying an invalid model response
Agent Status as the final user-role message
```

The Agent Status must satisfy these invariants:

- It is rebuilt for every model request.
- It is appended after any invalid-response or overflow-recovery reminder.
- It never enters `chat_messages`.
- It never becomes a `ContextUnit` or checkpoint.
- Compaction never summarizes or retains an old Status instance.
- The rebuilt Status is included in final serialized request token estimation.
- A post-Compaction request regenerates the Status before its final budget check.
- Replay material records the exact final model request, including the Status that the model actually observed.

This preserves the stable historical prefix while keeping mutable state at the request tail.

## TODO ownership and lifecycle

### Scope

A TODO plan is keyed by `input_message_id`, not by Attempt or Run.

- Worker reclaim and Attempt retry see the same plan.
- A user Retry Run for the same input Message sees the most recent successful plan snapshot from earlier Runs.
- A new Member Message starts with no inherited TODO plan.
- A Retry of a completed plan may call `rewrite_todo_list` to create a new execution path.

### Durable authority

Successful TODO Action Result checkpoints are the only durable TODO authority. No mutable TODO table is added in the first implementation.

Each successful TODO Result contains the complete normalized snapshot after the requested mutation. The latest snapshot is selected across Runs for the input Message using existing durable Chat-lane order:

1. Run creation order by `(agent_runs.created_at, agent_runs.id)`.
2. Checkpoint order by `sequence_no` within the Run.
3. Only accepted, successful TODO Action Results participate.

An in-memory list, Provider thread, Agent Status message, or Trace projection is never authoritative.

### Snapshot schema

```text
TodoSnapshotV1
  input_message_id
  revision
  next_ordinal
  items[]

TodoItemV1
  id
  content
  status
  created_at
  updated_at
```

Allowed statuses are:

- `pending`
- `in_progress`
- `completed`
- `cancelled`

Item IDs are deterministic, input-Message-scoped ordinals such as `todo_1`. `next_ordinal` is part of the snapshot. The mutation timestamp is the durable `created_at` of the accepted Action Proposal checkpoint, not a fresh wall-clock reading inside the Tool. Re-executing an uncheckpointed mutation therefore reads the same prior snapshot and Proposal timestamp and produces identical IDs and TODO timestamps.

At most one item may be `in_progress`. The runtime enforces this invariant after every mutation.

## TODO tools

### `rewrite_todo_list`

Purpose: create the initial plan or replace the unfinished portion after the model changes strategy.

Input:

```json
{
  "items": [
    "Inspect the current implementation",
    "Implement the selected change",
    "Run focused verification"
  ]
}
```

Rules:

- Accept 1 to 20 non-empty, trimmed, distinct items.
- Limit each item to 500 Unicode code points.
- Preserve existing `completed` and `cancelled` items.
- Mark replaced `pending` or `in_progress` items `cancelled` at the mutation timestamp.
- Append the new items as `pending` with new stable IDs.
- Return the complete normalized snapshot.

### `update_todo_status`

Purpose: advance, complete, cancel, or reopen existing items without rewriting the plan.

Input:

```json
{
  "revision": 2,
  "updates": [
    {"id": "todo_1", "status": "completed"},
    {"id": "todo_2", "status": "in_progress"}
  ]
}
```

Rules:

- Require the caller's observed snapshot revision to prevent stale overwrites.
- Accept 1 to 20 updates with unique IDs.
- Apply the batch atomically or reject it without a new snapshot.
- Reject unknown IDs, invalid statuses, stale revisions, and results containing multiple `in_progress` items.
- Update `updated_at` only for items whose status changes.
- Return the complete normalized snapshot.

The batch form lets one decision complete the current item and start the next item together.

## Scheduling, replay, and budgets

Both TODO tools are registered as `ordered_sync` and `CrashReplaySafe`.

- At most one TODO mutation may appear in one model proposal.
- A proposal containing a TODO mutation is serialized; it is not executed through `ToolBatchExecutor`.
- The transformation is deterministic over the latest accepted snapshot, canonical input, and durable Proposal checkpoint timestamp.
- If the process crashes before accepting the Result checkpoint, no TODO mutation has durably occurred. Re-execution reads the same prior snapshot and produces the same result.
- A stale revision returns a model-visible domain error instead of silently overwriting newer state.

TODO mutations are control-plane Actions:

- They do not consume the business `ActionLimit`.
- They have a separate pinned `PlanMutationLimit`, initially 12 per Run.
- Plan-only proposals do not consume the business `ActionDecisionLimit`.
- They still consume model calls, deadline, token budget, Action Result byte limits, and `ActionBatchLimit`.
- Reaching `PlanMutationLimit` removes the TODO mutation tools from the next request and exposes no unbounded update path.

The immutable Agent configuration and catalog must pin this limit and the TODO tool availability. Existing released definitions and prompts are not mutated in place.

## Tool-call counts

The runtime derives counts from accepted Action Proposal checkpoints associated with the current `input_message_id`, including earlier Retry Runs for that Message.

Two observations are produced:

1. Total proposals per Tool name.
2. Exact repeats per `Tool name + canonical JSON input`.

Counting proposals rather than Results makes an attempted call visible even when its execution later fails or is interrupted. Attempt retries do not create a second Proposal checkpoint, so infrastructure retry of the same accepted Action does not inflate the model-visible count.

The counter is derived state. It is not persisted separately and never affects idempotency or billing.

## Detailed Tool errors

### Current gap

The current Action Result checkpoint format stores only:

- `action_id`
- `status`
- successful `output`, or
- failed `error_code`

The proposal already stores the complete canonical Tool input, so duplicating arguments in the Result would waste tokens and create two copies that could diverge.

### Model-visible error schema

New domain-error Result checkpoints use payload version 2:

```json
{
  "action_id": "decision:2/action:0",
  "status": "domain_error",
  "error": {
    "kind": "domain",
    "code": "read_url_failed",
    "message": "The requested page could not be read.",
    "suggestion": "Verify the URL or use another accessible source.",
    "retryable": true
  }
}
```

`message` and `suggestion` are stable, bounded, code-owned text. They come from an Action error catalog or an explicitly safe Action-produced detail; they are not raw `error.Error()` strings.

Validation requires:

- a safe `kind` and `code`;
- a non-empty bounded `message`;
- an optional bounded `suggestion`;
- an explicit `retryable` boolean;
- no successful output on a domain error.

Historical payload-version-1 Result checkpoints remain valid and project their existing `error_code`. They are not rewritten.

### Error boundary

Model-visible domain errors cover failures where the model can choose a different next action, such as an unavailable URL, invalid query, rate limit, missing Source, or TODO revision conflict.

Harness failures remain Harness failures:

- lease or authority loss;
- deadline expiration or cancellation;
- checkpoint invariant failure;
- invalid materialized Tool definitions;
- MCP transport corruption;
- recording failure.

These failures continue through Attempt disposition and recovery. They are not converted into ordinary domain-error checkpoints merely to keep the loop alive.

Raw Go stack traces, internal file paths, secrets, authorization material, and unredacted Provider messages never enter model context. Operational causes, when retained for diagnosis, belong only in encrypted Replay or Trace under their redaction and access policies.

## Prompt contract

The next immutable Leader prompt versions instruct the model to:

- use `rewrite_todo_list` for tasks with three or more distinct steps;
- keep exactly one item `in_progress` when active work is underway;
- call `update_todo_status` immediately after a step's state changes;
- revise the unfinished plan when new evidence changes the approach;
- use Tool-call counts to notice repetition and change strategy;
- read detailed Tool errors before selecting an alternative;
- provide the requested Final answer after the work is complete.

The prompt also states that TODO state is a planning aid, not proof of completion. The Controller does not reject a Final solely because a TODO remains pending. The Status will still expose the unfinished item to the model before that Final decision.

## Component boundaries

### TODO projector

Reads successful TODO Result checkpoints for one input Message and folds them into the latest validated snapshot. It has no Provider or rendering logic.

### TODO Actions

Validate model inputs, request the current snapshot, compute one normalized successor snapshot, and return it. They do not write chat history or an independent TODO store.

### Status observer

Loads the latest TODO snapshot and derives Tool-call counts from accepted proposals. It receives the runtime Clock and pinned time zone and returns a typed `AgentStatusObservation`.

### Status renderer

Deterministically serializes one observation into the tagged text form. It performs no database reads and makes no policy decisions.

### Request finalizer

Appends recovery guidance when needed, appends Agent Status last, and performs the final whole-request token estimate. Compaction operates on durable Context Units before this final ephemeral tail is added.

### Error catalog

Maps stable Action error codes to safe message, suggestion, and retryability fields. Actions may select codes but cannot leak arbitrary internal errors through the catalog boundary.

## Data flow

```text
Model proposes rewrite_todo_list / update_todo_status
  -> Proposal checkpoint accepts canonical input
  -> ordered TODO Action loads latest input-Message snapshot
  -> code validates and computes successor snapshot
  -> Result checkpoint accepts the complete snapshot
  -> next decision projects durable Chat lane
  -> Status observer loads latest snapshot and proposal counts
  -> Status renderer adds current time and TODO timestamps
  -> request finalizer appends temporary role=user Status
  -> whole serialized request is budgeted and sent to Provider
```

## Failure behavior

- No prior snapshot: `rewrite_todo_list` creates revision 1; `update_todo_status` returns `todo_list_missing`.
- Stale revision: return `todo_revision_conflict` with guidance to use the current status snapshot.
- Unknown TODO ID: return `todo_item_not_found`.
- Multiple `in_progress` items: return `todo_multiple_in_progress`.
- Plan mutation limit reached: hide both TODO mutation tools; do not manufacture a successful update.
- Status projection failure: fail the pending decision with `context_failed`; never send a stale or partially reconstructed plan.
- Invalid time zone: fail configuration/admission as today; do not silently label local time incorrectly.
- Status pushes the request over budget: run normal Compaction when eligible, regenerate Status, and recheck. If it still cannot fit, return the existing stable context-budget failure.
- Historical v1 domain error: preserve and project the accepted code without inventing a historical raw cause.

## Security and privacy

Agent Status is runtime-authored but uses model-authored TODO content. Rendering must treat TODO content as data, bound its length, and place it only inside the fixed status envelope.

Tool inputs remain in their accepted Proposal checkpoint and continue through existing Replay redaction. Error descriptions must never echo arbitrary input values, response bodies, credentials, headers, tokens, local paths, or stack traces.

The Status observer uses the same authorized Chat/Run scope as Context Projection. A Retry may read only Runs already selected into the current input Message's authorized Chat lane.

## Observability

Record compact attributes for:

- whether Agent Status was injected;
- rendered Status byte and estimated token counts;
- TODO snapshot revision and item counts by status;
- plan mutation accepted/rejected and stable rejection code;
- maximum exact Tool-input repeat count.

Do not record TODO content as ordinary low-cardinality metric labels.

## Testing

### Unit tests

- Initial rewrite produces deterministic IDs, revision, and timestamps.
- Rewriting preserves terminal items and cancels replaced unfinished items.
- Atomic status update completes one item and starts the next.
- Stale revisions, duplicate IDs, unknown IDs, and multiple active items fail without a successor snapshot.
- Re-execution from the same snapshot produces the same successor.
- Status rendering is deterministic and places timestamps in the pinned time zone.
- Tool counts include proposals across Retry Runs but not Attempt re-execution.
- Canonically equivalent JSON inputs count as exact repeats.
- Error payload v2 validates safe detail and rejects raw/oversized detail.
- Historical error payload v1 remains loadable.

### Controller and context tests

- Agent Status is the final `user` message on first and later decisions.
- Invalid-response recovery guidance appears before Agent Status.
- The first decision receives a timestamp-only Status before a TODO or Tool call exists.
- Status is not included in `ContextUnit` or Compaction input.
- Final token telemetry includes the rendered Status.
- TODO mutation Actions do not spend business Action budget and stop at `PlanMutationLimit`.
- A batch containing a TODO mutation remains ordered.

### PostgreSQL integration tests

- Worker restart recovers the latest TODO snapshot from checkpoints.
- Retry Run for the same input Message inherits the latest accepted snapshot.
- A new input Message does not inherit the previous plan.
- Crash before Result acceptance leaves no TODO mutation; replay accepts exactly one deterministic Result.
- Cross-Run Tool counts follow durable Chat-lane order and authorization.
- Compaction followed by a decision regenerates one current Status and does not persist it.

## Acceptance criteria

1. A complex Leader task can create and update a four-state TODO plan through the two dedicated tools.
2. The latest plan survives restart and Retry for the same input Message using accepted checkpoints as authority.
3. Every subsequent model decision receives one current timestamp, TODO timestamps, TODO snapshot, and compact Tool-call counts in the final temporary `user` message.
4. Status content never appears in Chat history, checkpoints, or Compaction input, while its tokens are included in the final request budget.
5. Ordinary Tool domain failures provide a safe structured message and suggestion; Harness failures retain their existing recovery ownership.
6. TODO mutations are ordered, replay-safe, separately bounded, and do not reduce the business Action budget.
7. Historical checkpoint payloads remain readable and all focused unit and PostgreSQL integration tests pass.

## Deferred work

- Member-facing real-time TODO UI backed by derived checkpoint state.
- Dependencies, subtasks, blocked state, assignees, or multi-Agent plan ownership.
- Independent verifier or Goal authority that can block Final publication.
- Configurable Status sections beyond TODO, time, and Tool counts.
- Offline evaluation of iteration count and task-completion quality against a labeled multi-step suite.
