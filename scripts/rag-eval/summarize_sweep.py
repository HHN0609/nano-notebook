#!/usr/bin/env python3
"""Print a compact summary of a rag-eval sweep report."""

import argparse
import json


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("report", type=argparse.FileType("r", encoding="utf-8"))
    parser.add_argument("--top", type=int, default=10)
    args = parser.parse_args()

    report = json.load(args.report)
    rows = report["grid_results"]
    print(f"status={report['status']} rows={len(rows)} case_results={len(report['cases'])}")
    errors = sum(1 for case in report["cases"] if case.get("error"))
    print(f"errors={errors}")

    baseline = next(
        (
            row
            for row in rows
            if (row["dense_candidates"], row["sparse_candidates"], row["rrf_k"], row["rerank_candidates"])
            == (40, 40, 60, 20)
        ),
        None,
    )
    if baseline:
        print(
            "baseline "
            f"recall={baseline['recall']:.3f} mrr={baseline['mrr']:.3f} "
            f"failed={baseline['failed_cases']} total_ms={baseline['total_ms']:.1f}"
        )

    print("top by mrr:")
    for row in sorted(rows, key=lambda item: (-item["mrr"], -item["recall"], item["total_ms"]))[: args.top]:
        print(
            f"  d={row['dense_candidates']:2} s={row['sparse_candidates']:2} "
            f"k={row['rrf_k']:3} r={row['rerank_candidates']:2} "
            f"recall={row['recall']:.3f} mrr={row['mrr']:.3f} failed={row['failed_cases']:2} "
            f"total_ms={row['total_ms']:.1f}"
        )

    print(
        "ranges "
        f"recall={min(r['recall'] for r in rows):.3f}-{max(r['recall'] for r in rows):.3f} "
        f"mrr={min(r['mrr'] for r in rows):.3f}-{max(r['mrr'] for r in rows):.3f} "
        f"total_ms={min(r['total_ms'] for r in rows):.1f}-{max(r['total_ms'] for r in rows):.1f}"
    )


if __name__ == "__main__":
    main()
