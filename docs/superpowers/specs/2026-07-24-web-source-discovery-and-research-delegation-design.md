# Web Source Discovery And Research Delegation Design

**Date:** 2026-07-24; left-panel interaction amended 2026-07-25
**Status:** Approved; 2026-07-25 interaction amendment awaiting written-spec review
**Scope:** Source Discovery, Web Search, durable Leader-to-Research delegation, Source import, Web normalization, persisted Chat selection, and reuse of the existing grounded RAG pipeline

## 1. Outcome

Deliver one coherent Web Source workflow:

1. An Editor or Owner can search the public Web from the Source panel, inspect a bounded candidate list, select results, and import them into the current Notebook.
2. Every Chat turn is handled by a Leader Agent. A clear request to find, collect, or research new Sources is delegated to a durable Research child Run.
3. The Research child Run expands the request, uses the same Source Discovery capability as the manual UI, and creates a private candidate session. It does not silently import or answer from undisclosed Web context.
4. The user selects candidates. Selected URLs enter the existing secure Source lifecycle: immutable snapshot, format-specific normalization, authoritative Evidence persistence, deterministic chunking, dense and sparse indexing, verification, and `Ready` publication.
5. A newly Ready Source is selected automatically only in the Chat that originated the Discovery session. A later Chat turn may retrieve and cite it through the existing grounded RAG pipeline.

The sprint is intentionally larger than an Agent-only Web Search tool. Source Discovery is the primary reusable product capability; Agent delegation is a second entry point into it.

## 2. Superseded Boundaries

This design deliberately supersedes these earlier product boundaries for this delivery slice:

- the Research Agent cannot browse the internet;
- external Source discovery is deferred;
- the Research Agent is always read-only with respect to every durable Source-adjacent object;
- every Source added after Chat creation must remain unselected in that Chat.

The replacement boundaries are narrower than unrestricted Agent mutation:

- the Research Agent may create only a private Source Discovery session and candidates;
- it cannot create a Notebook Source;
- only an Editor or Owner may initiate Web discovery or import;
- a human selects candidates before import;
- only a successfully processed Source becomes shared Notebook evidence;
- automatic Chat selection is limited to the private Chat that originated that Discovery session.

The implementation plan must update the affected product requirements and architecture decisions so that this spec does not remain in conflict with older documents.

## 3. Product Decisions

### 3.1 Manual Discovery First

The Source panel is the canonical Web Search entry point. Agent research reuses its application capability instead of owning a separate search and import stack.

### 3.2 Human Selection Before Import

Search returns private candidates, not Sources. Candidates become shared Sources only after the user selects them and submits an import command.

### 3.3 No Same-Turn Web Answer

A delegated Research turn ends after creating candidates. It does not wait for Source processing, index the imported content, and then answer in the same user turn.

The Leader publishes a short message such as:

> 我找到了一些关于电影制作流程的资料，已经放到来源发现中。

It does not report the exact candidate count. The UI opens the candidate session automatically.

### 3.4 Explicit Discovery Intent Only

The Leader delegates only when the user clearly asks to search, find, collect, research, or add external material. Ordinary questions do not silently access the Web when Notebook evidence is insufficient.

### 3.5 Private Candidates, Shared Sources

Discovery sessions and candidates are private to their creator. Imported Sources follow the Notebook's existing shared Source permissions.

### 3.6 Supported Roles

Editor and Owner may:

- create manual Discovery sessions;
- trigger Research delegation;
- change a private candidate selection;
- import selected candidates.

Viewer may use existing Notebook Sources in private Chats but cannot perform any operation that can add a Source.

## 4. Provider Boundary

### 4.1 Provider

The initial provider is Brave Search API. Application code depends on a provider-neutral `WebSearchProvider` interface.

Brave Web Search is used for the human-facing candidate list. The provider result is normalized into bounded application fields:

- title;
- URL;
- display hostname;
- plain-text description;
- optional same-origin-served favicon reference;
- provider rank and bounded provider metadata.

Brave's raw response does not enter the UI, Agent Checkpoints, Source authority, or durable Trace payloads.

### 4.2 Credentials

The server reads `NANO_BRAVE_SEARCH_API_KEY`. The key must never enter:

- browser bundles or responses;
- PostgreSQL;
- Trace or Replay payloads;
- application logs;
- checked-in environment files.

Deterministic tests use fixtures. A separately invoked live smoke test runs only when the environment variable is available.

### 4.3 Search Content Is Not Source Content

Brave candidate descriptions and LLM Context snippets are query-specific discovery material. They are not treated as a complete Source, Evidence, or citation authority.

After user selection, the existing restricted Fetcher downloads the selected public URL. Only a successful immutable snapshot may create a Source.

The sprint does not add a Headless Browser. JavaScript-only pages, login walls, paywalls, unsupported responses, and inaccessible pages fail their individual import.

### 4.4 Official Provider References

- Brave Web Search: <https://api-dashboard.search.brave.com/app/documentation/web-search/get-started>
- Brave LLM Context: <https://api-dashboard.search.brave.com/documentation/services/llm-context>

## 5. Architecture

```text
                            +----------------------+
Manual Source UI ---------->| Source Discovery     |
                            | application boundary |
                            +----------+-----------+
                                       |
                                       v
                              WebSearchProvider
                                       |
                                   Brave API

Chat message -> Leader Run
                  |
                  +-- continue_chat -> existing grounded Agent Loop
                  |
                  +-- delegate_research
                            |
                            v
                    Research child Run
                            |
                            +-> query expansion
                            +-> Source Discovery application boundary
                            +-> private Discovery Session

Selected Candidate
        |
        v
URL Source Admission -> restricted Fetcher -> MinIO immutable snapshot
        |
        v
Source Worker -> normalize -> PostgreSQL Evidence -> chunk/embed/BM25 -> Qdrant
        |
        v
Ready -> originating Chat selection -> later grounded Agent Run
```

### 5.1 Leader Agent

Every Chat turn begins with a durable Leader routing decision:

```text
leader_route = continue_chat | delegate_research
```

`continue_chat` enters the existing Agent Controller, including selected-Source evidence search and publication rules.

`delegate_research` is legal only when:

- the request has clear Source discovery intent;
- the user remains Editor or Owner;
- no conflicting active delegation exists;
- the Leader and child budgets permit the work.

An invalid or unparseable routing decision never defaults to Web access. It fails safely and remains retryable.

### 5.2 Research Child Run

Research is a real child Run, not a prompt switch inside the Leader Run. It has:

- `parent_run_id`;
- `agent_role = research`;
- its own Job, deadline, budgets, Checkpoints, Attempt Trace, and failure state;
- no authority to publish a Chat message;
- no Source mutation capability.

The child expands one request into at most three bounded queries, calls the shared Discovery capability, merges and canonicalizes URLs, removes duplicates, ranks at most ten candidates, and produces a candidate-set summary.

Expanded queries are developer Trace data and are not Member-facing content.

### 5.3 Parent Waiting And Resume

When the Leader accepts a delegation:

1. it checkpoints the accepted delegation;
2. it creates the child Run, child Job, and empty private Discovery Session atomically;
3. it links the opaque Session identity into the Member-facing Leader Run projection;
4. its own Job enters an internal `waiting` state without a Worker Lease;
5. the user-facing Leader Run remains active, allowing the left panel to show the Session's searching state;
6. child completion or failure atomically requeues the parent Job;
7. the resumed Leader consumes the durable child outcome and publishes one short Assistant Message.

Child Runs are excluded from Member-facing active-Run lists and the one-active-interaction limit. They remain visible in restricted developer Trace tooling.

Stopping the Leader, deleting the Notebook, removing the Member, or losing `source.maintain` permission cancels the child and prevents later publication.

### 5.4 Source Discovery Module

The new `sourcediscovery` module owns:

- private Discovery sessions;
- normalized candidate results;
- candidate selection;
- provider-independent deduplication and ranking;
- candidate-set summaries;
- candidate import state and Source association;
- retry-safe state transitions.

It does not own Source content, Fetcher behavior, Evidence, Retrieval indexes, Chat messages, or Agent runtime state.

### 5.5 Reusable Source Import Application Service

The current URL import orchestration must move out of the HTTP handler into an application service. Both known-URL import and Discovery candidate import reuse it.

The service owns orchestration only. The Source Store remains authoritative for Source creation, quota, capability, idempotency, and the Source processing Job.

## 6. Discovery Data Model

### 6.1 `source_discovery_sessions`

Required fields:

| Field | Contract |
| --- | --- |
| `id` | Opaque Session identity |
| `notebook_id` | Containing Notebook |
| `user_id` | Private owner |
| `origin_chat_id` | Chat to select imported Sources into; nullable only when no current Chat exists |
| `origin` | `manual` or `research_agent` |
| `query` | Original Member query |
| `summary` | Bounded candidate-set overview; nullable on degradation |
| `status` | `searching`, `ready`, or `failed` |
| `research_run_id` | Producing child Run for Agent origin |
| `error_code` | Safe terminal code for failed sessions |
| timestamps | Creation, update, completion |

A user may have multiple sessions. The initial UI restores the most recent session for the current Notebook and user.

### 6.2 `source_discovery_candidates`

Required fields:

| Field | Contract |
| --- | --- |
| `id` | Opaque Candidate identity |
| `session_id` | Private owning Session |
| `ordinal` | Stable displayed order |
| `title` | Sanitized plain text |
| `canonical_url` | Normalized HTTP(S) URL |
| `display_url` | Safe display hostname/path |
| `snippet` | Sanitized bounded plain text |
| `favicon_ref` | Optional same-origin reference or generic fallback |
| `selected` | Defaults to `true` when importable |
| `status` | `discovered`, `importing`, `imported`, or `import_failed` |
| `source_id` | Created Source after admission |
| `import_error_code` | Safe per-item failure |

### 6.3 Agent Run Additions

The Agent schema gains enough structure to identify and link roles:

- `agent_role = leader | research`;
- `parent_run_id` for Research children;
- `discovery_session_id` for the exact private Session, exposed on the Leader projection from delegation onward;
- a durable parent-child delegation record or equivalent uniqueness constraint.

Only the Leader owns `output_message_id`.

### 6.4 Persisted Chat Source Selection

`chat_source_selections` becomes the server authority for each private Chat's Source selection. It records at least:

- `chat_id`;
- `source_id`;
- selected state;
- update time.

Creation of a new Chat records all then-Ready Sources as selected. Later Sources remain unselected unless they originated from that Chat's Discovery session or the user selects them manually.

Every Run freezes the current selection into its existing Run Evidence Set. Later selection changes never rewrite an active or historical Run.

### 6.5 Privacy And RLS

Session and Candidate access requires all of:

- `current principal = session.user_id`;
- current Notebook membership;
- Editor or Owner role for create, update, retry, and import.

Other Members cannot discover whether a private search exists. Imported Sources use the existing shared Source RLS rules.

Member removal deletes private Discovery sessions through the Notebook/identity lifecycle. A downgrade to Viewer immediately prevents further search and import operations.

## 7. Member API

```text
POST  /api/v1/notebooks/{notebook_id}/source-discovery-sessions
GET   /api/v1/notebooks/{notebook_id}/source-discovery-sessions/latest
GET   /api/v1/source-discovery-sessions/{session_id}
PATCH /api/v1/source-discovery-sessions/{session_id}/selection
POST  /api/v1/source-discovery-sessions/{session_id}/imports
POST  /api/v1/source-discovery-sessions/{session_id}/retry
```

Contracts:

- Manual create accepts one bounded raw query and returns `202 Accepted` with the durable Session.
- Research-origin sessions may be created only by the internal Research Run path.
- Selection replacement is atomic and server-validated.
- Import requires an Idempotency Key and consumes the server-persisted selection.
- Import returns per-Candidate outcomes and never rolls back unrelated successes.
- Retry is legal only for a failed Session or failed Candidate transition allowed by its state machine.
- Member APIs never return provider credentials, raw provider envelopes, expanded child queries, or hidden ranking diagnostics.

The Agent Run SSE projection gains an optional `discovery_session_id`. The frontend uses the transition event to open the result UI without placing candidate data inside Chat Message content.

## 8. Search Execution

### 8.1 Manual Search

```text
create private Session
-> enqueue bounded Discovery work
-> call Brave once with the user's raw query
-> normalize and sanitize at most ten results
-> canonicalize and deduplicate URLs
-> mark already imported Notebook URLs
-> generate one optional candidate-set summary
-> publish Session ready/failed
```

Summary generation failure is a permitted degradation. Results remain `ready` and the expanded UI omits the overview.

### 8.2 Research Search

```text
load child input
-> produce at most three query variants
-> call Brave at most three times
-> merge results
-> canonicalize and deduplicate URLs
-> apply bounded ranking and domain diversity
-> retain at most ten candidates
-> generate one optional candidate-set summary
-> commit the private Session
-> complete the child Run
```

The child does not fetch candidate pages, import Sources, or compose a research answer.

### 8.3 Result Sanitization

- Accept HTTP(S) only.
- Reject credentials and invalid host structure.
- Strip provider HTML from titles and snippets.
- Enforce valid UTF-8 and rune/byte bounds.
- Never render provider markup as HTML.
- Do not load arbitrary candidate favicons from the browser. Use a same-origin bounded proxy/cache or a generic icon.

## 9. Source Discovery UI

The Source panel is the permanent home of Web Search and Discovery, matching the NotebookLM interaction model. Its primary control is a persistent Web Search field rather than a large Add Sources button. A compact adjacent add control opens the existing file-upload and direct-URL dialog; Web Search does not live in that dialog.

Submitting a manual search switches the left Source panel into a dedicated `Sources > Source Discovery` view. On desktop, the left panel widens modestly from its normal width to approximately 560 px; it never becomes a centered modal or a full-screen state. Closing Discovery restores the normal panel width and Source list. Compact layouts keep the existing single-panel navigation and use the available viewport width without horizontal clipping.

As soon as an authorized Chat turn delegates to Research, the same left-side Discovery view opens in a searching state. The child Run never fabricates links in Chat. When the child commits its private Session, the existing view transitions to results for that exact Session. The Leader may publish only its generic completion message. Search completion does not import candidates automatically: the Member reviews the list and explicitly invokes Import Selected.

Expanded content contains:

- the search input;
- a candidate-set summary only while expanded;
- a result list;
- Select All in the upper-right of the list;
- Import Selected at the bottom.

Each result row displays, from left to right:

- site icon or generic fallback;
- title as an external hyperlink;
- provider snippet;
- status text when needed;
- checkbox fixed at the far right.

External links open in a new tab with `noopener noreferrer`. Nano does not implement a candidate page preview.

All importable candidates start selected. Already imported URLs are marked and disabled. Users may persistently select or clear any remaining item.

Per-item UI states are:

- ready to import;
- importing;
- imported/Source processing;
- imported/Source ready;
- import failed with retry;
- Source processing failed, linked to the existing Source failure experience.

When a Research child starts, the active UI opens its corresponding private Session automatically in the left panel. Completion updates that exact view to results. Reload restores the latest private Session without repeatedly fabricating a new search.

## 10. Candidate Import And Deduplication

### 10.1 Import Authority

The browser submits Candidate identities, not replacement URLs. The server reloads and locks the candidates, revalidates capability and Session ownership, and transitions only `discovered` or `import_failed` items to `importing`.

### 10.2 Import Sequence

```text
lock Candidate
-> create/reconcile URL Admission
-> restricted Fetcher GET
-> validate redirects, destination, type, size, encoding, and checksum
-> write immutable snapshot to MinIO
-> finalize Source and Source processing Job
-> link Candidate to Source
-> mark Candidate imported
```

The Candidate becomes `imported` only after Source and processing Job creation commit. Source `Ready` is a later state.

### 10.3 Duplicate Policy

Deduplication happens:

1. within one provider response;
2. across expanded Research queries;
3. against existing Notebook origin and final URLs before fetch;
4. against the normalized final URL after redirects.

A database-enforced Notebook URL identity prevents concurrent imports from creating two Sources for the same normalized final URL. Similar titles or content are not merged automatically.

## 11. Immutable Snapshot And Source Provenance

The successful Fetcher result records:

- requested origin URL;
- normalized final URL;
- media type;
- byte size;
- content SHA-256;
- fetch completion time;
- immutable object key;
- sanitized Source title;
- Discovery Candidate association.

Search query and Candidate metadata stay private. The shared Source exposes its normal title, format, lifecycle, origin URL behavior, and Evidence without exposing another Member's private Discovery session.

The local Blob Store remains the existing MinIO deployment behind the S3 Adapter. The `nano-sources` bucket stores original snapshots, normalized artifacts, and supported Viewer artifacts.

## 12. Web Normalization And Cleaning

### 12.1 Authority Principle

The raw fetched snapshot is immutable. Cleaning produces a versioned Evidence Revision and never overwrites the original.

A model may classify bounded ambiguous blocks as keep/drop. It may not rewrite, summarize, complete, translate, or otherwise generate Evidence text.

### 12.2 `html-primary-v2`

The Web normalization pipeline is:

1. bounded charset decoding into valid UTF-8;
2. bounded DOM parsing with depth and node-count limits;
3. removal of script, style, form, invisible, navigation, header/footer, advertising, and repeated template regions;
4. deterministic main-content selection using semantic containers, text density, link density, and block length;
5. preservation of headings, paragraphs, lists, tables, code, and quotations in source order;
6. optional keep/drop classification of bounded ambiguous blocks;
7. repeated-block removal and whitespace normalization;
8. stable block ordinal, kind, coordinate, and content-hash construction;
9. complete Artifact validation before publication.

Primary-content loss, empty useful content, invalid encoding, extreme boilerplate, detected login/error pages, or processing-budget exhaustion fails the Source.

### 12.3 Quality Gate

Hard deterministic failures include:

- no usable primary content;
- content below the minimum useful bound;
- almost entirely links or template material;
- login, error, or access-denied page shape;
- invalid character content;
- abnormal duplication;
- limit or extraction failure.

Soft quality findings include:

- thin but potentially useful content;
- uncertain ambiguous-block classification;
- known non-primary omissions;
- low cleaning confidence;
- unknown publication date.

Soft findings allow `Ready` but publish Coverage/Quality warnings in the Source Viewer. A model score may help candidate ordering and Trace diagnostics but cannot be the sole `Ready` decision.

### 12.4 Other Formats

Direct PDF, Office, image, audio, and public YouTube results reuse the existing format-specific normalization and Viewer pipelines. This sprint does not replace their extractor contracts.

## 13. Persistence Authority

```text
MinIO / S3-compatible Blob Store
├─ immutable original bytes
├─ normalized Artifact JSON
└─ page, slide, transcript, and Viewer artifacts

Application PostgreSQL
├─ Discovery sessions and candidates
├─ Source lifecycle and URL identity
├─ Evidence Revisions and Units
├─ Coverage and quality findings
├─ Chat Source selection
├─ Run Evidence Sets
├─ parent/child Run authority
└─ Citation and publication authority

Qdrant
└─ rebuildable dense/sparse Chunk projection and scoped identifiers
```

Qdrant never becomes Source-text or authorization authority.

## 14. Evidence, Chunking, And Indexing

### 14.1 Evidence Units

Normalized structural blocks become PostgreSQL Evidence Units. Each is tied to one Source, Evidence Revision, stable ordinal, block kind, text, and source-native coordinate.

### 14.2 Retrieval Chunks

Chunks are deterministic windows over Evidence Units:

- never cross Source or Evidence Revision;
- preserve heading context and semantic structures where possible;
- split an oversized Unit only at versioned source-relative boundaries;
- retain exact Unit references;
- receive stable identities derived from Index Version, Revision, ordinal, text, and references;
- remain rebuildable from PostgreSQL authority.

The sprint keeps the active Index Version's current development parameters, approximately 800 runes with 120-rune overlap, unless an offline Eval-gated candidate version proves a change. Web fixtures must be added before promoting any new configuration.

### 14.3 Dense And Sparse Projection

Each Chunk receives:

- a dense embedding using the Active Retrieval Index Version's configured Gemini document profile and dimensions; and
- a sparse BM25 vector using the existing mixed Chinese/English analyzer.

The current active development profile uses 768 dense dimensions. Code continues to read this value from the Index Version rather than hard-coding it into Source Discovery.

Qdrant stores Chunk identity, Notebook/Source/Revision/Index identities, Unit identities, dense and sparse vectors, and checksums. It does not store authoritative Source text.

### 14.4 Ready Barrier

A Source becomes `Ready` only after:

1. immutable snapshot verification;
2. valid normalized Artifact publication;
3. non-empty Evidence and valid Coverage;
4. complete dense and sparse projection;
5. Qdrant point-count, dimension, scoped-payload, and checksum verification;
6. Active Index Version and processing Lease revalidation;
7. atomic completion of the Source and processing Job.

A failed or processing Source never enters a Run Evidence Set.

## 15. Originating Chat Selection

When a Discovery-imported Source becomes `Ready`, the Source completion transaction or its fenced follow-up performs one idempotent selection command:

- verify `origin_chat_id` still belongs to the importing Member;
- verify the Chat still belongs to the same Notebook;
- verify the user still has access;
- insert the Source as selected for that Chat unless the user has already recorded an explicit deselection.

It does not select the Source in other Chats. A later manual deselection remains authoritative.

## 16. Query-Time Grounded RAG

For a later ordinary Chat turn:

```text
load persisted Chat selection
-> retain Ready and currently authorized Sources
-> pin Evidence Revision and Active Index Version in Run Evidence Set
-> Leader returns continue_chat
-> require the first search_evidence Action for a selected-Source Run
-> contextualize the retrieval query from bounded Chat history
-> execute Dense and BM25 under the identical server-built scope
-> deterministic RRF
-> PostgreSQL authorization and Evidence reload
-> bounded rerank
-> allow bounded query refinement
-> compose ordinary text with [source:<source_id>] markers
-> validate markers, authorization, deletion fences, and Run authority
-> publish Assistant Message and Source Citations atomically
```

Neither Brave snippets, candidate-set summaries, unimported candidates, nor processing Sources may enter the Context Builder.

A Source that becomes Ready after a Run begins is not added to that Run. It becomes eligible only on a later admission.

## 17. State Machines

### 17.1 Discovery Session

```text
searching -> ready
    |
    +-----> failed -> searching (explicit retry)
```

### 17.2 Candidate

```text
discovered -> importing -> imported
                   |
                   +-----> import_failed -> importing
```

The linked Source independently follows:

```text
uploaded -> validating -> normalizing -> segmenting -> indexing -> verifying -> ready
       \---------------------------------------------------------------> failed
```

## 18. Failure And Cancellation

- Brave timeout, rate limit, unavailable response, or invalid envelope fails the Session with a safe retryable error.
- Candidate-set summary failure degrades to results without a summary.
- One Candidate import failure never rolls back another Candidate.
- Fetch or processing failure is visible through the Candidate/Source relationship and existing safe Source failure reasons.
- Research child failure wakes the parent; the Leader publishes a short retryable failure message.
- Worker or process failure resumes from durable Session, Job, Checkpoint, Admission, Source, and index-build boundaries.
- Lease loss prevents late Candidate, Source, child, or Assistant publication.
- User Stop cancels the parent, child, and unfinished Agent-origin search.
- Notebook deletion, Member removal, role downgrade, or Source deletion reuses the existing cancellation and publication fences.
- Ordinary Chat and existing RAG remain available when Brave is not configured. Source Discovery reports a safe unavailable/configuration error.

## 19. Observability

Trace metadata covers:

- Leader route outcome;
- parent/child identities and lifecycle;
- bounded expanded-query count;
- provider call count, latency, status class, and normalized result count;
- deduplication and final candidate count;
- summary degradation;
- Candidate import outcomes;
- Source processing stage and quality-gate outcome;
- dense, sparse, fusion, authority reload, and rerank stages;
- final publication outcome.

Member-facing APIs do not expose expanded queries, provider diagnostics, ranking details, Checkpoints, or model reasoning. Sensitive Replay continues to use the existing explicit capability and retention boundary.

## 20. Testing

### 20.1 Unit

- Leader tagged-decision parsing and permission gating;
- query expansion and call budgets;
- Brave request/response normalization;
- HTML stripping and UTF-8 bounds;
- URL canonicalization and final-URL deduplication;
- candidate ranking and stable ordering;
- HTML primary extraction and optional block-classifier subset enforcement;
- hard quality failures and soft warnings;
- Chat selection precedence;
- Candidate and Session state machines.

### 20.2 Database And RLS

- Session privacy across users and Notebooks;
- Viewer denial;
- Member removal and downgrade behavior;
- parent/child uniqueness and output authority;
- parent waiting and wakeup atomicity;
- Candidate/Source association;
- concurrent final-URL import deduplication;
- idempotent originating-Chat selection.

### 20.3 Agent Runtime

- normal Chat never calls Brave;
- explicit discovery creates one child;
- child budget and Checkpoint recovery;
- parent wait, child success, and parent resume;
- child failure and parent resume;
- Stop and permission-loss propagation;
- no child Assistant Message;
- generic Leader completion text without exact candidate count.

### 20.4 API Integration

- manual Session creation and latest restoration;
- persistent selection replacement;
- partial batch import;
- Candidate import retry;
- already-imported URL display;
- Source processing association;
- Chat Snapshot and SSE `discovery_session_id` behavior.

### 20.5 UI

- persistent Web Search field and compact file/URL add control in the left Source panel;
- dedicated left-side Discovery mode, modest desktop widening, and compact viewport containment;
- no full-screen desktop state;
- summary only while expanded;
- right-aligned per-row checkboxes and upper-right Select All;
- default selection of importable items;
- safe external links and no page preview;
- import, Source processing, Ready, and failure states;
- automatic searching-state open on Research delegation and exact-Session results on completion;
- private latest-Session recovery.

### 20.6 Source And RAG

- public HTML, direct PDF, and captioned YouTube Discovery imports;
- JS-only, login-wall, inaccessible, and unsupported failures;
- immutable snapshot reuse after Worker restart;
- `html-primary-v2` fixtures with boilerplate, tables, code, duplicates, and partial coverage;
- deterministic Chunk rebuild;
- dense/sparse projection verification;
- new Source selection only in the origin Chat;
- later grounded answer using only imported, Ready, selected Source Evidence;
- no Brave or Discovery text in RAG context.

### 20.7 Live Smoke

An opt-in smoke test uses `NANO_BRAVE_SEARCH_API_KEY` to verify authentication, one bounded query, normalized results, and credential-safe logs. It is not part of deterministic CI.

## 21. Acceptance Journeys

### 21.1 Manual Discovery

1. An Editor searches for `怎么拍电影`.
2. The left Source panel enters Discovery mode, widens modestly, and shows up to ten results.
3. Every importable checkbox is on the right and selected by default.
4. The Editor clears unwanted items and imports the rest.
5. Successful URLs create Sources; failed URLs remain independently retryable.
6. Ready Sources become selected in the current origin Chat only.

### 21.2 Delegated Research

1. An Editor asks, `帮我搜集电影制作流程的资料`.
2. The Leader checkpoints `delegate_research` and creates one Research child Run.
3. The child performs at most three searches and publishes one private candidate Session.
4. The parent resumes and publishes a generic completion message without a count.
5. The left Source panel opens the exact Source Discovery Session automatically, beginning with searching state and then showing results.
6. The user selects and imports candidates.
7. No same-turn Web answer is produced.

### 21.3 Later Grounded Answer

1. Imported Sources finish processing and become Ready.
2. They are selected only in the originating private Chat.
3. A later question pins them in the Run Evidence Set.
4. `search_evidence` performs scoped hybrid retrieval and authoritative Evidence reload.
5. The final Answer cites only valid imported Source identities.

### 21.4 Negative Boundaries

- A normal question makes no Brave request.
- A Viewer cannot create Discovery state or import.
- One Member cannot see another Member's sessions or candidates.
- A failed or processing Source cannot enter RAG.
- Brave snippets never become Evidence.
- Stop, deletion, and permission loss prevent late child, Source, or Assistant publication.

## 22. Explicit Non-Goals

- Headless browser execution;
- authenticated or paywalled Source import;
- recursive crawling;
- automatic import without human candidate selection;
- same-turn Deep Research answer generation;
- using Brave Answers as the Chat answer provider;
- treating Brave snippets as durable Source evidence;
- user-visible Agent traces or expanded queries;
- multi-provider failover in the first delivery;
- changing active Chunk or embedding parameters without offline Eval promotion;
- redesigning non-Web extractors;
- Source page preview inside Discovery.

## 23. Implementation Ordering

The later implementation plan should preserve this dependency order:

1. update product and architecture decisions;
2. add Discovery schema, RLS, state machines, and Provider Adapter;
3. deliver manual Source Discovery UI and API;
4. refactor and deliver Candidate-to-Source batch import;
5. strengthen Web normalization, quality gates, and Source provenance;
6. persist per-Chat Source selection and origin-Chat auto-selection;
7. add Leader routing and durable Research child Run orchestration;
8. connect SSE auto-open behavior;
9. add end-to-end, recovery, security, RAG, and live smoke acceptance.

Manual Source Discovery must be demonstrably usable before Agent delegation is considered complete.
