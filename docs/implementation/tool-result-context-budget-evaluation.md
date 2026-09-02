# Tool Result context budget evaluation

Date: 2026-09-02

## Scope

This evaluation isolates one deterministic long `read_url` result and follows it through the context layers used by Deep Research. It measures serialized UTF-8 bytes, not semantic retention, answer quality, or a universal production compression rate.

The executable fixture is `TestToolResultContextLayerCompressionRatios` in `internal/agent/tool_result_context_layers_test.go`.

## Result boundary

The raw Action output is 510,048 bytes. With the canonical outer `{action_id,status,output}` envelope it would occupy 510,114 bytes in an Action message.

The Redis externalization projection produces a complete 51,200-byte Action message, including the outer envelope. The checkpoint payload and the immediately adjacent model request contain exactly the same 51,200 bytes. Redis retains all 510,048 original output bytes.

| Result representation | Bytes | Remaining vs. raw Action message | Reduction vs. raw Action message |
|---|---:|---:|---:|
| Raw Action message | 510,114 | 100.0000% | 0.0000% |
| Bounded checkpoint | 51,200 | 10.0370% | 89.9630% |
| Adjacent Action message | 51,200 | 10.0370% | 89.9630% |

This equality is the important pre-compaction invariant: rebuilding context from the accepted checkpoint neither truncates the projection again nor expands it back to the Redis body.

## Research Step layers

For an apples-to-apples context comparison, these rows serialize the same complete Research Step, including its Tool Call and Result representation.

| Context layer | Bytes | Remaining vs. raw Step | Reduction vs. raw Step |
|---|---:|---:|---:|
| Raw complete Step | 520,198 | 100.0000% | 0.0000% |
| Redis-backed bounded Step | 53,274 | 10.2411% | 89.7589% |
| Archival capsule plus rehydratable shell | 733 | 0.1409% | 99.8591% |
| Task memory | 539 | 0.1036% | 99.8964% |

The capsule and task-memory rows use deterministic valid fixture content. Their exact ratios will vary with the model-generated summaries and the number of Steps covered. The stable product invariants are the byte ceilings, complete Tool Call/Result pairing, durable checkpoint authority, and Redis range rehydration while the cache entry remains alive.

## Reproduce

```bash
go test ./internal/agent -run '^TestToolResultContextLayerCompressionRatios$' -count=1 -v
```
