# Agent Tool Capability Guard Design

## Problem

An Agent Definition can register a Tool that is not advertised for a specific
model decision because of runtime availability. The Controller previously
validated proposals against the full attempt registry rather than the exact
Tool definitions sent in that request. A model could therefore propose a
registered-but-unadvertised Tool, pass structural validation, and enter an
unrelated budget fallback that disabled every Tool.

The same path exposed a second mismatch: TODO Tools were governed by a separate
`plan_mutations` quota. `research.executor@15` did not declare that legacy
quota, so its TODO Tools were never advertised even though they were registered
and the model could reasonably need them throughout a long run.

## Design

TODO Tools remain classified as plan mutations only so they do not consume the
business Action or business decision budgets. They have no independent runtime
quota. Once registered for an Agent, they remain eligible throughout the Run,
subject only to their ordinary runtime availability and the Run deadline.

For every model request, the Controller constructs a map from the exact Action
definitions included in that request. A returned proposal must name only Tools
in this map before registry/schema validation or checkpoint acceptance. A name
outside the map produces a recoverable invalid-response detail that identifies
the unavailable Tool and lists the currently advertised Tools.

Batch-size, multiple-TODO, and remaining-business-budget violations use the
same bounded invalid-response recovery path. They do not disable Tools or force
an early Final. No invalid proposal is checkpointed or executed.

`rewrite_todo_list` also has a semantic duplicate guard. Before accepting a
rewrite, the Controller canonicalizes its JSON input and compares it with
successful rewrites already present in the checkpoint prefix. A match is a
recoverable invalid response: the duplicate is neither checkpointed nor
executed, and the model is instructed to choose another Tool or materially
change the plan. The guard is based on Tool name plus canonical input, not on
adjacent call names or a numeric mutation quota, so genuinely changed rewrites
remain available.

## Compatibility

Historical Definition JSON remains immutable. Existing `plan_mutations` values
are retained as `LegacyPlanMutations` solely for canonical JSON and hash
compatibility; runtime loading, Tool filtering, proposal validation, and budget
accounting do not read them.

## Verification

- A Controller test proves TODO Tools remain advertised without a dedicated
  quota and do not consume business budgets.
- A capability-map regression proves an unadvertised Tool is not executed and
  the retry receives both the rejected and currently available Tool names.
- A budget regression proves an oversized batch is retried while Actions stay
  advertised.
- A semantic-duplicate regression proves differently formatted but equivalent
  completed TODO rewrites are rejected before a second checkpoint or execution.
- An MCP Tool-plane test proves TODO Tools remain advertised after the business
  Action budget reaches zero.
- Catalog and PostgreSQL integration checks prove historical Definitions still
  register idempotently.
- A live VLA Deep Research run validates the real Planner/Executor path.
