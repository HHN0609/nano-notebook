package agent

import (
	"context"
	"sync"
)

// BatchTask is one unit of work handed to a ToolBatchExecutor: an action's
// index within its proposal batch, plus a closure that performs whatever
// tracing, authority checks, and execution the Controller already does for
// a single action. ToolBatchExecutor has no knowledge of what Run does —
// it owns only the concurrency policy, not action semantics.
type BatchTask struct {
	Index int
	Run   func(ctx context.Context) (ActionResult, error)
}

// BatchOutcome is the result of running one BatchTask, tagged with the
// task's original Index so callers can correlate it back to a specific
// action regardless of completion order.
type BatchOutcome struct {
	Index  int
	Result ActionResult
	Err    error
}

// ToolBatchExecutor runs a proposal's parallel-eligible action tasks
// concurrently. Callers decide whether a batch is parallel-eligible in the
// first place: a batch containing even one ordered_sync (or unscheduled)
// action falls back to nano's existing one-action-at-a-time execution path
// unchanged, matching Pi's "one sequential tool forces the whole batch to
// serialize" semantics — this type is only ever invoked for batches where
// every remaining action is ToolParallel.
type ToolBatchExecutor struct{}

// RunParallel executes every task concurrently — goroutines with no
// semaphore or pool, mirroring Pi's Promise.all — and returns outcomes in
// the same order as tasks (not completion order). ActionBatchLimit already
// bounds how many actions can appear in one proposal, so no separate
// concurrency cap is needed here.
func (ToolBatchExecutor) RunParallel(ctx context.Context, tasks []BatchTask) []BatchOutcome {
	outcomes := make([]BatchOutcome, len(tasks))
	var wg sync.WaitGroup
	wg.Add(len(tasks))
	for i, task := range tasks {
		i, task := i, task
		go func() {
			defer wg.Done()
			result, err := task.Run(ctx)
			outcomes[i] = BatchOutcome{Index: task.Index, Result: result, Err: err}
		}()
	}
	wg.Wait()
	return outcomes
}
