# Research PDF persistence as permanent Sources

## Context

Research currently discovers URLs through Brave and may call `read_url` on a
PDF. The implemented PDF path safely acquires the file, stores run-scoped
original and normalized artifacts, and returns a large page-marked Markdown
preview plus a `document_handle`. This proves that Nano can read remote PDFs,
but it puts too much paper text into Action checkpoints and Research context.
The completed embodied-AI safety run showed the resulting capsules and rollups
became prefix-biased and URL-heavy.

The desired product boundary is different:

- Web Search is discovery. The model sees titles, URLs, and bounded provider
  descriptions, not PDF body text.
- A useful PDF is explicitly imported as a permanent Notebook Source.
- Source processing parses, segments, embeds, verifies, and publishes the PDF
  asynchronously.
- The Research model sees PDF body evidence only through `search_evidence`
  after the Source is Ready.

This design covers only PDF persistence and retrieval admission. Context
compaction, Capsule compression, and removal of periodic Research rollups are
specified separately.

## Decision

Replace model-visible remote PDF reading with an explicit Source import path:

```text
web_search
    -> title + URL + bounded discovery description
    -> save_url_as_source(url)
       -> bounded public acquisition
       -> immutable original object
       -> permanent Source + queued processing job
       -> immediate accepted result
    -> asynchronous existing Source pipeline
       -> normalize -> Evidence Units -> Qdrant projection -> verification
       -> Source Ready
    -> search_evidence(query)
       -> bounded relevant original passages with Source/chunk/page authority
```

`save_url_as_source` succeeds when the permanent Source and its processing job
have been durably created. It does not wait for extraction or indexing. The
result must say `searchable: false` until the existing Source lifecycle reaches
`ready` with a verified projection against the active Retrieval Index Version.

Research automatically tracks imports started by the Run. It may continue
other useful work while they process. Before final report generation, the
Harness waits for every Research-imported Source to reach a terminal Ready or
Failed state without spending model calls on polling.

## Goals

- Make valuable Research PDFs permanent, member-visible Notebook Sources.
- Keep PDF body text out of `web_search`, `read_url`, Research Action results,
  and uncompressed Capsules.
- Reuse the existing Source processing, Evidence Revision, Source Admission,
  embedding, Qdrant verification, retry, and purge lifecycles.
- Return from the import Action as soon as durable Source processing has been
  admitted, rather than blocking on indexing.
- Make the import idempotent across Action replay, duplicate URL forms, and
  concurrent attempts to save the same PDF.
- Ensure `search_evidence` is the first model-visible boundary for PDF body
  evidence and exposes only Ready, authorized, verified Source content.
- Allow final Research reporting only after its requested imports are terminal.

## Non-goals

- Context Rollup or Capsule compaction redesign.
- Automatically importing every Brave candidate.
- Treating Brave descriptions as Source Evidence.
- Exposing raw PDF bytes, object-store keys, or normalized artifacts to the
  model.
- Adding remote DOCX, PPTX, audio, video, authenticated, paywalled, or
  CAPTCHA-protected imports.
- Making Qdrant authoritative for Source state or citation validity.
- Migrating old run-scoped Research PDF artifacts into Sources retroactively.

## Tool boundaries

### `web_search`

No acquisition or evidence behavior changes. It returns bounded discovery
metadata and records URLs as discovered Research candidates. Its descriptions
are leads, not evidence and not citable facts.

### `read_url`

`read_url` remains available for bounded HTML reading. When the acquired media
type is PDF, it must not return PDF Markdown or a model-usable document handle.
It returns a stable domain outcome such as `pdf_requires_source_import` with
the requested/final URL and safe metadata required to guide the model toward
`save_url_as_source`.

If `read_url` had to acquire bytes before discovering that the response is a
PDF, the Harness may retain a run-scoped acquisition receipt keyed by canonical
requested/final URL and content hash. A later `save_url_as_source` call reuses
those verified bytes instead of downloading the same PDF again. The receipt is
not a model-readable body handle and cannot bypass Source admission.

The Research prompt explicitly states that a PDF fact is unavailable until the
PDF is imported and queried through `search_evidence`. This rule is enforced by
the Action boundary, not only by prompt wording.

### `save_url_as_source`

Initial model-facing input:

```json
{
  "url": "https://example.org/paper.pdf"
}
```

The tool does not accept a title, parser, media type, Notebook ID, Source ID,
object key, embedding model, or indexing policy from the model. Notebook,
member, Chat, Research Session, Run, active Source Admission policy, and active
Retrieval Index Version are resolved from trusted execution authority.

Successful output:

```json
{
  "source_id": "src_...",
  "processing_job_id": "srcjob_...",
  "state": "processing",
  "searchable": false,
  "reused": false,
  "final_url": "https://example.org/paper.pdf"
}
```

If the same canonical final URL or exact PDF content already exists in the
Notebook, the tool returns that Source with `reused: true`. Its current state
and searchability are reported truthfully. Reuse never creates another Source
or processing job.

The Action may execute concurrently for different URLs. It is eligible for
parallel scheduling only because Source admission uses deterministic Action
identity, canonical URL identity, content SHA-256, Notebook-scoped locking,
and idempotent replay. If those guarantees cannot be established in the first
implementation, ship it as ordered synchronous scheduling first.

### `search_evidence`

PDF text becomes model-visible only through this tool. Retrieval continues to
require all existing authorities:

- Source belongs to the current Notebook and member is authorized;
- Source state is `ready`;
- Evidence Revision is active;
- Qdrant build for the active Retrieval Index Version is `verified`; and
- PostgreSQL Evidence reload succeeds for every Qdrant candidate.

The result should carry Source ID, Evidence Revision ID, chunk ID, bounded
original text, and page coordinates when the PDF extractor provides them.
Qdrant remains a rebuildable candidate index; PostgreSQL and immutable
artifacts remain authoritative.

The ordinary query form searches all Ready selected Notebook Sources. A later
extension may accept a Source filter, but pending Sources must never appear as
empty evidence. An explicit query against a pending Source returns
`source_not_ready` with its lifecycle state.

## Acquisition and persistence

The TypeScript web-reader remains the sole public-network boundary. The import
Action uses its bounded media-aware acquisition path so DNS, redirects, SSRF,
timeouts, decompression, MIME checks, byte ceilings, and PDF-signature checks
remain centralized.

For an accepted PDF:

1. Safely acquire and classify the final response.
2. Compute content SHA-256 while streaming.
3. Write the immutable original PDF to the permanent Source object namespace.
4. Complete URL Source admission transactionally, creating the Source and a
   queued `source_processing_job`.
5. Record the Research import relation.
6. Return the accepted result immediately and notify Source workers.

The import result is not returned until original-object integrity and the
Source/job transaction have succeeded. Extraction and indexing occur after the
return. This distinguishes asynchronous processing from an unsafe fire-and-
forget download.

The existing Source pipeline remains responsible for native PDF extraction,
PDFium/vision fallback, normalized Artifact validation, Evidence Unit
publication, Source Admission, chunking, embedding, Qdrant projection, point
count verification, Evidence Revision activation, and the terminal transition
to `source_sources.state='ready'`.

## Identity and idempotency

The current run-scoped `rdoc_*` handle is no longer a model-facing PDF reading
capability. An internal acquisition identity may remain to reuse bytes across a
retry, but it is not the permanent Source identity and is never accepted by
`search_evidence`.

Permanent identity uses the existing Source authorities:

- canonical requested and final URL identities;
- original PDF content SHA-256;
- Notebook-scoped Source ID;
- processing job ID;
- Evidence Revision ID; and
- active Retrieval Index Version ID.

Replay cases:

- Same Action identity and accepted output returns the checkpointed result.
- Crash after object write but before Source creation reuses the content-
  addressed object and retries admission.
- Crash after Source/job commit but before Action result reloads the existing
  Notebook Source by canonical final URL/content identity and returns it.
- Concurrent equivalent imports serialize on the existing Notebook Source
  admission lock and converge on one Source.

## Research import relation

Add a durable relation rather than copying Source lifecycle status into
Research state. Suggested shape:

```text
research_source_imports
- session_id
- run_id
- action_id
- requested_url
- final_url_identity
- source_id
- processing_job_id
- created_at
```

Constraints include unique `(run_id, action_id)` and a Notebook-safe uniqueness
rule that makes a repeated Action identity deterministic. Source/job state is
always joined from the Source tables; it is not duplicated in this relation.

This relation supports:

- compact `Pending Source Imports` projection for the model;
- automatic final-report waiting;
- Trace and product diagnostics;
- authorization-safe recovery; and
- proving which permanent Sources were intentionally admitted by a Research
  Run.

Deleting a Research Run does not delete a promoted Source. Deleting a Source
uses the existing Source purge lifecycle. The relation uses cascading foreign
keys for Research Session/Run deletion, while Source and processing-job foreign
keys are nullable with `ON DELETE SET NULL`; requested/final URL identity and
Action identity remain sufficient to explain the historical import while the
Research relation exists.

## Asynchronous lifecycle and waiting

The import Action result uses precise language:

```text
accepted != ready != searchable
```

After an accepted import, each Research model request receives a small,
code-owned state projection such as:

```text
Pending Source Imports:
- src_123: processing, not searchable
- src_456: ready, searchable
- src_789: failed, extraction_invalid
```

This projection contains identifiers and lifecycle state only, never PDF body
text or worker logs.

The model is not expected to poll. If useful tools remain, it may continue
discovery, import, HTML reading, report planning, or queries over already Ready
Sources. When the Research Run reaches a final-report boundary with pending
imports, the Harness:

1. records explicit Source dependencies for the Run;
2. moves the Agent Job to a waiting-for-sources state and clears its lease;
3. spends no model calls while waiting; and
4. requeues the Agent Job when every dependency is terminal or the Run deadline
   is reached.

Ready and Failed are both terminal for the barrier. Failed imports are shown to
the model with stable reason codes and cannot support claims. Deadline expiry
uses the existing Run deadline terminal behavior rather than waiting forever.

Before final report generation, the Harness rebuilds Research context after the
barrier so every newly Ready Source can be retrieved. Report generation does
not automatically inject full Source text; the model must have obtained the
supporting passages through `search_evidence`.

## Product behavior

- The Source appears in the Notebook immediately after the import Action
  succeeds, with its real processing state.
- Existing realtime Source notifications update the UI as processing advances.
- A Research-created Source is permanent and member-manageable like any other
  URL Source.
- Once Ready, it is selected for the originating Chat with `explicit=false`, so
  a later explicit member selection still wins.
- Import failure is visible in both the Source UI and Research pending-import
  projection. The member may use existing retry or delete behavior.

Research prompts should recommend importing substantive PDFs, not every search
result. Suitable candidates are primary papers likely to support a planned
claim or comparison. Quotas and Source Admission remain authoritative and may
reject or pause an import.

## Failure semantics

- Unsafe destination, redirect, DNS failure, timeout, oversized response,
  invalid PDF signature, or unsupported media type fails before Source
  creation with the existing bounded acquisition error.
- Object write failure creates neither Source nor processing job.
- Source/job transaction failure is retryable only under existing stable
  infrastructure classifications.
- Processing failure after Action success is a Source lifecycle failure, not a
  retroactive mutation of the accepted Action checkpoint.
- Admission `review_required` is treated as a terminal non-searchable outcome
  for the current Research barrier; the model cannot override Source Admission.
  A later member approval may resume Source processing outside that completed
  Research decision.
- Quota exhaustion returns a stable domain error and does not create a hidden
  or run-scoped substitute.
- A pending or failed Source is never silently treated as an empty successful
  retrieval result.

## Security and authorization

- The model cannot choose a Notebook, owner, Source ID, object key, or index
  configuration.
- Import authority is derived from the Research Session's member, Notebook,
  and accepted Run.
- The public acquisition sidecar receives no database, member-cookie, or
  durable object-store credentials.
- Raw bytes never enter PostgreSQL payloads, model messages, Action results, or
  Trace attributes.
- `search_evidence` reloads and authorizes PostgreSQL Evidence after Qdrant
  candidate retrieval.
- Source deletion and access revocation immediately remove retrieval authority
  even if stale Qdrant points remain temporarily.

## Migration and rollout

This change intentionally supersedes the model-facing PDF portion of
`2026-08-31-research-remote-pdf-reading-design.md`:

- PDF acquisition and shared extraction remain reusable implementation work.
- Run-scoped PDF body previews and `read_document_pages(document_handle, ...)`
  are removed from the Research model tool surface.
- PDF evidence becomes permanent Source evidence and is accessed through
  `search_evidence`.
- HTML `read_url` remains unchanged.

Ship behind a new immutable Agent Definition/Release. Existing completed Runs
retain their accepted checkpoints and old tool contracts for replay. New Runs
use only the new contracts; no historical checkpoint is rewritten.

Recommended delivery order:

1. Add Research import relation and status projection.
2. Add `save_url_as_source` using existing URL Source admission and processing.
3. Enforce PDF Source-first behavior in `read_url` and the Research prompt.
4. Add automatic waiting and Source terminal notifications.
5. Add page-aware PDF Evidence retrieval and citation validation.
6. Cut a new Agent Definition/Release and run a live multi-PDF Research test.

## Acceptance criteria

### Tool and persistence

- A Brave-discovered PDF can be passed to `save_url_as_source`.
- The Action returns after Source/job commit without waiting for indexing.
- The Source is immediately visible as processing and remains after Run
  deletion.
- Replay and duplicate URL forms converge on one Notebook Source.
- No PDF bytes or full body text appear in the Action result/checkpoint.

### Readiness and retrieval

- The Source becomes `ready` only after active-index Qdrant verification and
  Evidence Revision activation.
- `search_evidence` cannot return a pending, failed, unauthorized, deleted, or
  unverified Source.
- After Ready, a query retrieves bounded original PDF passages with Source,
  Evidence Revision, chunk, and page authority.
- `read_url` cannot expose PDF body text or a model-usable document handle.

### Research orchestration

- The model receives compact pending/ready/failed import state without worker
  logs or document bodies.
- Research continues useful work while imports process.
- Final reporting waits without model polling until all imported Sources are
  Ready or Failed.
- Failed imports are visible and cannot be cited.
- A completed report's PDF-supported claims trace only to successful
  `search_evidence` results over Ready Sources.

### Regression and live proof

- Existing member URL/File Source creation, Source Admission, retry, deletion,
  purge, and Retrieval Index Version tests continue to pass.
- HTML `read_url` behavior remains unchanged.
- Old Agent Releases remain replayable against their pinned tool contracts.
- A live Research run discovers multiple academic PDFs, imports them, observes
  asynchronous readiness, retrieves evidence, and publishes a report without
  any PDF body text entering pre-retrieval context.
