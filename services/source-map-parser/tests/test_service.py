import hashlib
import json
import unittest

import pymupdf
from fastapi.testclient import TestClient

from source_map_parser.service import PARSER_POLICY_ID, create_app, parse_pdf


def one_page_pdf() -> bytes:
    document = pymupdf.open()
    page = document.new_page(width=612, height=792)
    page.insert_text((72, 90), "Abstract", fontsize=18)
    page.insert_text((72, 125), "This paper studies deterministic source inspection.", fontsize=11)
    payload = document.tobytes()
    document.close()
    return payload


def manifest(payload: bytes) -> dict:
    return {
        "schema_version": 1,
        "source_id": "src_real_pdf",
        "input_sha256": hashlib.sha256(payload).hexdigest(),
        "input_bytes": len(payload),
        "parser_policy_id": PARSER_POLICY_ID,
        "ocr": False,
        "max_pages": 10,
        "max_output_bytes": 16 * 1024 * 1024,
    }


class SourceMapParserTest(unittest.TestCase):
    def test_liveness_is_public_and_discloses_no_parser_state(self) -> None:
        response = TestClient(create_app("sidecar-token")).get("/health/live")

        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json(), {"status": "ok"})

    def test_real_pymupdf4llm_path_returns_ordered_bounded_blocks_without_ocr(self) -> None:
        payload = one_page_pdf()
        result = parse_pdf(payload, manifest(payload))

        self.assertEqual(result["parser_identity"], "pymupdf4llm")
        self.assertEqual(result["parser_version"], "1.28.2")
        self.assertEqual(result["parser_policy_id"], PARSER_POLICY_ID)
        self.assertEqual(result["page_count"], 1)
        self.assertEqual(result["pages"][0]["ordinal"], 1)
        self.assertTrue(result["pages"][0]["blocks"])
        self.assertEqual(
            [item["reading_order"] for item in result["pages"][0]["blocks"]],
            list(range(len(result["pages"][0]["blocks"]))),
        )
        self.assertIn("deterministic source inspection", json.dumps(result))

    def test_converter_is_always_called_with_ocr_disabled(self) -> None:
        payload = one_page_pdf()
        observed = {}

        def converter(document, **kwargs):
            observed.update(kwargs)
            return {
                "pages": [
                    {
                        "metadata": {"width": 612, "height": 792},
                        "text": "Body text.",
                        "words": [(72, 72, 140, 90, "Body", 0, 0, 0)],
                    }
                ]
            }

        result = parse_pdf(payload, manifest(payload), converter=converter)
        self.assertIs(observed["use_ocr"], False)
        self.assertIs(observed["page_chunks"], True)
        self.assertIs(observed["extract_words"], True)
        self.assertNotIn("force_ocr", observed)
        self.assertEqual(result["pages"][0]["blocks"][0]["text"], "Body text.")

    def test_http_boundary_requires_auth_and_exact_manifest_identity(self) -> None:
        payload = one_page_pdf()
        client = TestClient(create_app("sidecar-token"))

        unauthorized = client.post(
            "/v1/parse-pdf",
            files={
                "manifest": (None, json.dumps(manifest(payload)), "application/json"),
                "document": ("input.pdf", payload, "application/pdf"),
            },
        )
        self.assertEqual(unauthorized.status_code, 401)

        response = client.post(
            "/v1/parse-pdf",
            headers={"Authorization": "Bearer sidecar-token"},
            files={
                "manifest": (None, json.dumps(manifest(payload)), "application/json"),
                "document": ("input.pdf", payload, "application/pdf"),
            },
        )
        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(response.headers["cache-control"], "no-store")
        self.assertEqual(response.json()["input_sha256"], manifest(payload)["input_sha256"])

        drifted = manifest(payload)
        drifted["input_sha256"] = "0" * 64
        response = client.post(
            "/v1/parse-pdf",
            headers={"Authorization": "Bearer sidecar-token"},
            files={
                "manifest": (None, json.dumps(drifted), "application/json"),
                "document": ("input.pdf", payload, "application/pdf"),
            },
        )
        self.assertEqual(response.status_code, 400)


if __name__ == "__main__":
    unittest.main()
