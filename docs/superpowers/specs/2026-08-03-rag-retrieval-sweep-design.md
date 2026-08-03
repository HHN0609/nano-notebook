# Retrieval Sweep Evaluation

## Problem

The existing RAG Eval suite (`evals/rag/sprint6-v1.json`) is a production promotion
gate: 15 critical cases run the full Agent loop, generate an answer, verify grounding,
and authorize an Index Version promotion. It is intentionally small and expensive.
It cannot answer the practical interview question "how did you tune `top_k`, RRF k,
and rerank candidates, and what data did that produce?".

This project adds a retrieval-layer-only sweep tool that reuses the production
`EvidenceSearchService`/`retrieval.Pipeline` code path, skips the LLM answer loop,
and runs a small real dataset corpus through a query-time parameter grid.

## Scope

In scope:

- A lightweight `RetrievalSuite` / `RetrievalCase` / `RetrievalReport` type family.
- A `rag-eval sweep` subcommand that emits CSV and JSON reports.
- A live retrieval executor that admits a real pinned Run and calls
  `EvidenceSearchService.SearchEvidenceWithOverrides`.
- A dataset sampler for MS MARCO passage and DuReader-retrieval, 50 cases per
  language, plus a semi-automatic annotation helper.
- An initial annotated suite that can be ingested and run locally.

Explicitly out of scope:

- No change to `internal/rageval/evaluator.go`, `Suite`, `Case`, `Evaluate()`, or
  the promotion path.
- No full Agent/LLM generation for the new cases.
- No chunk-size, embedding-model, analyzer, or BM25 parameter sweeps. Those require
  rebuilding the index and are a separate experiment class.

## Data Model

New files live in `internal/rageval/retrieval_eval.go` and
`internal/rageval/retrieval_live_executor.go`. The production evaluator remains
untouched.

```json
{
  "schema_version": 1,
  "id": "rag-retrieval-sweep-v1",
  "cases": [
    {
      "id": "msmarco-0001",
      "question": "what is a ...",
      "language": "en",
      "dataset_id": "msmarco-passage",
      "source_ref": "msmarco:passage:12345",
      "expected_evidence_sets": [["unit_msmarco_0001_0"]]
    }
  ]
}
```

`expected_evidence_sets` contains nano-notebook Evidence Unit IDs, not dataset
passage IDs. A source manifest maps each case to the ingested `source_id` and
`evidence_revision_id` in the local database.

```json
{
  "schema_version": 1,
  "index_version_id": "riv-...",
  "user_id": "user-...",
  "notebook_id": "notebook-...",
  "cases": [
    {
      "case_id": "msmarco-0001",
      "source_id": "src-...",
      "evidence_revision_id": "evr-..."
    }
  ]
}
```

## Sweep Grid

The grid is query-time only:

| Parameter | Values |
| --- | --- |
| `dense_candidates` | 20, 40, 80 |
| `sparse_candidates` | 20, 40, 80 |
| `rrf_k` | 30, 60, 100 |
| `rerank_candidates` | 10, 20, 40 |

The full cross product is 81 parameter combinations. Each combination is evaluated
against every suite case, so the default run is 81 x ~100 searches.

The baseline production values are 40 / 40 / 60 / 20, so the grid brackets the
production configuration without requiring a new index build.

## Scoring

For each search, the ranked result is converted to the ordered Evidence Unit IDs by
flattening `EvidenceCandidate.UnitRefs`. The existing evaluator's set semantics are
preserved:

- A case hits an `expected_evidence_sets` entry when any ID in that alternative set
  appears in the ranked unit list.
- `Recall = hits / len(expected_evidence_sets)`.
- `MRR` is the reciprocal rank of the first expected unit.

Each grid row aggregates the mean Recall, mean MRR, and mean stage latencies over
completed cases. Failed searches are counted separately and excluded from metric
means.

## Request-Time Overrides

`internal/agent/evidence_search.go` gets a new
`SearchEvidenceWithOverrides(ctx, attempt, query, overrides)` method. It reuses the
same pinned scope, embedding, sparse encoding, Qdrant search, authority reload, and
rerank code as `SearchEvidence`. The only deliberate divergence is that sweep
rerank candidates are honored as requested instead of being capped at
`maxSearchEvidenceCandidates = 8`. The existing `SearchEvidence` behavior does not
change; the cap remains for the normal Agent action budget.

The retrieval executor admits one durable Run per Notebook (the Source quota is
50 per Notebook, so the Chinese split uses a second Notebook and principal), then
calls the override search for every grid combination without creating another
Run. It pins the active Index Version through `PinEvidenceSet`, not a promotion
candidate, because this tool measures the current baseline. Before each search it
refreshes both the Job lease and the Run deadline, so a long sweep cannot expire
after the initial 10-minute admission deadline.

The first full pass exposed that refreshing only `agent_jobs.lease_expires_at`
was insufficient: `loadPinnedScope` also checks `agent_runs.deadline_at`, so a
sweep longer than 10 minutes lost authority. The fix extends both in
`RetrievalLiveExecutor.refreshLease`.

A second issue was provider cost/quota: the same query was embedded once per
grid combination, so an 81 x 100 sweep made 8,100 embedding calls. The live
executor now wraps the model with an in-memory query embedding cache, reducing
that to 100 calls. Rerank remains per search because its candidate set changes
with the grid.

## CLI

```sh
go run ./cmd/rag-eval sweep \
  -suite evals/rag/retrieval-sweep-v1.json \
  -grid evals/rag/retrieval-sweep-grid-v1.json \
  -manifest evals/rag/retrieval-sweep-sources-v1.json \
  -out-prefix evals/rag/sweep-out/retrieval-sweep-v1
```

Required flags:

- `-suite`: retrieval suite JSON.
- `-grid`: grid JSON.
- `-manifest`: live source manifest JSON.
- `-out-prefix`: output path prefix.
- `-database-url`, `-bifrost-url`, `-qdrant-url`, `-qdrant-api-key`,
  `-qdrant-collection`, `-executor-timeout`: live execution configuration.

Output:

- `<out-prefix>.json`: full report including case-level results and stage
  diagnostics.
- `<out-prefix>.csv`: one row per grid combination with Recall, MRR, completed
  cases, failed cases, and mean dense/BM25/fuse/evidence-load/rerank/total
  milliseconds.

## Dataset Preparation

`scripts/rag-eval/prepare_dataset.py` writes sampled JSONL to a gitignored
`evals/rag/.dataset-cache/` directory. Raw dataset text is not committed. The script:

- uses `ir_datasets` for `msmarco-passage`;
- uses HuggingFace `datasets` for DuReader-retrieval;
- samples 50 rows per language with a fixed seed;
- emits `dataset_id`, `query_id`, `query`, `doc_id`, `doc_text`, `source_ref`;
- records the dataset version and license in a small sidecar JSON.

`rag-eval ingest-samples` admits each sampled passage as a real text Source through
the same object store and source store used by the product upload API, then queues
the normal processing job. `rag-eval units` prints each Source's Evidence Unit ID,
ordinal, and the first 100 runes for manual inspection. Because each sampled case
is one official relevant passage, `rag-eval build-suite` resolves the active
Evidence Revision and writes every Unit from that Source as the expected set,
removing the error-prone manual ID transcription step.

```sh
go run ./cmd/rag-eval ingest-samples \
  -samples evals/rag/.dataset-cache/msmarco-passage-v1-50.jsonl \
  -manifest-out evals/rag/.dataset-cache/sources-msmarco.json \
  -database-url 'postgres://nano:nano@localhost:55432/nano?sslmode=disable' \
  -user-id usr_eval -notebook-id nb_eval -index-version-id riv_dev_baseline_v1

go run ./cmd/rag-eval build-suite \
  -samples evals/rag/.dataset-cache/msmarco-passage-v1-50.jsonl \
  -manifest evals/rag/.dataset-cache/sources-msmarco.json \
  -manifest-out evals/rag/retrieval-sweep-sources-v1.json \
  -database-url 'postgres://nano:nano@localhost:55432/nano?sslmode=disable' \
  -index-version-id riv_dev_baseline_v1 \
  -out evals/rag/retrieval-sweep-v1.json
```

## Verification

- Pure unit tests for suite/grid validation, cross-product generation, Recall/MRR,
  and report aggregation.
- CLI tests for grid decoding, CSV/JSON output shape, and a stub executor path.
- Existing `cmd/rag-eval` and `internal/rageval` tests remain green and the
  production `evaluator.go` is unchanged.
- A local live sweep over a small manifest must produce both output files and a
  sensible ranking: baseline should beat or match the lowest parameter variants.
