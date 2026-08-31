---
status: accepted
---

# Use web-reader as the public URL boundary

Nano Notebook will use the TypeScript web-reader as the only public-URL acquisition boundary. Control Plane URL imports, Source Discovery preflight checks, and uploaded-HTML quality fallback use the HTML-only `/v1/parse` contract. Deep Research `read_url` uses the media-aware `/v1/acquire` contract, which returns either cleaned HTML metadata or bounded PDF metadata plus raw bytes in strict `multipart/mixed`. The standalone Go Fetcher service remains removed.

For a URL Source, web-reader returns cleaned Markdown, title, final URL, and bounded extraction diagnostics. The Control Plane independently computes SHA-256 over the returned Markdown, stores those Markdown bytes as the immutable Source input, and records `text/markdown`, byte size, origin URL, final URL, and title in PostgreSQL. Downstream Source processing normalizes that stored Markdown; raw HTML is neither stored nor sent to chunking or vector indexing.

web-reader remains a least-privileged sidecar with no product database, object-store, user-cookie, or durable application credentials. It accepts only HTTP(S), validates every resolved destination and redirect against non-public ranges, bounds redirects, response bytes, decompression, elapsed time, concurrency, and output characters, and returns stable typed errors. Browser rendering is an internal engine upgrade behind the same contract, not general browser access.

Ordinary URL Source imports remain limited to readable public HTML pages. Direct PDF, office-document, media, and YouTube-caption Source ingestion still requires the existing file-upload pipeline. Research alone may read a direct public PDF: web-reader requires both `application/pdf` and `%PDF-`, applies a separate 20 MiB ceiling, returns SHA-256 and exact bytes, and never invokes Chromium for the PDF branch. The Go worker verifies the multipart contract and hash again, then reuses the shared native-text/PDFium/vision extraction path.

Remote Research PDFs are not Notebook Sources. Their original bytes and normalized artifact are content-addressed under the owning Run in the Research object store; deterministic acquisition/result records make crash replay reuse stored bytes instead of refetching a mutable URL. `read_url` returns bounded page-marked Markdown and an opaque same-Run handle. The Research-only `read_document_pages` tool resolves that handle under the current Run and reads at most 20 inclusive pages without network access. No object-store key or raw PDF enters model context, PostgreSQL, or Trace payloads.

Successful HTML and PDF reads share the Research Evidence Ledger `read` authority. The ledger additionally records media type, PDF page count, opaque document handle, and bounded failure reason. Brave snippets remain discovery leads rather than evidence. The PDF capability is pinned by `research.executor@8` through `nano.default@14`; older admitted Runs retain their immutable definitions. Unsupported remote formats continue to fail explicitly.

URL and final URL remain citation anchors. Keeping a URL does not prove that future bytes are unchanged: ordinary URL Sources retain immutable cleaned Markdown and its SHA-256 in the Source lifecycle, while Research PDFs retain run-scoped original bytes and normalized artifacts by hash. Neither path turns a future fetch of the same URL into the authority for previously accepted evidence.
