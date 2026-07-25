# Qwen Plus Default and Leader Replay Design

## Goal

Use `aliyun/qwen-plus` for new local Notebook runs and make the existing Trace Replay mechanism capture the Leader Router and Research Planner model exchanges.

## Scope

- Change the local and server fallback chat model from `aliyun/qwen-flash` to `aliyun/qwen-plus`.
- Allow `qwen-plus` in the repository-owned Bifrost configuration.
- Reuse the existing `InvokeDecisionModel`, `ModelTraceOptions`, encrypted `ReplayStager`, Collector projection, audit, retention, and Trace UI path.
- Give each Leader Router and Research Planner invocation stable request and decision Replay identities.
- Add regression tests for both model selection and the two Replay-enabled model phases.

The citation protocol, Source processing, Replay retention, encryption, authorization, and UI are unchanged.

## Design

`LeaderExecutor` receives the already constructed worker `ReplayStager` through a new explicit option. When it invokes the traced Leader Router or Research Planner, it passes the stager plus phase-specific request and decision identity keys through `ModelTraceOptions`. `InvokeDecisionModel` remains the sole instrumentation boundary: it creates the `agent.model.call` span, stages the normalized model request and decision, and attaches their IDs to the span records.

The control plane and local start script default to `aliyun/qwen-plus`. Bifrost permits `qwen-plus` for the Aliyun key. `NANO_CHAT_MODEL` remains an override, so deployments can deliberately select another configured model without code changes.

## Failure Behavior

Replay recording keeps its current fail-closed behavior. If staging a Leader Router or Research Planner request or decision fails, the invocation returns the existing recording error rather than producing an uninspectable model call. No raw provider envelope or chain of thought is recorded.

## Verification

- A focused test first demonstrates that Leader Router and Research Planner spans lack Replay attachments.
- After implementation, the same test requires one `model_request` and one `model_decision` attachment for each phase.
- Configuration tests require `aliyun/qwen-plus` as the fallback and local default.
- The relevant Go test packages and repository checks pass without modifying unrelated user changes.
