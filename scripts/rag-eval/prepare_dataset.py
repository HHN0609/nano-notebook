#!/usr/bin/env python3
"""Sample retrieval-layer eval cases from public datasets.

Each language's general-search dataset (MS MARCO / DuReader) is paired with a
vertical-domain dataset (FiQA / CmedqaRetrieval) so a threshold calibrated
against this suite is not overfit to one query style. Every sampled query also
gets `--distractor-count` random, unrelated passages from its own corpus,
tagged `is_distractor`, so `rag-eval build-suite` can admit them as ordinary
Sources without ever treating them as an expected answer. Their only purpose
is putting genuinely irrelevant material into the retrieval candidate pool.

Output is written under the gitignored .dataset-cache directory so raw dataset
text is not committed to the repository.
"""

import argparse
import gzip
import json
import os
import random
import sys
import urllib.request
from pathlib import Path


MSMARCO_LICENSE = (
    "non-commercial research only; Microsoft MS MARCO terms at "
    "https://microsoft.github.io/msmarco/"
)
DUREADER_LICENSE = (
    "apache-2.0; zyznull/dureader-retrieval-ranking mirrors Baidu DuReader-"
    "Retrieval for academic use"
)
DUREADER_DEV_URL = (
    "https://huggingface.co/datasets/zyznull/dureader-retrieval-ranking/resolve/main/dev.jsonl.gz"
)
FIQA_LICENSE = (
    "cc-by-sa-4.0; ir_datasets beir/fiqa mirrors the FiQA-2018 task data for "
    "academic use"
)
CMEDQA_LICENSE = (
    "mirrors C-MTEB/CmedqaRetrieval and C-MTEB/CmedqaRetrieval-qrels on "
    "HuggingFace for academic use"
)
CMEDQA_CORPUS_URL = (
    "https://huggingface.co/datasets/C-MTEB/CmedqaRetrieval/resolve/main/"
    "data/corpus-00000-of-00001-a3949861f65a3226.parquet"
)
CMEDQA_QUERIES_URL = (
    "https://huggingface.co/datasets/C-MTEB/CmedqaRetrieval/resolve/main/"
    "data/queries-00000-of-00001-daeedab899d3c839.parquet"
)
CMEDQA_QRELS_URL = (
    "https://huggingface.co/datasets/C-MTEB/CmedqaRetrieval-qrels/resolve/main/"
    "data/dev-00000-of-00001-57fb84a4aceaa695.parquet"
)


def download_cached(url: str, cache_name: str) -> Path:
    path = Path(os.environ.get("HF_DATASETS_CACHE", "~/.cache/huggingface")).expanduser() / cache_name
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        urllib.request.urlretrieve(url, path)
    return path


def read_parquet_rows(path: Path) -> list[dict]:
    try:
        import pyarrow.parquet as pq
    except ImportError:
        sys.exit("pyarrow is required: python3 -m pip install pyarrow")
    return pq.read_table(path).to_pylist()


def sample_msmarco(count: int, distractor_count: int, seed: int) -> list[dict]:
    try:
        import ir_datasets
    except ImportError:
        sys.exit("ir_datasets is required: python3 -m pip install ir_datasets")
    dataset = ir_datasets.load("msmarco-passage/dev/small")
    docs = {doc.doc_id: doc.text for doc in dataset.docs_iter()}
    qrels: dict[str, list[str]] = {}
    for qrel in dataset.qrels_iter():
        qrels.setdefault(qrel.query_id, []).append(qrel.doc_id)
    candidates = []
    for query in dataset.queries_iter():
        relevant = qrels.get(query.query_id, [])
        if not relevant:
            continue
        doc_id = relevant[0]
        text = docs.get(doc_id)
        if not text:
            continue
        candidates.append(
            {
                "dataset_id": "msmarco-passage",
                "query_id": query.query_id,
                "query": query.text,
                "doc_id": doc_id,
                "doc_text": text,
                "source_ref": f"msmarco:passage:{doc_id}",
                "is_distractor": False,
            }
        )
    sampled = deterministic_sample(candidates, count, seed)
    used = {row["doc_id"] for row in sampled}
    distractors = sample_distractors(docs, used, "msmarco-passage", "msmarco", distractor_count, seed + 5000)
    return sampled + distractors


def sample_dureader(count: int, distractor_count: int, seed: int) -> list[dict]:
    path = download_cached(DUREADER_DEV_URL, "dureader-retrieval-dev.jsonl.gz")
    docs: dict[str, str] = {}
    candidates = []
    with gzip.open(path, "rt", encoding="utf-8") as file:
        for line in file:
            row = json.loads(line)
            # Only positive_passages feed the distractor pool: negative_passages
            # are DuReader's own hard negatives for their paired query, and the
            # design deliberately calibrates this gate against random/unrelated
            # negatives only, not adversarial ones.
            positives = row.get("positive_passages") or []
            for passage in positives:
                docs[passage["docid"]] = passage["text"]
            if not positives:
                continue
            passage = positives[0]
            candidates.append(
                {
                    "dataset_id": "dureader-retrieval",
                    "query_id": row["query_id"],
                    "query": row["query"],
                    "doc_id": passage["docid"],
                    "doc_text": passage["text"],
                    "source_ref": f"dureader-retrieval:passage:{passage['docid']}",
                    "is_distractor": False,
                }
            )
    sampled = deterministic_sample(candidates, count, seed)
    used = {row["doc_id"] for row in sampled}
    distractors = sample_distractors(docs, used, "dureader-retrieval", "dureader", distractor_count, seed + 5000)
    return sampled + distractors


def sample_fiqa(count: int, distractor_count: int, seed: int) -> list[dict]:
    try:
        import ir_datasets
    except ImportError:
        sys.exit("ir_datasets is required: python3 -m pip install ir_datasets")
    dataset = ir_datasets.load("beir/fiqa/test")
    docs = {doc.doc_id: doc.text for doc in dataset.docs_iter()}
    qrels: dict[str, list[str]] = {}
    for qrel in dataset.qrels_iter():
        qrels.setdefault(qrel.query_id, []).append(qrel.doc_id)
    candidates = []
    for query in dataset.queries_iter():
        relevant = qrels.get(query.query_id, [])
        if not relevant:
            continue
        doc_id = relevant[0]
        text = docs.get(doc_id)
        if not text:
            continue
        candidates.append(
            {
                "dataset_id": "beir-fiqa",
                "query_id": query.query_id,
                "query": query.text,
                "doc_id": doc_id,
                "doc_text": text,
                "source_ref": f"beir-fiqa:passage:{doc_id}",
                "is_distractor": False,
            }
        )
    sampled = deterministic_sample(candidates, count, seed)
    used = {row["doc_id"] for row in sampled}
    distractors = sample_distractors(docs, used, "beir-fiqa", "fiqa", distractor_count, seed + 5000)
    return sampled + distractors


def sample_cmedqa(count: int, distractor_count: int, seed: int) -> list[dict]:
    corpus_path = download_cached(CMEDQA_CORPUS_URL, "cmedqa-retrieval-corpus.parquet")
    queries_path = download_cached(CMEDQA_QUERIES_URL, "cmedqa-retrieval-queries.parquet")
    qrels_path = download_cached(CMEDQA_QRELS_URL, "cmedqa-retrieval-qrels.parquet")
    docs = {row["id"]: row["text"] for row in read_parquet_rows(corpus_path)}
    queries = {row["id"]: row["text"] for row in read_parquet_rows(queries_path)}
    qrels: dict[str, list[str]] = {}
    for row in read_parquet_rows(qrels_path):
        if row.get("score", 0) > 0:
            qrels.setdefault(row["qid"], []).append(row["pid"])
    candidates = []
    for query_id, query_text in queries.items():
        relevant = qrels.get(query_id, [])
        if not relevant:
            continue
        doc_id = relevant[0]
        text = docs.get(doc_id)
        if not text:
            continue
        candidates.append(
            {
                "dataset_id": "cmedqa-retrieval",
                "query_id": query_id,
                "query": query_text,
                "doc_id": doc_id,
                "doc_text": text,
                "source_ref": f"cmedqa-retrieval:passage:{doc_id}",
                "is_distractor": False,
            }
        )
    sampled = deterministic_sample(candidates, count, seed)
    used = {row["doc_id"] for row in sampled}
    distractors = sample_distractors(docs, used, "cmedqa-retrieval", "cmedqa", distractor_count, seed + 5000)
    return sampled + distractors


def sample_distractors(
    docs: dict[str, str], exclude_ids: set[str], dataset_id: str, ref_prefix: str, count: int, seed: int
) -> list[dict]:
    if count <= 0:
        return []
    pool = sorted(doc_id for doc_id in docs if doc_id not in exclude_ids)
    random.Random(seed).shuffle(pool)
    chosen = pool[:count]
    if len(chosen) < count:
        print(f"warning: only {len(chosen)} distractors available for {dataset_id}, requested {count}", file=sys.stderr)
    return [
        {
            "dataset_id": dataset_id,
            "query_id": "",
            "query": "",
            "doc_id": doc_id,
            "doc_text": docs[doc_id],
            "source_ref": f"{ref_prefix}:distractor:{doc_id}",
            "is_distractor": True,
        }
        for doc_id in chosen
    ]


def deterministic_sample(candidates: list[dict], count: int, seed: int) -> list[dict]:
    candidates = sorted(candidates, key=lambda item: (item["query_id"], item["doc_id"]))
    random.Random(seed).shuffle(candidates)
    if len(candidates) < count:
        sys.exit(f"only {len(candidates)} candidates available; requested {count}")
    return candidates[:count]


def write_sidecar(out_dir: Path, datasets: dict[str, list[dict]], seed: int) -> None:
    def summarize(rows: list[dict]) -> dict:
        return {
            "count": sum(1 for row in rows if not row["is_distractor"]),
            "distractor_count": sum(1 for row in rows if row["is_distractor"]),
        }

    sidecar = {
        "seed": seed,
        "datasets": {
            "msmarco-passage": {"source": "ir_datasets msmarco-passage/dev/small", "license": MSMARCO_LICENSE, **summarize(datasets["msmarco-passage"])},
            "beir-fiqa": {"source": "ir_datasets beir/fiqa/test", "license": FIQA_LICENSE, **summarize(datasets["beir-fiqa"])},
            "dureader-retrieval": {"source": "HuggingFace zyznull/dureader-retrieval-ranking dev", "license": DUREADER_LICENSE, **summarize(datasets["dureader-retrieval"])},
            "cmedqa-retrieval": {"source": "HuggingFace C-MTEB/CmedqaRetrieval + CmedqaRetrieval-qrels dev", "license": CMEDQA_LICENSE, **summarize(datasets["cmedqa-retrieval"])},
        },
    }
    (out_dir / "dataset-manifest.json").write_text(
        json.dumps(sidecar, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--msmarco-count", type=int, default=120)
    parser.add_argument("--fiqa-count", type=int, default=40)
    parser.add_argument("--dureader-count", type=int, default=120)
    parser.add_argument("--cmedqa-count", type=int, default=40)
    parser.add_argument("--distractor-count", type=int, default=40, help="random distractors sampled per sub-dataset")
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=Path("evals/rag/.dataset-cache"),
    )
    args = parser.parse_args()

    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    msmarco = add_case_ids(sample_msmarco(args.msmarco_count, args.distractor_count, args.seed), "msmarco")
    write_records(out_dir, "msmarco-passage-v1.jsonl", msmarco)
    print(f"sampled {len(msmarco)} MS MARCO rows ({args.distractor_count} distractors)")

    fiqa = add_case_ids(sample_fiqa(args.fiqa_count, args.distractor_count, args.seed + 2), "fiqa")
    write_records(out_dir, "beir-fiqa-v1.jsonl", fiqa)
    print(f"sampled {len(fiqa)} FiQA rows ({args.distractor_count} distractors)")

    dureader = add_case_ids(sample_dureader(args.dureader_count, args.distractor_count, args.seed + 1), "dureader")
    write_records(out_dir, "dureader-retrieval-dev.jsonl", dureader)
    print(f"sampled {len(dureader)} DuReader rows ({args.distractor_count} distractors)")

    cmedqa = add_case_ids(sample_cmedqa(args.cmedqa_count, args.distractor_count, args.seed + 3), "cmedqa")
    write_records(out_dir, "cmedqa-retrieval-v1.jsonl", cmedqa)
    print(f"sampled {len(cmedqa)} CmedqaRetrieval rows ({args.distractor_count} distractors)")

    combined = msmarco + fiqa + dureader + cmedqa
    write_records(out_dir, "samples-combined.jsonl", combined)
    write_sidecar(
        out_dir,
        {"msmarco-passage": msmarco, "beir-fiqa": fiqa, "dureader-retrieval": dureader, "cmedqa-retrieval": cmedqa},
        args.seed,
    )
    en_notebooks = -(-sum(1 for row in msmarco + fiqa if not row["is_distractor"]) // 45)
    zh_notebooks = -(-sum(1 for row in dureader + cmedqa if not row["is_distractor"]) // 45)
    print(f"wrote {len(combined)} total rows to {out_dir}; plan for roughly {en_notebooks} English and {zh_notebooks} Chinese Notebooks under the 50-Source quota")


def write_records(out_dir: Path, name: str, rows: list[dict]) -> None:
    payload = "\n".join(json.dumps(row, ensure_ascii=False) for row in rows) + "\n"
    (out_dir / name).write_text(payload, encoding="utf-8")


def add_case_ids(rows: list[dict], prefix: str) -> list[dict]:
    return [{**row, "case_id": f"{prefix}-{index + 1:04d}"} for index, row in enumerate(rows)]


if __name__ == "__main__":
    main()
