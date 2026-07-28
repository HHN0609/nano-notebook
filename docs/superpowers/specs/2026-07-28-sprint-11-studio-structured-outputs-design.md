# Sprint 11 Studio Structured Outputs Design

**Date:** 2026-07-28

**Status:** Approved for implementation. The user explicitly pre-approved the remaining design and written-review gates.

**Product reference:** Current Gemini Notebook / NotebookLM Studio behavior and visual language, adapted to Nano Notebook's existing Source, permission, and configured Agent runtime boundaries.

## 1. Decision

Sprint 11 turns the existing placeholder Studio panel into a durable shared Output workspace and adds four real configured Agents:

- `studio.report@1`
- `studio.flashcards@1`
- `studio.mind-map@1`
- `studio.data-table@1`

The four Definitions bind different prompts and strict result contracts to one code-owned `studio_structured_output` Executor. Each generation is a root configured Agent Run selected by the exact `nano.default@2` release manifest. The Executor uses the existing Controller, Checkpoint ledger, MCP `search_evidence` tool, Agent Job/Attempt runtime, durable Trace, and immutable Agent Result machinery.

A new Studio Output product record owns Notebook sharing, the requesting Member, source snapshot, product status, and the published artifact. It is not a Chat Message and it does not create a fake Leader or child relationship.

Quiz is explicitly excluded. Audio Overview, Video Overview, Slide Deck, and Infographic remain future work because they require media or document rendering pipelines rather than only structured model output.

## 2. Product Reference

The current Google product is now branded Gemini Notebook in Help Center redirects, while many official pages and UI references still use NotebookLM. The following current official sources are the product authority for Sprint 11:

- [Create a notebook / Studio capabilities](https://support.google.com/notebooklm/answer/16206563?hl=en)
- [Use Mind Maps in Gemini Notebook](https://support.google.com/gemininotebook/answer/16212283?hl=en)
- [Generate Flashcards or Quizzes](https://support.google.com/notebooklm/answer/16958963?hl=en-GB)
- [NotebookLM Data Tables announcement](https://blog.google/innovation-and-ai/models-and-research/google-labs/notebooklm-data-tables/)
- [NotebookLM Flashcards announcement](https://blog.google/innovation-and-ai/models-and-research/google-labs/notebooklm-app-quizzes-flashcards/)

The adopted reference behavior is:

1. Studio Outputs are generated from the currently selected Source subset.
2. Generation runs in the background and does not block Sources or Chat.
3. Studio keeps a durable artifact list that can be revisited after navigation or refresh.
4. A compact grid of softly tinted creation cards sits above the artifact list.
5. Selecting an artifact opens a focused type-specific viewer.
6. Reports are documents; a Study Guide is a Report shape, not a separate Agent.
7. Flashcards are interactive front/back cards.
8. Mind Maps are branching topic diagrams with collapsible branches and viewport controls.
9. Data Tables organize facts from multiple Sources into named columns and rows.

Sprint 11 deliberately does not clone Google-specific services such as Docs/Sheets export, Google Drive sync, usage tiers, feedback collection, or auto-generated first-use artifacts.

## 3. Goals And Non-Goals

### 3.1 Goals

- Add four immutable Agent identities through the Sprint 10 configured framework.
- Provide one-click creation from one to fifty currently selected Ready Sources.
- Persist queued, running, completed, failed, and cancelled product state.
- Let every current Notebook Member view completed shared Outputs.
- Let Editors and Owners create and delete Outputs; Viewers cannot mutate them.
- Keep Output content source-grounded and preserve Source references at useful content nodes.
- Restore active and completed Output state after refresh.
- Match the current NotebookLM Studio hierarchy, density, color treatment, artifact list, and focused-viewer behavior on desktop and compact web layouts.
- Retain durable Run recovery, fencing, idempotency, budget, cancellation, Trace, and catalog governance.

### 3.2 Non-goals

- Quiz.
- Audio or Video Overview.
- Slide Deck or Infographic.
- Notes, editable drafts, collaborative editing, comments, or Output version history.
- Custom generation prompts, difficulty, length controls, presets, or AI-suggested report types.
- Flashcard mastery persistence, explanations, CSV export, or cross-device study progress.
- Mind Map node-to-Chat, download, arbitrary graph editing, or free-positioned layout persistence.
- Data Table editing or export to Google Sheets.
- Report export to Google Docs.
- Automatic Output generation when a Source is added.
- A generic workflow DAG, new delegation topology, or remote Agent transport.

## 4. Product Model

### 4.1 Studio Output

A **Studio Output** is shared Notebook product data produced by exactly one configured root Agent Run. It contains:

- stable opaque Output ID;
- Notebook ID;
- optional creator ID retained for attribution;
- one kind: `report`, `flashcards`, `mind_map`, or `data_table`;
- localized generated title;
- one exact root Agent Run ID;
- one optional immutable Agent Result ID after completion;
- selected Source count;
- product status and safe error code;
- type-specific artifact JSON only after successful publication;
- created, started, finished, and updated timestamps.

The Output is immutable after successful publication except for deletion. A failed Output never contains a partial artifact.

### 4.2 Source snapshot

Admission receives an explicit ordered list of one to fifty Source IDs from the browser. The server verifies every Source:

- belongs to the Notebook;
- is `Ready`;
- is readable by the requesting Member;
- has a current published Evidence revision and active Retrieval index.

The existing `agent_run_evidence_set` pins exact Source, Evidence revision, and Retrieval index identities. The Output stores only the count; the Agent Run evidence set remains the immutable generation snapshot. Deleting or losing access to a Source before publication prevents completion. A completed Output remains readable, but a Source reference whose Source later disappears reports unavailable through the existing Source-opening behavior.

### 4.3 Permissions

| Operation | Viewer | Editor | Owner |
| --- | --- | --- | --- |
| List and view completed/active Outputs | Yes | Yes | Yes |
| Generate an Output | No | Yes | Yes |
| Delete an Output | No | Yes | Yes |

The database enforces this independently from HTTP checks. A Member removed during generation loses execution authority; the active Run terminates without publishing an artifact. Output creation never exposes a private Chat or another Member's private Source selection.

## 5. Artifact Contracts

Every artifact is a strict JSON object. Unknown fields, invalid bounds, empty primary content, unpinned Source references, duplicate identifiers, malformed parent relations, and result/Definition mismatches fail closed.

### 5.1 Report

```text
title: 1..200 characters
summary: 1..2,000 characters
sections: 1..16
  id: stable local identifier
  heading: 1..200 characters
  markdown: 1..12,000 characters
  source_ids: 1..8 unique pinned Sources
```

The viewer renders a document title, overview, section headings, Markdown, and compact numbered Source chips. The default generation shape is a concise briefing document. Study Guide, FAQ, and custom report prompts are deferred variants of this Agent.

### 5.2 Flashcards

```text
title: 1..200 characters
cards: 5..24
  id: stable local identifier
  front: 1..500 characters
  back: 1..2,000 characters
  source_ids: 1..8 unique pinned Sources
```

The viewer supports flip, previous, next, shuffle, restart, and a visible card counter. Flip/navigation state is local UI state and is not shared product data.

### 5.3 Mind Map

```text
title: 1..200 characters
nodes: 3..64
  id: stable local identifier
  parent_id: null for exactly one root; otherwise an existing node
  label: 1..200 characters
  detail: 0..1,000 characters
  source_ids: 1..8 unique pinned Sources
```

The graph must be one connected acyclic tree with depth at most four. A flat node contract avoids recursive persistence and makes deterministic validation and rendering straightforward. The viewer provides expand/collapse and bounded zoom controls.

### 5.4 Data Table

```text
title: 1..200 characters
description: 0..2,000 characters
columns: 2..12 unique labels, each 1..120 characters
rows: 1..50
  id: stable local identifier
  cells: exactly one string per column, each 0..2,000 characters
  source_ids: 1..8 unique pinned Sources
```

The viewer renders a sticky-header horizontally scrollable table and Source chips per row. Column choice is model-generated from the selected Sources in Sprint 11.

## 6. Agent Catalog

### 6.1 Exact catalog additions

- Definitions: the four `studio.*@1` identities.
- Input contract: `studio.output-request@1`.
- Result contracts: `studio.report-result@1`, `studio.flashcards-result@1`, `studio.mind-map-result@1`, and `studio.data-table-result@1`.
- Model Policy: `agent.studio-default@1`, pinned to the existing approved generation provider route with deterministic temperature and bounded timeout/output tokens.
- Prompts: `agent.studio-report@1`, `agent.studio-flashcards@1`, `agent.studio-mind-map@1`, and `agent.studio-data-table@1`.
- Release: `nano.default@2`, retaining `chat.leader@1` and adding exact roots `studio_report`, `studio_flashcards`, `studio_mind_map`, and `studio_data_table`.

`nano.default@1` remains embedded and readable for already admitted Runs. It is never mutated in place.

### 6.2 Shared Executor ceiling

`studio_structured_output` permits:

- the four exact prompt purposes;
- the common input and four exact result contracts;
- only `search_evidence`;
- two Model Calls, one Action, batch size one;
- no child Agents;
- no Chat publication;
- bounded context/result bytes and three Attempts.

Definitions narrow this ceiling. A Definition cannot select another Output kind at runtime, accept a Definition identity from the browser or model, publish Chat content, or mutate Sources.

## 7. Execution Flow

```text
Studio action click
  -> POST /notebooks/{id}/studio-outputs
  -> verify Editor/Owner + exact root + Ready Source set
  -> transaction:
       Studio Output(queued)
       + Agent Tree
       + configured root Agent Run
       + pinned Evidence set
       + Agent Job
       + Trace root
       + idempotency record
  -> notify Agent Job

Worker claim
  -> Executor Registry resolves exact studio.* Definition
  -> Studio Structured Output Executor
       -> Controller decision 1 (required search_evidence proposal)
       -> MCP search_evidence over pinned Sources
       -> durable Proposal/Result Checkpoints
       -> Controller decision 2 (strict JSON Final Draft)
       -> validate exact result contract + pinned Source references
       -> transaction:
            immutable Agent Result
            + Studio artifact/status projection
            + Run/Job terminal state
            + terminal Trace
  -> notify Run

Browser
  -> list query while queued/running
  -> SSE for Outputs created by current browser, with query invalidation fallback
  -> durable refetch on reconnect/refresh
```

The first model request must call `search_evidence`; the second has no available Action budget and must return one JSON object as text. The Studio runtime reconstructs the Provider-neutral message sequence from the accepted Checkpoints after recovery. No Provider session or in-memory transcript is authoritative.

## 8. Runtime Boundaries

### 8.1 Reused unchanged

- Agent Definition, Model Policy, Contract, Prompt, and release catalogs.
- Executor Registry and pinned execution verification.
- Agent Tree budgets.
- Agent Job leasing, Attempts, fencing, heartbeat, retry, and recovery exhaustion.
- standard Action Proposal/Result/Final Checkpoints.
- MCP Host/Server and `search_evidence` adapter.
- Evidence-set pinning and retrieval.
- durable Agent Trace and Replay boundaries.
- immutable Agent Result storage.

### 8.2 New Studio runtime adapter

One `StudioRuntime` implements the existing Controller runtime contract for Studio roots. It delegates generic Checkpoint, Lease, Trace, and Action behavior to `PostgresRuntime`, and owns only:

- loading Output product context and the exact prompt/result kind;
- building the two bounded model requests;
- projecting accepted search results back into model context;
- strict artifact decoding and domain validation;
- atomic Agent Result and Studio Output publication;
- product status projection on failure.

This adapter is intentionally separate from Chat message building and Chat publication. It does not add Output branches to the Leader Agent.

### 8.3 Errors and retries

- transient model, MCP, retrieval, or database failures remain retryable Attempt failures under existing classification;
- lease loss abandons the Attempt without publishing;
- invalid model JSON, invalid contract shape, unpinned Source reference, or product invariant failure safely fails the Run with a stable code;
- Source deletion, Membership loss, cancellation, deadline expiry, or Output deletion invalidates publication authority;
- a failed Output remains in Studio with a safe retry-by-regeneration affordance; Sprint 11 does not reuse the same Run;
- deleting an active Output cancels its Run and Job before removing product visibility;
- no partial artifact becomes Member-visible.

## 9. HTTP Contract

### 9.1 List and create

`GET /api/v1/notebooks/{notebook_id}/studio-outputs`

Returns newest-first Output summaries visible to the current Member.

`POST /api/v1/notebooks/{notebook_id}/studio-outputs`

Requires CSRF and `Idempotency-Key`.

```json
{
  "kind": "report",
  "source_ids": ["src_..."] ,
  "locale": "zh"
}
```

Returns `202` with the queued Output summary. Repeating the same key and canonical request returns the same Output; changing the request under the key returns `409`.

### 9.2 Detail and delete

`GET /api/v1/studio-outputs/{output_id}` returns the summary plus the completed artifact, or no artifact while non-terminal/failed.

`DELETE /api/v1/studio-outputs/{output_id}` requires CSRF and Editor/Owner authority. It returns `204`, cancels active execution first, and removes the Output from product visibility.

### 9.3 Events

`GET /api/v1/notebooks/{notebook_id}/studio-outputs/events` emits bounded `studio_outputs` projections and periodic keepalives. Reconnect always begins with the durable current projection; notifications remain advisory.

No API exposes prompts, Provider payloads, Checkpoints, raw Evidence, hidden reasoning, Agent Result storage identifiers beyond what product projection needs, or another Member's private Chat.

## 10. UI Design

### 10.1 Studio panel

The existing right workspace region remains. At desktop widths it follows the current NotebookLM hierarchy:

1. `Studio` header with the existing panel treatment.
2. A two-column, 2-by-2 creation grid in this order: Report, Flashcards, Mind Map, Data Table.
3. Rounded, softly tinted cards with one Material Symbol, a short label, and no chevron-heavy navigation styling.
4. A `Recent` heading and newest-first artifact list.
5. Each artifact row shows its type icon, generated or pending title, Source count, relative creation time, status treatment, and overflow delete action where authorized.
6. Empty Studio uses a restrained icon and one short explanatory line; no Add Note control remains.

Quiz is absent. Unsupported media actions are absent rather than pretending to work.

### 10.2 Generation interaction

- An Editor/Owner click immediately generates the default form of that Output from the currently selected Ready Sources.
- With no selected Ready Source, the card remains visible but clicking shows a localized Source-selection message and creates nothing.
- Viewers see the artifact list but not enabled creation controls.
- A queued item appears optimistically and settles from the server projection.
- Multiple Studio generations may run in the background subject to the existing Worker capacity; Chat remains usable.
- A failed row uses a safe localized message and can be deleted, then generated again.

### 10.3 Focused viewers

Selecting a completed row opens a large, dark, rounded focused viewer over the workspace. Desktop uses a centered near-full-height surface; compact layout uses the viewport. The viewer header contains type icon, title, Source count, and close control.

- Report: readable centered document column with summary, headings, Markdown, and Source chips.
- Flashcards: one prominent card with flip animation, counter, previous/next, shuffle, and restart controls.
- Mind Map: scrollable tree canvas with branch toggles and bounded zoom controls.
- Data Table: full-width scroll region, sticky header, zebra/hover treatment, and per-row Source chips.

Source chips reuse the existing Source open action. Missing/deleted Sources retain the reference label but report unavailable.

### 10.4 Responsive acceptance

- At `1440x900`, Sources, Chat, and Studio remain simultaneously visible, and the Studio card grid/list are usable without horizontal workspace scroll.
- At `390x844`, the existing Sources/Chat/Studio tabs remain; Studio uses one compact column where necessary and focused viewers occupy the viewport.
- Keyboard focus, dialog semantics, card buttons, flip controls, branch toggles, table semantics, and reduced-motion behavior remain accessible.

## 11. Storage And Authorization

Add `studio_outputs` and the minimum supporting indexes/triggers/policies. Product state references a generic root Run but configured `agent_runs` remain product-neutral: they gain no Studio kind, Notebook, Member, or artifact columns.

The existing run-ownership helper learns how to find the requesting principal through either `chat_runs` or `studio_outputs`. Worker access remains unrestricted only under the existing `nano_worker` role. App access to Studio rows is bounded by Notebook membership and mutation capability.

The Agent Run status trigger projects lifecycle timestamps and safe error codes to Studio exactly as the existing Chat product projection does. Successful publication additionally stores the validated artifact and immutable Agent Result reference in the same transaction that terminalizes Run and Job.

## 12. Testing And Acceptance

### 12.1 Catalog and domain tests

- exactly six production Agent Definitions exist after Sprint 11: Chat, Research, and four Studio Definitions;
- `nano.default@1` remains unchanged and `nano.default@2` resolves all exact roots;
- shared Executor capability rejects wrong prompts, contracts, tools, children, limits, and unregistered variants;
- every artifact validator covers valid boundaries, empty content, excessive counts/bytes, duplicate IDs, bad tree topology, cell mismatch, and unpinned Sources.

### 12.2 Persistence and API integration

- Editor/Owner creation and Viewer rejection;
- idempotent admission and mismatched-key conflict;
- invalid/non-Ready/cross-Notebook Source rejection;
- generic Run neutrality and exact release/Definition/Policy pins;
- Evidence snapshot pinning;
- list/detail sharing across Members without Chat leakage;
- active and completed deletion;
- membership/source/deadline/cancellation publication fencing;
- Run/Job/Output atomic completion and failure projection;
- crash/reclaim resumes from Checkpoints without repeated logical Action budget;
- notification loss recovers through durable reads.

### 12.3 Agent integration

- each of the four definitions calls only `search_evidence` through MCP;
- the first decision is a single evidence search and the second is a strict final object;
- each result publishes only under its own Contract;
- Source IDs must be pinned and currently authorized;
- invalid output cannot publish;
- Trace identifies exact Definition, Policy, Prompt, Contract, model, Action, Attempts, and terminal state.

### 12.4 Web tests

- creation grid contains exactly the four enabled actions and no Quiz;
- Viewer permissions, no-Source validation, optimistic running row, durable reload, failure, delete, and Output selection;
- Report, Flashcards, Mind Map, and Data Table viewer interactions;
- desktop and compact accessibility and responsive behavior;
- Playwright screenshots at `1440x900` and `390x844` compared by inspection to the recorded NotebookLM reference hierarchy.

### 12.5 Final gates

- focused Go tests for catalog, Agent, Studio domain, App integration, and Worker wiring;
- full `./scripts/test-go`;
- focused Web unit tests, lint, build, and full `./scripts/test-web`;
- targeted race tests for catalog, Studio Executor, and end-to-end publication;
- source audit proving no Quiz/media Agent, no direct retrieval bypass, no Role field in new Runs, and no mutation of `nano.default@1`;
- `docs/sprint/SPRINT-11-ACCEPTANCE.md` maps every PRD success criterion to direct evidence.

## 13. Delivery Order

1. Commit this design, Sprint 11 PRD, and any required architecture decision/context updates.
2. Commit a detailed test-first implementation plan.
3. Add catalog definitions/contracts/policy/prompts and release v2.
4. Add Studio Output storage, permissions, and product lifecycle.
5. Add the shared Studio runtime/Executor and publication path.
6. Add list/create/detail/delete/events HTTP APIs.
7. Replace the placeholder Studio panel and add four focused viewers.
8. Run full acceptance, capture screenshots, write evidence, and commit the accepted Sprint.
