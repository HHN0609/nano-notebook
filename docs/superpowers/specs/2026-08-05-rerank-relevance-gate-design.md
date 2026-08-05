# Rerank Relevance Gate

## Problem

When every retrieved candidate for a `search_evidence` call is topically unrelated
to the question, nothing in the retrieval pipeline says so. `Pipeline.Search`
(`internal/retrieval/pipeline.go`) only distinguishes `CompleteEmpty` (both
channels returned zero candidates) and `Degraded` (a channel failed). A dense/BM25
channel that returns candidates — any candidates, however unrelated — always
reaches the Composer as ordinary `search_evidence` results. The only defense
against the model answering from irrelevant evidence is the system prompt
(`agent.chat-composer-grounded.v3.md`), which is a soft instruction, not a gate.

ADR 0038 deliberately removed the generation-time defense that used to exist for
this (a runtime Claim Support Verifier doing sentence-level entailment, per ADR
0033) in favor of coarse source-level citation provenance. This project does not
reopen that decision. It adds the cheaper, purely deterministic layer ADR 0038
left on the table: a backend numeric cutoff on retrieval relevance, applied before
candidates ever reach the model.

The signal for this cutoff already exists and is thrown away. Bifrost's rerank
response carries a `relevance_score` per candidate
(`internal/models/capabilities.go:153`); `BifrostClient.Rerank` reads it, uses it
only to preserve response order, and returns just `CandidateIDs`
(`internal/models/capabilities.go:170-186`). The RRF fusion score
(`retrieval.Candidate.Score`, produced by `FuseRRF`) is discarded even earlier, at
`pipeline.go:127-130`, and is unsuitable for an absolute cutoff regardless: it is
derived from rank position and the `rrf_k` constant, so its scale is not
comparable across queries. The cross-encoder rerank score, scored directly against
the query-passage pair, is the only signal in this pipeline suited to an absolute
threshold.

There is currently no way to calibrate a threshold even if the plumbing existed.
`evals/rag/retrieval-sweep-v1.json` pairs every query with exactly one relevant
passage and no distractors; the retrieval-sweep-runbook.md results show Recall at
1.000 across the entire 81-combination grid, so the dataset cannot show what a
rerank score looks like for a genuinely irrelevant candidate.

## Scope

In scope:

- Plumbing rerank relevance scores from Bifrost through to
  `retrieval.EvidenceCandidate`.
- A versioned `RerankRelevanceThreshold` on `IndexConfig`, applied as a
  post-rerank filter in `Pipeline.Search`.
- Extending the retrieval-sweep dataset with distractor passages (random,
  unrelated to each query) and two additional datasets, so score distributions
  for "relevant" and "irrelevant" candidates can actually be compared.
- Using the extended sweep to pick the threshold value and recording that in the
  runbook.

Explicitly out of scope:

- Any generation-time faithfulness/entailment check (LLM judge, NLI classifier,
  or reviving the ADR 0033 Claim Support Verifier). Already decided against.
- Catching "topically related but does not answer the question" candidates. A
  numeric relevance cutoff cannot distinguish this case from a genuinely relevant
  candidate — both score similarly. This gate only targets candidates with no
  topical relation to the query at all.
- Hard-negative mining (BEIR-style adversarial negatives, qrel-based hard
  negatives). Only random/unrelated negatives are needed to calibrate a cutoff
  for total irrelevance.
- Wiring any of this into CI. The extended sweep remains a manually-run tool, as
  it is today.

## Rerank Score Plumbing

`internal/models/capabilities.go`:

- `RerankOutcome` gains `Scores map[string]float64` (candidate ID → relevance
  score), populated from the already-decoded `RelevanceScore` field that is
  currently discarded after ordering.

`internal/retrieval/pipeline.go`:

- `RerankFunc` changes from `func(context.Context, string, []EvidenceCandidate)
  ([]string, error)` to `func(context.Context, string, []EvidenceCandidate)
  (RerankResult, error)`, where `RerankResult` is `{ OrderedIDs []string; Scores
  map[string]float64 }`.
- `EvidenceCandidate` gains `RerankScore float64`.
- `applyRerankOrder` sets `RerankScore` on each candidate from
  `RerankResult.Scores` while building the ordered slice.

`internal/agent/evidence_search.go`:

- The closure wiring `models.BifrostClient.Rerank` into `Pipeline.Rerank`
  (`internal/agent/evidence_search.go:141-150`) currently reads
  `outcome.CandidateIDs` and drops the rest of `RerankOutcome`. It is updated to
  also read the new `outcome.Scores` and return both as `RerankResult`.

This is additive for every other `EvidenceCandidate` consumer
(`search_evidence_contract.go`, `search_evidence_projection.go`,
`internal/rageval/retrieval_eval.go`) — none of them fail by gaining a field they
do not read.

## Threshold Configuration and Filtering

`internal/retrieval/version_store.go`:

- `IndexConfig` gains `RerankRelevanceThreshold float64` (JSON tag
  `rerank_relevance_threshold`), alongside `RerankCandidates`. A zero value means
  "not configured" and disables filtering, so existing pinned configs and tests
  that do not set it keep today's behavior exactly.

`internal/agent/evidence_search.go`:

- `retrievalSearchRequest` reads `RerankRelevanceThreshold` from `IndexConfig` and
  sets it on `retrieval.SearchRequest`, the same path `RerankCandidates` already
  takes to become `RerankLimit`.

`internal/retrieval/pipeline.go`:

- After `applyRerankOrder` succeeds, if `request.RerankRelevanceThreshold > 0`,
  filter `result.Candidates` to those with `RerankScore >=
  RerankRelevanceThreshold`.
- If the filtered list is empty, set `result.CompleteEmpty = true` and clear
  `result.Candidates`, reusing the existing status rather than introducing a new
  one — `search_evidence_action.go` and the prompt already treat `CompleteEmpty`
  as "nothing usable came back," and that is exactly what this case is.
- If reranking itself failed (`reranker_unavailable` degradation, existing
  branch at `pipeline.go:169-175`), there is no score to filter on. The gate is
  skipped and behavior is unchanged from today — a reranker outage should not
  compound into every result being treated as irrelevant.
- `SearchDiagnostics` gains `RelevanceFiltered []string`, the IDs removed by the
  threshold, for sweep reporting and debugging. This does not change
  `Diagnostics.Rerank.CandidateIDs`, which continues to reflect the returned
  (post-filter) candidates as it does today.

## Eval Dataset Extension

`scripts/rag-eval/prepare_dataset.py`:

- Per-language sample size increases from 50 to a size chosen to fit the
  50-Source/Notebook quota across the added datasets (target 150-200 per
  language; exact split determined against the quota during implementation).
- A distractor sampler adds N random passages per query, drawn from the same
  corpus and excluded from that query's relevant set. `ir_datasets` already loads
  the full MS MARCO document collection into memory
  (`prepare_dataset.py:34`), so this is a random draw with no new data source.
  Distractors are ingested as ordinary Sources through the existing
  `ingest-samples` path but are never added to a case's
  `expected_evidence_sets` — their only purpose is populating the candidate pool
  with material that should score low.
- Two vertical-domain datasets are added alongside the existing general-search
  corpora, both reachable through tooling already in use:
  - English: one of the BEIR datasets available via `ir_datasets` (FiQA or
    SciFact), to avoid calibrating the threshold only against MS MARCO's
    web-search-snippet style.
  - Chinese: C-MTEB's CmedqaRetrieval (medical-domain Chinese QA), mirrored on
    HuggingFace like the existing DuReader-retrieval source, to avoid
    calibrating only against DuReader's Baidu-search style.

## Threshold Calibration

Run the existing `rag-eval sweep` tool against the extended dataset and compare
the `RerankScore` distribution of true-relevant candidates against random
distractors. Pick the lowest threshold that separates the two populations
without dropping Recall on the relevant set — the goal is to catch clearly
irrelevant candidates, not to trade away real recall. Record the chosen value,
the score distributions it was chosen from, and the dataset composition in
`docs/implementation/rag-retrieval-sweep-runbook.md`, replacing the current
"Recall range: 1.000 across all combinations" result, which this dataset made
unable to discriminate.

## Verification

- `internal/retrieval` unit tests: candidates below threshold are removed;
  filtering to zero candidates sets `CompleteEmpty`; a `reranker_unavailable`
  result skips filtering entirely; a zero-value threshold leaves behavior
  unchanged (regression coverage for the default/back-compat path).
- `internal/agent` tests: a `search_evidence` call whose only candidates fall
  below threshold produces `CompleteEmpty: true` in the model-visible JSON
  contract.
- `internal/models` tests: `BifrostClient.Rerank` populates `RerankOutcome.Scores`
  from the decoded response.
- Eval: a sweep case with injected distractors asserts distractors do not appear
  in filtered results once the threshold is set, and that Recall on the same
  case's true-relevant unit is unaffected.
