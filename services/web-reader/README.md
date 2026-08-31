# Web Reader

A self-hosted public-URL reader sidecar: fetches HTML or direct PDF resources.
HTML is returned as clean, LLM-ready Markdown, while Research PDF acquisition
returns bounded raw bytes for the Go document pipeline. HTML parsing follows the
[jina-ai/reader](https://github.com/jina-ai/reader) approach — Mozilla
Readability for main-content extraction plus a rule-based Markdown
conversion — with a two-stage engine (plain HTTP fetch, optional headless
Chromium render) for JS-heavy and bot-protected pages.

In the Nano Notebook stack, the worker calls this service as a fallback when
its own HTML extraction quality gate fails (extraction config
`html-reader-v1`); the original HTML bytes remain the stored evidence, and
the reader's Markdown output is stored as a derived artifact.

## Features

- **Two engine modes with auto upgrade** — plain HTTP fetch first, then a
  real browser render when the page is JS-driven, thin, or rejected by
  anti-bot walls. The response reports which engine won and whether an
  upgrade happened.
- **Anti-bot wall handling** — consistent `sec-ch-ua*` client hints matching
  the overridden User-Agent, `navigator.webdriver` suppression, and
  challenge-page tolerance (a 4xx first response followed by a JS-driven
  reload, as used by zhihu's zse-ck, is judged by the final navigation).
- **SSRF protection** — every DNS lookup (including every redirect hop) is
  validated against public-address rules before connecting; the browser
  engine intercepts every request (main frame, redirects, subresources)
  through the same rules.
- **Content-quality post-processing** — media player chrome removal,
  fragmented figure/card text regrouped into compact lists, tracking-pixel
  and data-URI image removal, oversized auto-link URLs flattened to label
  text.
- **Bounded everything** — request body size, response size, redirects,
  timeouts, per-service and per-engine concurrency.
- **Media-aware Research acquisition** — `/v1/acquire` returns strict
  `multipart/mixed`; PDFs require both `application/pdf` and a `%PDF-`
  signature and never enter the browser engine.

## API

### `GET /health/live`

Public (no auth). Returns `{"status":"live","service":"web-reader"}`.

### `POST /v1/parse`

Requires `Authorization: Bearer <token>` when `NANO_WEB_READER_SERVICE_TOKEN`
is set (empty value disables auth). Request body is JSON:

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `url` | string | *required* | Absolute `http`/`https` URL. No userinfo, no fragment. |
| `format` | string | `"markdown"` | Output format: `markdown` \| `text` \| `html`. |
| `with_links` | boolean | `true` | Keep hyperlinks; `false` flattens them to plain text. |
| `with_images` | boolean | `true` | Keep images; `false` removes them entirely. |
| `max_chars` | integer | `250000` | Truncate `content` beyond this many chars (adds a `[content truncated]` marker and sets `truncated: true`). |

Unknown fields are rejected with `invalid_request`.

Example:

```bash
curl -X POST http://127.0.0.1:8085/v1/parse \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $NANO_WEB_READER_SERVICE_TOKEN" \
  -d '{"url":"https://example.com/article","format":"markdown","max_chars":50000}'
```

Response (200):

| Field | Description |
| --- | --- |
| `schema_version` | Always `"1"`. |
| `url` / `final_url` | Requested URL and the URL after redirects. |
| `title` / `description` / `site_name` / `published_time` / `lang` | Page metadata (Readability + meta tags). |
| `extraction` | `readability` or `fallback-body`. |
| `engine` | `lightweight` or `browser` — which engine produced the result. |
| `upgraded` | `true` when auto mode upgraded a lightweight attempt to the browser. |
| `format` / `content` / `char_count` / `word_count` / `truncated` | Output payload. |
| `fetch` | `{status, content_type, charset, bytes, redirects}` of the winning fetch. |

### `POST /v1/acquire`

Uses the same authentication and request JSON as `/v1/parse`. A successful
response is `multipart/mixed` with an `application/json` `metadata` part first.
HTML has only that part and includes the same parsed content fields as
`/v1/parse`, plus `media_type: "text/html"`. PDF adds a second `document` part
with `Content-Type: application/pdf`, exact `Content-Length`, and the raw bytes;
its metadata reports `media_type: "application/pdf"`, requested/final URLs, and
fetch details and SHA-256. Unknown, missing, duplicate, or reordered parts are contract
violations for clients.

### Error contract

Every failure returns `{"error":{"code":"...","message":"..."}}` with the
stable error codes shared with the repo's Go sidecars:

| Code | HTTP | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | Malformed body, unknown field, bad `max_chars`. |
| `unauthorized` | 401 | Missing/invalid bearer token. |
| `not_found` / `method_not_allowed` | 404 / 405 | Path or method contract. |
| `unsafe_destination` | 422 | Non-public IP, private range, userinfo/fragment, blocked by SSRF guard. |
| `parse_failed` | 422 | No extractable main content. |
| `response_too_large` | 413 | Upstream body exceeds the byte budget. |
| `unsupported_type` | 415 | Unsupported media type. |
| `document_type_mismatch` | 415 | PDF response media type and `%PDF-` signature disagree. |
| `upstream_failed` | 502 | DNS failure, timeout, upstream 4xx/5xx, too many redirects. |
| `engine_unavailable` | 503 | Browser engine requested but no executable found. |
| `service_busy` | 503 | Concurrency limit reached. |
| `internal_error` | 500 | Unexpected failure (logged server-side, never surfaced raw). |

## Architecture

```
POST /v1/parse
      │
      ▼
┌─────────────┐   lightweight   ┌──────────────────┐
│ engine.ts   │ ──────────────► │ fetcher.ts       │  plain http(s) + gzip,
│ (auto mode) │                 │ (node:http/https)│  validating DNS lookup,
│             │   browser       └──────────────────┘  redirect re-validation
│             │ ──────────────► ┌──────────────────┐
└─────────────┘                 │ browser.ts       │  puppeteer-core + shared
      │                         │ (headless        │  headless Chromium; UA +
      ▼                         │  Chromium)       │  client-hint metadata;
┌─────────────┐                 │                  │  challenge-reload
│ reader.ts   │ ◄───────────────└──────────────────┘  tracking; SSRF request
│ pre-clean   │                                     │  interception
│ + Readability│
└─────────────┘
      │
      ▼
┌─────────────┐
│ markdown.ts │  turndown + GFM, link/image rules,
│ (markdown   │  tidyMarkdown post-processing
│  output)    │
└─────────────┘
```

**Pipeline** (`reader.ts`):

1. Parse HTML with jsdom (no script execution, no resource loading).
2. Pre-clean the DOM: removable tags (`script`, `nav`, `aside`, ...), hidden
   elements, noise containers by class/id/ARIA role, media player chrome
   (`[class*="player"]`, `video`/`audio`/`source`/`track`, loading
   placeholders).
3. Extract main content with Mozilla Readability.
4. Fall back to the pre-cleaned `<body>` when Readability yields nothing
   (below a 60-char minimum, mirroring the Go normalize quality gate).

**Engine selection** (`engine.ts`):

- `lightweight` — always plain fetch.
- `browser` — always render.
- `auto` (default) — lightweight first; upgrade to the browser when parsing
  failed, the upstream rejected the plain fetch (bot walls), or the content
  is thinner than `NANO_WEB_READER_AUTO_UPGRADE_MIN_WORDS` words. Timeouts
  and security verdicts are never retried; every browser failure (engine
  unavailable, busy slots, render errors) degrades gracefully to the
  lightweight outcome, and the richer of the two results wins.

**SSRF posture** — the lightweight fetcher dials only pre-validated IPs
(validating `dns.lookup` hook; every redirect hop re-validated). Chromium
resolves DNS on its own, so the browser engine additionally installs a
request interceptor that resolves every requested hostname through the same
public-address rules before Chromium may connect; verdicts are cached per
render only, to avoid cross-request DNS-rebinding windows.

**Markdown post-processing** (`markdown.ts`) — links with URLs beyond 100
chars (entity auto-links, tracking parameters) are flattened to their label
text; runs of short non-sentence-final paragraphs (figure/card grids without
structural markup, common on zhihu/wechat) are regrouped into compact
markdown lists; decorative interpunct separator lines are dropped.

## Configuration

All configuration is environment-driven with fail-fast validation
(`src/config.ts`), following the repo's Go sidecar conventions
(`NANO_<SERVICE>_*`):

| Variable | Default | Description |
| --- | --- | --- |
| `NANO_WEB_READER_ADDR` | `127.0.0.1:8085` | Listen address (`:8085` binds all interfaces). |
| `NANO_WEB_READER_SERVICE_TOKEN` | *(empty)* | Bearer token for `/v1/parse` and `/v1/acquire`; empty disables auth. |
| `NANO_WEB_READER_ENGINE` | `auto` | `lightweight` \| `browser` \| `auto`. |
| `NANO_WEB_READER_MAX_CONCURRENT` | `8` | Max in-flight parse requests. |
| `NANO_WEB_READER_FETCH_TIMEOUT_MS` | `20000` | Per-request fetch timeout (ms). |
| `NANO_WEB_READER_MAX_REDIRECTS` | `5` | Redirect budget. |
| `NANO_WEB_READER_MAX_RESPONSE_BYTES` | `5242880` | HTML upstream body byte budget. |
| `NANO_WEB_READER_MAX_PDF_RESPONSE_BYTES` | `20971520` | PDF upstream body byte budget. |
| `NANO_WEB_READER_MAX_CONTENT_CHARS` | `250000` | Ceiling for `max_chars` and the default truncation point. |
| `NANO_WEB_READER_MAX_REQUEST_BODY_BYTES` | `16384` | Request body byte budget. |
| `NANO_WEB_READER_ALLOW_PRIVATE_TARGETS` | `false` | Allow private/non-public destinations (dev only). |
| `NANO_WEB_READER_ALLOW_SYNTHETIC_PROXY_TARGETS` | `false` | Trust local DNS-proxy synthetic ranges (OrbStack/Clash fake-IP). |
| `NANO_WEB_READER_USER_AGENT` | Edge UA on Windows | Sent by both engines; the browser engine derives matching client hints. |
| `NANO_WEB_READER_BROWSER_EXECUTABLE` | *(empty)* | Chromium/Edge executable path. Falls back to common Linux paths (`/usr/bin/chromium`, ...). |
| `NANO_WEB_READER_BROWSER_TIMEOUT_MS` | `30000` | Navigation timeout (ms). |
| `NANO_WEB_READER_BROWSER_MAX_CONCURRENT` | `2` | Concurrent browser renders (a Chromium spawn cluster). |
| `NANO_WEB_READER_AUTO_UPGRADE_MIN_WORDS` | `100` | Auto mode upgrades results thinner than this. |

## Running

### Local (Windows)

Requires Node.js 22+. The browser engine needs a local Chromium/Edge
executable — on Windows set it explicitly:

```powershell
cd services/web-reader
npm ci
npm run build

$env:NANO_WEB_READER_BROWSER_EXECUTABLE = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
npm run start
```

Without the variable, auto mode still works but degrades to lightweight
fetches (a warning is logged once) — bot-protected sites like zhihu will
then fail with `upstream_failed 403`.

### Docker

```bash
docker compose -f infra/compose/compose.yaml up web-reader
```

The image (`infra/web-reader/Dockerfile`) is a multi-stage build on
`node:22-bookworm-slim` with a system Chromium preinstalled
(`NANO_WEB_READER_BROWSER_EXECUTABLE=/usr/bin/chromium`), a non-root runtime,
a read-only rootfs with tmpfs `/tmp` (Chromium profile), `cap_drop: ALL`,
and a healthcheck against `/health/live`. The prod stack
(`infra/compose/compose.prod.yaml`) runs the same image with
`NANO_WEB_READER_SERVICE_TOKEN`, engine `auto`, and concurrency 4/2.

## Testing

```bash
npm test   # builds, then runs the node:test suite (dist/test/*.test.js)
```

Unit tests cover the SSRF validator, fetcher, engine orchestration, parser,
markdown conversion, server contract, and config validation. Browser-engine
integration tests are skipped automatically when no executable is
configured; set `NANO_WEB_READER_BROWSER_EXECUTABLE` to run them.
