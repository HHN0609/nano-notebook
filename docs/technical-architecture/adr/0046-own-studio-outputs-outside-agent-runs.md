# Own shared Studio Outputs outside Agent Runs

Sprint 11 introduces Report, Flashcards, Mind Map, and Data Table as durable shared Notebook Outputs. Each Output owns the Member-visible lifecycle, Notebook sharing boundary, selected-Source count, safe status, and validated artifact. It references exactly one configured root Agent Run and, after successful publication, one immutable Agent Result.

An Agent Run remains product-neutral execution state. It does not gain Notebook, Member, Output kind, Chat, artifact, or sharing columns. A Studio root is not represented as a fake Chat Run, Leader Role, or child Delegation. This keeps the Sprint 10 Definition/Executor model reusable while allowing Chat and Studio to retain different ownership, privacy, and publication rules.

The new `studio_outputs` product table owns authorization and display state. Current Notebook Members may read it; Editors and Owners may create or delete it. Admission receives an explicit ordered set of Ready Source IDs, and the existing Agent Run Evidence Set pins their exact Evidence and Retrieval identities. The Output stores the selected count, while the immutable Evidence Set remains the generation audit boundary.

Four immutable Definitions—`studio.report@1`, `studio.flashcards@1`, `studio.mind-map@1`, and `studio.data-table@1`—bind separate prompts and result contracts to one code-owned `studio_structured_output` Executor. The Executor may make two model calls and one `search_evidence` Action, may not create children, and may not publish Chat content. `nano.default@2` adds the four exact Studio roots while retaining Chat; `nano.default@1` is not changed.

A Studio-specific runtime adapter delegates generic Checkpoint, lease, Trace, and MCP authority behavior to the existing PostgreSQL runtime. It alone loads Studio product context, builds bounded model input, validates the Definition-specific JSON artifact, and atomically publishes Agent Result, Output, Run, Job, budget, and terminal Trace state. Failed or cancelled Outputs expose no partial artifact.

We rejected storing Outputs only as Agent Results because internal results do not own Notebook sharing, product lifecycle, list ordering, or safe presentation state. We rejected extending Chat Runs because Chats are private per Member while Studio Outputs are shared with all current Notebook Members. We also rejected adding a Studio Agent Role because new configured Runs dispatch by exact Definition and Executor identity; Role remains a legacy recovery field rather than a new authorization layer.

This decision extends ADR 0043's separation of Chat Runs from Agent Runs and ADR 0045's configured Definition model. It does not introduce a generic artifact system, workflow DAG, editable Output, or media rendering pipeline.
