# RRF-only ablation and online promotion

## Goal

Run a clean retrieval ablation that ends at Reciprocal Rank Fusion (RRF),
expands the evaluation corpus with additional single-hop datasets, measures
quality and latency across more candidate combinations, and promotes the best
validated Dense/BM25/RRF parameters to the online Retrieval Index configuration.

This experiment does not tune or change the online rerank candidate cap. The
reranker remains part of the final product validation but is never called by
the RRF-only sweep.

## Experiment corpus

Keep the current 320 relevant cases from MS MARCO, FiQA, DuReader Retrieval,
and CMedQA Retrieval. Add 60 relevant cases from each of these single-hop
datasets:

- SciFact for scientific claim-to-evidence retrieval;
- NFCorpus for biomedical retrieval;
- ArguAna for argument and counterargument retrieval.

Each new dataset also contributes 40 unrelated or difficult distractor
documents drawn from the same dataset while excluding labeled relevant
documents. The completed suite therefore contains 500 relevant query cases.
Every dataset must retain its dataset identifier, source reference, language,
and original relevance judgment in generated manifests. Dataset preparation
must record the upstream dataset revision and license metadata used by the run.

The existing quota of 50 Sources per Notebook remains authoritative. New
Sources are split across additional Notebooks and principals through the
existing per-case manifest overrides.

## Parameter grid

The sweep uses the full cross product:

| Parameter | Values |
| --- | --- |
| Dense candidates | 5, 10, 20, 40 |
| BM25 candidates | 5, 10, 20, 40 |
| RRF rank constant | 30, 60, 100 |

This produces 48 combinations and 24,000 searches over 500 cases. RRF retains
at most 20 fused candidates for evaluation. The retained-count ceiling is
fixed and is not an optimization variable in this experiment.

## Execution boundary

The RRF-only path is an evaluation-only variant of the production retrieval
path:

1. load the pinned Retrieval Scope;
2. build the query embedding and BM25 sparse query;
3. execute scoped Dense and BM25 searches concurrently;
4. fuse the two rankings with RRF;
5. retain the first 20 fused Chunk IDs;
6. reload authoritative Evidence only to map Chunk IDs to labeled Evidence
   Unit IDs and calculate metrics;
7. return without invoking the reranker or applying a rerank relevance
   threshold.

The experiment must not simulate RRF-only behavior through reranker failure.
The report must identify its mode explicitly and must reject a result if any
reranker call, reranker duration, or reranker degradation is observed.

Production `SearchEvidence` behavior remains unchanged while the sweep runs.
The evaluation-only option must not be selectable through a member-facing or
runtime API.

## Metrics and reporting

For every parameter combination, report both aggregate and per-dataset:

- Recall@5, Recall@10, and Recall@20;
- MRR@20;
- completed and failed case counts;
- Dense, BM25, and RRF mean, p50, and p95 latency;
- the RRF critical-path latency, measured as the wall-clock interval covering
  concurrent Dense/BM25 execution through completed fusion;
- Evidence reload latency as a separate evaluation-support cost;
- the number of reranker calls, which must equal zero.

Do not calculate retrieval wall-clock latency by adding Dense and BM25 stage
durations because they execute concurrently. Dataset preparation time, first
query-embedding population, and Evidence reload are not part of RRF critical
path latency and remain separately visible.

The report output includes a full JSON artifact, a combination-level CSV, and a
short Markdown comparison of the baseline and Pareto-leading combinations.

## Winner selection

Ignore the online Top-8 rerank cap while selecting RRF parameters. Select the
winner lexicographically:

1. highest macro-average Recall@20 across the seven datasets;
2. highest macro-average MRR@20;
3. lowest RRF critical-path p95 latency.

Macro-average gives every dataset equal weight even when case counts change.
Also report the Pareto frontier so a small quality difference is not hidden by
the single selected winner.

## Online configuration change

The sweep winner changes only these Retrieval Index parameters:

- `dense_candidates`;
- `sparse_candidates`;
- `rrf_k`.

Chunking, embedding model and dimensions, analyzer and BM25 parameters,
reranker model, rerank candidate cap, relevance threshold, and degradation
policy remain unchanged.

Create a candidate Retrieval Index Version with the winning configuration and
run the existing complete product RAG Eval, including reranking, grounded
answer generation, citation checks, latency, and cost gates. Promote the
candidate to active only if the complete gate passes and every Ready Source has
a verified candidate build. If the gate fails, preserve the current active
version and publish the failure in the experiment report.

Repository configuration changes are committed only after the complete gate
passes. Updating an external deployed production environment is outside this
repository run unless its credentials and deployment target are explicitly in
scope.

## Failure handling

- A dataset download, revision, license, or relevance-label failure stops suite
  preparation; it must not silently reduce the corpus.
- Any reranker invocation invalidates the RRF-only run.
- A failed search remains a failed case and is excluded from quality means;
  failure counts remain visible and a winner cannot be selected unless every
  combination completes all 500 cases.
- Lease and Run deadlines continue to be renewed using the existing live sweep
  mechanism.
- Generated datasets, manifests, and result artifacts remain reproducible and
  gitignored; the runbook records exact commands and hashes.

## Verification

Focused tests must prove:

- the RRF-only path never invokes `Rerank`;
- all 48 combinations are generated exactly once;
- cutoff metrics distinguish Recall@5, Recall@10, Recall@20, and MRR@20;
- RRF critical-path latency does not sum the two parallel channel durations;
- per-dataset macro aggregation and winner tie-breaking are deterministic;
- invalid or partially completed runs cannot select or promote a winner;
- normal production retrieval still invokes reranking and retains its current
  candidate cap.

Before handoff, run the focused Go tests, execute the 24,000-search sweep,
inspect the JSON/CSV/Markdown artifacts, run the complete product RAG Eval for
the winning candidate, and verify the active Retrieval Index configuration by
reading it back from PostgreSQL.
