# RAG query configuration 40/40/30/10

## Goal

Adopt the best currently supported online retrieval configuration:

- Dense candidates: 40;
- BM25 candidates: 40;
- RRF rank constant: 30;
- candidates sent to reranking: at most 10.

The change keeps Retrieval Index Versioning and the complete product Eval as
the authority for activation. It must not mutate an active version in place or
silently override its stored configuration at query time.

## Evidence and decision boundary

The seven-dataset RRF-only sweep completed 24,000 searches across 500
single-hop cases with no errors, degradation, or reranker calls. `40/40/30`
was selected by the declared ordering of macro Recall@20, macro MRR@20, and RRF
p95 latency. Its aggregate Recall@20 was 0.980 and MRR@20 was 0.896161.

Compared with `20/20/30`, `40/40/30` recovered two additional NFCorpus cases.
Their relevant Evidence ranked third and tenth before reranking. A focused live
rerank check with a limit of 10 retained both cases after the configured 0.28
threshold, at reranked positions five and seven.

This evidence establishes `40/40/30` as the RRF winner and supports 10 as the
smallest tested rerank input that preserves both incremental recoveries. It
does not prove that 10 is globally optimal across all 500 cases because the
full suite has not been rerun with reranking enabled.

## Configuration authority

Update `evals/rag/pinned-config-v1.json` so its `index` contains
`dense_candidates=40`, `sparse_candidates=40`, `rrf_k=30`, and
`rerank_candidates=10`. Keep chunking, embedding, analyzer, BM25 constants,
reranker model, relevance threshold, degradation policy, composer model, and
prompt configuration unchanged.

Production `SearchEvidence` currently applies an additional hard ceiling of
eight Evidence candidates. Raise that ceiling to ten so runtime behavior agrees
with the versioned configuration. The normal retrieval data flow remains:

1. retrieve 40 Dense and 40 BM25 candidates;
2. fuse the two rankings with RRF using `k=30`;
3. truncate the fused list to at most 10 candidates;
4. reload those candidates from PostgreSQL;
5. rerank all retained candidates;
6. remove candidates whose rerank score is below 0.28;
7. expose the remaining, reranked Evidence to the model.

`rerank_candidates=10` is therefore both the number sent to the reranker and
the maximum count available to the model before relevance filtering. The
pipeline does not rerank 20 candidates and then select 10.

## Versioned rollout

Repository changes define the candidate configuration but do not rewrite an
existing active database row. Rollout uses the existing lifecycle:

1. create a new candidate Retrieval Index Version from the pinned config;
2. rebuild every active Evidence Revision for that candidate and verify all
   builds;
3. run the frozen complete product RAG Eval with live Sources, reranking,
   answer generation, citation, latency, and cost gates;
4. record the Eval result and promote only a passing candidate;
5. read the active version back from PostgreSQL and verify `40/40/30/10`.

If live product fixtures or model capabilities are unavailable, stop before
promotion and leave the current active version unchanged. The current local
blockers are the missing Sprint 6 live Source manifest and transcription
provider required by the audio fixtures.

## Testing

Use test-driven development for the behavior change:

- first add a production-path test that expects at most 10 candidates and
  observe it fail against the current ceiling of eight;
- add a pinned-config assertion for `40/40/30/10` and observe it fail against
  the current `40/40/60/20` file;
- make the minimal constant and configuration changes;
- run focused agent and RAG Eval tests;
- run `scripts/test-go` for formatting, all Go tests, vet, and builds;
- inspect the candidate configuration and, only after the complete live Eval
  passes, inspect the active configuration.

## Acceptance criteria

- The pinned configuration is exactly `40/40/30/10`.
- Production retrieval sends no more than 10 candidates to reranking and
  exposes no more than 10 Evidence items before threshold filtering.
- Existing threshold and reranker-failure behavior remain unchanged.
- Focused tests and `scripts/test-go` pass.
- No active Index Version is modified or promoted without a recorded passing
  complete product Eval and verified candidate builds.
