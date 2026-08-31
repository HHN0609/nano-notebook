# Research remote PDF reading

## Context

Deep Research currently separates discovery from evidence reading. `web_search`
uses Brave to return bounded candidates containing title, URL, description, and
rank. Those candidates enter `research_evidence_ledger` as `discovered`, but
their provider snippets are not evidence. The Research executor must call
`read_url` successfully before a public URL may support a report claim.

`read_url` currently delegates every URL to the TypeScript web-reader
`POST /v1/parse` contract. That service accepts only HTML and XHTML, extracts
the readable page with the lightweight or browser engine, and returns cleaned
Markdown. A direct PDF response has media type `application/pdf`, so web-reader
returns `unsupported_type`; the Action becomes `read_url_failed`, and the
ledger URL becomes `failed`.

Nano already has a separate uploaded-file pipeline that can extract native PDF
text, render pages with PDFium, use the configured vision model for pages
without usable text, and publish page-coordinate Evidence Units. The missing
capability is a safe bridge from a public PDF URL to reusable document
extraction inside a Research Run.

## Decision

Keep Brave and `web_search` as discovery-only components. Extend the semantic
boundary of `read_url` so it reads supported public content independently of
whether the result is HTML or PDF:

```text
Brave web_search
    -> candidate URL
    -> read_url
    -> public URL acquisition and media-type routing
       -> HTML: existing TypeScript HTML extraction
       -> PDF: bounded binary acquisition and shared PDF extraction
    -> one normalized Research read result
    -> research_evidence_ledger status=read
```

A remotely read PDF is run-scoped Research evidence. It does not create a
member-visible Notebook Source. A future explicit promotion flow may create or
reuse a durable Source, but that product operation is outside this change.

## Goals

- Let Deep Research read a direct public PDF URL returned by Brave or found in
  an already-read HTML page.
- Preserve one model-facing `read_url` tool so the model does not select a
  parser or reason about media types.
- Preserve the TypeScript web-reader as the sole public-network acquisition and
  SSRF enforcement boundary.
- Reuse Nano's existing PDF normalization, rendering, and vision semantics
  instead of creating a second PDF parser in TypeScript.
- Produce page-aware, traceable evidence that can support Research report
  citations under the same successful-read rule as HTML.
- Keep automatic Research reads isolated from the Notebook Source list.

## Non-goals

- Automatically importing every discovered PDF into the Notebook.
- Adding a member-facing operation that promotes Research artifacts into
  durable Notebook Sources.
- Treating Brave descriptions or search result snippets as evidence.
- Recursively crawling all links on an academic landing page.
- Adding authenticated, paywalled, CAPTCHA-protected, or cookie-bearing fetches.
- Supporting remote DOCX, PPTX, audio, video, or arbitrary binary formats in
  this change.
- Special-casing arXiv URL shapes. arXiv is an acceptance fixture, not an
  architectural dependency.
- Sending PDF URLs directly to one model provider as the authoritative product
  path.

## Considered approaches

### 1. Parse PDF inside the TypeScript web-reader

The sidecar could add a JavaScript PDF library and return Markdown directly.
This keeps the wire contract simple, but duplicates the native-text, PDFium,
vision fallback, page-coordinate, and validation behavior already implemented
in Go. The two ingestion paths would drift and produce different evidence for
the same document.

### 2. Media router with shared PDF extraction

The selected approach keeps `read_url` as a format-neutral facade. The
TypeScript sidecar safely acquires public bytes and identifies the effective
media type. HTML stays in its existing extraction path. PDF bytes cross a
bounded internal contract and are passed to a Go document reader factored from
the existing Source extraction components.

This preserves the public-network security boundary, avoids parser duplication,
and keeps model and ledger contracts independent of content type.

### 3. Provider-native URL documents

Gemini or Claude could fetch and understand the PDF from its URL. This is a
useful fallback or experiment, but not the primary architecture: it binds
content acquisition and evidence semantics to a model provider, weakens Nano's
control over SSRF, byte limits, hashing, page evidence, replay, and failure
classification, and complicates multi-provider behavior.

## Component boundaries

### Brave provider and `web_search`

No behavior changes. Brave returns provider-neutral candidates. `web_search`
records them as `discovered` and exposes their metadata to the Research model.
It does not prefetch candidates, infer file type from URL suffixes, or call a
document parser.

### Model-facing `read_url`

The Action name and input remain:

```json
{"url":"https://example.org/paper.pdf"}
```

The Action depends on a new format-neutral `URLContentReader` interface rather
than directly on the HTML-only `webreader.Adapter`. The model cannot request an
engine, media type, parser, byte limit, or persistence policy.

The successful output remains bounded Markdown plus source identity and read
diagnostics. It adds stable fields for media type, PDF page count, and an opaque
same-Run `document_handle`. PDF Markdown carries explicit page boundaries so
compacted capsules and report writers retain page provenance without receiving
raw PDF bytes or object-store keys.

### TypeScript public URL acquisition

The web-reader remains the only component allowed to connect to the supplied
public URL. Its existing HTTP(S)-only, DNS, redirect, timeout, decompression,
byte, and concurrency controls continue to apply.

The sidecar gains a versioned internal acquisition contract capable of
returning either:

- the existing cleaned HTML result; or
- validated PDF bytes plus requested URL, final URL, status, media type, byte
  count, redirect count, and SHA-256.

The contract must be streamable and must not base64-expand the PDF in JSON. It
uses a versioned `multipart/mixed` response: the first part is strict JSON
metadata and the optional second part is the raw PDF byte stream. The Go
adapter rejects missing, duplicate, reordered, unknown, or oversized parts.
`/v1/parse` remains backward compatible for current HTML Source imports and
HTML fallback processing.

PDF classification requires both an allowed `application/pdf` response media
type and a valid PDF signature. A `.pdf` path suffix alone is never authority.
Browser rendering is not used for PDF acquisition.

### Shared Go PDF document reader

PDF extraction is factored behind a reusable internal component consumed by
both Source processing and Research remote reads. It owns:

- native text-layer extraction;
- PDFium page rendering;
- configured vision fallback for pages without usable text;
- normalized page-coordinate blocks;
- extraction coverage and validation;
- deterministic extraction identity and content hashes.

The Research path does not enqueue a `source_processing_job` or manufacture a
hidden Notebook Source. It invokes the shared document reader under Research
budgets and projects the normalized artifact into bounded page-marked Markdown.

### Run-scoped document artifacts

The acquired PDF and its normalized artifact are stored under a dedicated
run-scoped object namespace using content-addressed keys. The accepted Action
Result records object identity, byte count, SHA-256, extraction configuration,
page count, and normalized-content SHA-256 so crash replay can reuse an exact
accepted result without refetching a mutable URL.

These objects are Research intermediates, not workspace Markdown files and not
Notebook Sources. They have no independent TTL in this change: they follow the
owning Agent Run's authorization, deletion, and purge lifecycle, and cannot be
reused by unrelated Runs. Purge removes an artifact only after its owning Run
and accepted checkpoints are no longer available.

A future explicit promotion design may reuse content-addressed bytes when
identity and hash still match, but it must create independent durable Source
state, admission, Evidence Revision, and indexing authority. This design adds
no promotion API and leaves ordinary URL Source import HTML-only.

## Data flow

1. The Research model calls `web_search` with one to three queries.
2. Brave returns up to ten candidates per query. Nano records each candidate as
   `discovered`.
3. The model selects a candidate and calls `read_url` with its URL.
4. `URLContentReader` asks the TypeScript sidecar to acquire and classify the
   public response under the existing security limits.
5. For HTML, the sidecar returns the existing cleaned Markdown result.
6. For PDF, the sidecar streams validated PDF bytes and acquisition metadata to
   the Go reader. Nano stores the immutable bytes by hash and runs shared PDF
   extraction.
7. The Action returns bounded page-marked Markdown and diagnostics. Raw bytes
   never enter model context, checkpoints, Trace attributes, or PostgreSQL.
8. The accepted Action Result materializes the ledger URL as `read` with final
   URL, title, media type, hashes, word count, page count, reader identity,
   Run ID, and Action ID.
9. Research capsules and rollups retain a bounded substantive projection. The
   authoritative accepted Action Result and run-scoped artifact preserve exact
   recovery identity.
10. The report may cite the original or final public URL only after this
    successful read.

An HTML landing page containing a PDF link is handled without recursive crawl:
the first `read_url` returns the link in Markdown, and the model may explicitly
call `read_url` on that PDF URL in a later decision.

## Evidence and citation semantics

Successful HTML and PDF reads share the same ledger status and report
eligibility rule. Discovery snippets remain ineligible.

For PDF, the ledger and accepted Action Result preserve document-level identity
and page count. Normalized Markdown places the exact marker
`<!-- nano-pdf-page:<one-based-page> -->` before every page. The marker is part
of the versioned read contract and is not invented by the model. This design
does not change the existing URL citation syntax.

Truncating the model-visible Markdown does not truncate the stored normalized
artifact. The result explicitly reports truncation. A PDF whose relevant
content is outside the visible bound cannot silently support a claim unless a
subsequent bounded document-read operation retrieves that page range. This
change therefore adds `read_document_pages` to the Research executor. Its input
contains the opaque `document_handle` returned by a successful PDF `read_url`
plus an inclusive start and end page, with at most 20 pages per call. The handle
must resolve to an accepted PDF read in the same Run. The Action performs no
network request; it reads the immutable normalized artifact and returns bounded
page-marked Markdown. It is unavailable to ordinary Chat and cannot address an
object-store key directly.

## Budgets and limits

Remote PDF reading uses these initial defaults:

- 20 MiB acquired PDF bytes;
- 500 total pages;
- 20 pages requiring vision;
- the existing 144 DPI, 20 million pixels per page, and 256 MiB aggregate
  renderer-output ceilings;
- 20 million normalized runes;
- 120,000 model-visible characters per Action Result;
- 120 seconds for acquisition plus extraction;
- two concurrent PDF reads per Research Run and two per worker;
- 20 inclusive pages per `read_document_pages` call.

All values are immutable Research Policy inputs or worker capability ceilings,
not model-selected arguments. Budget exhaustion returns a stable domain error
and never falls back to an unbounded provider request.

## Failure handling

- Unsafe destination, unsafe redirect, DNS failure, timeout, or excessive bytes
  returns the corresponding acquisition-domain error.
- Unsupported media types remain explicit and do not enter PDF extraction.
- A claimed PDF without a valid signature returns `document_type_mismatch`.
- Encrypted, malformed, over-page-budget, or extraction-invalid PDFs return
  stable document errors.
- Missing native text invokes the existing bounded vision fallback. If vision
  is unavailable or its page budget is exceeded, the read fails rather than
  publishing partial evidence as complete.
- A Worker crash after an accepted artifact write reuses its content hash and
  checkpoint identity. A crash before an accepted result may safely retry the
  read because acquisition is read-only and artifact keys are deterministic.
- A failed PDF read records `failed` in the evidence ledger with a bounded
  reason code; it never upgrades a previous successful read to failed.

## Security and privacy

- The TypeScript sidecar remains least-privileged and receives no database,
  object-store, user-cookie, or durable application credentials.
- Every DNS resolution and redirect hop is revalidated against non-public
  destinations. PDF subresources are not fetched.
- Response media type, signature, byte count, and SHA-256 are verified before
  extraction.
- Raw PDFs and extracted text are never written to Trace payloads.
- Run-scoped objects use opaque keys and are accessible only through worker
  authority for the owning Run.
- The feature respects public accessibility; it does not bypass authentication,
  robots challenges, paywalls, or document encryption.

## Observability

Sanitized Trace data records requested host, final host, media family, byte
count, redirect count, page count, native-text pages, vision pages, extraction
duration, truncation, stable error code, and content hashes where current Trace
policy permits hashes. It does not record raw URL query secrets, PDF bytes, or
document text.

Metrics separate acquisition latency, PDF rendering latency, vision latency,
and result projection latency. HTML Reader latency remains comparable to its
current series rather than being merged into a single opaque duration.

## Verification

Implementation follows test-driven development and proves:

1. Brave candidates remain discovery-only and no search result is prefetched.
2. Existing HTML `read_url` behavior and `/v1/parse` compatibility remain
   unchanged.
3. A direct arXiv-style PDF URL passes the TypeScript SSRF and redirect boundary,
   is classified from response metadata and signature, and reaches the shared
   PDF reader.
4. Native-text PDFs return page-marked Markdown and become ledger `read`
   evidence.
5. Image-only pages use the bounded vision path and preserve page coordinates.
6. MIME spoofing, private redirects, oversized, malformed, encrypted, and
   over-page-budget PDFs fail with stable reason codes and no readable evidence.
7. Accepted Action replay reuses the same content-addressed artifact without a
   second upstream fetch.
8. A landing-page read followed by an explicit PDF-link read works across two
   Research decisions.
9. Automatic PDF reads do not create `source_sources` rows or appear in the
   Notebook Source list.
10. Model-visible output and Trace contain no raw PDF bytes.
11. Focused TypeScript, Go unit, PostgreSQL/MinIO integration, document-renderer,
    Research runtime, and end-to-end Research tests pass before broader gates.

## Rollout and compatibility

Roll out behind an immutable Research Definition/Policy version and a server
capability flag. Existing admitted Runs retain the old HTML-only tool contract.
New Runs use the media-aware `read_url` only when worker, web-reader, renderer,
object store, and vision dependencies satisfy startup validation.

Rollback selects the previous Agent Release and disables remote PDF reads for
new Runs. Run-scoped artifacts already referenced by accepted checkpoints stay
readable until their owning Run is purged. No automatic Notebook Source
requires rollback.

ADR-0049 must be revised with the implementation because its current accepted
scope explicitly rejects direct PDF URL ingestion. The revised ADR retains the
TypeScript sidecar as the sole public-URL acquisition boundary while allowing a
bounded PDF branch for Research; ordinary URL Source import remains HTML-only
unless separately designed and approved.
