# Remove Research compaction gain gate

## Decision

Research archival Capsules and Research Task Memories are accepted without
checking estimated token savings. The runtime no longer requires a minimum
absolute gain, a minimum gain ratio, or even `after < before`.

All semantic and safety checks remain unchanged: model output must satisfy the
strict schema and exact covered range, the complete candidate request must
reconstruct successfully, persistence must succeed, and the final Task Memory
candidate must fit the safe input budget before it becomes visible.

## Runtime change

Remove the gain predicate from both archival-Capsule acceptance and Task-Memory
acceptance. Remove the now-unused gain constants and helper. Do not introduce a
configuration flag or change immutable artifact schemas.

## Verification

Replace the integration expectation that a small-gain archival candidate is
rejected with an expectation that it is persisted and projected. Add focused
unit coverage proving that acceptance no longer depends on the before/after
token relationship. Keep existing invalid-output, reconstruction, persistence,
and safe-budget tests unchanged.
