# Discovery Admission and Citation Identity Design

## Outcome

Source Discovery becomes a chooser for usable material, not a raw search-results screen. Search results are validated before publication, selected results collapse into the Source library after import admission, terminally unusable URL imports are removed, and every answer citation exposes the Source it represents.

## Candidate admission

The Discovery worker validates each normalized search result through the same fetch and extraction boundary required by URL Source processing before persisting it as a visible candidate. Results that cannot be fetched, are blocked, contain no importable document, or fail the supported-content policy are omitted from the session. The UI therefore never renders an unusable result or an `import_failed` choice.

Selection remains server-authoritative. Only candidates in `discovered` state may be selected or imported. A stale client attempting to select another state receives a state conflict.

## Import lifecycle

An import request creates the Source and processing job using the existing URL Source pipeline. Once at least one selected result is admitted, the Source list refreshes and Discovery closes. Processing continues asynchronously.

If an admitted URL Source later reaches a terminal processing failure, the worker reconciles the originating candidate and Source atomically: the candidate is no longer selected or linked as imported, and the Source is removed through the existing durable purge mechanism. The unusable item is not shown in either Discovery or the Source library.

## Original links

Every ready URL-backed Source with a safe canonical final URL receives an external open action, regardless of detected format. This includes URL-backed PDFs. Uploaded files retain the existing inline-original format policy.

## Citation presentation

Persisted citation identity remains `source_id` plus ordinal. The answer renderer resolves that identity against the Source collection and displays a compact citation containing both the number and a readable Source title, falling back to the canonical hostname when needed. Activation uses the same Source open action as the left Source list. Raw source IDs never appear in user-facing text.

## Non-goals

- No embedded webpage preview.
- No application download action.
- No client-only URL probing.
- No retry checkbox for rejected candidates.
- No change to RAG evidence selection or claim grounding semantics.

## Acceptance criteria

1. A provider result that fails prevalidation never appears in the Discovery response or UI.
2. Only `discovered` candidates can be selected and imported.
3. Discovery closes after at least one selected Source is admitted and the Source list is refreshed.
4. A terminally failed discovered URL import leaves no Source row visible and no selectable Discovery row.
5. A ready URL-backed PDF citation opens its canonical external URL.
6. Inline and trailing citations visibly identify their Source title or hostname; no raw `src_*` identifier is rendered.
