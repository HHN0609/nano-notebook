# Source Workspace SSE Design

Date: 2026-07-26
Status: approved for implementation

## Goal

Remove browser polling from Source Discovery and Source processing status updates. The browser must receive committed state changes through SSE while the existing durable discovery and source-processing job queues retain their current lease and polling behavior.

## Scope

This change covers two browser projections:

1. A Source Discovery session, including `searching`, `ready`, `failed`, and candidate import-state changes.
2. A Notebook's Sources and the active Chat's Source selection, including `processing`, `ready`, and `failed` transitions.

The existing one-shot HTTP commands remain unchanged: create/retry a Discovery session, change candidate selection, import candidates, upload a Source, and change Chat Source selection. Existing initial GET requests may remain for initial page loading, but no timer or query refetch interval may repeatedly request state.

## Non-goals

- Do not change how discovery or source-processing workers claim durable jobs.
- Do not replace job leases, heartbeats, retry scheduling, or recovery scans.
- Do not introduce a general-purpose event bus.
- Do not deliver incremental patches that require client-side event replay.

## Architecture

Use the existing Agent Run projection pattern:

1. A state-changing transaction writes its durable rows and calls `pg_notify` with the affected projection identity.
2. PostgreSQL delivers the notification only after commit.
3. A Control Plane listener receives the notification and wakes an in-process keyed hub.
4. Each SSE handler reloads the authorized full projection from PostgreSQL and emits it.
5. `EventSource` reconnects automatically. Every connection starts with a full snapshot, so reconnect repairs any missed notifications.

Notifications are wakeups, not business records. The database remains the source of truth. No table or durable-job schema change is required.

Use two notification channels:

- `nano_source_discovery_sessions`, payload: Discovery session ID.
- `nano_notebook_sources`, payload: Notebook ID.

When a listener reconnects, it wakes all local subscribers so active streams reload their projections.

## SSE APIs

### Discovery session

`GET /api/v1/source-discovery-sessions/{session_id}/events`

- Requires the same Notebook membership authorization as the current session GET.
- Subscribes before reading the first projection to avoid the subscribe/snapshot race.
- Emits `event: discovery` with `{"session": <full session>}`.
- Remains open after `ready` or `failed`, because retry and candidate import operations can mutate the same session.

### Notebook Sources

`GET /api/v1/notebooks/{notebook_id}/sources/events?chat_id={chat_id}`

- Requires Notebook membership; when `chat_id` is present it must belong to the Notebook and be visible to the caller.
- Emits `event: sources` with `{"sources": [...], "source_ids": [...]}`.
- `source_ids` is the current Chat selection after filtering to ready Sources, matching the existing workspace behavior.
- The stream remains open for the component lifetime.

Both endpoints use `text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`, and a 15-second comment heartbeat. Heartbeats do not query PostgreSQL.

## Notification Coverage

Emit notifications in the same transaction as durable mutations:

- Discovery session creation, retry, completion, and failure.
- Discovery candidate selection and `importing`, `imported`, or `import_failed` changes.
- Source creation/admission into `processing`.
- Source transition to `ready` or `failed`, deletion, and restoration/replacement paths that change the Notebook Source projection.
- Chat Source selection changes, including automatic selection after a discovered Source becomes ready.

Repeated notifications may coalesce. Hubs use a one-element buffered wake channel because every wake reloads a full snapshot.

## Frontend Behavior

### Source Discovery

- Remove the one-second `setTimeout` loop.
- Once a session ID is known, open its Discovery EventSource.
- Replace local session state from each `discovery` event.
- Close the old EventSource when the active session changes or the component unmounts.
- Command responses may still update state immediately; the SSE projection is authoritative and provides cross-tab convergence.

### Notebook Sources

- Remove both 2.5-second React Query `refetchInterval` settings.
- Open one Sources EventSource for the active Notebook and optional Chat.
- Write each event's `sources` and `source_ids` directly into the existing React Query caches.
- Close and recreate the stream when Notebook or Chat identity changes.

Transient SSE disconnects do not display a terminal error because `EventSource` reconnects. Existing one-shot query and command errors retain their current UI behavior.

## Security and Correctness

- Never place user IDs, URLs, titles, or content in PostgreSQL notification payloads.
- Re-read and authorize every SSE projection under the request principal.
- Do not trust a notification payload as authorization or as state.
- Subscribe before the initial read, and send an initial full projection before waiting.
- Full projections make duplicate, coalesced, reordered, or missed wakeups harmless.

## Testing

Backend integration tests must prove:

- Each SSE endpoint sends an initial authorized snapshot.
- A committed Discovery change causes a new Discovery event.
- A committed Source/selection change causes a new Sources event.
- Uncommitted or unrelated identities do not wake/send the stream.
- Unauthorized access is rejected.
- Listener reconnect wakes subscribers and full snapshots recover current state.

Frontend tests must prove:

- Source Discovery opens the expected EventSource and updates from events without timers.
- Notebook Sources and Chat selection update from events with no `refetchInterval`.
- Streams close on unmount and identity changes.
- Search, retry, import, upload, and selection commands continue to work.

Repository acceptance requires Go tests/vet/build, web unit tests/typecheck/build, and the affected Source Discovery end-to-end test.
