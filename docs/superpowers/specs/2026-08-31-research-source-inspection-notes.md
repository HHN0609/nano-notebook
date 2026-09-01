# Research `inspect_source` Tool design

## Document status

- **Status:** Implemented and locally accepted in `research.executor@10`
- **Date:** 2026-08-31
- **Scope:** Giving Research an orientation view of a Ready PDF Source
- **Evaluation parser:** PyMuPDF4LLM for local learning and experimentation

This document defines the implemented model-facing Tool and its supporting
document-structure path. It is not a production dependency approval or legal
conclusion about distributing or operating PyMuPDF4LLM.

## Pinned first-implementation details

The first local implementation uses these deliberately bounded decisions:

- parser dependency: `pymupdf4llm==1.28.2` on Python 3.10 or newer;
- parser policy identity: `pdf-structure-no-ocr-v1` with `use_ocr=false`;
- internal protocol: authenticated multipart `POST /v1/parse-pdf` containing
  one JSON manifest and one already-validated PDF body, never a URL;
- parser limits: 100 MiB input, 500 pages, 120 seconds, and 16 MiB canonical
  JSON response; the deployment has no public-network route or credentials;
- Source Map schema: `nano.source-map.v1`, stored under
  `sources/<source_id>/evidence/<revision_id>/source-map/<map_id>.json`;
- PostgreSQL table `source_maps` stores the immutable map identity, Source,
  Notebook, Evidence Revision, original hash, parser and policy identity,
  artifact key/hash/bytes, page and entry counts, navigation kind, confidence,
  and creation time; one v1 map is allowed per Evidence Revision and policy;
- the sidecar returns normalized pages, blocks, bounding boxes, reading order,
  inferred heading levels, and the embedded PDF outline. Nano, not the
  sidecar, joins Evidence Unit identities and chooses the model projection;
- parser or structure inference failure falls back deterministically to
  page-aware Evidence Units. A rich-parser failure never replaces the existing
  normalization/vision path and never invalidates a valid Evidence Revision;
- object-store or database infrastructure failure is retryable. A permanent
  inability to persist either a rich map or deterministic page-sample map is
  recorded as inspection unavailable rather than leaking a partial body; and
- model projection is canonical JSON, at most 24 navigation entries and
  12 KiB. Previews shrink before entries are omitted, and the output reports
  omitted previews, entries, and uncovered page ranges.

The public PyPI metadata for 1.28.2 lists Python 3.10+ but its Source project
link was stale when this decision was made. The exact wheel is therefore
hash-pinned in the sidecar build, and production redistribution/licensing
approval remains explicitly out of scope.

## Problem

`web_search` should expose only discovery metadata, while `search_evidence`
should answer focused questions with bounded original evidence. A model that
has neither read the whole paper nor formed useful questions still needs a
low-cost way to learn the document's shape and find promising research entry
points.

Adding an `overview` mode to `search_evidence` would mix document navigation
with query-driven evidence retrieval. The two responsibilities should remain
separate.

## Decision

Introduce the standalone `inspect_source` Tool:

```text
inspect_source: What is this Source's structure and where should I look?
search_evidence: Which original passages support or challenge this question?
```

For the initial local experiment, use PyMuPDF4LLM to convert PDF bytes into a
structured JSON document representation. JSON is the authoritative derived
artifact for inspection because it can retain hierarchy, element types, page
identity, bounding boxes, reading order, and original text. Markdown is a
derived debugging or human-review view, not the stored authority.

## How Research starts without reading the whole paper

Research uses three different information boundaries:

```text
web_search
    -> discovery metadata: title, URL, provider description
save_url_as_source
    -> permanent asynchronous Source admission
inspect_source
    -> query-free, coverage-oriented map of one Ready Source
search_evidence
    -> query-driven, relevance-oriented original evidence
```

The discovery description helps the model choose which PDFs are worth saving,
but it is neither evidence nor a substitute for reading. Once an imported
Source becomes Ready, the model inspects a bounded number of promising Sources
to learn their abstracts, structure, representative passages, conclusions,
limitations, and unexplored regions. That orientation gives the model enough
material to formulate hypotheses, comparison dimensions, contradictions, and
focused retrieval questions.

The model then calls `search_evidence` with those questions. It never needs the
complete paper in one request, but it can progressively recover the original
passages needed for the report.

The Harness does not automatically inject every Source overview. The Research
workflow guidance should prefer inspecting Sources that are central, novel,
or difficult to understand from discovery metadata, rather than spending
context on every imported document.

## Experimental data flow

```text
permanent PDF original in object storage
    -> bounded internal parser request
    -> PyMuPDF4LLM sidecar
    -> immutable structured JSON artifact
    -> Nano Source Map adapter
    -> inspect_source(source_id)
       -> bounded document orientation result
    -> search_evidence(query)
       -> focused original Evidence Units
```

The parser receives trusted PDF bytes from Nano's existing acquisition and
Source pipeline. It does not accept a public URL, perform network acquisition,
select a Notebook, or decide authorization. The Go worker owns byte limits,
timeouts, Source identity, object persistence, and lifecycle transitions.

Keep the parser behind a narrow internal interface or sidecar boundary so a
later Docling or other parser evaluation does not change the model-facing Tool
contract.

## Initial parser scope

The first experiment targets native-text PDFs and disables OCR to keep runtime
and deployment light. Scanned or textless pages continue to use Nano's current
bounded vision fallback; PyMuPDF4LLM does not initially replace that path.

The experiment runs alongside the current PDF normalization path. It must not
delete or silently reinterpret existing normalized Artifacts, Evidence
Revisions, page viewers, or Evidence Units. Promotion to the primary parser
requires a separate design and evaluation result.

## `inspect_source` boundary

Model-facing Tool definition:

```text
name: inspect_source
description: Inspect the structure and representative original passages of
             one Ready Notebook Source before formulating focused evidence
             queries. This is for orientation, not final evidence retrieval.
scheduling: parallel-safe and read-only for distinct or repeated Source IDs
availability: Research executor only in the initial release
```

Input:

```json
{
  "source_id": "src_123"
}
```

Only a Ready, authorized Source may be inspected. The Tool does not accept a
query, URL, object key, page range, output budget, parser choice, or Notebook
identity from the model.

The result should contain a bounded subset of:

- Source title and document metadata;
- page count and parse diagnostics;
- an extractive abstract when identifiable;
- headings and their hierarchy;
- page ranges and bounding-box provenance;
- a short original-text preview for major sections; and
- explicit truncation, confidence, and fallback metadata.

Every returned original-text preview must retain its Source/Evidence identity
and page provenance. An inspection result may help the model form questions,
but it does not bypass the report's evidence and citation rules.

The model cannot request a parser, navigation strategy, confidence threshold,
number of entries, or byte limit. These are pinned Harness policy so replay
does not change because the model selected different extraction behavior.

### Result contract

Conceptual version-one output:

```json
{
  "result_version": 1,
  "source": {
    "source_id": "src_123",
    "evidence_revision_id": "rev_456",
    "title": "Example paper",
    "media_type": "application/pdf",
    "page_count": 18
  },
  "navigation": {
    "kind": "inferred_sections",
    "confidence": "medium",
    "structure_artifact_sha256": "..."
  },
  "abstract": {
    "text": "Original abstract text...",
    "page_start": 1,
    "page_end": 1,
    "evidence_unit_ids": ["ev_abstract"]
  },
  "entries": [
    {
      "entry_id": "entry_method",
      "parent_entry_id": null,
      "kind": "section",
      "heading": "Method",
      "heading_level": 1,
      "page_start": 4,
      "page_end": 8,
      "preview": "Original representative passage...",
      "evidence_unit_ids": ["ev_104"],
      "preview_omitted": false
    }
  ],
  "coverage": {
    "represented_page_ranges": [[1, 1], [4, 8], [16, 18]],
    "omitted_entry_count": 3,
    "truncated": true
  },
  "warnings": ["section hierarchy was inferred from PDF layout"]
}
```

`confidence` uses a small stable enum such as `high`, `medium`, or `low`
instead of a misleading floating-point probability. Stable entry and artifact
identities let the Harness explain and reproduce the projection.

The result has a server-controlled ceiling of 12 KiB and at most 24 navigation
entries in the implemented first version. They are pinned Tool policy, not model
inputs or Provider limits. Changing them requires a versioned policy update.

## Honest PDF degradation

PDF has no universal DOM. The Tool must not claim a reliable section tree when
the parser did not establish one. Its navigation output degrades explicitly:

1. `embedded_outline`: use a usable embedded table of contents or bookmarks.
2. `inferred_sections`: use parser-inferred headings and reading order, with
   confidence and warnings.
3. `page_samples`: when structure is unreliable, return representative
   original passages distributed across the beginning, body, and end.

The Harness chooses the degradation level from parser diagnostics; the model
does not choose it. A page-sampling fallback is preferable to a fabricated
section hierarchy.

For non-PDF Sources, existing native structure remains preferable: HTML uses
its cleaned heading structure and DOCX uses its explicit paragraph styles.
`inspect_source` exposes one format-neutral contract while each Source adapter
records how its structure was established.

## Source Map artifact

`inspect_source` must not reconstruct a document tree on every Tool call. The
Source pipeline materializes an immutable, versioned Source Map derived from a
specific original content hash, parser identity and version, parser policy,
and Evidence Revision.

The structured JSON body belongs in object storage. PostgreSQL stores its
identity, Source and Evidence Revision relationship, parser metadata, content
hash, byte size, status, and object reference. The exact schema and namespace
remain for the implementation design.

If rich structure extraction fails but the Source has valid page-aware
Evidence Units, the Source Map can still materialize a deterministic
`page_samples` representation. A failed rich structure parse should not make
an otherwise searchable Source unusable unless the underlying Source
normalization itself failed.

For the same Source Map version and Tool policy, repeated calls return the
same logical result. Accepted Action checkpoints remain authoritative for
replay, while the immutable Source Map provides the durable body from which
the bounded model projection can be reconstructed.

## Bounded projection

Selection happens before byte truncation:

1. retain a compact outline or page-coverage manifest;
2. prioritize the abstract, conclusion, and limitations when reliably
   identified;
3. allocate a small preview budget across major sections or page regions;
4. shrink previews before removing navigation entries; and
5. report omitted previews and uncovered regions explicitly.

The runtime must not concatenate the complete converted Markdown and take its
first bytes. That would reproduce the prefix bias the Tool is intended to
avoid.

## Separation from `search_evidence`

`inspect_source` is query-free and coverage-oriented. `search_evidence`
remains query-driven and relevance-oriented. Inspection does not change a
Source's readiness, admission status, Evidence Revision, vector projection, or
citation eligibility.

After inspection, the model forms concrete questions and calls
`search_evidence` for the original passages needed by the report. Generated
interpretations in the model's context are not evidence merely because they
were prompted by an inspection result.

Initial report policy remains strict: `inspect_source` output is navigational
and is not sufficient by itself to authorize a report claim. Even when a
preview is verbatim and carries provenance, the model must retrieve the
supporting material through `search_evidence` before citing it. This keeps one
clear evidence-eligibility path and prevents an orientation preview from being
mistaken for complete support.

This is a product-policy boundary, not a claim that inspection previews are
untraceable. Their identities are retained so later `search_evidence` results
and diagnostics can be related back to the same Source revision.

## Research workflow guidance

The Research workflow Skill teaches the executor to:

1. use discovery metadata to select promising PDFs rather than importing every
   search result;
2. wait for the Harness-owned Source dependency barrier instead of polling;
3. inspect a newly Ready Source when its title and discovery description are
   insufficient to formulate useful questions;
4. record resulting research questions or evidence gaps in the existing TODO,
   not copy inspection excerpts into TODO items;
5. use `search_evidence` for concrete claims, comparisons, contradictions, and
   report citations; and
6. avoid repeatedly inspecting an unchanged Source unless the earlier
   orientation is no longer available in the projected context.

Skill guidance cannot make pending Sources inspectable, enlarge Tool
permissions, or change citation eligibility.

## Context and compaction behavior

An `inspect_source` result is an ordinary bounded Action result in recent exact
history. When its Agent Step later crosses the Research compaction cut, the
compressed Capsule keeps only the Source ID, Source Map identity, useful
questions or changed assumptions, and durable follow-up state. It omits the
full set of previews.

Because the Source Map is permanent and immutable, the model can inspect the
same revision again if genuinely needed after compaction. The broader policy
for suppressing repeated evidence and repeated inspections is part of the
separate Research repetition design.

## Errors and lifecycle states

The Tool returns stable domain outcomes rather than pretending every Source
has a usable map:

- `source_not_ready`: the authorized Source exists but is not Ready; include
  only its safe lifecycle state;
- `source_not_found`: no authorized Source identity is visible to this Run;
- `source_inspection_unavailable`: neither a valid structure artifact nor a
  safe page-sampling fallback can be produced;
- `source_structure_invalid`: stored structure identity, hash, revision, or
  schema validation failed; and
- `inspection_projection_exceeded`: the Harness could not produce a valid
  bounded result without violating the minimum navigation contract.

Pending and failed states do not return partial paper text. Unexpected parser
or storage errors fail the Action with bounded diagnostics; raw parser logs,
object keys, and document bodies never enter model context or Trace
attributes.

## Security and authority

- Public acquisition remains in the existing bounded URL acquisition path;
  PyMuPDF4LLM never fetches model-supplied URLs.
- Original PDF bytes and full structure JSON never enter PostgreSQL Action
  payloads, model context, or Trace.
- Notebook, member, Run, Source, Evidence Revision, and object identities come
  from trusted execution authority rather than model input.
- Object reads validate expected byte size and SHA-256 before parsing or
  projection.
- Parser CPU, memory, pages, bytes, wall time, and output size are bounded.
- The parser service should run without public-network access and with a
  replaceable internal protocol.

## Observability

Record bounded metadata only:

- parser identity and version;
- navigation kind and confidence enum;
- Source Map status, byte size, and hash identity;
- page and entry counts;
- projection bytes and truncation status;
- parse and projection latency; and
- stable failure reason.

Do not record abstracts, previews, headings, user queries, object keys, or PDF
bodies in metrics and Trace attributes.

## Evaluation before implementation commitment

Evaluate the parser on a small fixed corpus containing:

- ordinary arXiv two-column papers;
- papers with formulas and tables;
- papers with weak or missing bookmarks;
- Chinese and English papers;
- malformed reading order cases; and
- scanned or mixed native-text/scan PDFs to verify graceful fallback.

Review heading hierarchy, reading order, abstract/conclusion identification,
page and bounding-box provenance, output determinism, latency, memory, and
failure classification. Compare the structured JSON with the rendered PDF,
not only with generated Markdown.

Tool-level acceptance should additionally prove:

- a model can formulate useful focused questions after inspection without
  receiving the whole paper;
- early, middle, and late document regions remain represented under the output
  budget;
- low-confidence structure degrades honestly to page sampling;
- no pending or unauthorized Source leaks content;
- repeated calls against one pinned Source Map are deterministic;
- compaction can omit previews without losing the ability to inspect again;
- report claims still require `search_evidence`; and
- parser failure does not corrupt the existing Ready Source or Evidence
  Revision.

The local acceptance gate now includes one combined seven-page PDF path using
the live network-isolated PyMuPDF4LLM 1.28.2 sidecar, immutable Source Map
persistence, `inspect_source`, a live Qwen tool decision that forms a focused
question from the bounded inspection projection, and page-aware
`search_evidence` retrieval. Separate PostgreSQL cases reject both a processing
Source and a Ready Source that is not pinned into the current Research Run.

## Non-goals

- Giving the model unrestricted page-by-page browsing or complete PDF text.
- Adding an overview mode to `search_evidence`.
- Generating an authoritative semantic summary of the paper during Source
  ingestion.
- Automatically inspecting every imported Source.
- Treating parser-inferred headings as certain when confidence is low.
- Replacing the current PDF extraction and vision paths before evaluation.
- Solving repeated `search_evidence` fragments; that is a separate design
  problem.

## Deferred beyond the first local implementation

- Evaluation thresholds required to adopt the parser beyond local learning.
- Production licensing and distribution review.
