# Research threshold-triggered layered compaction notes

## Document status

- **Status:** Implemented and locally accepted in `research.executor@10`
- **Date:** 2026-08-31; revised 2026-09-01
- **Scope:** Research Mode context projection and compression direction
- **Relationship:** Complements permanent Research PDF Source persistence and
  the implemented `inspect_source` Tool

This document records the implemented first-release policy. Its immutable
schemas, thresholds, prompts, migration, and acceptance tests are pinned by
`research.executor@10` / `nano.default@17`; later tuning requires new versions.

## Pinned first-release policy

The first immutable rollout is `research.executor@10` and leaves v9 and older
projection behavior replayable. It uses the already-resolved Deep Research v4
model-context budget: a 180,000-token compaction trigger, 32,768-token exact
recent suffix target, 16,384 output-token reservation, and 8,192 estimation
safety tokens.

Layer 2 selects the oldest contiguous prefix of complete, not-yet-archived
Agent Steps before the exact suffix, with at most 24 Steps in one batch. Its
single temperature-zero model call returns `nano.research-capsules@1`: one
strict JSON Capsule per selected decision, in the same order, with a maximum
8 KiB canonical JSON per Capsule. Capsules are stored append-only in
`research_archival_capsules` with exact checkpoint ranges and a hash of the
covered checkpoint payloads. Old eager `research_step_capsules` and
`research_rollups` remain readable for pinned older Releases but receive no
v10 writes.

Layer 3 selects the oldest contiguous range already covered by archival
Capsules and not by a prior Task Memory. Its single temperature-zero model
call returns one `nano.research-task-memory@1`, capped at 32 KiB canonical
JSON, and persists it append-only in `research_task_memories` with the covered
checkpoint range and source hash.

Each layer is accepted without checking its estimated token gain. Layer 3
executes only when the accepted Layer 2 request still exceeds the safe input
budget. One request preparation may call each layer at most once. Invalid
model output, range mismatch, persistence conflict, reconstruction failure, or
a post-layer request that still exceeds the safe budget stops the compactor and returns
`context_budget_exhausted`; it never retries in an internal loop and never
clears the preexisting projection.

For Layer 3, the final complete candidate must also be at or below the safe
input budget before its immutable Task Memory is inserted. A candidate that
has sufficient relative gain but remains over budget records
`safe_budget_exceeded` and leaves no Task Memory that a later request could
silently activate.

`research_compaction_failures` records only bounded reason code, range,
attempt number, token counts, and policy identity. It contains no Tool bodies,
prompts, Capsules, TODO, or Agent Status. The Controller's pinned provider
overflow retry remains the outer hard limit; it cannot mutate an accepted
compaction artifact.

## Problem

Research currently materializes a Capsule for each completed Agent Step and
periodically combines Capsules into a Rollup. Periodic Rollup generation spends
tokens and invalidates cached history even when the request is comfortably
below the model limit. Its paragraph selection can also become prefix-biased
and URL-heavy, while the accumulated Rollup itself keeps growing.

The replacement follows the layered production strategy described in sections
2.7.4 and 2.7.5 of *AI Agents in Depth*: externalize large bodies, summarize
only when needed, preserve semantic integrity and current-task relevance, and
use full compaction only after cheaper representations are insufficient.

## Decisions

1. Do not create or rewrite a Research Rollup every fixed number of decisions.
2. Estimate the fully serialized request and trigger compression only when it
   approaches the pinned model-context threshold.
3. Keep append-only Action proposal, Action result, and Final Draft checkpoints
   unchanged and authoritative. Compression changes only model projection.
4. Preserve a recent exact suffix of complete Agent Steps. Never put a cut
   between a Tool Call and its corresponding Result.
5. Use three operational layers: externalized bounded Tool results, archival
   compaction, and deep task-memory compaction.
6. Rebuild the existing checkpoint-backed TODO and ephemeral Agent Status on
   every request. TODO mutation Steps remain in checkpoints but are removed
   from the v10 model trajectory and compactor inputs; neither TODO nor Agent
   Status is copied into compression summaries.

## Layer 1: externalized bounded Tool results

This layer is always active and does not call a model. Each Tool persists its
complete durable artifact in the appropriate authority and returns a bounded,
deterministic model projection:

- permanent PDFs and Source Maps remain in object storage;
- Evidence bodies remain in authoritative Evidence Units;
- Research workspace documents remain in object storage; and
- accepted Action Results contain durable identities, hashes, lifecycle
  outcomes, and bounded previews rather than unbounded bodies.

The Tool owns its initial projection contract because it knows how to persist
and validate its result. The compactor does not switch on Tool names. For the
same accepted Action, artifact version, and projection-policy version,
reconstruction must produce the same preview.

## Layer 2: archival compaction

When the request reaches the trigger, select an old prefix of complete Agent
Steps while retaining the configured recent exact suffix. One task-aware model
call receives that prefix together with the accepted Research Plan and current
research phase. It returns a validated array containing one structured Capsule
per covered Step.

Layer 2 combines Tool Result clearing and per-Step archival summaries as one
safe operation. It commits in this order:

1. generate and strictly validate every Capsule in the selected batch;
2. rebuild an in-memory candidate projection using those Capsules;
3. replace covered Tool Result bodies with generic compacted Result shells;
4. retain each Tool Call name, Action ID, and complete accepted input;
5. retain Result status, stable error code, Action ID, and durable reference;
6. estimate the complete rebuilt request for telemetry and safe-budget checks;
7. persist immutable Capsules with their checkpoint ranges and source hash;
   and
8. expose the already-validated candidate only after persistence commits.

Candidate reconstruction before persistence is validation, not activation. It
keeps invalid model output from creating dead artifacts, while the active
projection still observes the required persistence-before-use boundary.

The generic Result shell is conceptually:

```json
{
  "action_id": "decision:12/action:0",
  "status": "succeeded",
  "content_state": "compacted",
  "result_ref": "accepted-checkpoint-identity",
  "rehydratable": true
}
```

For structured domain failures, the shell derives and retains only the stable
error code. Human-readable error messages and remediation prose leave the
active projection with the Result body.

The complete Tool input remains visible at this layer because it captures what
the model attempted, including the query, Source identity, file path, and
requested operation. Tool schemas must therefore enforce bounded inputs; the
compactor never silently truncates accepted parameters.

Layer 2 itself has no per-Tool compression logic. Tool-specific externalization
belongs to Layer 1. If generation, validation, persistence, reconstruction, or
token acceptance fails, no Result body is cleared from the active projection.

## Per-Step Capsule contract

A Capsule records how its Step changed current Research state rather than
reproducing everything the Tool returned. It should preserve, when applicable:

- decision and checkpoint range;
- research objective advanced;
- conclusions with material entities, dates, numbers, negation, uncertainty,
  scope, and other qualifications intact;
- decisions, constraints, and changed assumptions;
- Source imports and durable Source/Evidence identities;
- contradictions or evidence gaps;
- workspace artifacts created or revised;
- verification completed or still required; and
- follow-up state not already represented by the current TODO.

It should normally omit raw PDF/HTML excerpts, long Tool outputs, worker logs,
Brave result descriptions, repeated URL lists, workspace prose, transient
progress narration, and stale TODO or Agent Status copies.

Each Capsule remains a separate append-only record, like a `git log` entry. It
does not continually rewrite all earlier work into one narrative Rollup.

## Layer 3: deep task-memory compaction

After Layer 2, rebuild and estimate the complete request. If it remains above
the target, select the oldest archived range and make one task-aware model call
over its Capsules and retained Tool-call trajectory. The output is one
structured Research Task Memory covering that exact checkpoint range.

The Task Memory retains:

- research goals and phase;
- confirmed conclusions with semantic qualifiers intact;
- key decisions, constraints, and reasons;
- contradictions and unresolved evidence gaps;
- failed paths whose reasons matter for avoiding repetition;
- verification and report-section state;
- durable Source, Evidence, and workspace references; and
- the covered checkpoint range and source-content hash.

After validation and persistence, that old range's Tool calls, complete input
parameters, compacted Result shells, and per-Step Capsules leave the active
model projection. They remain recoverable from checkpoints and immutable
artifact authorities.

Layer 3 combines task-memory aggregation and last-resort full clearing. It is
not a periodically rewritten Rollup: it is threshold-only, range-bounded,
append-only, and provenance-bearing. A later deep compaction may cover another
old range, but an accepted Task Memory is never edited in place.

## Trigger, acceptance, and circuit breaker

Compression is demand-driven:

```text
estimated complete request tokens >= compaction trigger
```

The trigger comes from the pinned model-context policy and reserves output room
plus estimation uncertainty. The Harness then executes:

```text
Layer 2 archival compaction
    -> persist
    -> rebuild complete request
    -> estimate again
    -> under target: stop
    -> still over target: Layer 3 deep compaction
        -> persist
        -> rebuild complete request
        -> estimate again
```

Each model-driven layer must reduce the projected request by a configured
minimum before it is accepted. If deep compaction still cannot reach the safe
budget, or repeated attempts fail, a bounded circuit breaker stops retrying and
fails closed. The runtime must not enter an unbounded summarize-and-retry loop.

## Context projection

The intended Research request is conceptually:

```text
system instructions
available Tool definitions
accepted Research Plan and currently loaded Skill guidance
old Research Task Memories, if any
archived Capsules plus retained Tool Calls and compacted Result shells
recent exact Agent Steps
current user and run context
fresh Agent Status, including the current TODO snapshot
```

This is a logical description rather than a Provider-specific wire-order
commitment. Accounting covers the complete serialized request, including
system instructions, Tool schemas, Skill bodies, plan, projected history,
evidence previews, and Agent Status.

## Checkpoints versus model projection

Neither Layer 2 nor Layer 3 updates or deletes accepted
`agent_run_checkpoints`. Checkpoints remain the immutable execution authority.
Compaction appends separate immutable projection artifacts that identify their
covered checkpoint range and source hash.

Layer 2 may therefore hide old Result bodies, and Layer 3 may hide complete old
Tool calls and parameters from the model, without losing the original accepted
trajectory. Recovery, audit, replay validation, and rehydration continue to use
checkpoint and artifact authorities rather than lossy summaries.

## Research TODO skill boundary

Research retains the existing TODO list and Agent Status. A Research workflow
Skill may teach the model to derive concise TODO items from the accepted plan
and update them at meaningful phase boundaries.

TODO remains a control surface rather than memory or evidence storage. Skill
guidance cannot enforce Source readiness, citation eligibility, final-report
barriers, context limits, or compaction; those remain Harness-owned rules.

## Deferred tuning and evaluation

- evaluation corpus for semantic retention, provenance, context size, cache
  behavior, and final report quality.

## Non-goals

- Changing Agent Status or TODO schema.
- Treating compaction as long-term memory or Evidence storage.
- Deleting accepted checkpoints, permanent Sources, or workspace artifacts.
- Adding retrieval-level novelty or duplicate suppression.
