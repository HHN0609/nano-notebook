# Deep Research thinking mode

## Context

Nano currently pins Chat, source-discovery Research, and Deep Research to
`aliyun/qwen-plus`, which resolves to `qwen-plus-2025-07-28`. That snapshot is
a hybrid model: Alibaba Cloud documents that Qwen Plus snapshot versions from
2025-04-28 onward support both thinking and non-thinking modes, with thinking
disabled by default. A request enables thinking with the top-level
`enable_thinking` field in the OpenAI-compatible HTTP body.

Nano does not currently send that field. Its only registered Qwen Plus
capability is explicitly `non_thinking`, and the Bifrost adapter serializes
model, messages, temperature, output limit, tools, and tool choice only.
Consequently, all current modes use non-thinking inference even though the
provider model supports thinking.

References:

- Alibaba Cloud deep-thinking API and supported-model documentation:
  `https://help.aliyun.com/en/model-studio/deep-thinking`
- Alibaba Cloud Qwen Plus model information:
  `https://help.aliyun.com/zh/model-studio/qwen-plus`

## Goal

Enable Qwen Plus thinking for every model call owned by the selected Deep
Research planner and executor while keeping Chat and ordinary source-discovery
Research in non-thinking mode.

The setting must be immutable, pinned with the admitted Agent configuration,
visible in sanitized Replay, and included in the model-request identity used by
instrumentation. Hidden reasoning content must not enter product state, Replay,
Trace content, or the member-visible answer.

## Non-goals

- Enabling thinking for Chat, ordinary Research, or Studio.
- Changing the pinned provider model snapshot.
- Exposing or retaining chain-of-thought text.
- Preserving provider `reasoning_content` across model calls.
- Introducing streaming model responses.
- Changing Deep Research temperature, output limits, context-compaction
  thresholds, action budgets, or retry limits.
- Proving that thinking improves Deep Research quality. Quality evaluation is
  follow-up work after the invocation path is correct.

## Considered approaches

### 1. Immutable policy-driven invocation

Add the thinking switch to the immutable Model Policy, carry it through the
pinned runtime invocation policy, and serialize it in the provider request.
Publish new Deep Research Policy, Context Policy, Definition, and Release
versions.

This is the selected approach. It keeps ownership in the Catalog, preserves
old Runs exactly, and makes request behavior reviewable and replayable.

### 2. Runtime injection by Agent identity

The worker could inspect the active Agent definition and set thinking for
Deep Research at request construction time. This is smaller mechanically but
makes behavior depend on an implicit branch outside the pinned Model Policy.
Replay and request identity could disagree with the actual provider call.

### 3. Bifrost-wide model override

Bifrost could enable thinking for every `aliyun/qwen-plus` call. This cannot
distinguish Deep Research from Chat or ordinary Research and therefore violates
the requested scope.

## Catalog model

`agentcatalog.ModelPolicy` gains an optional `enable_thinking` boolean. Missing
values resolve to `false`; the field uses `omitempty` in the catalog structure
so loading and re-marshalling an old Policy does not change its canonical hash
or registered payload. New Deep Research policies set it explicitly to `true`:

- a new Planner Policy version retains temperature `0`, maximum output `16384`,
  and timeout `120000ms`;
- a new Executor Policy version retains temperature `0`, maximum output
  `16384`, and timeout `200000ms`.

Planner and Executor remain separate versions because enabling thinking must
not silently change their established timeout distinction.

The Qwen Plus capability receives a new immutable version with the same pinned
snapshot and token limits but `invocation_mode: "thinking"`. New Deep Research
Context Policies bind the two new invocation Policies to this thinking
capability while retaining the existing Deep Research context values:

- soft input limit: `180000`;
- estimation safety: `8192`;
- exact recent suffix: `32768`;
- summary output limit: `4096`;
- overflow retries: `2`.

Catalog validation rejects a Context Policy whose capability invocation mode
does not agree with its Model Policy's effective thinking value. This prevents
a `thinking` capability from being paired accidentally with a non-thinking
wire request, or the reverse.

The selected planner and executor Definitions receive new immutable versions
that differ only in their Model Policy references. A new `nano.default` Release
selects those Definition versions and leaves all other roots unchanged.
Control-plane, worker, and evaluation defaults advance together to that Release.
Previously admitted Runs retain their pinned old Policy and remain
non-thinking.

## Request flow

At admission, the selected Model Policy's `enable_thinking` value is persisted
in `agent_model_policy_versions` with the other immutable invocation fields.
The registry table gains a non-null boolean column defaulting to `false`; old
registered Policies remain valid because their canonical payload and SHA do
not change. An admitted Run continues to pin the Policy identity, version, and
SHA rather than copying a mutable switch onto the Run row. Loading an attempt
joins that exact Policy and reconstructs a `ModelInvocationPolicy` containing
the same value. Every Deep Research request, including planning, iterative
execution, step compaction, rollup, and report generation, already receives the
loaded invocation policy and therefore inherits thinking without
executor-specific branches.

The Bifrost adapter sends:

```json
{
  "model": "aliyun/qwen-plus",
  "enable_thinking": true
}
```

`enable_thinking` is a top-level field because Nano sends raw
OpenAI-compatible HTTP JSON; `extra_body` is an SDK-only wrapper.

For existing policies and non-Deep-Research requests, the adapter sends an
explicit `false`. This avoids dependence on a provider default and ensures the
wire request matches the pinned Nano policy.

## Response and privacy boundary

Thinking-mode Qwen may return `reasoning_content` and may report
`reasoning_tokens`. Nano continues to parse and publish only final `content` or
tool calls. Unknown response fields remain ignored by the strict normalized
domain boundary, so hidden reasoning text is neither projected into later
context nor retained in Replay.

When Bifrost reports `completion_tokens_details.reasoning_tokens`, the existing
metadata and Trace projection continue to record only the numeric token count.
The count is operational telemetry, not reasoning content.

Nano deliberately does not enable `preserve_thinking`. Each call may reason
afresh from durable business context, accepted Action Results, and compacted
summaries; prior chain-of-thought is not an authority and is not required for
crash recovery.

## Replay, hashing, and token budgeting

Sanitized model-request Replay adds `enable_thinking` to its normalized request
document. This reveals invocation mode but no hidden reasoning. The current
Replay payload has no decode path or strict external schema consumer, so this
additive field remains schema version 1.

The model-request hash includes `enable_thinking`, so otherwise identical
thinking and non-thinking calls cannot share a request identity.

Existing input budgets remain unchanged because `enable_thinking` adds no
model-visible prompt text. The current `max_completion_tokens=16384` continues
to bound the provider's combined thinking and answer generation. Thinking may
leave fewer tokens for the final answer; the implementation does not raise the
limit without separate measurement and design.

## Failure handling

- If Bifrost or DashScope rejects `enable_thinking`, the ordinary normalized
  model error path fails the call; Nano must not silently retry without thinking.
- Existing model timeouts remain authoritative. Thinking that exceeds them
  follows current retry and terminal-failure behavior.
- A response containing only reasoning and no final content or tool call remains
  an invalid model response.
- Existing Runs pinned to old non-thinking Policies are not migrated.

## Verification

Implementation follows test-driven development and proves:

1. the Bifrost HTTP body contains `enable_thinking: true` for a thinking
   invocation and explicit `false` for a non-thinking invocation;
2. request hashing differs only when the thinking setting differs;
3. sanitized request Replay records the setting without response reasoning;
4. Catalog loading resolves the new Planner and Executor Policies to a thinking
   capability while Chat and ordinary Research remain non-thinking;
5. the new default Release selects only the new Deep Research definitions;
6. persisted admission and runtime loading preserve the thinking flag;
7. focused model, Catalog, Replay, and runtime tests pass, followed by the
   relevant broader Go test suites and `go vet`.

A credentialed live Qwen call is desirable acceptance evidence when a local
DashScope key and Bifrost service are available, but it is not a deterministic
CI gate. If it is not run, the handoff must state that limitation explicitly.

## Rollout and rollback

Rollout is the new default Agent Release. New Deep Research Runs use thinking;
other modes and already admitted Runs do not change. Operational review should
compare Deep Research latency, timeout rate, output tokens, reasoning tokens,
and report completion rate before making any quality or budget claims.

Rollback selects the preceding `nano.default` Release. No database migration or
Run rewrite is required because Catalog objects and admitted pins are immutable.
