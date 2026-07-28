# Use MCP as the Agent Tool Plane

Every production executable Agent tool will be discovered and invoked through one internal Nano Tools MCP Server. Sprint 10 migrates `calculate`, `current_time`, `search_evidence`, and `web_search`, and generates definition-specific `delegate.*` tools from immutable Agent Definitions. Ordinary tools return synchronously; delegation tools may suspend and later resume their parent through MCP Tasks.

The Agent Controller remains the execution authority. It scopes discovery to the pinned Definition and Executor ceiling, validates Provider-neutral Action Proposals, enforces product authorization and logical budgets, records durable Proposal and Result Checkpoints, and controls recovery and waiting. The MCP Server implements typed adapters over application services; it owns no Run store, queue, retry policy, model gateway, or product lifecycle.

Tool arguments carry business input only. For discovery and invocation, the Host injects a short-lived opaque Attempt Context Handle in request metadata outside the model-visible schema. The Server resolves the handle and revalidates the current leased Attempt's fencing authority, pinned Definition, allowlist, product authorization, deadline, and budget. The handle is process-local, expires with Attempt authority, and is replaced after recovery.

Each accepted Action also has a stable `action_id`. The Host injects it as non-model-visible metadata and reuses it across infrastructure Attempts. Invocation therefore remains at-least-once: pure and read-only adapters tolerate repetition, delegation enforces database idempotency by `action_id`, and only the currently fenced Attempt may append the accepted Result. A generic Tool Invocation ledger is rejected because it would duplicate Checkpoints without providing exactly-once RPC.

The Nano Tool Registry is the single catalog behind scoped `tools/list` and `tools/call`. Existing Action executors move behind thin adapters rather than being copied, and the legacy Action Registry retires after every caller and recovery path has migrated. Existing durable Action Checkpoints remain readable.

Provider function-call syntax does not define architectural capability. `select_leader_route` remains a Decision Contract in the Models Module because it constrains structured output without executing anything. New Research Runs instead propose one MCP `web_search` Action carrying one to three queries and complete deterministically from its checkpointed result. Legacy query-plan and search-result Checkpoints remain supported during drain.

Tool adapters register scheduling classes that configuration cannot widen. `ordered_sync` tools execute proposal order sequentially and checkpoint each accepted Result. A generated delegation tool is `exclusive_task` and must be the proposal's only Action because it suspends the parent. Sprint 10 adds no parallel tool execution.

The Controller owns exhaustive error mapping. Only bounded contract-declared domain errors become model-visible Action Results. Transient transport, database, or Provider failure produces no Result and retries the Attempt with the same logical identity; Lease loss abandons the Attempt; capability, authorization, Definition, and schema-integrity failures terminalize safely. MCP `isError` is a representation, not lifecycle authority.

Long-running delegation targets the `io.modelcontextprotocol/tasks` extension associated with protocol version 2026-07-28 and described in ADR 0042. The selected official SDK, Go toolchain, and Nano conformance evidence are delivery gates; Nano does not implement a private protocol fork. The first transport is in-process, but it still traverses MCP discovery, invocation, result/error, and Tasks contracts so the old direct Action path does not survive beside it.

We rejected separate registries for Actions and MCP delegation because they duplicate schemas, allowlists, error normalization, tracing, and recovery. We also rejected moving Controller authority into the MCP Server: protocol transport is not authorization or durable orchestration.

Implementation note (2026-07-28): the synchronous tool plane is active for `calculate`, `current_time`, `search_evidence`, and `web_search`. Generated `delegate.*` tools remain unregistered because official Go SDK `v1.7.0-pre.3` and official `main` commit `bc72835f62eb` cannot publicly encode the required SEP-2663 Task-returning `tools/call`; the runtime safely records the configured relationship as unavailable and continues Chat without claiming Tasks conformance.
