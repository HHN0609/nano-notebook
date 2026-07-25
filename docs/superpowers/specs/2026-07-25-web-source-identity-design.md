# Web Source Identity Design

## Outcome

Discovery results and imported URL Sources consistently show a website favicon and a readable page title. A URL is user-facing copy only when no page title is available. Uploaded files retain their existing file presentation.

## Identity flow

Discovery already owns the provider-supplied page title, canonical URL, and optional favicon reference. Importing a candidate passes its title into the existing URL Source admission path, which persists that title instead of replacing it with the hostname. Manual URL admission extracts a bounded HTML `<title>` from the fetched immutable snapshot. If neither a candidate title nor an HTML title is available, the final URL becomes the Source title.

Historical Discovery imports resolve the matching imported candidate title for display only when their current title is still the generated hostname or `Web source`. This condition preserves titles that users renamed.

## Favicon presentation

The Web derives a same-site `/favicon.ico` URL from each safe external Source URL. Discovery uses its persisted `favicon_ref` when present and otherwise applies the same derivation. An image load failure replaces the image with the existing globe symbol. No third-party favicon service, proxy, download, or embedded webpage preview is introduced.

## Source list layout

Each ready URL Source row renders, in order: selection checkbox, favicon, title, state, edit, and delete. The title remains the external link. Non-URL Sources do not receive a website favicon and keep their current behavior.

## Acceptance criteria

1. A newly imported Discovery Source persists and returns the candidate page title rather than its hostname.
2. A manually imported HTML URL uses the document title and falls back to the final URL when the title is absent.
3. Historical Discovery Sources with generated hostname titles display their candidate title without overwriting renamed Sources.
4. Discovery and URL Source rows render a site favicon with a globe fallback when the image fails.
5. Uploaded files, Source opening behavior, selection, status, edit, and deletion remain unchanged.

## Non-goals

- No third-party favicon API.
- No webpage preview or download action.
- No favicon blob persistence.
- No title generation by an LLM.
