# Left Source Original Viewer Design

## Status

- Approved: 2026-07-25
- Scope: Sprint 8 Source opening behavior in the left panel and Chat citation routing
- Supersedes: the Sprint 6 processed Source Viewer interaction and its prohibition on serving original files for inline inspection

## Problem

Nano Notebook currently opens a modal Source Viewer that renders processed Evidence Units, Evidence Coverage, and derived page images. This exposes ingestion and RAG internals as product content, covers the workspace, and conflicts with the workspace layout rule:

- inputs and Sources stay on the left;
- conversation stays in the middle;
- generated outputs stay on the right.

Users need a simpler Source interaction modeled on the relevant NotebookLM behavior: a webpage opens at its original URL, while a locally stored file is shown only when the browser can display the immutable original directly. Nano Notebook must not create its own webpage preview, document conversion, processed-text preview, or download workflow.

## Product Invariant

There is one server-declared open action for every Source, and every product entry point obeys it.

```text
Source row or Chat citation
  -> Workspace Source open router
  -> external        -> open original URL in a new browser tab
  -> inline_original -> show immutable original in the left Source panel
  -> none            -> perform no open action
```

The Web client does not infer behavior from filename extensions, MIME types, URL shape, Citation metadata, or Source family.

## Interaction Design

### Left-panel states

The Source panel has three mutually exclusive states:

1. **Source list**
   - shows the Source search bar, Add Source action, and existing Sources;
   - a Source title is interactive only when its open action is `external` or `inline_original`.
2. **Discovery**
   - keeps the current search form and Candidate results in the upper region;
   - keeps the existing Source collection visible in the lower peek region;
   - opening an inline original from the lower region replaces only the left panel.
3. **Original viewer**
   - occupies the complete left Source panel, never the Chat or Studio panels;
   - header reads `Sources › {source title}` and includes Back;
   - body contains only the browser-rendered immutable original, or a bounded load-failure state;
   - contains no parsed text, Evidence Unit, Evidence Coverage, RAG state, generated page image, download action, or edit action.

The former Source Preview Dialog is removed.

### Navigation and return state

- Opening an inline original from the Source list records `list` as the return state. Back restores the Source list.
- Opening an inline original from the existing-Sources region in Discovery records the current Discovery session as the return state. Back restores the query, summary, Candidate results, selections, import states, and scroll state.
- Opening an external Source does not change left-panel state.
- A Source with `none` is rendered without title click behavior. No empty Viewer is opened.
- If an inline original cannot be loaded after the Viewer opens, the left panel shows `原件无法显示` and Back only. It does not offer conversion, processed content, or download.

### External Sources

HTML webpages and YouTube Sources never enter the Original viewer. Their titles and Chat citations are real links using:

```html
<a target="_blank" rel="noreferrer noopener">
```

The link destination is the server-owned final/original Source URL. The client must not replace it with a Candidate URL, reconstruct it from display text, or embed the remote page.

### Chat citations

Chat citations use the same Workspace Source open router and the same action returned for the addressed Source:

- `external` opens the original page in a new tab;
- `inline_original` opens the immutable original in the left panel and records the current left-panel state for Back;
- `none` is non-interactive.

Citation clicks no longer open the processed Source Viewer, show an Evidence excerpt tooltip, render derived pages, or focus normalized Evidence coordinates. Citation generation, grounding, persistence, and Source attribution remain unchanged.

## Backend Contract

Every Source representation consumed by the Source list or Chat includes an explicit `open_action`:

```json
{
  "open_action": {
    "kind": "external",
    "href": "https://example.com/final-source"
  }
}
```

```json
{
  "open_action": {
    "kind": "inline_original",
    "href": "/api/v1/sources/{source_id}/original-asset",
    "media_type": "application/pdf"
  }
}
```

```json
{
  "open_action": {
    "kind": "none"
  }
}
```

Contract rules:

- `kind` is a closed enum: `external`, `inline_original`, or `none`.
- `href` is required only for `external` and `inline_original`.
- `media_type` is required only for `inline_original` and is the stored, verified media type.
- `inline_original.href` is an application URL, never a Blob Store key, bucket name, presigned object-store URL, or credential-bearing URL.
- Sources that are not `Ready` return `none` until they become ready.
- A missing or invalid action fails closed as `none` in the client.

## Format Policy

The backend owns one allowlist that maps a ready Source's admitted family and verified media type to its open action.

| Original Source | Action | Product behavior |
| --- | --- | --- |
| HTML webpage | `external` | Open final/original URL in a new tab |
| YouTube | `external` | Open original YouTube URL in a new tab |
| PDF | `inline_original` | Browser displays immutable PDF in the left panel |
| PNG, JPEG, WebP | `inline_original` | Browser displays immutable image in the left panel |
| MP3, WAV, M4A | `inline_original` | Browser displays native audio controls in the left panel |
| Plain text, Markdown | `inline_original` | Browser displays immutable original text bytes in the left panel |
| DOCX, PPTX | `none` | No preview, conversion, external Office viewer, or download action |
| Any unknown or mismatched type | `none` | No open action |

Browser support is allowed to vary. Being on the allowlist means Nano Notebook may serve the original inline; it does not promise that every browser can render it. A browser rendering failure follows the bounded load-failure behavior and never changes the Source's canonical data.

## Original Asset Endpoint

`GET /api/v1/sources/{source_id}/original-asset` serves an original only when all conditions hold:

1. the caller has read capability for the Source's Notebook;
2. the Source exists, is not deleted, and is `Ready`;
3. the Source's admitted family and verified media type are on the inline-original allowlist;
4. the immutable original object exists;
5. the object's byte length equals the persisted original byte length;
6. the object's SHA-256 equals the persisted original SHA-256.

Any failed condition returns `404 Not Found`. This avoids disclosing Source existence, lifecycle state, storage layout, or integrity details across an authorization boundary. The endpoint never falls back to a normalized artifact, transcript, extracted text, rendered page, thumbnail, or remote refetch.

A successful response:

- streams the original object bytes;
- uses the verified allowlisted `Content-Type`;
- uses `Content-Disposition: inline` with a sanitized display filename;
- uses `Cache-Control: private, no-store`;
- uses `X-Content-Type-Options: nosniff`;
- does not expose Blob Store identifiers or credentials;
- remains subject to existing request cancellation and bounded streaming behavior.

Nano Notebook provides no download button, attachment response, or download-specific API. Because a browser receives bytes in order to render an original, browser-native controls may still allow saving them. This design does not claim to prevent browser- or operating-system-level saving.

## Persistence and RAG Boundary

This change affects presentation and Source opening only. Existing ingestion remains authoritative:

- immutable originals stay in the configured S3-compatible Blob Store, including local MinIO;
- normalized artifacts, Evidence Units, Evidence Coverage, embeddings, and retrieval metadata continue to be generated and persisted;
- RAG retrieval, Citation generation, grounding checks, Source selection, Source deletion, and artifact purge behavior remain unchanged;
- no remote Source is refreshed when opened;
- the original viewer never reads Qdrant as a content authority.

Processed artifacts remain internal inputs to retrieval and generation. They are no longer rendered as the user's Source preview.

## Security and Failure Behavior

- Authorization is checked on every original-asset request; knowledge of a Source ID or previously returned application URL is insufficient.
- Unsupported, not-ready, deleted, missing, corrupt, size-mismatched, or hash-mismatched originals are indistinguishable at the public endpoint and receive `404`.
- The server never trusts a requested filename, extension, MIME query parameter, or client-declared action.
- `external` links are emitted only from the persisted canonical Source URL and opened with opener isolation.
- Inline HTML, SVG, JavaScript, XML, and other active document types are not allowlisted.
- The client never embeds external pages and never sends Source authorization data to an external URL.
- Viewer failure UI contains no object key, digest, internal error, stack trace, or retry-to-download path.

## Compatibility and Supersession

This design intentionally supersedes these Sprint 6 interaction rules:

- the single processed Source Viewer shell;
- direct Source inspection of Evidence Units and Evidence Coverage;
- precise Citation focus into processed Viewer artifacts;
- the blanket rule that original files are never served for inspection.

The replacement boundary is narrower: allowlisted immutable originals may be served inline after authorization and integrity verification, but Nano Notebook still provides no download product action. Sprint 6 ingestion, Evidence authority, historical Citation data, deletion, capability checks, and Blob Store isolation remain in force.

`docs/sprint/SPRINT-6-PRD.md` and `docs/technical-architecture/ARCHITECTURE.md` must be amended during implementation so they point to this supersession instead of describing the removed Viewer interaction as current behavior.

## Testing

### API and storage

- authorized `Ready` Sources of every allowlisted type return exact original bytes and the required response headers;
- unauthorized, cross-Notebook, missing, deleted, not-ready, and unsupported Sources return `404`;
- object byte-length mismatch and SHA-256 mismatch return `404` without a derived-artifact fallback;
- response bodies and headers do not expose bucket names, object keys, storage endpoints, presigned URLs, or credentials;
- active and unknown media types cannot be forced inline through filenames or request parameters;
- existing Source ingestion, deletion, purge, and restricted-fetcher suites remain green.

### Web behavior

- Source list, Discovery existing-Source region, and Chat citations route the same Source action identically;
- external Sources open the server-provided URL in a new isolated tab and do not mutate left-panel state;
- inline originals replace only the left panel at desktop and compact widths;
- Back restores either the Source list or the exact active Discovery session;
- `none` Sources and corresponding citations are non-interactive;
- a failed inline load shows `原件无法显示` and Back only;
- the removed Source Preview Dialog, Evidence text, Evidence Coverage, processed excerpts, and generated page previews are absent;
- no application download control or attachment request is rendered;
- Source search, Candidate import, Source selection, Chat, RAG Citations, and Studio regressions remain green.

### Browser acceptance

- PDF, image, supported audio, plain-text, and Markdown originals render from the internal endpoint in the left panel where the test browser supports them;
- webpage and YouTube Sources open their persisted original destinations in a new tab;
- DOCX and PPTX do nothing when their titles or Citations are activated;
- Discovery remains recoverable after entering and leaving an inline original;
- Chat and Studio remain visible and usable while an original is open;
- there is no horizontal workspace overflow at supported desktop and compact widths.

## Acceptance Criteria

1. Inputs, Source discovery, and original-file inspection remain confined to the left panel; Chat remains in the middle and Studio remains on the right.
2. Webpage and YouTube Sources never show an in-product preview and open only their server-provided original URL in a new tab.
3. Only allowlisted, authorized, `Ready`, integrity-verified immutable originals can be rendered inline.
4. Unsupported or unrenderable originals receive no conversion, processed-content fallback, external viewer, or download workflow.
5. Source rows and Chat citations have identical behavior because both consume the same backend-declared action and Workspace router.
6. No user-facing Source surface renders Evidence Units, Evidence Coverage, normalized content, RAG internals, or derived viewer pages.
7. Existing ingestion, cleaning, persistence, RAG, Citation generation, deletion, and Studio behavior continue to work.

## Non-goals

- webpage embedding, snapshotting, reader mode, or HTML preview;
- DOCX/PPTX conversion or online Office integration;
- derived PDF/page/image preview generation;
- user-facing extracted text, transcript, Evidence, Coverage, chunk, embedding, or retrieval inspection;
- download buttons, download endpoints, attachment responses, or export workflows;
- annotations, highlights, Citation-coordinate focusing, or in-viewer search;
- changing Source ingestion, cleaning, chunking, embedding, retrieval, or persistence semantics.
