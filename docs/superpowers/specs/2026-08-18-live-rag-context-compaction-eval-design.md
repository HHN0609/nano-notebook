# Live RAG and context-compaction evaluation

## Context

Nano's current Qwen Plus model capability advertises a 1,000,000-token context window, while the chat/research context policy proactively compacts at 98,304 estimated input tokens and keeps an exact recent suffix targeting 12,288 tokens. Existing tests prove projection, cut-boundary, persistence, and overflow-recovery mechanics, but they do not establish that a real model-generated summary preserves the active research goal, constraints, retrieval facts, failures, and citation authority.

The earlier ad-hoc pressure check used one synthetic filler message and a hand-written summary. It proved only that the latest User Message remains in the exact suffix. It is not evidence for RAG quality or semantic retention and must not be used to justify the 98,304-token policy.

## Goal

Run a credentialed, production-shaped evaluation that:

1. discovers current public material through Nano's Brave adapter;
2. imports selected URLs through the real Source admission and processing path;
3. indexes immutable evidence in Qdrant and BM25;
4. asks grounded questions through `search_evidence` and real Qwen Plus;
5. builds realistic long research histories at approximately 10K, 20K, 50K, and 100K estimated input tokens from the resulting User, Tool Call, Action Result, and Assistant records;
6. compares full-history answers with answers produced from a real Qwen compaction summary plus the configured exact recent suffix; and
7. writes a reviewable report containing requests, token usage, retrieval evidence, summaries, answers, scores, and limitations.

The evaluation topic is: **How production coding agents manage long context: long model windows, compaction, Tool Result truncation, and durable recovery.**

## Non-goals

- Changing the current 98,304 or 12,288 policy values during this experiment.
- Treating Brave snippets as Source Evidence. Only fetched, normalized, indexed Source content may support an answer.
- Claiming that one topic or one model run proves a globally optimal threshold.
- Adding an LLM-as-judge score as the only acceptance criterion.
- Modifying production Agent, retrieval, Source Processing, or context-compaction behavior.
- Committing credentials, raw provider envelopes, unrestricted fetched bodies, or large evaluation artifacts.

## Recommended approach

Use a hybrid live evaluation rather than either a fixture-only unit test or an uncontrolled browser conversation.

- The **discovery and RAG half** uses the real product boundaries: Brave candidate discovery, explicit candidate selection, URL admission, restricted fetching, Source Processing, Qdrant/BM25 indexing, `search_evidence`, and citation-ready evidence projection.
- The **context sensitivity half** reuses the real retrieved records and model-visible checkpoint shape in a controlled harness. This makes 10K/20K/50K/100K runs comparable without manually conducting dozens of browser turns or introducing meaningless repeated filler.
- Summary generation and final answers use the current Bifrost-backed Qwen Plus model, current compaction prompt, current token estimator, and current model invocation policies.

This separates three questions that otherwise become conflated:

1. Did retrieval find the right evidence?
2. Did compaction preserve the relevant historical state?
3. Did the final model answer the current question correctly from the context it received?

## Source discovery and admission

### Discovery queries

Run bounded Brave searches for:

- `Pi coding agent context compaction reserveTokens keepRecentTokens`
- `OpenAI Codex CLI context compaction long context documentation`
- `Claude Code context window compaction documentation`
- `OpenCode context compaction source code`
- `coding agent durable checkpoints tool calls recovery`

The harness may add one narrower query when a discovered claim cannot be verified from an authoritative page.

### Selection policy

Select approximately 8–12 candidates using this order:

1. official product documentation;
2. the owning project's GitHub repository, source, changelog, issue, or discussion;
3. engineering posts from the team operating the system;
4. high-signal maintainer discussion only when it links to an implementation artifact.

Reject SEO summaries, generic comparisons, pages with no implementable detail, duplicate canonical URLs, and pages the restricted Fetcher cannot safely admit. X posts may be used for discovery, but a claim enters the fact checklist only after verification against an implementation artifact.

Brave candidate title, snippet, and raw response remain discovery-only. The user-selected URL must pass ordinary URL admission and Source Processing before it can enter RAG.

## RAG evaluation

### Grounded question set

Use a fixed question set with explicit fact expectations:

1. **Pi mechanics:** What triggers Pi auto-compaction, and what are the default reserve and recent-retention budgets?
2. **Nano comparison:** How does Nano's proactive 98,304-token policy differ from Pi's model-window-relative threshold?
3. **Cut integrity:** How do the systems avoid separating Tool Calls from Tool Results during compaction?
4. **Durability:** Which state is authoritative for recovery, and which state is merely diagnostic?
5. **Recommendation:** For a source-grounded Q&A Agent, when is early compaction preferable to consuming the full provider window?

Each question has a deterministic checklist of required facts and an expected set of supporting Source IDs established from the admitted Sources. The checklist is stored with the evaluation artifacts, not inferred after seeing the answer.

### Retrieval evidence

For every question, record:

- query and purpose sent to `search_evidence`;
- selected Source scope and pinned Index Version;
- returned chunk IDs, Source IDs, titles, previews, and final rank;
- degradation flags, especially reranker unavailability;
- whether at least one expected Source is present in the returned evidence;
- whether every citation in the answer refers to returned evidence.

Primary RAG metrics are expected-source hit rate, fact-checklist coverage, citation validity, and unsupported-claim count. Ranking-stage diagnostics are recorded when exposed by the current retrieval result or Trace, but the harness must not fabricate unavailable per-stage ranks.

## Long-context dataset

### Production-shaped records

Build history only from these model-visible record shapes:

- User research questions;
- Assistant `search_evidence` Tool Calls with stable Action IDs and JSON input;
- Action Results using the actual checkpoint/model-projection JSON shape and real retrieved Source IDs/chunk previews;
- Qwen research answers with source markers;
- explicit failure records for unavailable, irrelevant, or empty retrieval where observed or deliberately injected as a domain-valid failure case.

No `filler filler` strings are allowed. To increase history size, generate additional deterministic research turns from real admitted Sources: definitions, comparisons, implementation details, failure modes, and trade-offs. A turn may reuse a Source, but its User question and selected evidence must represent a coherent research subtask. Large Action Results remain subject to the same model projection limits as production.

### Size bands

Build four immutable context snapshots using the repository's current `EstimateModelRequestTokens` implementation:

| Band | Target range | Production compaction expectation |
|---|---:|---|
| 10K | 9,000–11,000 | no automatic compaction |
| 20K | 18,000–22,000 | no automatic compaction |
| 50K | 47,000–53,000 | no automatic compaction |
| 100K | 98,304–105,000 | threshold compaction |

Every snapshot ends with the same exact current User question. The recent suffix also contains the latest relevant `search_evidence` call and result so the primary Q&A path does not depend on a summary for information that was just retrieved.

Add one counterexample snapshot per band where a critical instruction exists only in the older prefix: report retrieval failures honestly and distinguish provider capability from Nano's chosen policy. This measures summary dependence separately from current-question retention.

## A/B execution

For each size band:

1. **Full baseline:** send the full projected context to Qwen and record the answer.
2. **Forced sensitivity compaction:** run the current `CompactionSystemPrompt` over the selected old prefix, even below the production threshold, then answer from summary plus the configured approximately 12,288-token exact suffix.
3. **Production-policy result:** mark whether production would actually compact at this size. At 100K, use the normal threshold path; below 98,304, forced compaction is sensitivity analysis and must not be described as current runtime behavior.

The intended live model-call budget is approximately twelve calls: one full answer, one summary, and one compacted answer for each of four bands. Discovery planning or Source Processing model calls are recorded separately.

Temperature is pinned to the current catalog policy. Inputs, model identity, prompt identity/version, provider-reported usage, latency, and output are retained so repeated runs can be compared.

## Scoring

Score each baseline and compacted answer with deterministic checks first:

- current question answered directly;
- required facts present;
- old-prefix constraints retained;
- failed/empty retrieval not represented as success;
- citations refer only to returned Source IDs;
- no unsupported precise numbers or quotations;
- provider capability and Nano policy are not conflated.

Then perform a blinded manual comparison of the A/B pair for conclusion drift, missing caveats, and materially changed recommendations. An optional second model may assist with diff classification, but its judgment is advisory and the report must include the underlying answers and checklist evidence.

Report deltas, not only pass/fail:

- fact coverage before and after compaction;
- citation validity before and after compaction;
- unsupported claims before and after compaction;
- input-token reduction and summary cost;
- answer latency before and after compaction;
- whether the final recommendation changed materially.

## Execution architecture

The live experiment is opt-in and credential-safe.

1. Start the existing Compose PostgreSQL, MinIO, Qdrant, Bifrost, document renderer, restricted fetcher, and any Source Processing dependencies.
2. Ensure the isolated `nano_test` database exists; never point destructive integration setup at the development `nano` database.
3. Read `DASHSCOPE_API_KEY` and `NANO_BRAVE_SEARCH_API_KEY` from the existing untracked Compose environment without printing them.
4. Run a dedicated live evaluation entrypoint guarded by an explicit environment flag. It may use current internal application services, but it must not alter production behavior.
5. Write artifacts beneath `test-results/context-budget-eval/<run-id>/`, which is already ignored by Git.
6. Clean only resources created by the evaluation or use unique IDs so repeated runs remain isolated. Do not purge unrelated local data.

The evaluation entrypoint should fail closed when credentials are absent, a Source never becomes ready, Qdrant indexing is incomplete, Bifrost is unavailable, or Provider usage is missing from a supposedly live call. Partial results remain in the artifact directory with a clear terminal status.

## Artifact schema

Each run directory contains:

- `manifest.json`: run ID, time, git commit, model/context policy pins, service endpoints without credentials;
- `sources.json`: Brave queries, normalized selected candidates, admission status, Source IDs, final URLs, processing and Index Version status;
- `rag.json`: grounded questions, retrieval results, expected-source checks, answers, citations, and usage;
- `contexts/<band>-full.json`: exact full Model Request and token estimate;
- `contexts/<band>-compaction-input.json`: exact summary request;
- `contexts/<band>-summary.json`: Qwen summary, usage, and latency;
- `contexts/<band>-compacted.json`: exact summary-plus-suffix Model Request;
- `answers/<band>-baseline.json` and `answers/<band>-compacted.json`;
- `report.md`: human-readable tables, A/B excerpts, fact/citation deltas, failures, and recommendation.

Artifacts must redact credentials and omit unrestricted source bodies. Model-visible evidence previews and request payloads are retained because they are necessary to audit the result.

## Acceptance criteria

The experiment is complete only when:

- Brave returns real normalized candidates and selected URLs complete Source admission;
- at least six authoritative Sources reach `ready` and are indexed in one pinned Index Version;
- all five grounded questions execute against real `search_evidence` results;
- all four context bands land in their target ranges;
- all twelve planned Qwen comparison calls either complete with provider usage or are reported individually as failed;
- every A/B request, summary, answer, retrieval result, and score is present in the artifact directory;
- the report distinguishes mechanical suffix retention, summary-dependent historical retention, RAG quality, and final-answer quality;
- the report gives an evidence-backed judgment on whether 98,304 appears too early, acceptable, or insufficient for this workload, without claiming global optimality.

## Risks and interpretation limits

- One research topic does not characterize all Q&A workloads.
- The current `characters/4` deterministic estimate may differ materially from Qwen's observed token count; both values must be reported.
- Real web pages can change, reject fetching, or contain extraction noise. Source failures are evaluation data, not reasons to silently substitute Brave snippets.
- Forced compaction at 10K/20K/50K measures sensitivity, not production behavior.
- Reusing real evidence across synthetic research turns is production-shaped but not identical to organic human conversation.
- A stable current answer can coexist with loss of older facts; the counterexample cases exist to expose that difference.
- Provider output is nondeterministic. A single run is exploratory evidence; repeated runs are required before changing the policy.

## Rollout and follow-up

This work produces an opt-in evaluation harness and ignored artifacts only. It does not change runtime defaults. If the first run is healthy, repeat the 50K and 100K bands at least three times before proposing a policy change. Any change to `soft_input_limit_tokens`, `keep_recent_tokens`, summary prompt, or token estimation requires a separate design and acceptance cycle based on the recorded evidence.
