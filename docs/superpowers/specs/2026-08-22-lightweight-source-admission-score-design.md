# Lightweight Source Admission Score design

## Outcome

Nano Notebook evaluates each Source before its Evidence enters the active retrieval projection. The first version is deliberately lightweight: it performs deterministic integrity and extraction checks for every Source and, for public URL Sources only, issues at most three bounded Web Search queries to establish external traceability. It does not extract Claims, compare every document statement, call an LLM, or add work to answer generation.

The result is a versioned, explainable **Source Admission Report**. A public Source with enough observed signals and a score at or above the active threshold can proceed automatically to indexing. A public Source with insufficient signal coverage or a score below the threshold pauses for member review. A user-supplied private file skips public Web verification and proceeds after the existing technical checks, visibly classified as user-supplied rather than externally verified.

The score means “this Source is sufficiently traceable and well extracted to enter this Notebook's searchable material.” It is not a probability that every statement in the Source is true.

## Goals

- Keep expensive or low-quality public material out of Qdrant until admission is decided.
- Add no LLM calls during Source Processing or answer generation.
- Make every automatic decision reproducible from bounded observations and a versioned policy.
- Explain the decision in the Source list and viewer instead of presenting an opaque score.
- Preserve user authority over useful niche material through an explicit review override.
- Keep PostgreSQL and object storage authoritative; Qdrant remains a rebuildable retrieval projection.

## Non-goals

- Claim extraction, Natural Language Inference, or factual truth verification.
- A global support/contradiction graph across Source contents.
- Domain-wide political, editorial, or institutional reputation ratings.
- Treating search rank, result count, or repeated syndication as proof of truth.
- Answer-level confidence, answer-time groundedness scoring, or a new citation contract.
- Public Web Search for private uploads, internal documents, or Sources without an admitted public URL.
- Automatically rejecting a technically valid Source solely because Web Search is unavailable or inconclusive.

## Product language and authority

The member-facing term is **Source Admission**, not factual confidence or truth verification.

- PostgreSQL owns the Source, active Evidence Revision, Admission Report, review decision, and reason codes.
- Object storage owns the immutable original and normalized artifacts.
- The Search Provider supplies untrusted discovery observations. A search snippet never becomes Source Evidence.
- The Admission Policy deterministically computes signal coverage, component scores, and the final automatic status.
- A Notebook member with `source.maintain` authority may admit a review-required Source. The override is durable and applies only to the exact Source content hash, Evidence Revision, and policy version reviewed.
- Qdrant receives only Sources that were automatically admitted, explicitly overridden, or exempt as user-supplied private material after technical checks.

## Lifecycle

The current processing chain gains an explicit pre-index admission boundary:

```text
uploaded
→ validating
→ normalizing
→ segmenting
→ qualifying
   ├─ deterministic profile and extraction observations
   ├─ bounded public Web Search when applicable
   └─ Source Admission Report
→ indexing
→ verifying
→ ready
```

The Source row is created and shown immediately, so the member can observe progress, delete it, or retry it. `ready` continues to mean that processing and index verification completed. Admission status is separate from lifecycle status.

When a public Source requires review, automated processing ends without creating a Qdrant projection. The Source enters `review_required`; its processing job completes successfully because automation reached a valid business outcome. An authorized approval records an immutable override, returns the Source to the resumable pre-index state, and queues projection work. Rejection leaves it unindexed and removable or retryable.

Private file uploads do not enter `review_required` merely because public corroboration is unavailable. They record `external_verification_not_applicable` and continue after deterministic technical checks.

## Deterministic observations

### Technical gate

Existing hard invariants remain outside the numeric score. Any of these failures is terminal and cannot be overridden:

- original byte size or SHA-256 differs from the admitted object;
- the extractor produces no usable primary content;
- the normalized Artifact or Coverage contract is invalid;
- Evidence Units are missing, malformed, out of bounds, or inconsistent with the Artifact;
- a configured processing budget is exceeded; or
- the fetched page is a detected login, challenge, access-denied, or error document rather than primary content.

Technical success does not imply credibility. It only allows scoring to begin.

### Source profile

The first version records only values obtained without semantic inference:

- input kind, original URL, final URL, canonical URL identity, and registrable domain;
- media type, format, byte size, original content SHA-256, and retrieval time;
- exact title and metadata explicitly declared by the document or HTML;
- stable identifiers found by bounded parsers, initially DOI, ISBN, and configured report-number patterns; and
- exact duplicate identity through existing content hashes and canonical URLs.

Document-declared author, publisher, and publication date remain labelled as declared metadata. Nano does not claim to have authenticated them.

### Extraction quality

The Admission Policy derives extraction observations from the normalized Artifact and Evidence rows:

- complete versus partial Coverage;
- total primary-content runes and Evidence Unit count;
- Coverage Gap count and affected coordinates;
- invalid-character and repeated-block ratios;
- primary-text versus boilerplate/link ratio where the format exposes it; and
- format-specific completeness such as processed pages, slides, or media duration when deterministically available.

Missing optional metadata and partial but usable extraction are soft observations. They cannot override a hard invariant, and they do not independently declare the content untrustworthy.

## Bounded Web Search

Public URL Sources may issue at most three queries. Query construction is deterministic and uses the strongest available identity in this order:

1. exact stable identifier;
2. exact title;
3. exact title plus declared author or publisher.

Blank, duplicate, overlong, or low-information queries are omitted rather than replaced by model-authored queries. The policy bounds total queries, results per query, title/snippet/URL bytes, provider timeout, and retained observations.

Search results are normalized through canonical URL identity and registrable domain. Repeated canonical URLs and repeated results from one domain do not count as independent corroboration. The v1 Search-only path does not fetch every result body and therefore cannot prove that two different domains are editorially independent.

The first version recognizes only mechanical agreement:

- a result resolves to the same canonical public Source;
- a stable identifier matches exactly;
- normalized title, author, publisher, or date fields agree under bounded deterministic comparison;
- an independent domain references the same stable identifier or exact title; and
- a result exposes conflicting identity metadata.

Snippet similarity is a weak identity signal only. Search snippets remain untrusted and never enter normalized Evidence, the model context, or citations.

## Score and signal coverage

The initial policy defines four component weights:

| Component | Weight | Meaning |
| --- | ---: | --- |
| Provenance | 0.40 | Is the public Source identity externally traceable and internally consistent? |
| Extraction | 0.30 | Did Nano obtain usable, structurally complete primary content? |
| External corroboration | 0.20 | Do distinct-domain results mechanically refer to the same item? |
| Freshness | 0.10 | Is the Source date known and acceptable for its configured Source class? |

Each observed component produces a value from 0 to 1 and stable reason codes. Unobserved components do not silently receive a zero. Instead:

```text
signal_coverage = sum(weights of observed components)
admission_score = sum(weight × component_value) / signal_coverage
```

Automatic admission requires both:

```text
at least one Web-derived exact identity match
signal_coverage >= 0.70
admission_score >= 0.75
```

An exact identity match is a matching canonical Source URL or stable identifier returned by Web Search. Title or snippet similarity alone cannot satisfy this requirement. These are versioned v1 candidate values, not universal truth thresholds. Before enforcement, Nano runs the policy in shadow mode and calibrates them against frozen fixtures plus sampled human review. Policy identity, canonical configuration SHA-256, observations, component values, coverage, score, status, and reasons persist with every report.

Final automatic statuses are:

- `passed`: sufficient observed signal and score at or above the threshold;
- `review_required`: technically valid, but coverage is insufficient, score is below threshold, or public search ended inconclusively;
- `not_applicable`: private user-supplied Source with no public verification attempt; and
- `failed`: terminal technical gate failure, represented through the existing Source failure path rather than rescued by a score.

There is no `truth_verified` status.

## Persistence

The implementation adds one current immutable report per Evidence Revision and retains bounded audit observations. The report contains:

- Source and Evidence Revision identity;
- original content and Artifact hashes;
- policy identity and canonical policy hash;
- applicability and terminal status;
- component values, signal coverage, score, and stable reason codes;
- normalized query identities and bounded result observations;
- Provider identity, attempt count, and completion time; and
- the identity of any accepted member override.

A separate override record captures member identity, reviewed report identity, decision time, and an optional bounded note. Changing Source bytes, active Evidence Revision, or policy identity invalidates the old override for future projection. Historical Runs keep their already-pinned Source and Evidence identities; a later policy change does not rewrite past citations.

The score is never authoritative in Qdrant. Qdrant payloads may carry report identity or admission class for diagnostics, but every retrieval hit continues to reload and authorize PostgreSQL Evidence.

## API and interface

Source list and viewer responses expose a compact Admission summary:

- status, score when applicable, and signal coverage;
- policy version;
- short stable reason codes mapped to localized explanations; and
- whether admission was automatic, not applicable, or member-overridden.

The Source list uses explanation-first labels such as:

```text
Source admission: Review required
✓ Content extracted completely
✓ Original public page found
⚠ No stable identifier found
⚠ External identity evidence is insufficient
```

The detail view shows the exact deterministic queries and retained result identities without presenting snippets as evidence. Members with `source.maintain` may approve or reject a review-required Source. Stale clients receive a conflict if the report, Source hash, Evidence Revision, or lifecycle state changed.

Answers and citations do not gain a Source Admission score in v1. This prevents members from reading an ingestion score as proof that a generated answer is correct. A later product decision may show a neutral Source-quality label in the Citation viewer, but it is not part of this design.

## Failure and retry semantics

- Search timeout, rate limiting, or Provider unavailability uses bounded retry. Exhaustion produces `review_required` with `external_verification_unavailable`; it does not lower the score or declare the Source unreliable.
- Zero useful results produces insufficient signal coverage and therefore review, not rejection.
- Malformed or oversized Provider results are discarded under stable reason codes.
- Duplicate queries and results are idempotently collapsed.
- A repeated attempt with the same Source hash, Evidence Revision, policy, and normalized observations produces the same report identity and decision.
- Cancellation, deletion, permission loss, or lease loss prevents publication of a new report or projection.
- Member approval is transactional: the override and resumed processing authority commit together, or neither takes effect.
- No Admission failure can cause Search Provider text to enter Source Evidence or model context.

## Rollout

The feature ships in two stages.

### Shadow mode

Public Sources receive reports, but technically valid Sources continue to indexing regardless of score. The UI may expose the report as experimental. Nano records score distribution, signal coverage, query count, Provider failure, latency, and the eventual member assessment on a bounded sample.

### Enforcement mode

Enforcement may be enabled only after the accepted fixture suite and sampled human review establish tolerable false-admit and false-review rates. In enforcement, `passed` auto-indexes and `review_required` pauses before Qdrant projection. Returning to shadow mode changes future admission behavior only; it never rewrites historical reports.

## Evaluation and tests

Deterministic unit tests cover:

- query construction and maximum-query enforcement;
- canonical URL, registrable-domain, stable-identifier, and duplicate collapse;
- component computation, missing-signal coverage, threshold boundaries, and canonical policy hashing;
- reason-code stability and idempotent report identity; and
- private-upload `not_applicable` behavior.

Integration tests cover:

- public Source pass through qualifying, indexing, projection verification, and ready;
- insufficient coverage and below-threshold review without a Qdrant build;
- authorized approval resuming exactly the pinned Evidence Revision;
- stale or unauthorized approval failing without state change;
- Provider timeout degrading to review rather than Source failure;
- content or policy identity changes invalidating a prior override;
- private uploads indexing without Web Search; and
- cancellation, deletion, retry, and lease recovery at the qualifying boundary.

The frozen calibration suite includes original publisher pages, stable-identifier documents, duplicate mirrors, syndication, ambiguous titles, stale but intentionally historical material, login/error pages, partial extractions, zero-result niche material, and private uploads. It reports false automatic admission, false review, hard-failure accuracy, signal-coverage distribution, queries per Source, p50/p95 qualifying latency, and Provider cost. No threshold is promoted from aggregate accuracy alone: false automatic admission is the higher-cost error.

## Accepted limitations

- A passed Source can still contain false or biased statements.
- Search results can reproduce the same upstream mistake and are not independent factual witnesses.
- Distinct domains can still be mirrors, syndication partners, or consumers of the same upstream publication; v1 does not attempt to infer editorial independence.
- Niche or newly published public material will often require manual review.
- Declared author, publisher, and date metadata can be dishonest.
- The score is policy-specific and cannot be compared across future policy versions without recalibration.
- No answer-level guarantee is added; current Source-level citation allowlisting remains unchanged.

These limitations are intentional. The first release improves Source traceability and extraction quality without introducing Claim-scale model work or answer latency.
