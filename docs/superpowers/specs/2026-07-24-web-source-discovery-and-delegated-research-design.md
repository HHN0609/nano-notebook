# Web Source Discovery And Delegated Research Design

**Date:** 2026-07-24

**Status:** Approved for written-spec review

## 1. Decision

Nano Notebook will add Web Source Discovery as a first-class Source workflow
and will reuse that workflow from Chat through durable Leader-to-Research
delegation.

The delivery order is deliberate:

1. an Editor or Owner can search the public web from the Sources surface,
   inspect a compact candidate list, select results, and import them;
2. every Chat turn is led by a Leader Agent that either continues normal Chat
   or delegates explicit source-discovery intent to a durable Research child
   Run; and
3. the Research child Run expands the request, creates the same private Source
   Discovery result used by manual search, and returns control to the Leader.

Search results are candidates, not Sources and not evidence. The user must
select candidates and request import. Only a successfully fetched, normalized,
persisted, indexed, and verified item becomes a Ready Source that may enter a
later Run's Evidence Set.

The first Provider implementation uses Brave Search behind a replaceable
`WebSearchProvider` Adapter. Brave snippets help a Member decide what to
import; they never become Source evidence. Selected URLs are fetched through
the existing restricted Fetcher and processed through the existing Source and
Retrieval authorities.

## 2. Context And Superseded Boundaries

The current product deliberately separates known-URL Source admission from
web search and defines the initial Research Agent as unable to browse the
internet or mutate Sources. This design promotes Source Discovery from future
scope into the next delivery slice and supersedes only those exclusions.

The following existing boundaries remain:

- the restricted Fetcher remains a fetch-only Adapter and does not become a
  search engine;
- a Search Provider never receives PostgreSQL, object-store, or Notebook
  credentials;
- the Research Agent cannot create, rename, delete, or select shared Sources;
- a discovered item must become a Ready Source before Chat may cite it;
- selected-Source Chat continues to use `search_evidence` and the existing
  grounding and publication rules;
- Chats remain private, and only imported Sources become shared Notebook
  content; and
- model chain of thought is neither captured nor exposed.

General answer-the-web Chat and full Deep Research are not introduced. This
slice discovers candidate Sources. It does not answer from hidden web context.

## 3. Goals

This design delivers:

- manual Web Search from the Add Sources surface;
- a bounded expanded Source Discovery dialog with persistent result state;
- private, durable Discovery Sessions and candidate selections;
- batch URL import with independent item success, failure, and retry;
- canonical-URL duplicate handling before and after redirects;
- a Leader Agent that performs semantic routing for every Chat turn;
- a real durable Research child Run with its own Job, Checkpoints, budgets, and
  Trace;
- bounded query expansion and multi-query result merging for Research;
- automatic presentation of Research results in Source Discovery;
- durable per-Chat Source selection;
- automatic selection of newly Ready imports in the originating Chat only;
- improved, versioned HTML primary-content cleaning;
- reuse of the existing immutable Source, Evidence, hybrid Retrieval, and
  publication pipeline; and
- deterministic acceptance coverage for security, privacy, recovery, cost,
  and RAG quality.

## 4. Non-Goals

This slice does not add:

- headless-browser or JavaScript page rendering;
- authenticated, paywalled, or cookie-bearing fetches;
- recursive crawl, link following, or site mirroring;
- Brave snippets as a substitute for a complete Source snapshot;
- automatic import without the Member's candidate selection;
- an answer generated from newly discovered candidates in the same Chat turn;
- user-visible query expansions, Agent Actions, internal reasoning, or Trace;
- Search access for Viewers;
- silent web fallback for ordinary questions or insufficient Notebook
  evidence;
- model-authored evidence rewriting;
- a new object store or vector database; or
- provider-specific types outside the Brave Adapter.

## 5. Canonical Terms

**Source Discovery Session** is one private, durable web-search result owned by
one Notebook Member. It records the original request, an optional overview,
and normalized candidate results.

**Source Candidate** is a title, public URL, plain-text snippet, ordering, and
import state returned through Source Discovery. It is not evidence.

**Leader Agent** is the user-facing Agent role for every Chat Run. It performs
the durable route decision and completes normal conversation.

**Research Agent** is a durable child Run created only for explicit
source-discovery intent. It expands queries and creates one Discovery Session.
It does not publish a Chat Message or mutate shared Sources.

**Discovery Selection** identifies candidate URLs that the Member wants to
import. It is distinct from **Chat Source Selection**, which defines which
Ready Sources a later Agent Run may search.

**WebSearchProvider** is the application-owned interface that returns bounded,
Provider-neutral search results. Brave Search is its first implementation.

## 6. User Experience

### 6.1 Manual Source Discovery

An Editor or Owner opens Add Sources and enters a search query. The server
creates a private Discovery Session and returns `202 Accepted`. A bounded
background job searches Brave and persists normalized results.

Once results are ready, the existing Add Sources dialog becomes moderately
wider and taller. It does not become a full-screen page. The expanded state
shows:

- the query;
- an optional result-set overview;
- at most ten candidates;
- a same-origin or fallback site icon;
- an external title link;
- a plain-text snippet;
- one checkbox at the far right of every row;
- Select All at the top right; and
- an Import Selected Sources action.

All importable candidates are selected by default. Existing Notebook URLs are
marked Imported and cannot be selected. External links open a new browser
context with `noopener noreferrer`. Nano provides no candidate-page preview.

The overview exists only in the expanded state. Overview generation failure
does not hide otherwise valid results.

### 6.2 Delegated Source Discovery

Every submitted Chat message enters a Leader Run. Its first durable decision
is exactly one of:

```text
continue_chat
delegate_research
```

The Leader chooses `delegate_research` only when the Member explicitly
expresses source-discovery intent, including requests to search, find,
collect, or research material to add. Ordinary questions never trigger web
search merely because selected Sources are absent or insufficient.

On delegation, the Leader creates a Research child Run and parks without
holding a Worker Lease. The Research Run expands the request into at most
three queries, calls the shared Discovery capability, merges and deduplicates
results, persists at most ten candidates, and completes with a
`discovery_session_id`.

The Leader then resumes and publishes a short response such as:

> I found some material about the requested topic and placed it in Source
> Discovery.

The response describes the type of material but does not report a numeric
count. The Run projection carries the private `discovery_session_id`, causing
the browser to load and automatically open the expanded Discovery dialog.

The turn ends there. The system does not wait for import, indexing, or a
second research answer. The Member selects and imports candidates. Newly Ready
Sources become available to later questions.

## 7. Architecture

### 7.1 Component Boundaries

```text
Browser
├─ Source Discovery UI ───────────────┐
└─ Chat UI → Leader Agent             │
               └─ Research child Run │
                                      ▼
                         Source Discovery Module
                         ├─ Discovery Store
                         ├─ Discovery Executor
                         ├─ Result Normalizer
                         ├─ Overview Composer
                         └─ WebSearchProvider
                                      │
                                      ▼
                                Brave Search API

Selected Candidate
→ URL Import Job
→ restricted Fetcher
→ immutable Source snapshot
→ Source Processor
→ Evidence Publisher
→ Retrieval Projection
→ Ready Source
```

The Source Discovery Module is the reusable center. The manual REST path and
Research child Run have different lifecycles but call the same normalization,
deduplication, persistence, and overview contracts.

### 7.2 Provider Boundary

Conceptually:

```go
type WebSearchRequest struct {
    Query          string
    Country        string
    SearchLanguage string
    ResultLimit    int
}

type WebSearchResult struct {
    Title      string
    URL        string
    Snippet    string
    FaviconRef string
    Rank       int
}

type WebSearchProvider interface {
    Search(context.Context, WebSearchRequest) ([]WebSearchResult, error)
}
```

Provider HTML highlights are stripped inside the Adapter. Provider request or
response envelopes never enter durable domain state. The Brave API key exists
only in Worker configuration.

Manual discovery issues exactly one Provider query. Research issues at most
three. Both retain at most ten normalized candidates.

### 7.3 Execution Ownership

Manual discovery creates a bounded `source_discovery_job`. A Discovery Worker
executes the Provider call and overview generation under a lease. A crash or
lease loss resumes the Session rather than abandoning `searching` state.

Research executes Provider calls as accepted, checkpointed Research Actions
inside its child Agent Job. The same normalized result may be checkpointed and
persisted idempotently without repeating an already accepted Provider result
after recovery.

Candidate import creates one independent leased import job per selected item.
This keeps up to ten public fetches out of the browser request and preserves
partial success. A successful import queues the existing Source Processing
Job.

## 8. Leader And Child Run Model

`agent_runs` gains:

- `agent_role`, constrained to `leader` or `research`;
- nullable `parent_run_id`, present only for Research Runs; and
- nullable `discovery_session_id`, populated for successful Research Runs.

One Leader may create at most one Research child in this slice. The child uses
the same private Chat and input Message for context but never receives an
`output_message_id` and cannot execute final Chat publication.

The Leader Checkpoint prefix records its route decision and child identity.
The Parent Job enters `waiting` with no Lease while the child is active; the
Leader Run remains active for the Member-facing Stop experience. Child
terminalization atomically requeues the Parent Job. User-facing active-Run
queries and Chat snapshots return Leader Runs only.

The child deadline cannot exceed the Parent's remaining absolute deadline.
Parking the Parent pauses neither the user-visible Run deadline nor its
admission-pinned budgets.

Stop, deadline, Notebook deletion, membership removal, or loss of Source
maintenance capability cascades from Parent to child. A child failure always
wakes the Parent so the Leader can publish a short safe failure response. No
failure leaves a Parent permanently waiting.

The Leader router uses a Provider-neutral tagged decision. Both/neither
variants and invalid schema fail safely; the application never defaults to web
search. `continue_chat` enters the existing Agent Controller and grounding
path.

## 9. Discovery Persistence

### 9.1 Sessions

`source_discovery_sessions` contains:

- `id`;
- `notebook_id`;
- `user_id`;
- nullable `origin_chat_id`;
- `origin`, constrained to `manual` or `research_agent`;
- the original `query`;
- nullable `summary`;
- `status`, constrained to `searching`, `ready`, or `failed`;
- nullable `research_run_id`;
- nullable safe `error_code`; and
- timestamps.

### 9.2 Candidates

`source_discovery_candidates` contains:

- `id` and `session_id`;
- zero-based `ordinal`;
- `title`, `canonical_url`, `display_url`, and plain-text `snippet`;
- optional same-origin `favicon_ref`;
- `selected`, defaulting to true;
- `status`, constrained to `discovered`, `importing`, `imported`, or
  `import_failed`;
- nullable `source_id` and safe `import_error_code`; and
- timestamps.

The Session owns no shared content. RLS requires the current principal to be
the exact `user_id`, remain a Notebook Member, and hold Editor or Owner Source
maintenance capability. A Member cannot read another Member's Session.

Member removal or Notebook deletion follows private-Chat lifecycle and deletes
the Member's Sessions. Sources already imported from those Sessions remain
shared until explicitly deleted through the normal Notebook lifecycle.

## 10. Member APIs

```text
POST  /api/v1/notebooks/{notebook_id}/source-discovery-sessions
GET   /api/v1/notebooks/{notebook_id}/source-discovery-sessions/latest
GET   /api/v1/source-discovery-sessions/{session_id}
PATCH /api/v1/source-discovery-sessions/{session_id}/selection
POST  /api/v1/source-discovery-sessions/{session_id}/retry
POST  /api/v1/source-discovery-sessions/{session_id}/imports
POST  /api/v1/source-discovery-candidates/{candidate_id}/retry-import
GET   /api/v1/chats/{chat_id}/source-selection
PATCH /api/v1/chats/{chat_id}/source-selection
```

Every mutation requires CSRF protection. Creation and import require an
Idempotency Key. Research-origin Sessions can be created only through the
internal Agent boundary; a browser cannot declare `origin=research_agent` or
attach a Run.

Selection updates replace the complete selected Candidate set atomically.
Import uses the server-persisted selection and returns `202 Accepted` after
independent item jobs are queued. The Session response includes normalized
candidate and Source lifecycle state but no Provider envelope.

Chat Source Selection updates likewise replace the complete selected Ready
Source set after server-side authorization. The Chat snapshot includes
`selected_source_ids`, so reload and another browser observe the same evidence
scope.

Run SSE projection gains an optional `discovery_session_id`. Chat Message text
remains ordinary text and does not embed hidden UI commands or candidate data.

## 11. Candidate Normalization And Duplicate Policy

Only HTTP(S) URLs without user information are accepted. Fragment identifiers
are removed. Scheme and host are lowercased; default ports and equivalent
empty paths are canonicalized; tracking parameters may be removed only by a
versioned allowlist. The canonicalizer never changes semantic query parameters
using heuristics.

Normalization then:

1. rejects invalid titles, URLs, snippets, and over-budget fields;
2. strips markup and normalizes whitespace as plain text;
3. preserves Provider rank;
4. removes same-Session canonical-URL duplicates;
5. marks URLs already imported into the Notebook; and
6. applies bounded domain diversity and Research relevance ranking before the
   ten-result limit.

Duplicate checking occurs again after the Fetcher observes a final redirect
URL. A database-enforced canonical final-URL key permits at most one active URL
Source per Notebook. Redirect collisions reuse the existing Source identity
for presentation and never create a second Source. Similar titles or content
hashes alone do not merge distinct public URLs.

## 12. Candidate Import And Source Admission

The browser submits Candidate identities, never replacement URLs. The server
locks the selected rows, verifies the current private owner and Source
maintenance capability, and creates independent import jobs.

Each import executes:

```text
candidate discovered/import_failed
→ importing
→ idempotent URL Admission
→ restricted Fetcher
→ media-type, size, and SHA-256 validation
→ immutable object write
→ final-URL duplicate check
→ Source + Source Processing Job creation
→ candidate imported with source_id
```

The Candidate reaches `imported` only in the transaction that commits a real
Source or resolves a final-URL collision to an existing Source. Failed fetches
leave the Candidate as `import_failed` with a safe retry code. Successful
siblings never roll back.

Brave snippets and result overviews are excluded from Source payloads. Public
HTML, supported documents, supported media, and captioned YouTube continue
through existing format admission. JavaScript-only pages, login walls,
paywalls, unsupported media, and videos without usable captions fail that
Candidate.

## 13. Immutable Snapshot And Provenance

For every newly admitted URL Source, the application persists:

- requested origin URL;
- observed final URL and its canonical key;
- media type and byte length;
- content SHA-256;
- fetch completion time;
- the immutable original object key;
- Source format and sanitized title; and
- the private Candidate-to-Source association.

The local object store remains the existing MinIO-backed S3 Adapter. The
`nano-sources` bucket stores originals, normalized artifacts, and existing
Viewer assets. This design introduces no new Blob Store.

## 14. HTML Cleaning

The current HTML normalizer is upgraded to a versioned `html-primary-v2`
configuration. Cleaning is evidence extraction, not summarization.

The pipeline:

1. decodes a bounded supported character set into valid UTF-8;
2. parses a DOM under depth, node-count, input-byte, and output-rune budgets;
3. removes executable, styled, hidden, form, navigation, header, footer,
   advertising, and repeated-template nodes;
4. finds primary-content candidates using semantic elements, text density,
   link density, block length, and repeated-template signals;
5. preserves headings, paragraphs, lists, tables, code, and quotations in
   source order;
6. optionally asks a model to label only ambiguous input blocks as keep or
   drop with a bounded reason;
7. rejects model output that adds, rewrites, reorders, or expands the supplied
   blocks;
8. normalizes entities and whitespace, deduplicates identical blocks, and
   assigns stable block order, type, coordinate, and content checksum; and
9. produces complete or partial Evidence Coverage.

The raw snapshot never changes. A model may filter but never paraphrase,
complete, or correct evidence. Empty primary content, suspected login/error
pages, invalid structure, lost primary content, or budget overflow fails the
Source safely.

Deterministic non-primary removal is not an Evidence gap. Ambiguous omitted
content that may matter becomes a typed Coverage Gap. A cleaning-configuration
change creates a new Evidence Revision and rebuilds Retrieval projection; it
does not mutate an existing Revision or historical Citation.

Other Source formats continue through their existing native extractors.

## 15. Storage Authority

```text
MinIO / S3
├─ sources/{source}/original/{sha256}
├─ sources/{source}/evidence/{revision}/normalized.json
└─ existing rendered Viewer artifacts

PostgreSQL
├─ Source and URL admission lifecycle
├─ Discovery Sessions and Candidates
├─ Candidate-to-Source provenance
├─ Evidence Revisions, Units, Coverage, and gaps
├─ per-Chat Source selection
├─ Retrieval Index Versions and verified build records
├─ Run Evidence Sets, Checkpoints, and Citations
└─ deletion and publication authority

Qdrant
└─ rebuildable dense/sparse Chunk projections and scoped identifiers
```

PostgreSQL remains the evidence-text and authorization authority. Qdrant does
not become a Source content store. Retrieval results must be reauthorized and
reloaded from PostgreSQL before reaching the Agent.

## 16. Durable Chat Source Selection

The current browser-local selection is replaced with durable
`chat_source_selections` state containing `chat_id`, `source_id`, selection
state, and update time.

- A new Chat selects the Ready Sources that exist at Chat creation.
- A later Source does not silently enter unrelated Chats.
- A Discovery Session records the private `origin_chat_id` visible when the
  search began.
- When an imported Source becomes Ready, it is selected automatically in that
  originating Chat only.
- A failed Source is never selected.
- A Member's later manual deselection is authoritative.
- Chat selection changes affect later Runs only.

At Run admission, the server intersects the Chat's selected identities with
currently authorized Ready Sources and freezes the resulting Evidence
Revisions and active Retrieval Index Version in the Run Evidence Set.

## 17. Evidence, Chunking, And Projection

The normalized structural blocks become authoritative Evidence Units in
PostgreSQL. A Citation addresses Evidence, not a Retrieval Chunk.

The active Retrieval Index Version deterministically builds structure-aware
overlapping Chunks:

- no Chunk crosses a Source or Evidence Revision;
- headings remain with following content where budgets permit;
- lists, tables, and code remain intact until an oversized Unit requires a
  versioned split;
- every Chunk retains exact Evidence Unit references; and
- Chunk identity derives from Index Version, Revision, ordinal, text, and
  references.

The current development configuration of approximately 800 runes with 120
runes overlap remains until an offline Eval promotes a replacement. Search-
imported HTML fixtures must be added to that Eval before changing Chunking.

Each Chunk receives:

- a dense vector using the active Gemini document Embedding profile, currently
  768 dimensions and formatted with Source title plus Chunk text; and
- a classic BM25 sparse vector using the existing Chinese/English mixed
  analyzer.

Qdrant payload contains Chunk, Notebook, Source, Revision, Index Version, Unit
identities, and a checksum. It does not contain authoritative Source text.

## 18. Ready Gate

A Source becomes Ready only after:

1. original size and content hash verification;
2. normalized Artifact schema, coordinate, ordering, coverage, UTF-8, and hash
   verification;
3. non-empty Evidence Revision and Evidence Units publication;
4. deterministic Chunk construction;
5. complete dense and sparse projection;
6. Qdrant point-count, dimension, payload-scope, and checksum verification;
7. confirmation that the pinned Retrieval Index Version remains active; and
8. confirmation that the Source Job Lease remains valid.

Failure at any stage keeps the Source outside RAG. Source Processing is
resumable and never refetches a successfully persisted immutable URL snapshot.

## 19. Query-Time RAG

For a later normal Chat turn:

```text
durable Chat Source selection
→ authorized Ready Source intersection
→ frozen Run Evidence Set
→ Leader continue_chat
→ required first search_evidence
→ contextualized query
→ scoped Dense + BM25 candidates
→ deterministic RRF
→ PostgreSQL authorization and Evidence reload
→ bounded rerank
→ optional refined search_evidence calls
→ plain-text final with valid [source:<source_id>] markers
→ Publication Barrier
→ Assistant Message and Source Citations
```

Brave snippets, Discovery summaries, unimported Candidates, Processing
Sources, Failed Sources, unselected Sources, and Sources added after Run
admission are excluded.

The final publication path retains only markers backed by accepted Evidence
and current Source authorization. Deletion, permission loss, cancellation, or
an invalid Evidence Revision prevents publication. Existing precise historical
Citations remain readable under their prior contract; new Runs follow the
current Source-reference contract.

## 20. Quality Gates

Source quality uses hard deterministic rejection and soft warnings.

Hard failures include:

- no usable primary content;
- content below the minimum evidence budget;
- a page consisting almost entirely of links or templates;
- detected login, access-denied, challenge, or error pages;
- invalid character encoding or DOM structure;
- abnormal repeated content;
- lost primary content; and
- any configured processing-budget violation.

Soft warnings include:

- thin but usable primary text;
- ambiguous block-classification boundaries;
- known non-primary gaps;
- missing publication metadata; and
- low cleaning confidence that still satisfies evidence invariants.

Soft warnings permit Ready state and appear in the Source Viewer as
Coverage/Quality warnings. Model quality scores may influence candidate
ranking and developer Trace but are never the only Ready gate. Query-time
hybrid Retrieval and reranking still decide whether Evidence is relevant to a
question.

## 21. Failure And Cancellation Semantics

- Search timeout, rate limiting, or Provider unavailability fails the Session
  safely and permits retry.
- Empty valid search completes Ready with an empty candidate list and a
  localized no-results presentation; it does not create Sources.
- Overview failure leaves results Ready without an overview.
- One import failure does not roll back siblings.
- Import and Source Processing errors have separate safe states and retry
  actions.
- Research failure wakes the Parent and yields a short retryable Leader
  response.
- Invalid Leader routing never defaults to web access.
- Worker crash and Lease loss resume from the first incomplete durable state.
- Stop and authority loss cancel active Leader, Research, Discovery, and import
  work before further external calls or publication.
- A Source that becomes Ready after a Run starts cannot enter that Run.

## 22. Budgets And Configuration

Initial fixed bounds:

- manual Provider queries per Session: 1;
- Research-expanded queries: at most 3;
- normalized candidates retained: at most 10;
- one bounded overview model call per Session;
- Brave LLM Context: not used for Source admission or RAG;
- candidate URL length: at most 4,096 characters;
- Provider and model responses: fixed byte and rune limits;
- external calls: timeouts, bounded transient retries, and rate-limit-aware
  backoff; and
- import concurrency: governed by the existing interactive workload capacity.

Leader routing, Research expansion, and overview composition use separately
configured model capabilities and versioned prompts. No concrete model is
hardcoded into domain behavior.

Required server configuration includes a Brave endpoint, API key, request
timeout, result limit, country/language defaults, and safe-search policy.

## 23. Security And Privacy

- Viewer requests to create, retry, or import Discovery state are rejected.
- Sessions and candidates are private to the exact Member even inside a shared
  Notebook.
- Search and model Providers receive no Notebook credentials or unrelated
  private content.
- Provider text is untrusted data. It cannot instruct the Agent or change its
  action policy.
- Browser rendering escapes all text and never injects Provider markup.
- The browser never loads arbitrary candidate resources. Favicons use a
  bounded same-origin proxy with an allowlist and a generic fallback.
- Candidate import still passes through SSRF, redirect, DNS rebinding,
  decompression, content-type, size, and timeout defenses.
- Search query, expansion, and normalized result metadata enter restricted
  Trace/Replay policy; raw Provider envelopes and chain of thought do not.
- Candidate external links do not grant trust or import authority.
- RAG filters are constructed from server-pinned Evidence, never client-sent
  Qdrant filters.

## 24. Observability

Developer Trace records bounded metadata for:

- Leader route outcome and prompt version;
- Parent and child Run identity;
- query-expansion count and normalized hashes;
- Provider name, latency, status, rate-limit outcome, and result count;
- normalization, duplicate, and domain-diversity counts;
- overview success or degradation;
- Session and import state transitions;
- fetch media type and safe failure class;
- HTML cleaning configuration, kept/dropped block counts, coverage, and
  quality gates;
- Chunk count, embedding latency, projection verification, and Ready outcome;
  and
- automatic originating-Chat selection.

Trace metadata does not expose Source or Chat bodies. Bounded query, snippet,
Evidence, and model payloads remain available only through existing audited,
retention-bounded Replay.

## 25. Testing

### 25.1 Unit And Contract Tests

- Brave request encoding, response mapping, markup stripping, timeout, rate
  limit, malformed response, and over-budget response;
- Leader tagged decision parsing and fail-closed routing;
- Research query expansion, three-query budget, merge, canonicalization,
  ordering, domain diversity, and ten-result bound;
- URL canonicalization before and after redirects;
- deterministic HTML cleaning, ambiguous-block classification validation,
  no-rewrite enforcement, quality gates, and Coverage gaps;
- Chat Source Selection semantics; and
- Provider-neutral behavior through a second fake Adapter.

Regular tests use deterministic Brave fixtures. An explicit live smoke test is
available only when a real key is configured.

### 25.2 Database And Authorization Tests

- Session/candidate state constraints and RLS;
- cross-Member and cross-Notebook denial;
- Viewer denial and Editor/Owner success;
- idempotent Session creation and batch import;
- canonical final-URL collision;
- independent Candidate partial success;
- Candidate-to-Source commit atomicity;
- automatic selection in the originating Chat only;
- member-removal and Notebook-deletion cleanup; and
- imported shared Source survival after private Session removal.

### 25.3 Agent And Recovery Tests

- `continue_chat` preserves current grounded and source-less behavior;
- explicit source-discovery intent creates one Research child;
- ordinary insufficient evidence performs no web call;
- Parent waits without a Lease and resumes after child completion;
- child does not publish a Chat Message;
- accepted search results are not repeated after recovery;
- child failure always wakes Parent;
- Stop and authority loss cascade; and
- retry creates the correct new Leader and child identities.

### 25.4 API And UI Tests

- asynchronous manual search and reload recovery;
- latest private Session restoration;
- bounded expanded dialog, never full screen on desktop;
- overview visible only when expanded;
- right-aligned per-row checkboxes and top-right Select All;
- default selection, persistent selection, and import action;
- external safe links and no candidate preview;
- Imported, Importing, Failed, Retry, Processing, Ready, and Source Failed
  presentations;
- Research completion automatically opens the Session; and
- Leader response describes material without a numeric result count.

### 25.5 Retrieval And Eval Tests

- search-imported HTML, PDF, and captioned YouTube fixtures complete the Ready
  gate;
- noisy HTML retains primary evidence and drops templates;
- imported Sources remain absent before Ready and present after originating-
  Chat selection;
- Qdrant scope cannot expand beyond the frozen Evidence Set;
- PostgreSQL Evidence reload prevents stale or unauthorized results;
- mixed Chinese/English retrieval, RRF, and reranking quality;
- Source-reference precision and answer quality; and
- latency, cost, Source-job capacity, and Provider-budget gates.

## 26. Acceptance Journeys

The slice is accepted when all of these journeys pass:

1. An Editor manually searches for `怎么拍电影`, sees at most ten linked
   candidates in the moderately expanded dialog, deselects some, and imports
   the remainder.
2. Successful items create immutable Source snapshots and independently reach
   Ready; an inaccessible sibling fails without rollback and can be retried.
3. Ready imported Sources are selected only in the originating private Chat.
4. A later question searches the imported Sources through scoped Dense and
   BM25 Retrieval, reloads authoritative Evidence, and publishes valid Source
   Citations.
5. An Editor asks Chat to collect filmmaking material. The Leader creates a
   real Research child Run, which expands queries and produces a private
   Session. The Leader gives a generic material description and the UI opens
   Discovery automatically.
6. The Research turn does not import, index, or answer from candidates. The
   Member selects and imports them explicitly.
7. An ordinary question never calls Brave, even when Notebook evidence is
   absent or insufficient.
8. A Viewer cannot search, delegate web Research, or import. Two Editors cannot
   see one another's private Sessions.
9. Reload, Worker restart, Lease loss, Stop, partial Provider failure, partial
   Fetcher failure, role downgrade, member removal, and Notebook deletion
   preserve the defined durable and privacy boundaries.

## 27. External References

- [Brave Web Search documentation](https://api-dashboard.search.brave.com/app/documentation/web-search/get-started)
- [Brave LLM Context documentation](https://api-dashboard.search.brave.com/documentation/services/llm-context)
