# Compact Research Discovery Design

## Status

- Approved: 2026-07-26
- Scope: left Source panel presentation and interaction for Research Discovery sessions

## Problem

A ready Discovery session currently opens the complete Candidate list in the left Source panel. Ten result rows, snippets, selection controls, and the Import action then dominate the panel and push the Member's existing Sources into a small lower region.

The desired interaction follows NotebookLM's hierarchy: a completed Research operation should leave a compact summary card in the normal Source view. The complete Candidate list is a secondary detail state that opens only when the Member explicitly chooses to view it.

## Decisions

### Terminology

The product uses **Research** for this workflow. It does not distinguish Fast Research from Deep Research. Manual web search and Chat-triggered Research use the same Discovery presentation.

### Normal Source view

The normal left Source panel keeps the existing low-emphasis web-search input and the existing Source collection. A ready Discovery session is rendered between them as one compact Research card.

The card contains:

- a `Research 已完成` / localized equivalent status heading;
- a **View** action at the top right;
- the first three visible Candidates in ordinal order, each with site identity, title, and one bounded description line;
- `另有 N 个来源` / localized equivalent when more than three Candidates are visible; and
- the existing Import action, operating on the session's currently selected importable Candidates.

The compact card does not render Candidate checkboxes, Select All, the full summary, or a nested scroll region. Imported Candidates may remain represented according to existing status rules, while `import_failed` Candidates remain excluded exactly as they are today.

Deleting a Discovery session is not part of this change because the application has no corresponding user-facing session-deletion contract.

### Complete Discovery detail

Choosing **View** switches the left panel into its existing dedicated Source Discovery state:

- the header uses the `Sources > Source Discovery` breadcrumb;
- the query, optional summary, all visible Candidates, Select All, Candidate checkboxes, and Import action are available;
- the close icon returns to the normal Source view and its compact Research card; and
- existing Sources are not duplicated below the complete Candidate list.

This state is navigation, not an inline accordion and not a modal.

The complete state must also be materially wider and more readable than the normal Source panel:

- on large desktop viewports, Discovery occupies a responsive `640–720 px` column, targeting approximately `680 px` at a `1440 px` viewport;
- on intermediate desktop viewports above the compact breakpoint, Discovery remains at least `560 px` wide while Chat and Studio share the remaining space without horizontal page overflow;
- Candidate titles use `15 px` text and Candidate URL, snippet, status, summary, and toolbar text use `13 px` text;
- Candidate icons, checkboxes, row gaps, and row padding increase with the typography so the result list does not look sparse inside the wider panel; and
- at compact viewports, the existing single-panel layout remains full-width, with Candidate titles at `14 px` and supporting text at `12 px` to preserve useful line lengths.

These expanded-detail rules do not change the compact Research card's width, typography, or density.

### Automatic opening

Discovery does not automatically enter the complete detail state when:

- a Member submits a manual web search;
- a manual search becomes ready;
- Chat delegates to the Research Agent;
- the requested Research Discovery session is searching, ready, or failed; or
- the browser loads a latest or pinned Discovery session.

Searching, empty, and failed states remain visible in the normal Source view as bounded status content so the Member can understand and retry the operation without opening a separate detail state. A ready session always settles into the compact card. Only **View** opens its complete Candidate list.

There is therefore no separate “collapse Discovery” affordance beside the search input. The complete state retains its existing close/back affordance because that action navigates back to Sources.

## Component Boundaries

`SourceDiscovery` remains responsible for:

- loading the latest or explicitly requested session;
- polling a searching session;
- rendering search, progress, failure, compact ready, and complete ready states;
- Candidate selection mutations; and
- import mutations.

It receives an explicit presentation mode or equivalent owner-controlled state. Its compact and complete renderers share the same loaded session and derived Candidate sets so status filtering, selection, and import behavior cannot drift.

`SourcePanelContent` remains responsible for panel navigation. It owns whether the complete Discovery detail is open, opens it only from the card's View callback, and closes it from the detail header. Loading a requested session does not change that navigation state.

`NotebookWorkspace` may continue receiving the Discovery-detail state only for layout styling. The normal compact card must not activate the wide/expanded workspace modifier.

No REST, persistence, Research Agent, Source ingestion, or retrieval contract changes.

## Data And State Flow

```text
manual search or Chat Research
  -> Discovery session loads/polls in the normal Source view
  -> searching/failed/empty status remains bounded in that view
  -> ready session renders compact card (first 3 + remaining count)
  -> Member chooses View
  -> SourcePanelContent enters complete Discovery detail
  -> selection/import mutations update the shared loaded session
  -> close returns to the compact card
```

When the Member imports from either presentation, successful imports refresh the Source collection. A successful import returns to the normal Source view, preserving the existing import-accepted behavior. Settled sessions must not reopen automatically afterward.

## Accessibility

- View is a semantic button with localized text.
- Candidate title links in both presentations retain safe external-link behavior.
- The compact preview does not place hidden or duplicate checkboxes in the accessibility tree.
- Status and failure content retain their current `status` and `alert` semantics.
- Focus follows normal button navigation: View enters the detail state; the existing close control provides an explicit way back.

## Error Handling

- Search admission failures and failed sessions remain visible near the search input with Retry when supported.
- Import errors remain bounded and do not erase the compact card or its session data.
- A ready session with no visible Candidates renders the existing no-results state instead of an empty card.
- A ready session with one to three visible Candidates omits the `另有 N 个来源` line.

## Testing

Component tests are written first to prove:

1. a ready session initially renders only the first three visible Candidates and the remaining count;
2. Candidates after the preview boundary are absent from the normal accessibility tree;
3. View opens the complete list and close returns to the compact card;
4. neither manual search submission nor a requested Research session automatically opens the complete detail state;
5. the compact Import action uses the current selected Candidate set and retains the successful-import return behavior;
6. up to three results omit the remaining-count label; and
7. failed imports remain excluded under the existing filtering rule.

Regression checks cover type-check, lint, the Web test suite, and a production build. Browser acceptance covers desktop and compact widths, confirming that the compact card does not crowd out existing Sources, the complete list scrolls without horizontal overflow, and the Search field remains visually secondary.

Browser assertions also verify that the complete Discovery column is materially wider than the normal Source column, falls within the specified large-desktop range, and applies the specified detail typography at desktop and compact viewports.

## Acceptance Criteria

1. A completed Research Discovery never opens the full Candidate list automatically.
2. The normal Source view shows at most three Candidate previews plus the correct `N` remaining count.
3. View is the only action that enters the complete Discovery detail.
4. The complete detail exposes all existing selection and import controls and returns cleanly to the compact card.
5. The compact card leaves a materially larger, usable portion of the existing Source collection visible.
6. Manual search and Chat-triggered Research follow the same presentation rules.
7. The UI uses Research terminology and introduces no Fast/Deep distinction.
8. Existing Discovery, Candidate selection, import, safe-link, and Source refresh behavior remains intact.
9. At a `1440 px` viewport, complete Discovery is between `640 px` and `720 px` wide and materially wider than the normal Source panel.
10. Complete Discovery Candidate typography is `15 px` / `13 px` on desktop and `14 px` / `12 px` on compact viewports.
