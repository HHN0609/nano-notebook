# Web Source Discovery Contract

This document freezes Sprint 8's Member API, durable states, and safe error vocabulary. The approved design and ADRs own the rationale; OpenAPI generation is outside this sprint.

## Member API

All endpoints authenticate the Principal. Session access additionally requires private ownership and current Notebook membership. Commands require Editor or Owner authority.

### Create a manual session

`POST /api/v1/notebooks/{notebook_id}/source-discovery-sessions`

```json
{"query":"bounded raw query","origin_chat_id":"optional private Chat ID"}
```

Returns `202 Accepted` with a Session projection. `query` is trimmed, non-empty, and at most 500 Unicode code points. `origin_chat_id`, when present, must be the Principal's Chat in the same Notebook.

### Restore or read a session

- `GET /api/v1/notebooks/{notebook_id}/source-discovery-sessions/latest` returns `200` or `204` when none exists.
- `GET /api/v1/source-discovery-sessions/{session_id}` returns `200` or `404`; unauthorized and nonexistent private sessions are indistinguishable.

### Replace selection

`PATCH /api/v1/source-discovery-sessions/{session_id}/selection`

```json
{"candidate_ids":["candidate ID"]}
```

Returns `200` with the complete Session projection. Replacement is atomic. Every ID must belong to the Session and be importable; an empty array clears selection.

### Import selected candidates

`POST /api/v1/source-discovery-sessions/{session_id}/imports`

Requires `Idempotency-Key` and no request body. Returns `202 Accepted` with one outcome for every selected Candidate. Replaying the key returns the same admissions. One failure never rolls back another success.

### Retry

`POST /api/v1/source-discovery-sessions/{session_id}/retry`

Requires `Idempotency-Key` and returns `202 Accepted`. It retries a failed Session, or re-admits selected `import_failed` Candidates when the Session itself is ready. Other states return `409`.

## Session projection

```json
{
  "id":"session ID",
  "notebook_id":"notebook ID",
  "origin":"manual",
  "origin_chat_id":"optional Chat ID",
  "query":"original query",
  "summary":"optional bounded summary",
  "status":"searching",
  "error_code":"optional safe code",
  "candidates":[],
  "created_at":"RFC3339",
  "updated_at":"RFC3339",
  "completed_at":"optional RFC3339"
}
```

`origin` is `manual` or `research_agent`. `status` is `searching`, `ready`, or `failed`. Candidate records expose only `id`, `ordinal`, sanitized `title`, `canonical_url`, safe `display_url`, bounded plain-text `snippet`, optional safe `favicon_ref`, `selected`, `status`, optional `source_id`, and optional `import_error_code`.

Member APIs never expose expanded queries, raw Provider payloads, credentials, ranking diagnostics, child Checkpoints, or model reasoning.

## Durable states

Session transitions:

```text
searching -> ready
searching -> failed -> searching
```

Candidate transitions:

```text
discovered -> importing -> imported
                      \-> import_failed -> importing
```

The Source linked after admission follows the existing Source Processing state machine. A Candidate may be `imported` before that Source becomes `ready`; fetch or processing failure remains visible through the linked Source.

## Safe errors

| Code | HTTP use | Meaning |
| --- | --- | --- |
| `discovery_invalid_query` | 400 | Query violates the bounded input contract |
| `discovery_not_configured` | 503 | No Search Provider credential is configured |
| `discovery_timeout` | 504 | Provider deadline expired |
| `discovery_rate_limited` | 429 | Provider rate limit prevented completion |
| `discovery_unavailable` | 503 | Provider or network is temporarily unavailable |
| `discovery_invalid_response` | 502 | Provider returned an unusable envelope |
| `discovery_invalid_state` | 409 | Command is illegal for the current durable state |
| `discovery_candidate_invalid` | 400 | Candidate selection is foreign or not importable |
| `discovery_forbidden` | 403 | Current role cannot run the command |
| `discovery_import_failed` | per item | URL admission failed with a safe mapped reason |

Errors never include Provider response bodies, request headers, expanded child queries, or credentials.

## Runtime additions

Agent Runs distinguish `leader` and `research`, link children with nullable `parent_run_id`, and expose a nullable `discovery_session_id` child outcome internally. The parent job adds `waiting`; entering it clears its lease. A uniqueness constraint permits at most one Research child for a Leader delegation. Only the Leader can own `output_message_id`, and only the Leader is projected through Member Run APIs.

The Agent SSE snapshot adds optional `discovery_session_id` only after the Leader consumes the child outcome. Candidate data remains in the Source Discovery APIs.
