# Agent Status TODO Recitation Implementation Plan

**Goal:** Add a crash-safe, checkpoint-backed TODO planner and inject its current state as the final temporary user message in every Leader decision request.

**Acceptance:** A real PostgreSQL integration test must execute `rewrite_todo_list` and `update_todo_status`, persist their Action Results, rebuild a later Leader request, and observe the updated TODO list plus tool-call counts at the request tail.

## 1. TODO domain and checkpoint projection

- Write failing table tests for rewrite/update validation, stable IDs, revisions, one-active enforcement, cancellation of replaced unfinished work, and deterministic timestamps.
- Implement the pure TODO snapshot/state transition package.
- Preserve proposal/result checkpoint timestamps in accepted-prefix projection.
- Project the latest TODO snapshot for one `input_message_id` across retry Runs solely from accepted Proposal/Result pairs.

## 2. Safe errors and tool-call observations

- Write failing compatibility tests for historical Action Result v1 and structured domain-error v2 payloads.
- Add safe error fields: kind, code, message, suggestion, retryable. Keep raw Go stacks, filesystem internals, and secrets out of model-visible state.
- Derive per-tool counts and exact duplicate counts from accepted Proposals using canonical JSON input.

## 3. TODO actions and separate control budget

- Write failing controller/action tests proving TODO mutations are ordered, crash-replay safe, limited to one per proposal, and excluded from business Action/ActionDecision budgets.
- Add `PlanMutationLimit` with a default of 12 and persist it in immutable Run configuration.
- Implement and register `rewrite_todo_list` and `update_todo_status`; Action Result output is the complete new snapshot.

## 4. Agent Status request tail

- Write failing projection tests proving the status block is the final request message, has role `user`, remains outside durable chat/checkpoint projection, and participates in final token budgeting.
- Render bounded current time/timezone, TODO state, accepted tool-call counts/duplicates, and the latest safe structured domain error.
- Finalize the request after all recovery messages so no later message can displace the status tail.

## 5. Immutable prompt/catalog release

- Add immutable tool definitions and prompt versions teaching TODO use for multi-step work, one active item, immediate updates, and the rule that TODO completion is not evidence of task completion.
- Add a new catalog release instead of mutating prior releases.
- Test loader validation, release selection, scheduling, replay safety, and worker registration.

## 6. End-to-end acceptance

- Add a PostgreSQL integration test with a scripted Leader sequence: rewrite -> business action/observation -> update -> final.
- Assert accepted Proposal/Result checkpoints, unchanged business action budget, plan mutation count, exact TODO snapshot, and the final request-tail status after each decision.
- Run focused tests, race tests, `scripts/test-go`, vet/build, diff checks, and create atomic commits.
