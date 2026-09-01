# Ephemeral Tool Result Cache

## Status

- **Status:** Implemented
- **Date:** 2026-09-01
- **Initial scope:** Deep Research read-only tools

## Decision

Large Tool Result bodies may be removed from the durable Agent trajectory and
stored only in Redis for 30 minutes. The model receives a bounded preview and
an opaque `result_ref`. It can call `read_tool_result` while the entry exists.
After expiry, the body is intentionally unavailable and the model must repeat
the original read-only Tool Call.

Redis is not an authority or recovery store. Existing permanent artifacts are
unchanged: PDFs and Source Maps remain in object storage, Evidence Units remain
in their existing authority, and Research workspace files remain in object
storage. Redis contains only the transient body returned by a Tool invocation.

## Goals

- Keep large Tool Result bodies out of model context and checkpoints.
- Let the model selectively reread a recent result without repeating network,
  parsing, or retrieval work.
- Bound Redis memory with a fixed 30-minute TTL.
- Keep the mechanism generic and independent of PDF, HTML, or Evidence schemas.

## Non-goals

- Durable recovery of expired Tool Results.
- Using Redis as Source, Evidence, workspace, checkpoint, or citation storage.
- Automatically rerunning expired Tool Calls inside the Harness.
- Caching side-effecting Tool Results in the first release.
- Changing the layered Capsule and Task Memory compaction policy.

## Eligibility

The first release applies only to allowlisted, read-only, replay-safe Deep
Research tools. A Tool Result is externalized when its Layer 1 projection is
lossy: the complete serialized result is larger than the bounded result that
would otherwise be placed in model context.

Small results may remain inline. Mutation results such as workspace writes are
not eligible merely because their response is large; an expired result must
never force the model to repeat a side effect.

## Write path

After a successful eligible Tool invocation:

1. Serialize the complete result using the Tool's accepted result encoding.
2. Compute its byte length and SHA-256.
3. Generate an unguessable opaque `result_ref`.
4. Write one scoped envelope to Redis with an atomic 30-minute TTL.
5. Build the bounded model projection only after the Redis write succeeds.
6. Checkpoint the compact projection, not the Redis body.

The TTL is absolute from creation. Reads do not renew it. This keeps retention
and memory use predictable.

The model-visible result has this shape conceptually:

```json
{
  "action_id": "decision:12/action:0",
  "status": "succeeded",
  "content_state": "externalized",
  "preview": "bounded Tool-owned preview",
  "result_ref": "tr_opaque_random_value",
  "result_bytes": 182400,
  "sha256": "...",
  "expires_at": "2026-09-01T12:30:00Z",
  "read_tool": "read_tool_result"
}
```

The Redis value contains the result bytes plus the minimum metadata required
to authorize and validate a read:

- schema version;
- owner, Chat, Run, Action, and Tool identities;
- media type or result encoding;
- byte length and SHA-256;
- creation and expiry timestamps; and
- complete serialized result bytes.

Redis keys must not contain user text, URLs, queries, or object-storage keys.
The opaque reference must have enough entropy to resist guessing. Redis must
also have an instance-level memory limit and an explicit eviction policy; an
early eviction is treated exactly like expiry.

## Read path

Add one generic read-only Tool:

```text
read_tool_result(result_ref, offset?, max_bytes?)
```

The Harness resolves the opaque reference and verifies that the current actor,
Chat, Run, and Tool permissions match the stored envelope. It validates the
stored length and SHA-256 before returning content.

Each call returns a bounded byte range, never the entire unbounded value:

```json
{
  "result_ref": "tr_opaque_random_value",
  "offset": 0,
  "content": "...",
  "next_offset": 16384,
  "complete": false,
  "expires_at": "2026-09-01T12:30:00Z"
}
```

The implementation must clamp `max_bytes` to a versioned server limit and
reject invalid offsets. Text slicing must not emit invalid UTF-8. Structured
JSON is returned as byte-range text; this first release does not add JSONPath
or semantic search over cached results.

## Cache miss and rerun behavior

If the key expired, was evicted, or disappeared after a Redis restart,
`read_tool_result` returns the stable domain error:

```json
{
  "error_code": "tool_result_expired",
  "result_ref": "tr_opaque_random_value",
  "retryable": false,
  "remediation": "Repeat the original read-only Tool Call if the content is still needed."
}
```

The Harness does not automatically repeat the original call. At Layer 2, the
original Tool name and complete accepted input remain in model context, so the
model can choose whether to repeat it. At Layer 3, an old result may disappear
together with its Tool Call and parameters; the Task Memory is then the only
active representation, and exact rereading starts again from its durable
Source, Evidence, or workspace references.

A Redis write failure must not create a model-visible `result_ref`. The Tool
returns its ordinary bounded preview with `content_state: "not_cached"`; if
the complete result is still needed, the model can repeat the original
read-only call. Redis failure therefore degrades performance, not correctness.

## Checkpoint and compaction boundary

For an externalized result, the accepted Action Result checkpoint stores only
the compact result envelope: status, preview, `result_ref`, size, hash, expiry,
and stable error data. It does not store the complete transient body.

Checkpoint recovery after the TTL may reconstruct a valid trajectory whose
`result_ref` is expired. This is expected. Checkpoints preserve what was called
and what the Agent observed; they do not make ephemeral Tool bodies durable.

Layer 2 Capsules may summarize the result before expiry, but compaction must
not depend on the Redis entry still existing. Layer 3 remains responsible for
retaining task-level conclusions and durable domain references.

## Deployment

Nano currently does not depend on Redis. This feature therefore requires:

- one Redis/Valkey-compatible service in local and deployed environments;
- connection, timeout, TLS, authentication, maximum-value-size, and TTL
  configuration;
- bounded connection pools and short operation deadlines;
- `maxmemory` plus a deliberate eviction policy;
- metrics for writes, reads, hits, misses, evictions, bytes, latency, and
  errors; and
- logs that contain hashes and identities but never cached result bodies.

The feature must be guarded by an immutable Research executor/policy version so
old Runs retain their original checkpoint and projection semantics.

## Acceptance criteria

1. A large eligible Tool Result produces a bounded checkpoint/model projection
   and a Redis entry with a 30-minute TTL.
2. `read_tool_result` can page through the cached bytes and reconstruct the
   exact original result before expiry.
3. Cross-user, cross-Chat, and cross-Run reads are rejected.
4. Expiry, eviction, restart loss, and a missing key return
   `tool_result_expired` without an automatic Tool rerun.
5. Redis write failure returns no false readable reference and leaves the Agent
   able to repeat the original read-only Tool Call.
6. Checkpoints and traces contain no complete externalized body.
7. Side-effecting Tools are ineligible in the first release.
8. Permanent PDF, Evidence, Source Map, and Research workspace retrieval still
   works after the Redis entry expires.
9. Unit and integration tests cover TTL, pagination, UTF-8 boundaries, hash
   validation, authorization, cache outage, early eviction, and checkpoint
   recovery after expiry.

## Implementation evidence

- The behavior is pinned by `research.executor@14` and `nano.default@21`.
- Only successful, eligible results larger than the 16 KiB inline limit are
  externalized. Small results stay inline and side-effecting tools remain
  ineligible.
- The authoritative full Planner-to-Executor E2E artifacts, exact cache lifecycle evidence, full
  before/after messages, and per-layer compression measurements are preserved
  under `.codex-artifacts/tool-result-cache-vlm-vla-full-e2e-20260901T1747/`.
- The accepted session used its own completed Planner output without plan
  substitution: Planner `run_u-zu-2eZb06ZK2JtwXk4gw`, Executor
  `run__Gn78OUd_28BGBmexFhP5g`.
- A deliberate Redis restart removed all three ephemeral keys while the
  Evidence/Capsule/Rollup/report fingerprint and sampled workspace object hash
  remained unchanged. Permanent PDF and page-aware Source Map integration tests
  also passed with Redis empty.
- Layer 1, Layer 2, Layer 3, and cumulative compression are measured separately
  in both UTF-8 bytes and deterministic estimated tokens.
