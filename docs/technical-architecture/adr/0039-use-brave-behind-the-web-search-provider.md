# Use Brave behind the Web Search Provider

Source Discovery depends on a provider-neutral `WebSearchProvider` contract and uses Brave Web Search as its first adapter. The adapter accepts bounded query and locale hints, returns at most the requested number of sanitized candidate records, classifies provider failures into safe application errors, and never persists or exposes a raw Brave envelope. The server reads `NANO_BRAVE_SEARCH_API_KEY`; the credential never enters browser code, PostgreSQL, Trace, Replay, or logs.

Provider titles, URLs, snippets, ranks, and bounded metadata are discovery material, not Source content or Evidence. A selected candidate must pass the existing restricted public-URL Fetcher and immutable Source processing pipeline before it can become Ready or support retrieval. This decision adds neither a headless browser nor general Agent Web access.
