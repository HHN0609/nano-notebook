# Aggressive Chat Discovery and Agent Activity Design

**Date:** 2026-09-04

**Status:** Proposed for review

## 1. Outcome

Ordinary Chat will actively retrieve information for factual questions. The Chat model first searches the Notebook's selected Sources and may concurrently search for complementary public Sources. Public search becomes a normal `discover_sources` tool call instead of a delegated `research.source-discovery` child Run.

When public search produces at least one Source that is not already in the Notebook, the left Discovery panel opens and Chat pauses its substantive answer so the user can decide what to import. If every result already exists in the Notebook, the panel stays closed and Chat continues with any usable selected-Source evidence. Explicit Deep Research keeps its existing multi-step planner and executor. Its central plan/progress panel gains one lifecycle correction: after the Research session is cancelled, that panel closes instead of remaining as a terminal metrics card.

While a Run is active, the member UI shows elapsed Agent work time and a safe, readable description of every currently executing tool. These descriptions are produced by a server-owned allowlist and never expose raw tool names, unrestricted arguments, model prompts, chain of thought, internal identifiers, or raw tool results.

## 2. Goals

1. Make retrieval the strong default for factual, explanatory, comparative, current, or source-dependent Chat questions.
2. Let `search_evidence` and `discover_sources` execute in the same Action batch when both are useful.
3. Replace the one-model-call, one-search-call Chat discovery child with a smaller ordinary tool boundary.
4. Open Discovery only for novel, ready candidates that require a user decision.
5. Expose useful live Agent activity and elapsed time without exposing internal working data.
6. Preserve deterministic replay, idempotency, historic pinned Runs, and partial results from concurrent Actions.

## 3. Non-goals

- No classifier service, regex intent gate, or evidence-sufficiency score is introduced.
- No change to explicit Deep Research planning, execution, report synthesis, or source-admission rules; only its cancelled-state panel presentation changes.
- Discovery candidates are not answer evidence until the user imports them and the resulting Source becomes authorized, ready, and searchable.
- No automatic import of public search results.
- No generic rendering of tool JSON, model messages, traces, stack traces, or hidden reasoning.
- No durable second activity log beside the existing Run and Checkpoint records.
- No attempt to guarantee that an external read-only search provider executes exactly once.

## 4. Current Boundary and Motivation

The ordinary Chat release currently gives the leader `search_evidence` and delegates public Source discovery to `research.source-discovery@2`. That child is not a general research worker: its planner makes one model call to form one to three queries, its executor performs one bounded `web_search` Action, then it merges and persists candidates. The parent resumes with a fixed review message rather than using the child result as model-visible evidence.

This child Run adds a delegation lifecycle without providing multi-turn research, page reading, source import, iterative reasoning, or answer synthesis. Those richer behaviors belong to explicit Deep Research and remain there. For ordinary Chat, Source discovery is more accurately represented as a business tool whose side effect is a Discovery Session.

The current Discovery client can activate the panel while a session is still `searching`. That is too early: the final result may contain no novel candidates. Activation must instead be based on the completed server result.

## 5. Resulting Chat Flow

```text
User question
  -> Chat model applies retrieval-first prompt policy
  -> one Action batch may contain
       search_evidence(selected Notebook Sources)
       discover_sources(complementary public Sources)
  -> Harness executes independent Actions concurrently
  -> each Action Result is checkpointed independently
  -> Discovery Session becomes terminal
       ready + novel candidates -> open left panel; pause substantive answer
       ready + duplicates only  -> keep panel closed; continue Chat
       ready + zero candidates   -> keep panel closed; continue if possible
       failed                    -> keep panel closed; continue if possible
  -> Chat synthesizes only from authorized model-visible evidence
```

The browser may display both active Actions at once. Completion order does not change ownership: `search_evidence` returns citable content to the model, while `discover_sources` returns only discovery metadata and creates the user-facing selection session.

## 6. Prompt Policy

The grounded Chat prompt receives a new version with these behavioral instructions:

1. For factual questions, strongly prefer calling `search_evidence` before answering when selected Sources are available.
2. For questions that could benefit from public facts, recency, broader coverage, corroboration, or missing Notebook coverage, strongly prefer calling `discover_sources`.
3. When both local evidence and complementary public Sources are useful, propose both tools in the same Action batch. Do not wait for local retrieval merely to decide whether public discovery is allowed.
4. A bare factual question is sufficient reason to attempt discovery. The model does not need an explicit request such as “search the web.”
5. Do not retrieve for greetings, acknowledgements, conversational coordination, or pure transformation tasks such as rewriting and translation when no factual verification is requested.
6. Never cite or summarize Discovery candidates as verified evidence. When novel candidates exist, tell the user that supplementary Sources were found and ask them to review or import from the left panel before the substantive answer continues.
7. If all discovered candidates already exist but are not selected, say that relevant Sources already exist in the left Sources panel and ask the user to select them. Do not imply that their contents were read.
8. If retrieval or discovery fails, disclose the limitation briefly and use whatever authorized evidence remains; do not fabricate support.

These are model instructions, not a second deterministic intent classifier. Harness validation continues to enforce tool schemas, authorization, action budgets, and evidence/citation boundaries, but it does not add a regex or relevance gate for deciding whether a question deserves discovery.

## 7. `discover_sources` Tool Contract

### 7.1 Input

The tool accepts one to three concise, complementary natural-language queries. It may also accept a bounded result target already supported by the provider policy. The schema rejects empty queries, excessive query counts, and unbounded provider options.

The Chat model owns query formulation in its existing model turn. The tool does not make another planning model call.

### 7.2 Execution

The tool:

1. creates or reuses the Discovery Session identified by the current `run_id` and `action_id`;
2. invokes the existing public search provider for the bounded query set;
3. applies existing normalization, canonical URL handling, domain caps, validation, and candidate persistence;
4. compares canonical candidates with all Sources in the current Notebook, not only selected Sources;
5. marks existing candidates through the current imported/existing representation and does not present them as novel choices; and
6. completes the session as `ready` or `failed` according to the existing Discovery lifecycle; a successful search with no candidates is `ready` with zero counts.

Provider results remain candidate metadata. The tool does not read result pages into the Chat context and does not bypass Source admission.

### 7.3 Model-visible result

The Action Result contains only bounded aggregate metadata:

```json
{
  "discovery_session_id": "opaque session reference",
  "status": "ready",
  "novel_candidate_count": 3,
  "existing_candidate_count": 2,
  "existing_selected_count": 1
}
```

No candidate snippets, page bodies, raw provider payloads, internal errors, or unrestricted URLs become answer evidence through this result.

### 7.4 Idempotency and replay

`run_id + action_id` is the idempotency key. Re-executing the same Action reuses its Discovery Session and upserts candidates by canonical identity. If external search completed but its Action Result was not checkpointed, replay reads the completed session result and checkpoints it instead of creating another session. A provider call that was interrupted before durable completion may repeat because it is read-only; canonical candidate persistence still prevents duplicates.

## 8. Concurrency and Result Semantics

`discover_sources` is advertised as a normal concurrent-capable tool in the new Chat definition. Existing batch execution schedules it independently from `search_evidence`; it is not serialized behind retrieval and it does not create a child Run.

Each proposal and result keeps its own Action identity and checkpoint. If one concurrent Action fails, the successful sibling result remains available after resume:

- local retrieval success plus discovery failure allows an evidence-grounded answer with a brief discovery limitation;
- discovery success plus local retrieval failure opens Discovery when novel candidates exist, but candidates still cannot support a substantive answer before import;
- cancellation or Run failure terminates the visible activity state for every unfinished Action;
- replay reconstructs completed and pending Actions from checkpoints rather than relying on browser memory.

## 9. Discovery Panel Activation

The server projection and client activation rule use the completed session state:

```text
activate = session.status == ready && novel_candidate_count > 0
```

The UI must not open merely because a session is `searching`, because a session ID exists, or because duplicate candidates exist.

Behavior by terminal outcome:

| Outcome | Panel | Chat behavior |
|---|---|---|
| Ready with novel candidates | Open | Pause substantive answer and ask the user to review/import |
| Ready with duplicates only | Closed | Continue with selected-Source evidence; if matches are unselected, direct the user to Sources |
| Ready with zero candidates | Closed | Continue with local evidence or disclose that no supplement was found |
| Failed | Closed | Preserve sibling results and disclose the discovery limitation when relevant |

The server supplies or makes derivable the novel count from candidate state so the browser does not infer novelty from titles or URLs on its own.

## 10. Deep Research Cancellation UI

The Deep Research plan/progress panel in the central Chat area is transient workflow UI. It is useful while a session is `planning`, `awaiting_confirmation`, `queued`, `running`, or `publishing`, and it may retain the completed or failed terminal presentation where that outcome is useful. It must not render when the Research session status is `cancelled`.

After a successful cancel response, the client updates both the Run and Research session projections so the panel closes immediately. The terminal SSE or a subsequent refetch remains authoritative and must also close it after reconnect or cross-client cancellation. The associated user message may continue to show the compact existing “已停止” state; cancellation does not delete messages, plans, checkpoints, or persisted Research history.

This rule applies only to the central `ResearchStatusCard`/plan-progress surface. It does not close the left Discovery panel, change the user's manual Sources navigation, or discard a completed Discovery Session.

## 11. Agent Activity Projection

### 11.1 Source of truth

Activity is an ephemeral projection of existing product state:

- Run timestamps and terminal status come from `agent_runs` or the existing Chat Run projection;
- Action start time comes from the corresponding `action_proposal` checkpoint `created_at`;
- an Action is active when its proposal has no matching terminal `action_result` checkpoint;
- completion, cancellation, or Run failure clears active Actions; and
- the final elapsed duration is computed from persisted `started_at` and `finished_at`.

No Trace/ClickHouse query is required for member UI liveness, and no new activity table is introduced. Trace remains the operational and analytics record; PostgreSQL Run state remains the product-facing authority.

After appending an Action proposal or result, the transaction emits the existing `nano_agent_runs` notification. The current SSE path then re-reads the authoritative projection. This avoids a second event protocol and makes reconnect recovery deterministic.

### 11.2 Public API shape

The member API exposes only a public projection similar to:

```json
{
  "status": "running",
  "started_at": "2026-09-04T09:00:00Z",
  "finished_at": null,
  "activities": [
    {
      "kind": "reading_document",
      "label": "正在阅读 PDF",
      "detail": "Attention Is All You Need · 第 3–5 页",
      "started_at": "2026-09-04T09:00:02Z"
    }
  ]
}
```

`kind` is a stable presentation category, not the internal tool name. `label` and `detail` are server-projected, localized display strings. The client must never receive the raw Action payload and then decide what is safe to show.

### 11.3 Allowlisted mappings

The server owns a per-tool projector. It resolves authorized Source metadata where useful and fails closed to a generic activity.

| Internal action | Public label | Allowed detail |
|---|---|---|
| `search_evidence` | 正在检索已选资料 | Safe Source titles or count; optionally a short sanitized topic |
| `discover_sources` | 正在搜索补充资料 | One-line sanitized and truncated natural-language query summary |
| `inspect_source` | 正在浏览资料结构 | Safe Source title and public document type |
| `read_document_pages` | 正在阅读 PDF | Safe Source title and validated page or page range |
| `read_url` | 正在阅读网页 | Public page title, otherwise sanitized public URL |
| `save_url_as_source` | 正在保存资料 | Safe title, otherwise sanitized public URL |
| `calculate` | 正在计算 | Short safe expression summary, never the full result payload |
| TODO/checkpoint planning tools | 正在整理处理步骤 | No TODO body or hidden plan text |
| Unknown or invalid mapping | 正在处理中 | No detail |

When the model is running but no Action proposal is active, the UI shows `正在分析问题`.

### 11.4 Privacy and sanitization

The projector follows an explicit allowlist. It never passes arbitrary keys through. In particular, the member activity surface excludes:

- internal tool names and versions;
- `run_id`, `action_id`, Source IDs, object keys, bucket paths, and provider request IDs;
- system or developer prompts, tool `purpose`, hidden TODO content, model scratch work, and chain of thought;
- raw arguments, raw results, page contents, evidence chunks, stack traces, and internal error strings;
- URL credentials, query strings, and fragments.

Displayed URLs are parsed structurally, restricted to safe public schemes, stripped of user information, query, and fragment, and length-bounded. Control characters are removed from all display text. Query and title details are single-line and truncated. Source titles are preferred to storage or download URLs. Any parse, lookup, or mapping failure produces `正在处理中` with no detail.

### 11.5 Elapsed time

The server sends timestamps; the browser computes elapsed time and advances it locally once per second while the Run is active. SSE events update state boundaries but are not emitted every second. On terminal completion, failure, or cancellation, the displayed duration freezes at `finished_at - started_at`. After an SSE reconnect or page reload, the browser reconstructs the same value from persisted timestamps.

Concurrent active Actions render as separate rows under one Run timer. Row ordering is stable by proposal time, then checkpoint sequence.

## 12. Catalog and Release Migration

Implementation publishes new immutable catalog versions:

1. a new grounded Chat prompt version containing the retrieval-first and pause semantics;
2. `chat.leader@6`, advertising `discover_sources`, retaining the required local tools, allowing concurrent `search_evidence`, and removing the `research.source-discovery@2` child; and
3. `nano.default@24`, switching ordinary Chat to the new leader.

Exact version numbers must be advanced if those identifiers have been claimed before implementation lands; immutability is more important than the illustrative number.

Old prompt versions, Chat definitions, `research.source-discovery` definitions, executor support, and release manifests remain readable so in-flight or historic pinned Runs can recover. They are no longer selected for new ordinary Chat Runs. Explicit Deep Research roots remain unchanged.

## 13. Failure and Recovery Rules

1. Proposal without result is visible as active; matching terminal result removes or transitions it.
2. A retried Action retains the original logical start time when it reuses the same Action identity.
3. An SSE reconnect re-reads PostgreSQL state and does not depend on missed transient events.
4. A malformed activity payload or failed metadata lookup degrades to the generic public activity, never to raw JSON.
5. A failed discovery does not open the panel and does not discard successful local evidence.
6. Duplicate-only discovery does not block Chat and does not claim that unselected Sources were read.
7. Novel candidates block the substantive answer only at the user-decision boundary; importing them starts the existing asynchronous Source admission path.
8. Historic Runs pinned to the child-agent release continue through the existing delegation recovery path.
9. A cancelled Deep Research session does not render its central Research plan/progress panel after the cancel response, terminal event, refetch, reload, or reconnect.

## 14. Testing and Acceptance

Implementation follows red-green-refactor in independently reviewable slices. Acceptance requires evidence that:

1. prompt tests make factual questions strongly select retrieval/discovery while greetings, acknowledgements, pure rewriting, and pure translation remain eligible for direct response;
2. a model proposal can contain `search_evidence` and `discover_sources` in one Action batch;
3. a blocking concurrency test proves the two Actions overlap rather than execute sequentially;
4. both Action Result checkpoints are independently durable and recoverable after interruption;
5. replay of `discover_sources` reuses one session and does not duplicate canonical candidates;
6. a URL already present anywhere in the Notebook is counted as existing, including the selected subset count;
7. duplicate-only and zero-candidate ready sessions do not activate Discovery, while a ready session with a novel candidate does;
8. local success remains usable when discovery fails, and discovery success does not become answer evidence when local retrieval fails;
9. concurrent Actions appear as multiple friendly activity rows and disappear at their own terminal checkpoints;
10. PDF activity shows a safe title and page range, while webpage activity strips credentials, query strings, and fragments;
11. member Run responses contain no internal names, IDs, prompts, full arguments, raw results, internal errors, or unallowlisted fields;
12. elapsed time advances through a live Run, survives SSE reconnect and page reload, and freezes at terminal completion;
13. unknown or malformed Actions render only the generic fallback;
14. cancelling Deep Research immediately removes the central Research panel while preserving the compact stopped state on the related message;
15. a cancelled session remains hidden after terminal SSE, refetch, page reload, and reconnect, without closing the left Discovery panel;
16. an in-flight Run pinned to the previous release can still recover its source-discovery child; and
17. focused Go tests, web component tests, TypeScript checks, and the relevant Chat/Discovery end-to-end path pass.

## 15. External Design References

The activity design follows established public boundaries without copying their wire formats:

- Vercel AI SDK models tool UI as typed lifecycle parts rather than prose parsing: <https://ai-sdk.dev/docs/ai-sdk-ui/chatbot-tool-usage>.
- OpenAI Agents SDK distinguishes raw model streaming from higher-level Run Item and Agent events: <https://openai.github.io/openai-agents-python/streaming/>.
- LangGraph exposes tool lifecycle updates as a separate stream mode: <https://docs.langchain.com/oss/javascript/langgraph/streaming>.
- assistant-ui supports running and completed tool-call presentation, but its generic fallback exposes raw arguments and results; Nano therefore requires its own server-projected member surface: <https://www.assistant-ui.com/docs/ui/tool-fallback>.

These references support the separation between internal execution events and user-facing activity. Nano's security boundary remains the server-side allowlist described above.
