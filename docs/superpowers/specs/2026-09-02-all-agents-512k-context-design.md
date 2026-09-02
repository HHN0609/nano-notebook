# All-Agent 512K Context Design

## Goal

Move every Agent reachable from the default Agent Catalog release to the same context-compaction budget:

- `soft_input_limit_tokens`: `512000`
- `keep_recent_tokens`: `96000`
- `estimation_safety_tokens`: `8192`
- `summary_max_output_tokens`: `4096`
- `overflow_retry_limit`: `2`

The scope includes Chat, Chat's `research.source-discovery` child, Research Planner, Deep Research Executor, and all four Studio output Agents.

## Boundaries

This change only standardizes context budgeting and compaction behavior. Each Agent keeps its existing invocation behavior, including `max_output_tokens`, timeout, temperature, and `enable_thinking`. In particular, ordinary Chat, Source Discovery, and Studio do not gain Deep Research thinking behavior.

Historical Catalog artifacts remain immutable. Existing releases, Definitions, Model Policies, and Model Context Policies are not edited.

## Catalog Versioning

Create new Model Policy and Model Context Policy versions for every currently smaller-window execution path. The invocation fields are copied unchanged, while the new context policies use the shared budget above and the one-million-token Qwen Plus provider capability. Keep `pinned_max_output_tokens` equal to the corresponding invocation policy's output limit.

Create new Definition versions that point to those policies:

- Chat Leader, including a new Source Discovery child Definition
- Research Planner
- Source Discovery
- Studio Report, Flashcards, Mind Map, and Data Table

The current Deep Research Executor already points to the required 512K policy and can be reused unchanged.

Publish `nano.default@23` with the new roots, then update the Control Plane, Worker, and Agent Eval defaults from `nano.default@22` to `nano.default@23`. Runs that explicitly pin an older release retain their prior behavior.

## Runtime Flow

The runtime continues to resolve context configuration through the pinned Definition and Model Policy. No global override or Agent-name conditional is added. Consequently, registry persistence, Run pinning, Replay, and compaction telemetry remain aligned with the actual configuration used for each invocation.

## Verification

Add a Catalog test that starts at every `nano.default@23` root, recursively visits child Definitions, resolves each Definition's Model Context Policy, and asserts the shared five context-budget values. This prevents a root or delegated child from silently retaining the old window.

Update default-release tests for the Control Plane and Worker, and verify the Agent Eval CLI default. Run the focused Catalog and command tests first, followed by the repository's full Go acceptance gate.
