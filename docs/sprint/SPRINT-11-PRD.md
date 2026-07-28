# Nano Notebook Sprint 11 PRD

## Document Status

- **Sprint:** Sprint 11
- **Status:** Approved for implementation
- **Date:** 2026-07-28
- **Theme:** Source-grounded Studio Outputs and four configured product Agents
- **Delivery boundary:** Report, Flashcards, Mind Map, and Data Table only; no Quiz or generated media

## 1. Decision

Sprint 11 ships the first durable Studio product capability. Editors and Owners can generate four shared, source-grounded Outputs from the current selected Ready Sources. Every current Notebook Member can revisit completed Outputs from the Studio panel.

The four Output types are four exact Agent Definitions using the Sprint 10 configured Agent framework:

1. Report
2. Flashcards
3. Mind Map
4. Data Table

They share one reviewed structured-output Executor while retaining separate Prompt and Result Contracts. Quiz is not implemented.

## 2. Product Reference

Product behavior and Studio visual hierarchy follow the current Gemini Notebook / NotebookLM product within Nano's existing architecture. The authoritative decomposition is:

- `docs/superpowers/specs/2026-07-28-sprint-11-studio-structured-outputs-design.md`
- [Google Studio capability documentation](https://support.google.com/notebooklm/answer/16206563?hl=en)
- [Google Mind Map documentation](https://support.google.com/gemininotebook/answer/16212283?hl=en)
- [Google Flashcards documentation](https://support.google.com/notebooklm/answer/16958963?hl=en-GB)
- [Google Data Tables announcement](https://blog.google/innovation-and-ai/models-and-research/google-labs/notebooklm-data-tables/)
- `docs/sprint/SPRINT-10-PRD.md`
- `docs/product-discovery/CONTEXT.md`
- `docs/product-discovery/REQUIREMENTS.md`
- `docs/technical-architecture/ARCHITECTURE.md`

Google-specific export, usage tier, feedback, and Drive behavior is reference context, not Nano scope.

## 3. Problem

Sprint 10 made Agent execution configuration-driven but deliberately shipped no new product Agent. Studio still presents placeholder cards that only show a coming-soon toast. Nano therefore has strong Agent infrastructure but only two production Agent Definitions and no durable Output product loop.

The next Sprint must prove that the framework can add several real product Agents without duplicating orchestration or turning configuration into executable code. It must also establish shared Output ownership separately from private Chat ownership.

## 4. Sprint Goal

Deliver these dependent slices:

1. four exact Studio Agent Definitions, prompts, contracts, one policy, and a version-two release manifest;
2. a durable shared Studio Output product model and permission boundary;
3. one bounded structured-output Executor over the existing Controller and MCP tool plane;
4. background admission, recovery, publication, failure, and deletion APIs;
5. a NotebookLM-referenced Studio panel, artifact list, and four focused viewers;
6. end-to-end evidence that all four Outputs survive refresh and remain source-grounded.

## 5. Success Criteria

Sprint 11 is complete only when all of the following are true:

1. The embedded production catalog contains exactly `chat.leader@1`, `research.source-discovery@1`, `studio.report@1`, `studio.flashcards@1`, `studio.mind-map@1`, and `studio.data-table@1`.
2. Quiz, audio, video, slide, and infographic Agent Definitions do not exist.
3. The four Studio Definitions bind the one `studio_structured_output` Executor but use separate exact Prompt and Result Contract references.
4. The Studio Executor ceiling permits only the four reviewed prompt/result shapes, `search_evidence`, two Model Calls, one Action, no children, and no Chat publication.
5. `nano.default@1` remains immutable and readable; `nano.default@2` retains the Chat root and adds four exact Studio roots.
6. Fresh Control Plane admission and Worker readiness select one exact release version and fail when any Studio root, prompt, contract, policy, tool, or executor binding is unresolved.
7. A Studio Output is durable shared Notebook product data and is not a Chat Message, private Chat, Note, Source, Agent Role, or child Delegation.
8. Configured Studio Agent Runs remain product-neutral and contain no Member, Notebook, Output kind, Role, Chat, or artifact columns.
9. Every Output references exactly one root configured Agent Run; successful publication references exactly one immutable Agent Result.
10. Studio Output status is one of queued, running, completed, failed, or cancelled; only completed rows contain an artifact.
11. Successful artifacts are immutable until deletion and failed/cancelled Outputs expose no partial content.
12. Admission accepts only `report`, `flashcards`, `mind_map`, or `data_table`; a browser cannot submit an Agent Definition or Executor identity.
13. Admission accepts one to fifty explicit Source IDs and atomically pins their exact Evidence and Retrieval versions.
14. Every admitted Source is Ready, belongs to the target Notebook, is visible to the Member, and has usable published Evidence and an active Retrieval index.
15. A changed, deleted, unauthorized, unready, duplicate, cross-Notebook, or unindexed Source set fails without creating partial Output/Run/Job state.
16. Editors and Owners may create and delete Outputs; Viewers may list and view but cannot mutate them.
17. Every current Notebook Member sees shared Output summaries and completed artifacts without receiving the creator's private Chat data.
18. Membership loss, Source invalidation, Notebook deletion, Output deletion, deadline expiry, cancellation, or Lease loss prevents later publication.
19. Removing the creator after completion does not expose private data and does not make the shared artifact depend on their private Chat.
20. `POST /api/v1/notebooks/{id}/studio-outputs` requires CSRF and `Idempotency-Key` and returns the same Output for the same canonical request/key.
21. Reusing an idempotency key with a different kind, locale, or ordered Source set returns conflict.
22. `GET /api/v1/notebooks/{id}/studio-outputs` returns newest-first durable summaries and never embeds hidden model/runtime data.
23. `GET /api/v1/studio-outputs/{id}` returns a completed artifact only to a current Notebook Member.
24. `DELETE /api/v1/studio-outputs/{id}` cancels active execution before removing product visibility and is idempotently safe.
25. The Output event stream begins from durable state, sends bounded updates/keepalives, and reconnects without depending on notification delivery.
26. Each Studio Run makes at most two Model Calls and executes at most one logical `search_evidence` Action.
27. The first Model Call must propose exactly one `search_evidence` Action; the second must return a final JSON object and cannot call another tool.
28. `search_evidence` is discovered and invoked through the existing scoped MCP Host/Server with current Attempt authority and stable logical `action_id`.
29. Studio execution has no direct Retrieval, Qdrant, Source SQL, or Provider tool bypass parallel to MCP.
30. Accepted Proposal, Result, and Final Draft Checkpoints recover after crash without consuming a second logical Action budget.
31. The final object is decoded strictly for the pinned Definition's Result Contract; unknown fields, trailing JSON, excessive size, or a different Output shape fail closed.
32. Every Source reference in an artifact belongs to the pinned Evidence set; unpinned and duplicate references fail publication.
33. A Report has a title, summary, one to sixteen structured Markdown sections, and at least one Source reference per section.
34. Flashcards contain five to twenty-four unique cards with non-empty front/back content and Source references.
35. A Mind Map contains one connected acyclic tree, exactly one root, three to sixty-four nodes, depth at most four, and Source references.
36. A Data Table contains two to twelve unique columns, one to fifty rows, exact cell/column cardinality, and Source references per row.
37. Studio publication stores the immutable Agent Result, Output artifact/status, Run/Job terminal state, budget consumption, and terminal Trace in one fenced transaction.
38. Invalid model output, product invariant failure, and authorization failure terminate safely with stable non-sensitive codes.
39. Transient model, MCP, retrieval, or database failures retain existing typed Attempt retry/backoff behavior.
40. Trace identifies exact Definition, Policy, Prompt, Contract, Provider model, Model Calls, MCP Action, Attempts, budgets, and terminal outcome without chain of thought.
41. The Studio panel contains exactly four enabled creation cards in a compact two-column NotebookLM-style grid: Report, Flashcards, Mind Map, and Data Table.
42. Quiz and unsupported media controls are absent from the active Studio panel rather than shown as working actions.
43. With no selected Ready Source, generation creates nothing and shows a localized actionable message.
44. Viewers can see the Output list but cannot activate generation or deletion controls.
45. Queued/running Outputs appear in the Recent list, update in the background, and restore correctly after navigation or refresh.
46. The Recent list shows type, title/pending label, Source count, relative time, safe status, and authorized delete action.
47. Selecting a completed Output opens a large focused viewer with title, type, Source count, close control, and Source-opening references.
48. The Report viewer renders a readable document hierarchy and Markdown without unsafe HTML.
49. The Flashcards viewer supports flip, previous, next, shuffle, restart, and counter without mutating the durable deck.
50. The Mind Map viewer supports branch expand/collapse and bounded zoom while keeping one understandable tree hierarchy.
51. The Data Table viewer uses semantic table markup, sticky headers, horizontal overflow, and Source references per row.
52. Missing or deleted referenced Sources remain identified but use the existing unavailable behavior and reveal no former evidence.
53. At `1440x900`, Sources, Chat, and Studio remain simultaneously usable; the Studio grid and list require no workspace-level horizontal scrolling.
54. At `390x844`, the existing three tabs remain usable and every Studio viewer fits the viewport with keyboard-accessible controls.
55. Existing Sprint 1–10 authentication, sharing, Sources, Chat, Research, MCP, cancellation, RAG, Citation, Trace, Replay, and deletion tests remain green.
56. Automated tests cover catalog strictness, artifact validation, permissions/RLS, idempotent admission, recovery, fencing, publication, APIs, four viewer interactions, and responsive Studio states without live credentials.
57. Acceptance includes inspected desktop and compact screenshots compared with the current NotebookLM Studio information hierarchy.
58. `docs/sprint/SPRINT-11-ACCEPTANCE.md` maps every criterion above to direct test, source, API, or screenshot evidence before the Sprint is marked accepted.

## 6. Canonical Terms

- **Studio Output:** durable shared Notebook product result.
- **Studio Agent:** one exact configured root Definition that produces one Output kind.
- **Studio artifact:** validated type-specific JSON published into a completed Output.
- **Focused viewer:** the large type-specific read-only surface opened from an Output row.
- **Source reference:** an artifact's bounded pointer to a Source in the Run's pinned Evidence set.

`Artifact` alone remains an implementation term in other modules. Product copy says Output. `Study Guide` is a future Report preset, not a fifth Agent. `Quiz` is absent.

## 7. Product Flow

1. An Editor or Owner selects one or more Ready Sources.
2. They click one of four Studio creation cards.
3. A queued Output appears immediately in Recent while the background Agent runs.
4. The Agent searches only the pinned Sources through MCP and produces its strict result.
5. Completion replaces the pending row with a titled Output.
6. Any Member can open the focused viewer and follow Source references.
7. An Editor or Owner can delete the Output. Failed Outputs may be deleted and generated again as new Outputs.

## 8. Explicit Non-Goals

- Quiz or test-taking.
- Audio, video, slides, infographics, or image generation.
- Notes and editable shared drafts.
- Custom prompts, report-type picker, difficulty, length, language picker, or generation settings UI.
- Export to Docs, Sheets, CSV, PDF, image, or downloadable Mind Map.
- Flashcard learning-history persistence or explanations.
- Mind Map node-to-Chat and graph editing.
- Data Table editing.
- Output comments, versioning, collaboration cursors, public links, or cross-Notebook Outputs.
- General workflows, additional child Agents, fan-out, joins, recursion, or parallel tool batches.
- New external tools, direct Retrieval bypass, or mutable Agent configuration.

## 9. Delivery Slices

1. Document and register the four exact Agent/Prompt/Contract/Policy identities and release v2.
2. Add Studio Output storage, RLS, projection triggers, and domain validators.
3. Add idempotent admission and exact source pinning.
4. Add the Studio runtime adapter and shared Executor over Controller + MCP.
5. Add atomic publication/failure/cancellation and Run ownership integration.
6. Add list/detail/delete/events APIs.
7. Replace placeholder Studio with the four-action grid, Recent list, and background state.
8. Add Report, Flashcards, Mind Map, and Data Table focused viewers.
9. Run regression, race, visual, and requirement-by-requirement acceptance gates.
