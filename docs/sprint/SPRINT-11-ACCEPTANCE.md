# Sprint 11 Acceptance Evidence

## Result

- **Status:** Accepted.
- **Verified:** 2026-07-28
- **Authority:** `docs/sprint/SPRINT-11-PRD.md`
- **Scope:** Report, Flashcards, Mind Map, and Data Table Studio Agents only. Quiz and generated media are absent.
- **Go gate:** `./scripts/test-go` exits 0 after formatting checks, every Go package and PostgreSQL/MinIO integration, `go vet`, and all production binary builds. The final `internal/app` integration package, including Studio SSE, authority, Worker publication, and immutable-artifact checks, completed in 156.602s.
- **Web gate:** `npm test -- --run && npm run lint && npm run build` exits 0: 73 tests pass, ESLint passes, TypeScript/Vite production build passes. Vite reports only its existing non-blocking bundle-size advisory.
- **Browser gate:** `NANO_WEB_URL=http://127.0.0.1:5174 npm run test:e2e -- tests/e2e/studio-outputs.spec.ts` exits 0 in `chromium-desktop` (`1440x900`) and `chromium-compact` (`390x844`).
- **Visual inspection:** `studio-recent.png` and `studio-report.png` from both Playwright projects were inspected. Desktop retains simultaneous Sources/Chat/Studio columns; compact retains the three tabs; the four tinted cards, Recent metadata, focused viewer, and viewport containment match the current NotebookLM/Gemini Notebook information hierarchy used by the PRD.
- **Scope audit:** the user's unrelated `infra/compose/.env.example` modification and untracked `.superpowers/` directory remain untouched and uncommitted.
- **Done:** yes. All 58 Sprint 11 success criteria have direct source, database, automated-test, or inspected-browser evidence.

## Success-Criterion Evidence

| # | Status | Evidence |
| --- | --- | --- |
| 1 | Met | `TestEmbeddedCatalogContainsSprint11ProductionAgents` asserts the exact six production Definitions: the two Sprint 10 roots plus four Studio roots. |
| 2 | Met | The same exact-set assertion and catalog source audit show no Quiz, audio, video, slide, or infographic Definition. |
| 3 | Met | Four embedded Definition JSON files bind `studio_structured_output`; catalog tests assert four distinct Prompt and Result Contract references. |
| 4 | Met | `StudioStructuredOutputExecutorCapability` allows only `search_evidence`, two Model Calls, one Action, no child, and `TestExecutorRegistryResolvesEveryStudioDefinitionThroughOneBoundedExecutor` checks narrowing. |
| 5 | Met | `nano.default@1` is unchanged; `nano.default@2` contains Chat plus four exact Studio roots, verified by the embedded catalog test. |
| 6 | Met | Control Plane and Worker default to exact `nano.default@2`; catalog binding/readiness tests reject unresolved Definition, Prompt, Contract, Policy, tool, or Executor pins. |
| 7 | Met | ADR 0046 and the `studio_outputs` table make Output shared Notebook product data outside Chat, Note, Source, Role, and Delegation storage. |
| 8 | Met | `TestStudioOutputAdmissionPinsConfiguredRootAndListsDurably` inspects the configured Run and proves null Role, Member, and Chat ownership; product context stays in Output/evidence records. |
| 9 | Met | Database unique foreign keys bind each Output to one root Run and each completed Output to one immutable `agent_run_results` row; the Worker integration asserts one Result. |
| 10 | Met | Database status/publication checks and `nano_sync_studio_output_projection` enforce the five states and artifact-only-on-completed shape. |
| 11 | Met | `studio_outputs_completed_artifact_immutable` rejects title/artifact/result mutation after completion; `TestStudioExecutorSearchesThenPublishesValidatedArtifact` proves both first publication and later mutation rejection. |
| 12 | Met | `TestParseKindAcceptsOnlySprint11Outputs` and HTTP validation accept only the four kinds; API input contains no Definition or Executor selector. |
| 13 | Met | Admission validates one-to-fifty ordered Source IDs and `PinEvidenceSet` writes exact evidence/index pins inside the same transaction as Output, Run, Tree, and Job. |
| 14 | Met | `PinEvidenceSet` joins Notebook-visible Ready Sources, active Evidence revisions, and active Retrieval versions; the real admission fixture exercises this path. |
| 15 | Met | Duplicate/invalid sets fail before the admitting transaction commits; evidence-set integration coverage plus atomic transaction boundaries prevent partial Output/Run/Job rows. |
| 16 | Met | `TestStudioOutputViewerCanReadButCannotMutate` proves viewer list/detail success, viewer create/delete 403, and non-member detail 404. |
| 17 | Met | Notebook-member RLS returns shared summaries/artifacts from `studio_outputs`; no Chat join or private Chat payload exists in list/detail/SSE projections. |
| 18 | Met | Runtime lease/deadline checks, membership revalidation in `studioRuntime.Load`, active Source/Evidence/Index revalidation in publication, cascading Notebook/Output deletion, and fenced Run/Job updates block stale publication. |
| 19 | Met | Completed Output holds only validated artifact JSON and Source IDs; creator identity is nullable and no private Chat reference participates in reads. |
| 20 | Met | HTTP integration proves CSRF/idempotency admission and same-key/same-request returns the same Output. |
| 21 | Met | Canonical request hashing includes Notebook, kind, locale, and ordered Source IDs; `Store.Idempotent` returns conflict on hash mismatch. |
| 22 | Met | `Store.List` orders `created_at desc,id desc`, caps results, and serializes only the Output product projection. |
| 23 | Met | Detail reads through request-principal RLS; viewer and intruder assertions prove completed/current-member visibility boundaries. |
| 24 | Met | Delete first cancels queued/running Run and Job in the same statement, then deletes Output; repeated/not-found deletion is safely non-mutating. |
| 25 | Met | `TestStudioOutputSSEReconnectBeginsFromDurableState` proves initial durable snapshot and a later running projection. The stream has bounded full snapshots, 15-second heartbeats, Run LISTEN/NOTIFY wakeups, reconnect safety, and UI polling fallback. |
| 26 | Met | Executor/catalog ceilings plus the Worker integration assert two Model requests and one `search_evidence` Action. |
| 27 | Met | `BuildQueryContextRequest` requires exactly `search_evidence`; `BuildDecisionRequest` requires its accepted Result and exposes no second tool. The Worker integration inspects both requests. |
| 28 | Met | `NewMCPController` and `MCPToolHost` execute the Action through the scoped MCP plane with stable checkpoint-generated `action_id`; existing MCP plane tests remain green. |
| 29 | Met | Studio Executor imports no Retrieval/Qdrant adapter and receives evidence only through Controller/MCP plus the existing server-authorized search projection. |
| 30 | Met | Studio uses the generic immutable Proposal/Result/Final checkpoints; Controller recovery tests prove accepted nodes resume without repeating logical Action budget. |
| 31 | Met | `ValidateArtifact` uses strict decoding, unknown-field rejection, trailing-data rejection, kind-specific types, and a 64 KiB ceiling; `TestValidateArtifactFailsClosed` covers failure. |
| 32 | Met | Artifact validators compare every reference with the pinned set and reject missing, unpinned, or duplicate references before publication. |
| 33 | Met | Report validator enforces title, summary, 1–16 sections, bounded Markdown, unique IDs, and Source references; strict-shape tests pass. |
| 34 | Met | Flashcards validator enforces 5–24 unique non-empty cards and Source references; strict-shape tests pass. |
| 35 | Met | Mind Map validator enforces one root, 3–64 nodes, valid parents, acyclic connected ancestry, depth ≤4, and Source references. |
| 36 | Met | Data Table validator enforces 2–12 unique columns, 1–50 rows, exact cardinality, and Source references. |
| 37 | Met | `PublishFinal` stores Agent Result, budget charge, Output artifact, Run/Job terminal state, notification, and terminal Trace in one worker transaction guarded by lease authority. |
| 38 | Met | Strict artifact errors and invariant failures fail closed; generic Attempt disposition exposes stable safe codes rather than model content. |
| 39 | Met | Studio delegates model/MCP/database failures to the existing typed Attempt classification, retry, backoff, and lease-abandonment machinery; full Worker regression is green. |
| 40 | Met | Existing instrumentation records pinned Definition/Policy/Prompt/Contract/model, physical Model/MCP calls, Attempt/budget and terminal state without content/chain-of-thought; trace regression is green. |
| 41 | Met | App test asserts exactly four creation cards in the two-column grid: Report, Flashcards, Mind Map, Data Table. |
| 42 | Met | App and Playwright tests assert Quiz absent; unsupported media cards were removed rather than left active. |
| 43 | Met | `StudioPanelContent` refuses an empty selected Source set before POST and emits localized actionable copy. |
| 44 | Met | `canMaintain=false` disables all creation buttons and hides delete actions; backend viewer mutation checks independently return 403. |
| 45 | Met | Queued/running rows are cached immediately, projected by Output SSE, polled as fallback, and loaded durably by notebook query after navigation/refresh. |
| 46 | Met | Recent rows show tinted type icon, title/pending status, localized Source count, localized relative time, safe failure state, and authorized delete control; unit and inspected screenshots cover the metadata. |
| 47 | Met | Completed rows open a large dialog with title, type, Source count, close control, and Source chips. App and browser tests open durable Outputs. |
| 48 | Met | Report viewer renders semantic headings, paragraphs/lists, and literal text only; it never uses `dangerouslySetInnerHTML`. Browser and unit tests verify hierarchy. |
| 49 | Met | Flashcards viewer implements flip, previous, next, shuffle, restart, counter, and local-only state; Playwright verifies flip. |
| 50 | Met | Mind Map viewer renders one nested tree with branch toggles and bounded 70%–140% zoom; Playwright verifies collapse. |
| 51 | Met | Data Table viewer uses `table`/`thead`/`th`/`tbody`, sticky headers, an overflow container, and row Source chips; Playwright verifies semantic roles. |
| 52 | Met | Missing Source IDs remain numbered chips; unavailable/non-inline Sources are disabled and expose no former Evidence body. |
| 53 | Met | Inspected `1440x900` screenshot shows simultaneous usable Sources/Chat/Studio and no document horizontal overflow; Playwright asserts containment. |
| 54 | Met | Inspected `390x844` screenshots show usable tabs and viewport-contained viewer; controls are native keyboard-accessible buttons and wide inner content scrolls inside its viewer. |
| 55 | Met | Final full Go and Web gates keep Sprint 1–10 authentication, sharing, Sources, Chat, Research, MCP, RAG, Citation, Trace, Replay, cancellation, and deletion coverage green. |
| 56 | Met | Catalog/artifact unit tests, PostgreSQL admission/authority/SSE/Worker publication tests, generic recovery/fencing suites, 73 Web tests, and two-project Studio Playwright acceptance are deterministic and credential-free. |
| 57 | Met | Desktop and compact Recent/report screenshots were generated and manually compared with the current NotebookLM Studio card/list/focused-output hierarchy. |
| 58 | Met | This document maps every Sprint 11 criterion to direct implementation or verification evidence. |

## Delivery Commits

- `fd9f3a3` — Define Sprint 11 Studio agents.
- `de7c5ad` — Add four configured Studio agents.
- `880d6d0` — Validate Studio output artifacts.
- `ae007f2` — Run durable Studio outputs through agents.
- `c95232f` — Build NotebookLM-style Studio outputs UI.
- `ad91f34` — Keep prompt registry count aligned with catalog.
- `71c6dd4` — Cover Studio outputs in browser acceptance.
- `1bb2ec6` — Stream Studio output progress.
- `e7e9b05` — Keep completed Studio artifacts immutable.

The acceptance document itself is committed separately after the final all-repository gate so its commit hash is intentionally not self-referential.
