# Chat and Research Mode

## Status

- Conversation design approved: 2026-08-22
- Written-spec review approved: 2026-08-22
- Scope: end-to-end Research Mode infrastructure, UI, live acceptance, and delivery

## Problem

Nano currently has one interactive Chat root and a bounded Source Discovery
child. The Chat root is intentionally short-lived: `chat.leader@3` allows five
model calls and eight Actions. The Source Discovery child performs one planning
call and one `web_search`. Neither is a long-horizon research harness capable of
planning an investigation, reading many URLs, iterating on evidence gaps, and
publishing a durable report.

The existing Chat compaction is also the wrong projection for this workload.
It correctly preserves a complete Agent Step as the smallest legal cut unit,
but old steps are flattened into one summarizer request, every Action Result is
temporarily truncated to 2,000 characters, and the prefix becomes one coarse
summary. A research run may contain dozens or hundreds of model decisions and
large `read_url` results. Its projection must preserve source identity, tool
outcomes, findings, contradictions, and incomplete work at step granularity.

Nano needs two user-selectable modes in the same Chat:

- **Chat** remains the current low-latency conversational path.
- **Research** confirms a plan, runs a durable long-horizon investigation, and
  publishes a cited, versioned Report Artifact.

This work must build the infrastructure and then prove it with a real research
task. The product must not hard-code a universal minimum URL count, but live
acceptance for an Agent Harness research task must demonstrate broad source
discovery, including at least 30 unique URLs and current primary material for
the relevant DeepSeek, Claude, Codex, and other harness implementations found
during the run.

## Goals

1. Let a Member choose Chat or Research for each submitted message in the same
   Chat.
2. Show and allow editing of a Research Plan before execution starts.
3. Add a dedicated `research.root` definition with versioned prompts, model
   policy, context policy, Skill allowlist, tools, and long-horizon limits.
4. Add a trusted, immutable Skill Catalog with progressive disclosure and ship
   `skill.grill-me@1`.
5. Wrap the newly added Web Reader service as a model-callable `read_url` Tool.
6. Support durable multi-round use of `web_search`, `read_url`, Notebook
   evidence, and Skills.
7. Add Research-specific per-step capsules and rolling synthesis without
   changing Chat compaction behavior.
8. Maintain deterministic evidence provenance from URL discovery through
   report citations.
9. Publish an independently openable and versioned Research Report Artifact.
10. Recover from Worker, provider, tool, compaction, and publication failures
    from accepted checkpoints without restarting the investigation.
11. Add UI, integration coverage, and a live end-to-end quality acceptance run.

## Non-goals

- A general multi-Agent research orchestrator in the first release.
- A universal numeric source minimum enforced by the Controller.
- Arbitrary local-path, remote, or user-uploaded Skills.
- Persisting or exposing hidden chain-of-thought.
- Replacing the existing Source ingestion, RAG, or Studio Output products.
- Rewriting or deleting Chat Messages, Run Checkpoints, or raw tool results
  after compaction.
- Measuring research quality with a production promotion gate in this change.
  The first release uses tests, telemetry, and one live acceptance report.

## Core authority model

The product has distinct facts and projections:

| Concern | Authority |
| --- | --- |
| Shared user-visible conversation | `chat_messages` |
| Research request and lifecycle | Research Session records |
| Accepted plan | Accepted Research Plan Version |
| Model decisions, Action calls, and Action results | Agent Run checkpoints |
| URL fetch metadata and report citation eligibility | Evidence Ledger derived from accepted checkpoints |
| Model-visible compressed research history | Step Capsules and Research Rollups |
| Published deliverable | Research Report Version |
| Operational diagnosis and replay | Trace and encrypted Replay |

Step Capsules, Rollups, Trace, and Replay never replace checkpoints. They are
derived or operational views. A Worker can rebuild all Research progress from
the accepted plan and checkpoint prefix.

## Architecture

### Separate roots, shared Chat

`nano.default` gains a `research` root alongside `chat`.

```text
Chat message with mode=chat
  -> existing chat.leader root
  -> existing Chat context and compaction

Chat message with mode=research
  -> Research Session
  -> research.plan Agent Run
  -> editable Plan Version
  -> Member confirmation
  -> research.root Agent Run
  -> checkpoints + Evidence Ledger + Research Compaction
  -> Research Report Version
  -> report summary and link in shared Chat
```

Research execution history is Run-scoped. The shared Chat receives the initial
request, plan state, terminal summary, and Report reference, not hundreds of
internal Research Steps. A later Chat turn may use the published report summary
without inheriting the entire trajectory. A later Research turn can explicitly
pin a prior Report and its evidence lineage.

### Research lifecycle

A Research Session has these states:

```text
planning
  -> awaiting_confirmation
  -> queued
  -> running
  -> publishing
  -> completed
```

Terminal exceptional states are `failed` and `cancelled`. Tool failures are not
Session failures; they are accepted Action Results and normal inputs to the next
decision.

The planning Run produces a structured plan. Natural-language edits create a
new immutable Plan Version. Starting research atomically pins one accepted Plan
Version, the `research.root` definition, model policy, context policy, prompt
versions, Skill versions, and deadline into the root Run manifest.

The Plan contract contains:

- objective and intended audience;
- scope, exclusions, time boundary, and named subjects;
- research questions and expected report sections;
- source families and verification strategy;
- completion criteria and known constraints.

The Planner may ask concise questions only when missing information would
materially change the investigation. Once the Member confirms the plan,
execution does not return to the Member for ordinary research ambiguity or
external-service failures.

Planning has no Web-evidence authority. Its output is a proposal for Member
review, and the confirmed or edited Plan Version is authoritative for execution.
Prompts discourage invented exact repositories, URLs, paths, and metrics, but
the first release does not attempt brittle semantic validation of every planning
claim; the plan confirmation/edit surface is the product boundary.

## Agent definition and budgets

`research.root@1` is a configured root, not a mode flag inside `chat.leader`.
Its catalog entry pins:

- Research Executor and Reporter prompts;
- a Research model and model-context policy;
- allowed tools and Skills;
- model-call, Action, batch, context-byte, result-byte, Attempt, and absolute
  deadline limits;
- enough headroom for long search, reading, drafting, review, and assembly loops.

Initial limits must permit at least a hundred sequential decisions and a larger
Action budget. Exact numbers live in the versioned catalog/policy JSON, not in
Controller branches. Planner, executor, compactor, and reporter model calls all
consume explicit tree-level budgets. Completion is model-led: the Controller
does not impose a source-count or dedicated-second-Final gate. At a true budget
boundary, the existing tool-closure path asks for the best supported deliverable
possible from accepted progress.

## Prompt architecture

Research uses separate, immutable prompt contracts:

### `agent.research-planner@1`

Produces either concise clarification questions or a structured Research Plan.
It covers the objective, scope, time boundary, focal subjects, exclusions,
source families, verification strategy, report outline, and completion
criteria. It supports natural-language plan revision before acceptance.

### `agent.research-executor@1`

Directs the iterative loop:

```text
inspect plan and coverage
  -> search
  -> read primary material
  -> extract and compare evidence
  -> identify contradictions and gaps
  -> search or read again
  -> report only when coverage is adequate or the hard budget requires it
```

The prompt requires source diversity appropriate to the task, prioritizes
primary sources and real code, distinguishes search snippets from read
evidence, and instructs the model to route around local failures. It does not
contain a universal URL counter or topic-specific list.

### `agent.research-step-compactor@1`

Produces structured, independently addressable capsules for complete Agent
Steps. It preserves public reasoning summaries and tool-derived facts, not
hidden chain-of-thought.

### `agent.research-rollup@1`

Maintains plan coverage, claim-to-evidence relationships, contradictions,
discarded directions, unresolved gaps, and next work across a contiguous range
of Step Capsules.

### `agent.research-reporter@1`

Produces the versioned Report contract from the accepted plan, eligible
evidence, Rollup, Capsules, and exact suffix. It writes a decision artifact,
not a research transcript: conclusions, tradeoffs, risks, and actions determine
the structure. It separates fact from inference, describes only limitations
that change decision confidence, and never cites a URL that was only returned
by search. URL counts, cross-validation mechanics, checkpoint progress, and
other operational telemetry remain code-owned metadata instead of mandatory
report sections.

Later immutable Reporter versions tighten the same contract: a plain source
title is not a citation, every material product-specific observation must link
to a successfully read Ledger URL or be labelled as an evidence gap, and any
discovered-only or failed URL is downgraded to plain text before publication.
The downgrade is recorded in Report evidence statistics and never discards an
otherwise complete Report.

Research does not require a dedicated second reporting decision. A substantive
Final may be accepted directly for backward compatibility, while a bare
completion token such as `Final` or `done` is accepted only when a checkpointed
assembled `report.md` exists. In that case the runtime publishes the assembled
artifact rather than the token. Completion criteria guide the model's judgment
and coverage telemetry, but never become a numeric publication gate.

Duplicate Action recovery does not declare the investigation complete. It
removes the raw recent Step suffix that can cause parameter-copying, retains
Capsules and the Evidence Ledger, keeps the full allowed tool set, highlights
fresh unread sources, and lets the Agent choose between further evidence work
and workspace drafting. Only the Agent's completion judgment or a true catalog
budget boundary closes tools.

## Skill infrastructure

Product Skills live under `internal/skillcatalog`, not the removed top-level
project tooling directory. Each Skill is immutable and contains:

```text
identity + version + name + description + body + sha256
```

Definitions allowlist Skill references. Admission pins exact references and
hashes. The initial system prompt exposes only each allowed Skill's name,
description, and trigger guidance. The model calls `read_skill` to retrieve the
full body; that Tool Result is checkpointed and becomes part of the append-only
context.

`read_skill` accepts only an allowed embedded identity and version. It cannot
read files or arbitrary paths.

`skill.grill-me@1` helps the Planner identify decisions that materially affect scope
or the report. It is optional: explicit Member intent or genuine plan ambiguity
may trigger it; clear tasks proceed directly to a plan. It must not create an
unbounded interview.

Core Research behavior remains in the mandatory Executor prompt. Optional Skill
disclosure cannot be the only mechanism enforcing the research workflow.

## Tool plane

The first Research root exposes:

- `web_search`
- `read_url`
- `search_evidence`
- `read_skill`
- `write_research_file`
- `read_research_file`
- `list_research_files`
- `assemble_research_report`

### `read_url`

`read_url` is a thin Agent Action around the newly merged
`internal/webreader.Adapter`. It does not introduce another fetch or extraction
stack.

The Tool input contains a URL. Output format and maximum characters are pinned
by the Research Tool policy rather than selected without bounds by the model.
The model-visible result includes:

- requested and final URL;
- title;
- cleaned Markdown content;
- selected reader engine;
- word count;
- truncation status.

The Web Reader owns public-destination validation, redirects, bounded fetching,
Readability cleanup, and optional browser upgrade for JavaScript shells. The
Action owns tool authorization, result-byte enforcement, checkpoint encoding,
Trace attributes, and Evidence Ledger projection.

`web_search` remains ordered and provider-rate-limited. Independent
`read_url` calls are read-only and crash-replay-safe; the scheduler may run an
eligible batch concurrently within configured Web Reader capacity.

### Research workspace

The report workspace follows the planning-with-files pattern without exposing a
host filesystem or shell. The model can write only `report_plan.md`,
`review.md`, `notes/<slug>.md`, and `sections/<slug>.md`. It may list and read
the latest checkpoint-accepted logical versions, then assemble an ordered set of
section files into `report.md`; direct writes to `report.md` are rejected.

Content is stored as immutable MinIO objects under the Run and a deterministic
Action identity. Checkpoint results retain logical path, object key, content
hash, and byte size. On replay, the same Action addresses the same object, while
the latest accepted checkpoint prefix—not a mutable MinIO listing—decides which
logical version is current. Reads verify stored size and SHA before returning
content. Assembly is deterministic and reports whether an accepted `review.md`
exists, but review and revision remain prompt-guided rather than Controller
gates.

## Evidence Ledger

The Ledger is deterministically projected from accepted tool results. A URL is
eligible for a Report citation only after a successful `read_url` result or an
equivalent accepted Notebook evidence result.

Each Web entry records:

- requested URL, canonical/final URL, and title;
- discovering query and Search Action, when applicable;
- Run ID, Decision number, Action ID, and Step identity;
- reader engine, word count, and truncation state;
- stable content/result hash;
- citation identity and Report eligibility;
- failure status when reading did not succeed.

Coverage telemetry reports discovered URLs, successfully read URLs, unique
domains, source-family classification, official/primary-source counts,
repository/code coverage, report citations, and unresolved plan items. These
are observations, not a universal completion gate.

## Research context and compaction

### Exact projection

Raw checkpoints are projected into the same causally valid unit used by Chat:
one Model Decision, its complete Action batch, and every sibling result form one
indivisible Agent Step. Incomplete batches are resumed before another model
request.

### Level 1: Step Capsules

When estimated input crosses the Research compaction trigger, the runtime
selects complete old Steps before an exact-suffix cut. Each selected Step gets
one structured capsule keyed by Run ID, Decision number, source Step hash,
compactor prompt/model versions, and policy hash.

A capsule contains:

- Step intent and public decision summary;
- each tool name and important input, including queries and URLs;
- success, failure, timeout, or interruption status;
- Evidence references and important findings;
- supported and challenged claims;
- limitations, unresolved questions, and next work.

Serialization is tool-aware rather than a fixed character prefix:

- Search results preserve queries and candidate metadata.
- Web Reader Markdown is segmented by headings and paragraph boundaries before
  synthesis when one result cannot fit safely.
- Structured JSON preserves relevant fields.
- Error results preserve stable kind and impact.

Multiple Steps may be compacted in one bounded model call, but the response is
still a list of independently stored capsules. A failed call accepts no capsule
or cut boundary.

### Level 2: Research Rollups

When Capsules themselves exceed a second policy threshold, a contiguous range
is summarized into an append-only Rollup. Capsules remain durable and
addressable. The Rollup maintains:

- accepted Plan status;
- important conclusions with Evidence references;
- source and URL coverage;
- conflicting findings;
- discarded approaches and reasons;
- remaining gaps and best next Actions.

The model request becomes:

```text
Research system prompt
+ accepted plan
+ disclosed Skills
+ latest Rollup
+ post-Rollup Step Capsules
+ exact recent Agent Steps
+ compact Evidence/Coverage Ledger
+ available Tool definitions
```

Research compaction is scoped by root Run ID. It never shares Chat's
`chat_id`-scoped compaction chain. Every accepted Rollup identifies its
predecessor, covered Step range, exact-suffix start, prompt/model/policy hashes,
and before/after token counts. Concurrent Workers converge through a
deterministic idempotency identity and transactional serialization.

Provider overflow may trigger one compact-and-retry cycle. The cut never splits
an Action call/result pair or sibling results. If compaction cannot produce a
safe request, the runtime retains the last valid projection and returns a stable
context-budget failure rather than corrupting history.

## Recovery and failure semantics

Recovery uses checkpoints, not UI state, Trace, or model memory.

On every Controller iteration the Worker reloads the accepted prefix:

1. If a Report Final checkpoint exists, publish it idempotently.
2. If a proposal has incomplete Actions, execute the first incomplete Action or
   an eligible parallel batch.
3. Only after every result exists may the model make the next decision.

`web_search`, `read_url`, `search_evidence`, and `read_skill` are read-only and
crash-replay-safe. Workspace writes and assembly are also crash-replay-safe
because their object identities are immutable and Action-derived. A Worker crash
after an external call but before its Result checkpoint may repeat that Action
with the same Action ID.

External failures do not normally fail the Run:

- a failed URL read becomes an explicit result;
- the Executor searches for an official mirror, repository source, alternate
  report, or other evidence;
- a failed source family leaves a coverage gap while other plan branches
  continue;
- bounded provider retries do not erase accepted progress;
- a provider timeout or malformed decision retries from the same checkpoint
  prefix within the Definition's Attempt and invalid-response limits;
- the model does not ask the Member to resolve ordinary tool failures after
  plan confirmation.

Research uses a separately versioned model-call timeout sized for long report
generation. It must exceed the observed successful Provider latency while
remaining below the enclosing Worker/Run deadline; changing it creates a new
Model Policy, Definition, and Release rather than mutating an admitted Run.

At a hard budget or deadline boundary, the runtime prioritizes a Report from all
accepted evidence. A genuinely incomplete area is disclosed in the Report, but
local service failure alone is not a reason to stop early. If publication
fails, a Report Final checkpoint and content hash make retry idempotent. The
ordinary user-triggered retry API intentionally does not restart a terminal
Research Run; recovery in this release is same-Run checkpoint recovery for
retryable Worker/provider/tool attempts.

## Report product

Research Reports are separate from Studio Outputs because they are tied to a
Chat request, dynamically discovered Web evidence, a confirmed Plan, and a
long-horizon root Run.

A Report has immutable versions. A completed version contains decision-focused
Markdown plus separately stored operational metadata:

- a structure chosen for the Member's decision rather than fixed boilerplate;
- accepted Plan Version and root Run identity;
- citation list linked to Evidence Ledger entries;
- creation model/prompt/configuration hashes;
- material limitations and unresolved gaps that affect the decision;
- content hash and publication timestamp.

During execution, large working drafts live in the Run-scoped MinIO workspace.
The model writes a plan and sections, reads them back, records review findings,
and may revise sections before deterministic assembly. `PrepareFinal` prefers
the accepted assembled artifact, with a substantive one-pass Final retained as
a compatibility fallback when the workspace was not used.

Coverage counts and citation eligibility are stored in Report metadata and UI
projections. The runtime may correct a factual count already written by the
model, but it does not inject an Evidence Ledger banner, method section,
cross-validation table, or source inventory into the Report body.

The Chat timeline shows a compact completion message and an action to open the
full report. A follow-up may request a revised Report, which creates a new
version rather than mutating the published one.

## API and UI

Message admission accepts a validated `mode` field. Missing mode remains
`chat` for backward compatibility. Research admission returns a Research
Session projection instead of immediately queuing the execution root.

The UI adds:

- a Chat/Research selector near the composer;
- a plan card with edit and Start Research actions;
- durable background progress showing current phase, completed plan items,
  discovered/read source counts, and stop control;
- reconnection through the existing SSE snapshot pattern;
- an inline Report summary and full Report view.

UI projection is never lifecycle authority. Refreshing or leaving the page does
not pause the Worker.

## Observability

Trace adds stable attributes/events for:

- mode and Research Session/Plan/Report identities;
- Skill index disclosure and `read_skill` calls;
- discovered/read URL counts and reader engine;
- Step Capsule and Rollup creation, covered ranges, token counts, and trigger;
- recovery attempts and repeated read-only Actions;
- Report completeness, citation count, and unresolved gaps.

Replay captures the pinned prompts, model request/decision, and tool payloads
under the existing encrypted custody policy. Trace and Replay failures follow
their existing recording policy and do not silently mutate product state.

## Security and limits

- `read_url` inherits Web Reader destination validation and content bounds.
- Skill reads are restricted to pinned embedded allowlists.
- Tool and Report payloads obey tree and per-result byte budgets.
- Report citations expose public URLs, never internal object keys or Replay
  payload references.
- Existing membership and RLS checks apply to Research Sessions and Reports.
- Prompt, Skill, Definition, and policy versions are immutable after
  registration.

## Testing

Implementation follows test-first development.

### Catalog and prompt tests

- Resolve the Research root and all pinned references.
- Reject missing, mutable, duplicate, or unauthorized Skill references.
- Verify Research limits are distinct from Chat limits.
- Validate all Planner, Capsule, Rollup, and Report contracts.

### Tool tests

- `read_url` maps the Web Reader response into an Action Result.
- It rejects invalid input, oversized results, unavailable service responses,
  and malformed sidecar contracts with stable tool errors.
- It is allowlisted only for Research and is crash-replay-safe.
- Independent eligible reads preserve stable proposal-order projection.

### Context tests

- Complete Steps remain atomic.
- Search and URL results use tool-aware serialization without the old 2,000
  character prefix behavior.
- Capsules remain independently keyed and Rollups cover contiguous ranges.
- Previous Rollups roll forward idempotently.
- Chat compaction output and policy remain unchanged.
- Provider overflow compacts and retries at most once.

### Recovery tests

- Restart after proposal, after one sibling result, during compaction, after
  Report Final, and during publication.
- Read-only Actions replay safely with the same Action ID.
- Tool failure remains visible and allows a later alternative Action.
- Budget exhaustion closes tools and requests the best supported Report from
  accepted progress.
- A Final containing any discovered-only citation is published with that link
  downgraded to plain text and the downgrade count recorded only in Report
  metadata, never as an operational note in the Report body.
- Workspace paths are bounded, immutable object hashes are verified, replay is
  idempotent, current versions derive from checkpoints, and assembly order is
  deterministic.
- A bare completion signal cannot become a five-character Report; it resolves
  to an accepted assembled artifact or remains an invalid model response.

### API and UI tests

- Per-message mode selection and backward-compatible Chat admission.
- Plan generation, edit, confirmation, idempotency, and authorization.
- SSE reconnection during a long Research Run.
- Report opening and immutable version publication.
- Existing Chat journeys remain unchanged.

### Live end-to-end acceptance

Run a real Agent Harness research task through the product stack. Acceptance is
evidence-based, not just a green test:

1. The Member can select Research, review the plan, and start it.
2. The Run performs multiple search/read/analyze rounds rather than one bounded
   discovery call.
3. Trace shows at least 30 unique discovered URLs for this acceptance topic and
   successful reading across multiple primary and independent source families.
4. Current DeepSeek-, Claude-, Codex-, and other relevant harness material is
   verified from official documentation, repositories, evaluation reports, and
   implementation code where available; the report does not assume that every
   named product has the same openness or architecture.
5. Every published Web citation maps to a successful `read_url` evidence entry.
6. The Report compares implementations and evaluations, synthesizes findings,
   calls out conflicts and gaps, and is useful without reading the raw trace.
7. A local URL or service failure is bypassed with alternate evidence rather
   than ending the Run.
8. Checkpoint inspection proves the Run can resume without repeating completed
   Steps.
9. Chat mode and its existing compaction regression suite remain green.
10. Checkpoints show section-by-section workspace writes, read-back, review, and
    deterministic assembly rather than a single oversized report response.

If the live Report is superficial, misses essential source families, contains
invalid citations, or stops after local tool failure, the feature is not
accepted. Missing tools and implementation bugs discovered by this run are in
scope and must be fixed before delivery.

## Delivery sequence

1. Register the Research Definition, prompts, contracts, policies, and Skill
   catalog.
2. Add `read_skill` and `read_url` through the MCP Tool Plane.
3. Add Research Session/Plan admission and execution lifecycle.
4. Add Step Capsules, Rollups, Research request projection, and recovery.
5. Add Report Version publication and citations.
6. Add UI and SSE projections.
7. Run focused, integration, Web, and full regression gates.
8. Execute the live Agent Harness research acceptance; fix gaps until accepted.
9. Commit each independently verifiable behavior atomically.
