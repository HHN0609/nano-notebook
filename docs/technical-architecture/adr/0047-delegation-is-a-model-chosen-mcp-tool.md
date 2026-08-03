# Delegation is a model-chosen MCP tool

Every capability a Chat Leader can invoke, including delegating to a child Agent, is now a real MCP tool the model discovers in `tools/list` and chooses through the normal Controller loop. There is no separate structured-output router deciding whether to delegate on the model's behalf.

The embedded Catalog already expresses the source-discovery child as `delegate.research.source-discovery.v1`. `configuredDelegationAction.Available(Execution)` now returns `(ok, reasonCode)` and applies the same membership, provider, relationship, and single-child policy checks that the old `EvaluateDelegationPolicy` performed. `search_evidence` uses the same availability contract. When a conditional tool is hidden, both MCP and non-MCP tool-list construction emit a `nano.tool.filtered` Trace event carrying the tool name and reason.

`LeaderExecutor` no longer owns `DecideRoute()`, `delegateConfigured()` synthetic calls, `agent_run_routes`, or leader-route Trace records. A delegated child is scheduled when the model proposes the delegation tool; the Controller stops that attempt while the parent Job is suspended, then resumes normally after the child completes. Checkpoint replay, not a route row, provides idempotency.

New Chat Runs are always `configured`; `CreateConfiguredChatQueued` is the only Chat admission path and `AgentRelease` is required at startup. `legacy_role` remains a read-only/historical representation and an offline eval admission path; it is not used for new Chat traffic.

This supersedes the router-decision framing in ADR 0041 and ADR 0042 for new Chat delegation and realizes ADR 0045's configured Definition/Executor direction.
