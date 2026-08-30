import importlib.util
import unittest
from pathlib import Path
from types import SimpleNamespace


MODULE_PATH = Path(__file__).with_name("prepare_dataset.py")
SPEC = importlib.util.spec_from_file_location("prepare_dataset", MODULE_PATH)
prepare_dataset = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(prepare_dataset)


class FakeDataset:
    def docs_iter(self):
        return iter(
            [
                SimpleNamespace(doc_id="d1", title="Title one", text="Body one"),
                SimpleNamespace(doc_id="d2", title="", text="Body two"),
                SimpleNamespace(doc_id="d3", title="Distractor", text="Body three"),
                SimpleNamespace(doc_id="d4", title="Ignored", text="Body four"),
            ]
        )

    def qrels_iter(self):
        return iter(
            [
                SimpleNamespace(query_id="q1", doc_id="d1", relevance=1),
                SimpleNamespace(query_id="q2", doc_id="d2", relevance=2),
                SimpleNamespace(query_id="q3", doc_id="d4", relevance=0),
            ]
        )

    def queries_iter(self):
        return iter(
            [
                SimpleNamespace(query_id="q1", text="Claim one"),
                SimpleNamespace(query_id="q2", text="Question two"),
                SimpleNamespace(query_id="q3", text="Ignored query"),
            ]
        )


class PrepareDatasetTest(unittest.TestCase):
    def test_deduplicate_by_source_text_keeps_one_source_per_content_hash(self):
        rows = [
            {"query_id": "q1", "doc_id": "d1", "doc_text": "same"},
            {"query_id": "q2", "doc_id": "d2", "doc_text": "same"},
            {"query_id": "q3", "doc_id": "d3", "doc_text": "different"},
        ]
        self.assertEqual(
            ["q1", "q3"],
            [row["query_id"] for row in prepare_dataset.deduplicate_by_source_text(rows)],
        )

    def test_sample_ir_dataset_builds_single_hop_cases_and_distractors(self):
        rows = prepare_dataset.sample_ir_dataset(
            FakeDataset(), 2, 1, 42, "beir-scifact", "scifact"
        )
        relevant = [row for row in rows if not row["is_distractor"]]
        distractors = [row for row in rows if row["is_distractor"]]

        self.assertEqual(2, len(relevant))
        self.assertEqual(1, len(distractors))
        self.assertEqual({"q1", "q2"}, {row["query_id"] for row in relevant})
        self.assertIn("Title one\n\nBody one", {row["doc_text"] for row in relevant})
        self.assertEqual("beir-scifact", distractors[0]["dataset_id"])
        self.assertTrue(distractors[0]["source_ref"].startswith("scifact:distractor:"))


if __name__ == "__main__":
    unittest.main()
