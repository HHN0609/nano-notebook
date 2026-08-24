---
status: accepted
---

# Use web-reader as the public URL boundary

Nano Notebook will use the TypeScript web-reader as the only public-URL acquisition and page-extraction boundary. Control Plane URL imports, Source Discovery preflight checks, Research `read_url`, and uploaded-HTML quality fallback all use the same `/v1/parse` contract. The standalone Go Fetcher service and its raw-response contract are removed.

For a URL Source, web-reader returns cleaned Markdown, title, final URL, and bounded extraction diagnostics. The Control Plane independently computes SHA-256 over the returned Markdown, stores those Markdown bytes as the immutable Source input, and records `text/markdown`, byte size, origin URL, final URL, and title in PostgreSQL. Downstream Source processing normalizes that stored Markdown; raw HTML is neither stored nor sent to chunking or vector indexing.

web-reader remains a least-privileged sidecar with no product database, object-store, user-cookie, or durable application credentials. It accepts only HTTP(S), validates every resolved destination and redirect against non-public ranges, bounds redirects, response bytes, decompression, elapsed time, concurrency, and output characters, and returns stable typed errors. Browser rendering is an internal engine upgrade behind the same contract, not general browser access.

This decision narrows URL imports to readable public HTML pages. Direct PDF, office-document, media, and YouTube-caption URL ingestion are no longer part of the URL contract; users import those formats through the existing file-upload pipeline. Reader rejection, empty or truncated content, invalid final URLs, object-store failure, and normal Source processing/admission failures prevent a Source from becoming Ready.

URL and final URL remain the citation and source-identity anchors. Keeping a URL does not prove that future bytes are unchanged, so Nano also retains the immutable cleaned Markdown and its SHA-256 inside the ordinary Source lifecycle. This is a reproducibility and integrity record for the semantic input actually indexed, not a raw-page audit archive.
