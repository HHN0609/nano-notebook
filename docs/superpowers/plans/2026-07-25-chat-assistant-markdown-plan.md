# Chat Assistant Markdown Implementation Plan

1. Add an App-level test fixture containing assistant GFM, user Markdown-like text, raw HTML, and an inline source citation; assert semantic DOM and existing citation behavior.
2. Run the focused test and confirm it fails because assistant text is still rendered literally.
3. Install the assistant-ui Markdown integration and GFM plugin.
4. Add a scoped assistant Markdown renderer and connect it only to assistant text parts.
5. Preserve source markers through Markdown preprocessing and the existing citation button/viewer.
6. Add chat-scoped Markdown typography, table/code overflow, and compact-layout styles.
7. Run the focused test until green, then run the full test suite, lint, type check, and production build.
8. Start the app fixture used by browser tests and inspect the rendered chat at desktop and compact widths.
