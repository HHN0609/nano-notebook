# Nano Notebook Sprint 8 PRD

## Document Status

- **Sprint:** Sprint 8
- **Status:** Implemented and accepted
- **Date:** 2026-07-24; amended 2026-07-25
- **Theme:** Web Source Discovery, durable Research delegation, and imported-Source RAG
- **Delivery boundary:** Sprint 8 first delivers manual Web Source discovery and import, then reuses the same capability through a durable Leader-to-Research child Run. A delegated turn produces private candidates, not a same-turn Web answer.

## 1. Decision

Sprint 8 delivers one end-to-end Web Source workflow:

> Search for public material → review private candidates → import selected URLs → build immutable Evidence and retrieval indexes → use the Ready Sources in later grounded Chat turns.

Every Chat turn is owned by a Leader Agent. When an Editor or Owner clearly asks to find or collect external Sources, the Leader creates a durable Research child Run. The child expands the request, searches through Brave, and publishes a private Source Discovery session. The user selects candidates before any shared Source is created.

Manual Source Discovery is the primary product capability. Agent delegation is complete only when it reuses the same search, candidate, import, Source processing, and RAG boundaries.

## 2. Source Documents

This PRD derives from:

- `docs/superpowers/specs/2026-07-24-web-source-discovery-and-research-delegation-design.md`
- `docs/superpowers/specs/2026-07-25-discovery-source-peek-and-fetcher-dns-design.md`
- `docs/product-discovery/CONTEXT.md`
- `docs/product-discovery/REQUIREMENTS.md`
- `docs/technical-architecture/ARCHITECTURE.md`
- `docs/technical-architecture/adr/0004-modular-monolith-with-workers.md`
- `docs/technical-architecture/adr/0005-use-s3-api-for-blob-storage.md`
- `docs/technical-architecture/adr/0008-use-qdrant-as-the-derived-retrieval-index.md`
- `docs/technical-architecture/adr/0017-isolate-retrieval-with-indexed-payload-filters.md`
- `docs/technical-architecture/adr/0020-separate-citation-identity-from-retrieval-chunks.md`
- `docs/technical-architecture/adr/0023-process-sources-with-a-fixed-durable-state-machine.md`
- `docs/technical-architecture/adr/0027-schedule-jobs-with-leases-and-workload-classes.md`
- `docs/technical-architecture/adr/0030-cancel-cooperatively-and-publish-through-a-barrier.md`
- `docs/technical-architecture/adr/0032-fetch-public-urls-through-a-restricted-adapter.md`
- `docs/technical-architecture/adr/0038-always-search-and-publish-plain-text-source-references.md`
- `docs/sprint/SPRINT-6-PRD.md`
- `docs/sprint/SPRINT-7-PRD.md`
- Brave Web Search documentation: `https://api-dashboard.search.brave.com/app/documentation/web-search/get-started`
- Brave LLM Context documentation: `https://api-dashboard.search.brave.com/documentation/services/llm-context`

The approved Sprint 8 design supersedes older statements that defer Source discovery, prohibit all Research Agent Web access, or forbid selecting a later Source in the exact Chat that originated its Discovery session. The implementation must update those documents and record the new runtime decisions before delivery is complete.

## 3. Sprint Goal

Deliver these dependent slices in order:

1. **Manual Source Discovery:** an Editor or Owner searches the Web from the persistent field in the left Source panel, restores a private candidate session, selects results, and imports them.
2. **Imported-Source readiness:** selected URLs become immutable Sources only through the existing restricted Fetcher, versioned normalization, Evidence publication, chunking, hybrid projection, and Ready barrier.
3. **Persisted Chat selection:** a Ready imported Source becomes selected only in the private Chat that originated its Discovery session.
4. **Leader routing:** every Chat turn receives a durable `continue_chat` or `delegate_research` decision.
5. **Research delegation:** a real child Run creates a private Discovery session through the same application capability and returns control to the Leader.

The Sprint does not deliver same-turn Deep Research answers.

## 4. Success Criteria

Sprint 8 is complete only when all of the following are true:

1. The left Source panel provides a persistent Web Search input; a compact adjacent add control retains file upload and direct-URL import.
2. Search switches the left panel into Discovery mode and widens it modestly without a centered dialog or full-screen desktop state.
3. The expanded view displays a candidate-set summary, title links, snippets, site icons or fallbacks, right-aligned checkboxes, and upper-right Select All.
4. Importable results default to selected; already imported URLs are disabled and labeled.
5. Manual search uses the exact user query, makes at most one Brave request, and retains at most ten candidates.
6. Search sessions and candidates survive reload and remain private to the initiating Member.
7. Viewer cannot search, change candidate selection, import, or trigger Research delegation.
8. Batch import succeeds or fails per Candidate without rolling back unrelated successes.
9. Candidate import accepts server-owned Candidate identities and cannot substitute browser-supplied URLs.
10. Origin and final URL identities prevent duplicate Notebook Sources under concurrent or redirected imports.
11. Brave snippets and candidate summaries never become Source Evidence or RAG context.
12. A selected URL creates a Source only after a restricted Fetcher produces a validated immutable snapshot.
13. HTML cleaning uses deterministic primary-content rules and permits a model only to keep or drop bounded ambiguous input blocks.
14. A model cannot write, paraphrase, translate, or expand Evidence text.
15. Deterministic hard-quality failures reject unusable pages; soft findings publish Viewer warnings without model-only rejection.
16. Normalized Evidence, Retrieval Chunks, dense embeddings, sparse BM25 projection, Qdrant verification, and the Ready barrier remain versioned and recoverable.
17. Chat Source selection is durable server state rather than browser-only state.
18. A Ready Discovery import is selected only in the originating private Chat and never silently enters another Chat.
19. Every Chat turn receives a durable Leader route decision before normal grounded execution or Research delegation.
20. Ordinary questions never call Brave merely because selected Sources are insufficient.
21. A clear Source discovery request by an Editor or Owner creates exactly one Research child Run.
22. The Research child expands at most three queries, calls Brave at most three times, and publishes at most ten private candidates.
23. The child has independent Job, Attempt, budget, Checkpoint, cancellation, and Trace authority but cannot publish an Assistant Message.
24. Parent waiting releases its Worker Lease; child success or failure durably requeues the parent.
25. Research delegation immediately opens left-side Source Discovery in a searching state; completion shows the exact Session there and the Leader publishes only a generic message that does not report candidate count.
26. Stop, Notebook deletion, Member removal, and role downgrade prevent late child, Candidate, Source, or Assistant publication.
27. A later ordinary Chat turn pins only Ready, selected Sources and retrieves them through the existing scoped hybrid RAG pipeline.
28. The final grounded response publishes only valid Source markers and Citations through the current Publication Barrier.
29. Deterministic tests require no real Brave credential; an opt-in smoke test uses `NANO_BRAVE_SEARCH_API_KEY` without logging or returning it.
30. Sprint 1 through Sprint 7 authentication, Source, RAG, sharing, recovery, observability, and deletion behavior remains green.
31. Leader routing and Research query expansion publish their actual requested/selected model and Provider-reported token usage to each Run Trace; Brave HTTP calls do not invent LLM usage.
32. Expanded Discovery preserves a separately scrolling existing-Source region at approximately 180 px on desktop and 140 px in compact layouts.
33. A Fetcher-only RFC 8484 resolver may be enabled only when both its HTTPS endpoint and fixed public bootstrap address are configured; partial configuration fails startup.
34. The local start script enables Fetcher DoH defaults so Candidate import works under Clash/Mihomo Fake IP DNS without changing global application DNS behavior.
35. System DNS remains the production default when Fetcher DoH is unset, and every resolved target still passes the existing public-address and mixed-answer rejection policy.
36. Live desktop and compact acceptance must demonstrate a public Brave Candidate import without `unsafe_destination`, while `198.18.0.0/15` and private/reserved IPv4 and IPv6 remain blocked.

## 5. Canonical Terms

- **Source Discovery:** the private Member capability that searches for public URLs and persists candidates before import.
- **Discovery Session:** one private, durable search result set owned by a Member inside a Notebook.
- **Candidate:** a normalized public URL result that is not Source Evidence.
- **Candidate Selection:** the Member's private choice of which Candidate URLs to import.
- **Chat Source Selection:** the durable private set of Ready Sources eligible for the next Run in one Chat.
- **Leader Agent:** the only Chat-facing Agent; it continues normal conversation or delegates Source discovery.
- **Research child Run:** an internal durable Run that expands a Source discovery request and creates a Discovery Session.
- **Web Search Provider:** the adapter boundary that returns bounded Provider-neutral candidates; Brave is the first provider.
- **Candidate Summary:** a bounded overview of result coverage shown only in expanded Source Discovery.
- **Imported Source:** a shared immutable Notebook Source created after a Candidate passes URL admission and snapshot validation.
- **Web Evidence Revision:** one versioned normalized representation of an immutable Web snapshot.

Search snippets are Candidate metadata. They are not Evidence Units, Retrieval Chunks, Citations, or Source previews.

## 6. Product Journeys

### 6.1 Manual Web Discovery

1. An Editor enters `怎么拍电影` in the persistent Web Search field at the top of the left Source panel.
2. Nano creates a private Discovery Session and returns `202 Accepted`.
3. The session completes through Brave Web Search with at most ten normalized candidates.
4. The left panel enters Discovery mode, widens modestly, and shows the candidate-set summary only in that state.
5. All importable results start selected; checkboxes remain at the far right.
6. The Editor follows title hyperlinks in a new browser tab when desired; Nano provides no in-product page preview.
7. The Editor clears unwanted results and clicks Import Selected.
8. Each selected Candidate independently becomes imported or import-failed.
9. Imported Sources show existing processing, Ready, or failure states.
10. Each Ready Source is selected in the current origin Chat only.

### 6.2 Delegated Discovery

1. An Editor asks, `帮我搜集电影制作流程的资料`.
2. The Leader accepts `delegate_research`, checkpoints it, and creates one Research child Run.
3. The parent Job waits without a Lease.
4. The child produces at most three query variants and searches through the shared Discovery boundary.
5. The child commits a private Discovery Session and completes without a Chat Message.
6. The parent resumes and publishes a generic completion message.
7. The UI opens the left-side Discovery searching state when delegation begins and transitions the same exact Session to results automatically.
8. The user selects and imports Sources through the same manual import path.

### 6.3 Later Grounded Research

1. The imported Source becomes Ready after Evidence and index verification.
2. A later user question admits a new Leader Run.
3. The Run pins the Ready Source through persisted Chat selection.
4. Leader chooses `continue_chat`.
5. The existing Agent Loop must perform `search_evidence` for the selected-Source Run.
6. Dense and sparse candidates run under the identical server-built scope.
7. RRF, authoritative Evidence reload, and reranking select bounded Evidence.
8. The Assistant Message and Source Citations publish atomically.

## 7. Permissions And Privacy

| Capability | Viewer | Editor | Owner |
| --- | --- | --- | --- |
| View imported Ready Sources | Yes | Yes | Yes |
| Use Ready Sources in private Chat | Yes | Yes | Yes |
| Create manual Discovery Session | No | Yes | Yes |
| Trigger Research child Run | No | Yes | Yes |
| View own Discovery Sessions | No after downgrade | Yes | Yes |
| Change Candidate selection | No | Yes | Yes |
| Import Candidates | No | Yes | Yes |

Discovery Sessions require both Notebook membership and `session.user_id = current principal`. No Member may enumerate another Member's private searches.

Only an imported Source becomes shared. Neither private queries nor Candidate summaries become shared Source metadata.

## 8. Source Discovery UI Contract

- Web Search is always visible at the top of the left Source panel.
- A compact add control next to Search opens the existing file-upload and direct-URL dialog.
- Search and Research delegation switch the left panel into a dedicated `Sources > Source Discovery` view.
- Desktop widens the left panel to approximately 560 px; it is neither a centered dialog nor full screen.
- Compact layouts remain usable without horizontal clipping.
- Candidate Summary is absent before expansion and visible only in expanded results.
- Select All appears at the list's upper right.
- Every item checkbox aligns at the row's far right.
- Title is an external link using `noopener noreferrer`.
- No result opens an embedded preview.
- Candidate state and linked Source state are distinct and visible.
- Partial import keeps successful items and exposes per-item retry.
- Reload restores the latest private Session for the current Notebook and Member.
- Research delegation opens the exact searching Session and completion updates it, rather than relying only on latest-session ordering.

## 9. Search Provider Contract

`WebSearchProvider` accepts a bounded query and locale hints and returns Provider-neutral candidate records. The initial Brave adapter:

- authenticates only server-side;
- requests Web results intended for the human candidate list;
- maps title, URL, hostname, description, rank, and optional icon metadata;
- strips HTML and rejects invalid UTF-8 or unsafe URLs;
- applies request timeout, rate-limit handling, and bounded retries;
- returns typed unavailable, rate-limited, invalid-response, and cancelled errors;
- never exposes the raw response or credential.

Manual execution calls the provider once. Research execution calls it at most three times.

The first delivery does not implement Provider failover.

## 10. Discovery Persistence

Sprint 8 adds authoritative tables for:

- private Discovery Sessions;
- normalized Candidates and selection;
- bounded Discovery Jobs for manual asynchronous search;
- Agent parent-child relationships and internal parent waiting;
- persisted Chat Source selection;
- Notebook URL identity required for concurrent deduplication.

Required Session states:

```text
searching -> ready
    |
    +-----> failed -> searching
```

Required Candidate states:

```text
discovered -> importing -> imported
                   |
                   +-----> import_failed -> importing
```

State transitions are fenced, idempotent, and validated in PostgreSQL.

## 11. Candidate Import

The batch import endpoint:

1. validates CSRF, Idempotency Key, capability, Session ownership, and Candidate state;
2. reloads the persisted selected Candidates;
3. transitions each accepted Candidate independently;
4. invokes the reusable URL Source admission application service;
5. follows the existing restricted Fetcher boundary;
6. stores the immutable snapshot in the `nano-sources` MinIO bucket;
7. atomically creates Source and Source processing Job;
8. links the Candidate to the Source;
9. reports bounded per-item outcomes.

The browser cannot replace a Candidate URL at import time.

## 12. Web Cleaning And Quality

The new `html-primary-v2` configuration must:

- decode bounded HTML into valid UTF-8;
- parse a bounded DOM;
- remove executable, invisible, navigation, form, advertising, and repeated template content;
- select primary content through deterministic semantic and density rules;
- preserve heading, paragraph, list, table, code, and quotation structure;
- optionally ask a model to keep or drop only the provided ambiguous blocks;
- reject any model result that expands or rewrites the input set;
- produce stable block identities, source order, coordinates, and hashes;
- publish complete or partial Evidence Coverage.

Hard failures are deterministic. Soft quality findings remain visible in Source Viewer Coverage/Quality warnings. A model quality score is diagnostic and may influence candidate ordering but cannot solely fail a Source.

## 13. Evidence, Indexing, And Ready Barrier

The existing Source Worker remains authoritative for:

```text
uploaded -> validating -> normalizing -> segmenting -> indexing -> verifying -> ready
       \---------------------------------------------------------------> failed
```

Persistence remains:

| Store | Authority |
| --- | --- |
| MinIO/S3 | Immutable originals, normalized artifacts, Viewer artifacts |
| PostgreSQL | Sources, Evidence Revisions, Units, Coverage, Chat selection, Run Evidence Sets, Citations |
| Qdrant | Rebuildable dense and sparse Chunk projection |

Chunks never cross Source or Evidence Revision and retain exact Evidence Unit references. Sprint 8 uses the Active Retrieval Index Version rather than introducing unreviewed Chunk, analyzer, embedding, fusion, or reranking parameters.

A Source becomes Ready only after Artifact validation, Evidence publication, dense and sparse projection, point-count verification, active-version revalidation, and lease-fenced completion.

## 14. Persisted Chat Source Selection

Sprint 8 replaces browser-only selection authority with server persistence.

- A new Chat selects all then-Ready Sources.
- A later Source remains unselected by default.
- A Source imported from a Discovery Session becomes selected only in that Session's `origin_chat_id` after Ready.
- An explicit user deselection overrides later automatic selection attempts.
- Agent admission resolves the persisted selection and pins only Ready, authorized Evidence.
- Selection changes never modify an admitted Run or historical Answer.

## 15. Leader And Child Runtime

### 15.1 Leader Route

The first accepted Leader outcome is exactly one of:

```text
continue_chat
delegate_research
```

The route is Provider-neutral, checkpointed, and permission-checked. Invalid results do not default to Web access.

### 15.2 Research Child

The child:

- shares the parent's user, private Chat, input message, and Notebook context;
- has `agent_role = research` and `parent_run_id`;
- owns a separate Agent Job and Attempt lifecycle;
- may create one private Research-origin Discovery Session;
- may call only the bounded Web discovery capability required by its config;
- cannot publish an Assistant Message or mutate Sources;
- returns `discovery_session_id` as its durable outcome.

### 15.3 Waiting

After child creation, the parent Agent Job enters an internal waiting state with no Lease. Child terminalization requeues the parent in the same transaction that records the child outcome. Parent cancellation recursively cancels the child.

Member-facing Run APIs expose only the Leader Run. Developer Trace links the full parent-child tree.

## 16. RAG Reuse

Sprint 8 does not create a separate Web-answer RAG path. Once imported and Ready, a Web Source behaves like every other selected Source:

1. Run admission freezes its Evidence Revision and Active Retrieval Index Version.
2. `search_evidence` uses contextualized query, purpose, and server-built scope.
3. Dense and BM25 retrieval use identical scope filters.
4. RRF fuses bounded candidates.
5. PostgreSQL reauthorizes and reloads authoritative Evidence.
6. Reranking cannot expand scope.
7. The Agent may refine bounded evidence queries.
8. Final text uses Source markers validated through the current grounding and publication path.

Brave metadata is forbidden from this Context Builder.

## 17. Failure And Recovery

- Missing Brave configuration fails Discovery safely without affecting ordinary Chat.
- Search timeout, rate limit, invalid response, and empty result use typed Session outcomes.
- Summary generation may degrade without failing valid candidates.
- Candidate import and Source processing fail independently.
- Manual Discovery work resumes through its durable job boundary.
- Research work resumes through child Checkpoints and Agent Job reclaim.
- Parent waiting never holds a Worker Lease.
- Uncertain child completion reconciles before parent requeue.
- Uncertain Source admission reconciles through its existing Idempotency Key and URL Admission.
- Stop and authorization loss prevent new Provider, import, and publication work.

## 18. Observability

Required metadata includes:

- Leader route;
- parent and child identities;
- query expansion count;
- Provider call count, status, latency, and normalized result count;
- deduplication and retained-candidate count;
- summary degradation;
- Candidate import outcomes;
- Web cleaning quality outcome;
- existing Source processing and RAG stage metadata;
- requested and selected model, Provider, token usage, and Provider-reported cost for Leader routing and Research query expansion;
- final publication or cancellation outcome.

The credential, raw Brave envelope, unrestricted page body, and model chain of thought are forbidden from logs and standard Trace payloads.

## 19. Delivery Order

### Step 1: Decisions And Contracts

- update conflicting product documents;
- add the runtime and Search Provider ADRs;
- freeze API, schema, error, and state-machine contracts.

### Step 2: Manual Discovery

- Brave Provider Adapter;
- Discovery schema, RLS, job, store, service, and APIs;
- persistent left-panel Search and dedicated expanding Discovery UI;
- latest private Session restoration.

### Step 3: Candidate Import And Source Quality

- reusable URL Source import application service;
- batch Candidate import and final-URL deduplication;
- `html-primary-v2` and quality warnings;
- Source provenance association.

### Step 4: Persisted Chat Selection And RAG Acceptance

- durable per-Chat Source selection;
- origin-Chat selection after Ready;
- imported-Web Source RAG fixtures and evaluation.

### Step 5: Leader And Research Delegation

- durable Leader route;
- child Agent Run and parent waiting;
- Research query expansion and shared Discovery execution;
- cancellation, recovery, Trace, and generic Leader completion.

### Step 6: Integrated UI And Acceptance

- SSE `discovery_session_id` projection;
- automatic exact-Session opening;
- full manual, delegated, import, processing, and later-RAG journeys;
- live Brave smoke test.

No later step may compensate for an incomplete manual Source Discovery path.

## 20. Test And Acceptance Matrix

| Area | Required evidence |
| --- | --- |
| Provider | Fixture-backed mapping/error tests and optional live smoke |
| Discovery | Store, RLS, state, restore, selection, retry tests |
| UI | Desktop/compact interaction tests and screenshot evidence |
| Import | Partial success, idempotency, redirect deduplication, safe fetch tests |
| Cleaning | Boilerplate, duplicate, login wall, tables, code, quality warning fixtures |
| Selection | New Chat, later Source, origin Chat, explicit deselection tests |
| Runtime | Route, child creation, waiting, resume, recovery, cancel, and parent/child model-usage Trace tests |
| RAG | Ready-only pinning, scoped hybrid retrieval, authoritative reload, citation tests |
| Security | Viewer denial, cross-Member isolation, URL substitution, credential-redaction tests |
| Regression | `go test ./...`, Web unit tests, typecheck, lint, and Playwright journeys |

## 21. Explicit Non-Goals

- Headless browser or arbitrary JavaScript execution;
- authenticated or paywalled page import;
- recursive crawling;
- automatic Source import without user Candidate selection;
- same-turn Deep Research answers;
- Brave Answers as the Nano Chat model;
- Brave snippets as durable Evidence;
- Candidate page preview;
- Provider failover;
- public Discovery sessions;
- Viewer Source mutation;
- user-visible Agent reasoning or expanded queries;
- un-gated retrieval parameter changes;
- redesigning PDF, Office, audio, image, or YouTube extractors.

## 22. Sprint Acceptance Commands

The implementation plan must provide focused RED/GREEN commands for each step and finish with at least:

```text
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
npm --prefix web run lint
npm --prefix web run test:e2e
```

The live Brave smoke command must be explicit, opt-in, credential-safe, and skipped rather than failed when `NANO_BRAVE_SEARCH_API_KEY` is unavailable.
