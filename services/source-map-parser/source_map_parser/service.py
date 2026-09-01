from __future__ import annotations

import hashlib
import hmac
import importlib.metadata
import json
import math
import os
import re
from collections import defaultdict
from typing import Any, Callable

import pymupdf
import pymupdf4llm
from fastapi import FastAPI, File, Form, Header, HTTPException, UploadFile
from fastapi.responses import JSONResponse


PARSER_IDENTITY = "pymupdf4llm"
PARSER_VERSION = importlib.metadata.version("pymupdf4llm")
PARSER_POLICY_ID = "pdf-structure-no-ocr-v1"
MAX_INPUT_BYTES = 100 * 1024 * 1024
MAX_OUTPUT_BYTES = 16 * 1024 * 1024
MAX_PAGES = 500
MANIFEST_KEYS = {
    "schema_version",
    "source_id",
    "input_sha256",
    "input_bytes",
    "parser_policy_id",
    "ocr",
    "max_pages",
    "max_output_bytes",
}


class InvalidRequest(ValueError):
    pass


def _validate_manifest(value: dict[str, Any], payload: bytes) -> None:
    if set(value) != MANIFEST_KEYS:
        raise InvalidRequest("invalid manifest fields")
    digest = hashlib.sha256(payload).hexdigest()
    if (
        value.get("schema_version") != 1
        or not isinstance(value.get("source_id"), str)
        or not 0 < len(value["source_id"].strip()) <= 128
        or value.get("input_sha256") != digest
        or value.get("input_bytes") != len(payload)
        or not 0 < len(payload) <= MAX_INPUT_BYTES
        or value.get("parser_policy_id") != PARSER_POLICY_ID
        or value.get("ocr") is not False
        or not isinstance(value.get("max_pages"), int)
        or not 0 < value["max_pages"] <= MAX_PAGES
        or not isinstance(value.get("max_output_bytes"), int)
        or not 0 < value["max_output_bytes"] <= MAX_OUTPUT_BYTES
    ):
        raise InvalidRequest("invalid manifest identity or budget")


def _decode_converter_output(raw: Any) -> dict[str, Any]:
    if isinstance(raw, str):
        raw = json.loads(raw)
    if isinstance(raw, list):
        return {"pages": raw}
    if not isinstance(raw, dict):
        raise InvalidRequest("parser returned an invalid document")
    return raw


def _page_chunks(raw: dict[str, Any]) -> list[dict[str, Any]]:
    pages = raw.get("pages")
    if isinstance(pages, list):
        return [page if isinstance(page, dict) else {} for page in pages]
    if isinstance(raw.get("page_chunks"), list):
        return [page if isinstance(page, dict) else {} for page in raw["page_chunks"]]
    return []


def _heading_map(text: str) -> dict[str, int]:
    headings: dict[str, int] = {}
    for line in text.splitlines():
        match = re.match(r"^(#{1,6})\s+(.+?)\s*$", line)
        if match:
            headings[" ".join(match.group(2).split())] = len(match.group(1))
    return headings


def _word_blocks(words: Any, headings: dict[str, int], parser_text: str) -> list[dict[str, Any]]:
    grouped: dict[tuple[int, int], list[tuple[float, float, float, float, str, int]]] = defaultdict(list)
    if not isinstance(words, list):
        return []
    for index, word in enumerate(words):
        if not isinstance(word, (list, tuple)) or len(word) < 5:
            continue
        try:
            x0, y0, x1, y1 = (float(word[0]), float(word[1]), float(word[2]), float(word[3]))
        except (TypeError, ValueError):
            continue
        text = str(word[4]).strip()
        if not text or not all(math.isfinite(item) for item in (x0, y0, x1, y1)):
            continue
        block_no = int(word[5]) if len(word) > 5 else index
        line_no = int(word[6]) if len(word) > 6 else 0
        word_no = int(word[7]) if len(word) > 7 else index
        grouped[(block_no, line_no)].append((x0, y0, x1, y1, text, word_no))
    blocks: list[dict[str, Any]] = []
    for _, items in sorted(grouped.items()):
        items.sort(key=lambda item: item[5])
        text = " ".join(item[4] for item in items)
        level = headings.get(" ".join(text.split()), 0)
        blocks.append(
            {
                "reading_order": len(blocks),
                "kind": "heading" if level else "paragraph",
                "text": text,
                **({"heading_level": level} if level else {}),
                "bbox": {
                    "x0": min(item[0] for item in items),
                    "y0": min(item[1] for item in items),
                    "x1": max(item[2] for item in items),
                    "y1": max(item[3] for item in items),
                },
            }
        )
    normalized_parser_text = " ".join(
        line.lstrip("#").strip() for line in parser_text.splitlines() if line.strip()
    )
    normalized_word_text = " ".join(item["text"] for item in blocks)
    if blocks and normalized_parser_text and normalized_parser_text != normalized_word_text:
        level = headings.get(normalized_parser_text, 0)
        return [
            {
                "reading_order": 0,
                "kind": "heading" if level else "paragraph",
                "text": normalized_parser_text,
                **({"heading_level": level} if level else {}),
                "bbox": {
                    "x0": min(item["bbox"]["x0"] for item in blocks),
                    "y0": min(item["bbox"]["y0"] for item in blocks),
                    "x1": max(item["bbox"]["x1"] for item in blocks),
                    "y1": max(item["bbox"]["y1"] for item in blocks),
                },
            }
        ]
    return blocks


def _native_blocks(page: pymupdf.Page, headings: dict[str, int]) -> list[dict[str, Any]]:
    blocks: list[dict[str, Any]] = []
    for item in page.get_text("blocks", sort=True):
        if len(item) < 5:
            continue
        text = " ".join(str(item[4]).split())
        if not text:
            continue
        level = headings.get(text, 0)
        blocks.append(
            {
                "reading_order": len(blocks),
                "kind": "heading" if level else "paragraph",
                "text": text,
                **({"heading_level": level} if level else {}),
                "bbox": {"x0": float(item[0]), "y0": float(item[1]), "x1": float(item[2]), "y1": float(item[3])},
            }
        )
    return blocks


def parse_pdf(
    payload: bytes,
    manifest: dict[str, Any],
    *,
    converter: Callable[..., Any] = pymupdf4llm.to_json,
) -> dict[str, Any]:
    _validate_manifest(manifest, payload)
    document = pymupdf.open(stream=payload, filetype="pdf")
    try:
        if document.page_count < 1 or document.page_count > manifest["max_pages"]:
            raise InvalidRequest("PDF page budget exceeded")
        converted = _decode_converter_output(
            converter(
                document,
                use_ocr=False,
                page_chunks=True,
                extract_words=True,
                show_progress=False,
            )
        )
        chunks = _page_chunks(converted)
        pages: list[dict[str, Any]] = []
        for index in range(document.page_count):
            page = document[index]
            chunk = chunks[index] if index < len(chunks) else {}
            text = str(chunk.get("text") or chunk.get("markdown") or chunk.get("md") or "")
            headings = _heading_map(text)
            blocks = _word_blocks(chunk.get("words"), headings, text)
            if not blocks:
                blocks = _native_blocks(page, headings)
            pages.append(
                {
                    "ordinal": index + 1,
                    "width": float(page.rect.width),
                    "height": float(page.rect.height),
                    "blocks": blocks,
                }
            )
        outline = []
        for item in document.get_toc(simple=True):
            if len(item) < 3:
                continue
            level, title, page = int(item[0]), " ".join(str(item[1]).split()), int(item[2])
            if 1 <= level <= 6 and title and 1 <= page <= document.page_count:
                outline.append({"level": level, "title": title[:1000], "page": page})
        result = {
            "schema_version": 1,
            "source_id": manifest["source_id"],
            "input_sha256": manifest["input_sha256"],
            "parser_identity": PARSER_IDENTITY,
            "parser_version": PARSER_VERSION,
            "parser_policy_id": PARSER_POLICY_ID,
            "page_count": document.page_count,
            "outline": outline,
            "pages": pages,
        }
        encoded = json.dumps(result, ensure_ascii=False, separators=(",", ":")).encode()
        if len(encoded) > manifest["max_output_bytes"]:
            raise InvalidRequest("parser output budget exceeded")
        return result
    finally:
        document.close()


def create_app(service_token: str) -> FastAPI:
    service_token = service_token.strip()
    if not service_token:
        raise ValueError("Source Map parser service token is required")
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/health/live")
    async def liveness() -> dict[str, str]:
        return {"status": "ok"}

    @app.post("/v1/parse-pdf")
    async def parse_pdf_endpoint(
        manifest: str = Form(...),
        document: UploadFile = File(...),
        authorization: str = Header(default=""),
    ) -> JSONResponse:
        provided = authorization.removeprefix("Bearer ")
        if not hmac.compare_digest(provided, service_token):
            raise HTTPException(status_code=401, detail="unauthorized")
        pdf_payload = await document.read(MAX_INPUT_BYTES + 1)
        if len(manifest.encode()) > 64 * 1024 or len(pdf_payload) > MAX_INPUT_BYTES:
            raise HTTPException(status_code=413, detail="input too large")
        if document.content_type != "application/pdf":
            raise HTTPException(status_code=400, detail="invalid media type")
        try:
            parsed_manifest = json.loads(manifest)
            if not isinstance(parsed_manifest, dict):
                raise InvalidRequest("manifest must be an object")
            result = parse_pdf(pdf_payload, parsed_manifest)
        except (InvalidRequest, json.JSONDecodeError, pymupdf.FileDataError, ValueError):
            raise HTTPException(status_code=400, detail="invalid parse request") from None
        except Exception:
            raise HTTPException(status_code=422, detail="parse failed") from None
        return JSONResponse(
            result,
            headers={"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"},
        )

    return app


app = create_app(os.environ.get("NANO_SOURCE_MAP_PARSER_SERVICE_TOKEN", "nano-local-source-map-parser-token"))
