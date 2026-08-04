# Prompt-driven Leader decision loop (retire the forced first-search phase)

## Context

The Leader's per-turn decision loop (`Controller.Execute`, `internal/agent/controller.go`) is not actually uniform today, even after `delegate.research.source-discovery.v1` was wired up as a real, freely-choosable MCP tool (`docs/superpowers/specs/2026-08-03-unify-agent-delegation-as-tool-design.md`). Two separate, code-level gates still hard-constrain the first step of every grounded turn:

1. **`controller.go:159-176`** — when `execution.PromptVersion == GroundedPromptVersion`, `groundedRequiredAction(prefix)` forces decision 1 to be `search_evidence` and nothing else, via `acceptContextualizedSearch` (`controller.go:269-369`). This runs an isolated model call using `agent.query-contextualizer.v1.md`, whose prompt literally says "Call search_evidence exactly once ... Do not answer the user, summarize Sources, or call any other Action." The model has no choice at this step, and `delegate.research.source-discovery.v1` is not even shown to it.
2. **`GroundingService.prepare()`** (`internal/agent/grounding.go:108-139`) — independent of the loop, this runs when the model tries to submit a Final draft. If `SelectedSourceCount > 0` and `parseResearchState(prefix)` finds no `search_evidence` action was ever executed (`grounding.go:191-198`, checks `action.Name == "search_evidence"` specifically — a `delegate.research...` call does not count), it returns `ErrGroundingIncomplete` and the whole run fails with `grounding_failed`. This is a second, structurally separate backstop that would still hard-fail a turn even if gate 1 were removed and the model simply chose, of its own judgment, not to call `search_evidence`.

Real trace evidence (`d603ba9a-c634-4c07-aaae-800caa02b12c`, `9efba170-4720-47c7-a1f9-319e6bb77af1`) shows the downstream symptom: decision 1 always calls `search_evidence` against the Run's already-pinned Sources; at decision 2 (a genuinely free choice — `delegate.research.source-discovery.v1` is present and unfiltered, confirmed by the absence of any `nano.tool.filtered` trace event), the model consistently chose to answer "not found" rather than delegate, even when the user explicitly asked it to search for more material. The active prompt at decision 2, `agent.chat-composer-grounded.v1.md`, never mentions `delegate.research...` exists and explicitly frames a failed/empty search as "not a reason to refuse... answer normally" — there is no textual signal nudging the model toward escalation.

Separately, `agent.query-contextualizer.v1.md` was deliberately introduced (`docs/superpowers/specs/2026-07-23-query-contextualization-design.md`) to fix a real, previously-observed bug: a long prior grounded answer can anchor a small model (qwen-plus) strongly enough that an unrelated new message (e.g. `你好`) reuses the prior turn's search query and repeats the prior answer. The fix was isolating the query-authoring call to see only the current Message plus a small bounded history window, with a dedicated instruction that history may only resolve references, never replace a self-contained current topic. This isolation is a real behavioral guard, not incidental structure — removing it is a named, accepted risk in this design (see below), not an oversight.

## Goal

Collapse the Leader's decision loop into one uniform mechanism from decision 1 onward: every decision (1, 2, 3, ...) offers the model the same full set of currently-available catalog tools (`search_evidence`, `delegate.research.source-discovery.v1`, and whatever is added later) and lets the model choose freely — including the choice to finalize without calling anything. All behavioral sequencing guidance (look at local Sources before answering unless clearly unnecessary, do not batch `search_evidence` and `delegate.research...` in the same decision, escalate to `delegate.research...` when local evidence is insufficient and the user needs new/external material, preserve current-message authority against anchoring) lives entirely in one prompt (a rewritten `agent.chat-composer-grounded`), not in Go code. No dedicated router/classifier, no required-first-action gate, no post-hoc grounding-completeness gate, no code-level fallback if the model doesn't follow the prompt's guidance.

## Non-goals

- Not changing `delegate.research.source-discovery.v1`'s `Available()` gating (membership role, provider availability, relationship registration, `ExistingChildCount == 0`) — that is a resource/authorization limit, not a decision-sequencing hardcode, and stays as-is.
- Not changing `search_evidence`'s retrieval backend, ranking, or its own `Available()` gate (`SelectedSourceCount > 0`).
- Not touching the no-Sources path (`chat-composer-bare` / `BarePromptVersion`) — it never had a forced-search gate and is unaffected.
- Not adding any new classifier, heuristic pre-filter, or soft prompt-hint fallback layer — consistent with the routing-precision risk already accepted in the delegation-as-tool design.
- Not changing `[source:<source_id>]` citation marker syntax or `normalizeSourceMarkers`'s parsing.

## Design

### 1. Remove the forced-first-action gate in the Controller loop

Delete the `execution.PromptVersion == GroundedPromptVersion` branch in `Controller.Execute` (`controller.go:159-177`) that calls `groundedRequiredAction`/`acceptContextualizedSearch`. Decision 1 becomes an ordinary iteration of the same loop body already used for decision 2+: `actionDefinitions()` returns the full available tool list (gated only by each tool's own `Available()`), `BuildDecisionRequest` builds the request, the model decides to propose one or more actions or finalize.

Delete the now-dead supporting code: `acceptContextualizedSearch` (`controller.go:269-369`), the `QueryContextRuntime` interface (`controller.go:45-47`), `PostgresRuntime.BuildQueryContextRequest` (`query_contextualization.go`), `preserveCurrentSearchQuery` (`query_contextualization.go:180`), `TraceSpanQueryContextualization` and its span-start/end calls, and `studioRuntime.BuildQueryContextRequest` (`studio_executor.go:160`, the eval-harness twin).

`GroundedPromptVersion` itself (`context_builder.go:12`) is **not** deleted — it still distinguishes which composer prompt/tool set binds (`prompt_bindings.go:30`, `context_builder.go:23,31,78`) for Sources-selected vs. no-Sources runs. Only its use as a trigger for a *required first action* goes away.

### 2. Remove the post-hoc grounding-completeness gate

`GroundingService.prepare()` (`grounding.go:108-139`) currently fails the run (`ErrGroundingIncomplete`) whenever `SelectedSourceCount > 0` and no `search_evidence` action was ever executed. This must change in lockstep with (1): once decision 1 can legitimately end in a Final draft with zero `search_evidence` calls (the model judged it unnecessary, or it used `delegate.research...` instead), this gate would otherwise hard-fail exactly the turns this design intends to allow.

Change: when `research.performed` is false (`grounding.go:137-139`), do not return `ErrGroundingIncomplete`. Instead follow the same path already used for `selectedCount == 0` (`grounding.go:117-131`): normalize/strip any invented `[source:<id>]` markers from the draft (the model has no retrieved evidence to legitimately cite), persist a `source_less`-equivalent outcome, and let the answer through. This treats "Sources were selected but the model chose not to search them" the same as "no Sources were selected" for citation-validity purposes — both mean there is no retrieved evidence available to attribute text to.

### 3. Merge `query-contextualizer` into `chat-composer-grounded`, bump to v2

Delete `agent.query-contextualizer.v1.md` and its `chat.leader.v1.json` catalog reference (the `query_contextualizer` prompt binding). Replace `agent.chat-composer-grounded.v1.md` with a new `agent.chat-composer-grounded.v2.md` (prompt content is treated as immutable once published/replayed, per existing convention — bump the version rather than editing v1 in place) covering, as one cohesive set of instructions:

1. **Unknown-content disclosure**: the model does not know what the Run's selected Sources contain. Unless the current request is clearly unrelated to any selected Source, it should call `search_evidence` before answering rather than guessing or answering blind.
2. **Query-authoring rules** (absorbed from the old contextualizer): when calling `search_evidence`, `query`/`purpose` must be concise, self-contained, and preserve the current Message's key terms/entities/qualifiers/language; recent history may only resolve pronouns/ellipsis/omitted subjects in the current Message, never replace a self-contained current topic with an older one. This is the direct textual mitigation for the anchoring risk described above — enforced by instruction, not input isolation.
3. **Look before escalating**: do not propose `search_evidence` and `delegate.research.source-discovery.v1` in the same decision/batch. Local retrieval results should be seen before deciding whether escalation is warranted — batching both blindly defeats the purpose of evaluating real evidence first, and `delegate.research...` can only be called once per Run, so it should not be spent before checking whether it's actually needed.
4. **Escalation criteria**: when local retrieval is empty, irrelevant, or clearly stale, *and* either the user explicitly asked to search for new/more material or the request needs information genuinely outside the selected Sources, call `delegate.research.source-discovery.v1` (its `request` field describing what to find) instead of concluding "not found."
5. **Existing grounding/citation rules preserved as-is**: `[source:<source_id>]` placement rules, "a failed or unhelpful search is not a reason to refuse when the request is answerable without Sources," no invented Sources/quotations/markers, no exposed chain-of-thought.

## Accepted risks (explicitly chosen by the user during brainstorming)

- **Grounding is no longer structurally guaranteed.** A grounded turn can now finalize having never called `search_evidence` or `delegate.research...`, if the model judges (per the prompt) that neither is needed. Previously this was impossible by construction (`groundedRequiredAction` + `GroundingService.prepare()`'s hard gate). No code-level fallback compensates for a model that judges incorrectly.
- **Routing/sequencing precision risk extends to decision 1**, not just decision 2 (already accepted in the delegation-as-tool design). A general tool-choice model deciding *whether to search at all* is likely less reliable than the old code-enforced "always search first."
- **Query-anchoring regression risk.** The isolated-input mitigation for the qwen-plus anchoring bug (`2026-07-23-query-contextualization-design.md`) is removed; the merged prompt's "current message is authoritative" instruction (§2 above) is the only mitigation, and it is a soft, model-followed instruction rather than a structural one (the model now sees full conversation history, including its own prior long answers, at the moment it authors a new query). If this regresses in practice, it should be diagnosed from real usage/traces rather than pre-emptively re-adding isolation.

## Testing plan

- `controller_test.go`: replace forced-first-action test cases with cases proving decision 1 offers the full tool list (`search_evidence`, `delegate.research...`) and the model's choice — including choosing to finalize directly — is honored.
- `grounding_test.go` (or equivalent): a case where `research.performed == false` and `SelectedSourceCount > 0` now succeeds with a normalized, marker-stripped draft instead of returning `ErrGroundingIncomplete`.
- Delete tests exercising `acceptContextualizedSearch`, `groundedRequiredAction`, `preserveCurrentSearchQuery`, and the old contextualizer's fallback/history-bounding behavior (`query_contextualization_test.go`), since the code under test is deleted.
- Add a regression-style integration test for the anchoring scenario from the original contextualization design (`docs/superpowers/specs/2026-07-23-query-contextualization-design.md`'s table): a Source-backed answer, then `你好`, then `你有哪些工具` — assert the new turns address themselves rather than repeating the old topic. This test exists to catch anchoring regressions from real model behavior, not to enforce them structurally.
- `mcp_tool_plane_test.go`: confirm `delegate.research.source-discovery.v1` and `search_evidence` are both present, unfiltered, at decision 1 when their respective `Available()` gates pass.
- Prompt catalog tests: confirm `chat.leader.v1.json` no longer references a `query_contextualizer` prompt purpose, and the new `agent.chat-composer-grounded.v2` is correctly bound.

## Rollout

Single branch, sequential commits, no feature flag — consistent with how this codebase already treats agent/prompt catalog changes as immutable, fail-closed configuration rather than runtime toggles. Land in this order: (1) merged v2 prompt + catalog rewiring, (2) `GroundingService.prepare()` gate relaxation, (3) `Controller.Execute` loop unification + dead-code deletion — in this order so the prompt and grounding-acceptance behavior are correct before the loop stops enforcing the old sequencing, keeping the system in a coherent state at each intermediate commit.
