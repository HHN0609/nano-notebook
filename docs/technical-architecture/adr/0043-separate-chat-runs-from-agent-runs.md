# Separate Chat Runs from Agent Runs

A Chat Run will own the Member-visible lifecycle of one requested Chat answer and its input/output Message relationship. A product-neutral Agent Run will represent one durable invocation of an immutable Agent Definition and carry its Executor binding, Job, Checkpoints, Trace, Agent Tree membership, and Delegation state. Each Chat Run references one root Agent Run; child Runs exist only in the Agent runtime layer.

This replaces the current assumption that every Agent Run is itself a Chat answer. We rejected keeping nullable Chat fields on generic runtime records because it would force every future product caller to imitate a Chat. We also rejected introducing speculative product-run or Output tables: Sprint 10 has one real caller to separate, so `chat_runs` is sufficient and future products can define their own ownership records when designed.

The existing `agent_runs` identity is generalized in place rather than renamed to `agent_executions`. `agent_trees` owns the shared absolute deadline and logical budgets for a root and its child. Generic Delegations relate Agent Runs without Role columns. Jobs, Checkpoints, Trace, and Run evidence retain Agent Run foreign keys.

Migration is expand, backfill, cutover, drain, and contract. Existing public Chat identifiers and API behavior remain stable. Transitional Chat and legacy dispatch columns may be read only by compatibility code for existing Runs but receive no new-path writes. They are removed only after durable state proves no non-terminal legacy Run depends on them; historical records need not be rewritten merely to adopt new terminology.

`Agent Definition` names reusable configuration, `Agent Run` names one durable invocation, `Agent Attempt` names one leased effort, and `Agent Job` names its Worker delivery record. We rejected `Agent Execution Run` because “Execution” duplicates the meaning of “Run” without clarifying Definition versus instance.

An Agent Run's durable memory is its normalized Checkpoint stream, not a process-local conversation or Provider thread. Every Model Call reconstructs bounded Provider-neutral context from pinned configuration, authorized product input, and accepted Checkpoints. Required state that cannot fit its budget fails closed rather than being silently truncated.
