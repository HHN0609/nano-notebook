# Assistant Chat Markdown Rendering

## Goal

Render assistant chat responses as safe GitHub Flavored Markdown while keeping user messages as literal plain text. Existing source citations must remain inline, clickable, and compatible with Markdown around them.

## Scope

- Render assistant text parts with the assistant-ui Markdown integration.
- Support CommonMark plus GitHub Flavored Markdown: headings, emphasis, links, lists, blockquotes, inline and fenced code, tables, task lists, and strikethrough.
- Keep user messages on the existing plain-text renderer.
- Preserve precise citation chips and inline `[source:<source_id>]` citation markers.
- Add chat-scoped typography and overflow styles for desktop and compact layouts.

The change does not add syntax highlighting, Mermaid, LaTeX, message editing, or Markdown rendering for user messages.

## Architecture

Add `@assistant-ui/react-markdown` and `remark-gfm`, using versions compatible with the existing `@assistant-ui/react` dependency. A focused chat Markdown component wraps `MarkdownTextPrimitive` and is supplied as the assistant message `Text` part renderer through `MessagePrimitive.Parts`.

Assistant responses without source markers flow directly through this renderer. For source-grounded responses, a preprocessing step converts recognized `[source:<source_id>]` markers into internal Markdown links. A custom link renderer recognizes only those internal targets and renders the existing inline source citation button; ordinary Markdown links remain links. Unknown or malformed source markers stay visible as text rather than becoming interactive citations.

The Markdown pipeline will not enable raw HTML. Ordinary external links open in a new tab with `noopener noreferrer` protection.

## Presentation

Markdown styles are scoped to assistant chat content so they do not alter documents or other application surfaces. The stylesheet defines readable spacing and hierarchy for headings, paragraphs, lists, blockquotes, links, inline code, fenced code, tables, task checkboxes, and strikethrough. Long code and tables scroll horizontally inside the message width; prose and links wrap without expanding the chat panel.

## Error and Safety Behavior

- Markdown parsing is local and deterministic; a response remains readable without network activity.
- Raw HTML is displayed as text and is never executed.
- Unsupported Markdown degrades to readable text.
- Existing citation dialogs and source viewer error states remain unchanged.
- Citation preprocessing only recognizes source IDs already present in the message citation payload.

## Testing and Acceptance

Component tests will first demonstrate the missing behavior, then verify:

1. An assistant response renders semantic GFM elements, including a heading, emphasis, list, table, task item, strikethrough, link, and code.
2. The same Markdown-looking syntax in a user message remains literal text.
3. Raw HTML in an assistant response does not create executable DOM elements.
4. Markdown formatting and existing inline source citation buttons render together without exposing `[source:...]` markers.

Acceptance requires the focused component tests, the full web test suite, type checking, production build, and a browser inspection of the affected chat state at a representative viewport.
