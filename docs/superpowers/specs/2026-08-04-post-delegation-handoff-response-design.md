# Post-delegation handoff response shape

## Context

`docs/superpowers/specs/2026-08-04-prompt-driven-leader-decision-loop-design.md` made `delegate.research.source-discovery.v1` a genuinely free choice at any decision, and live testing today confirmed the model now correctly escalates to it when local Sources are insufficient (trace `1245d3f5-8322-4e4a-b9be-5e83d3baff8f`: local `search_evidence` found generic-but-insufficient evidence, model delegated, delegation succeeded).

The gap: after delegation succeeds, the model's Final answer for that same turn still tries to compose a substantive, best-effort answer — restating what little local evidence exists, then appending raw links pulled from the delegation's web-search candidates as if they were vetted material. This is unwanted for two reasons: (1) delegation's candidates are unvetted web-search results, not admitted Sources — presenting them as answer content blurs that distinction; (2) the notebook's Source Discovery panel (`web/src/components/workspace/source-discovery.tsx`) already auto-opens and shows these exact candidates for the user to review/select the moment the triggering run's `discovery_session_id` is set (confirmed via `notebook-workspace.tsx`'s `requestedDiscoverySessionID` derivation and the panel's own SSE subscription) — so restating them inline in chat text is redundant with a UI surface that already exists and already works.

## Goal

When a decision proposes `delegate.research.source-discovery.v1` and it succeeds, that turn's Final answer should: state plainly that the currently selected Sources don't cover the request, note that candidate sources have been found and are available for review in the notebook's Source panel, and stop there — not draft a substantive answer from the delegation's raw findings. The expected flow becomes two user-initiated turns: this turn hands off to source review; the user reviews/imports candidates from the left panel, then asks again (or follows up) in a second message, at which point ordinary `search_evidence` over the now-enriched Source set produces the real answer.

## Design

Add one paragraph to `agent.chat-composer-grounded` (bump to v3) covering this handoff shape, alongside the existing search/escalation guidance from v2. No code changes — this is prompt-only, consistent with the two prior decisions in this area (both times the user chose to trust the model's own compliance over a code-level backstop, matching this design's own "Accepted risks" precedent).

## Non-goals

- No code-level check verifying the Final draft's shape after a successful delegation (explicit user choice, matching precedent).
- No automatic second-turn triggering when the user selects/imports a candidate source — the second turn is always user-initiated (explicit user choice).
- Not touching the Source Discovery panel, its SSE wiring, or `source_discovery_sessions`/`candidates` — confirmed already working correctly for this use case.

## Testing

Prompt-content change only; covered by the existing `context_builder_test.go` phrase-presence assertions for `agent.chat-composer-grounded` (extend with an assertion for the new handoff phrase) and by live trace inspection, matching how v2's rollout was verified.
