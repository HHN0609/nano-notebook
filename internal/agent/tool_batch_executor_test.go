package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestToolBatchExecutorRunParallelRunsTasksConcurrently(t *testing.T) {
	const delay = 80 * time.Millisecond
	tasks := make([]BatchTask, 4)
	for i := range tasks {
		i := i
		tasks[i] = BatchTask{Index: i, Run: func(context.Context) (ActionResult, error) {
			time.Sleep(delay)
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{}`)}, nil
		}}
	}
	started := time.Now()
	outcomes := ToolBatchExecutor{}.RunParallel(context.Background(), tasks)
	elapsed := time.Since(started)

	if len(outcomes) != 4 {
		t.Fatalf("outcomes=%+v", outcomes)
	}
	for _, outcome := range outcomes {
		if outcome.Err != nil || outcome.Result.Status != ActionSucceeded {
			t.Fatalf("outcome=%+v", outcome)
		}
	}
	// Serial execution of four delay-sleeping tasks would take at least
	// 4*delay; concurrent execution takes roughly delay.
	if elapsed >= 2*delay {
		t.Fatalf("tasks ran serially: elapsed=%s delay=%s", elapsed, delay)
	}
}

func TestSearchEvidenceAndDiscoverSourcesCanRunInTheSameParallelBatch(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	tasks := make([]BatchTask, 0, 2)
	for index, name := range []string{"search_evidence", "discover_sources"} {
		index, name := index, name
		tasks = append(tasks, BatchTask{Index: index, Run: func(context.Context) (ActionResult, error) {
			started <- name
			<-release
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{}`)}, nil
		}})
	}
	done := make(chan []BatchOutcome, 1)
	go func() { done <- (ToolBatchExecutor{}).RunParallel(context.Background(), tasks) }()

	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("search_evidence and discover_sources did not overlap")
		}
	}
	close(release)
	if outcomes := <-done; len(outcomes) != 2 || !seen["search_evidence"] || !seen["discover_sources"] {
		t.Fatalf("seen=%v outcomes=%+v", seen, outcomes)
	}
}

func TestToolBatchExecutorRunParallelPreservesOutcomeOrderRegardlessOfCompletionOrder(t *testing.T) {
	tasks := []BatchTask{
		{Index: 0, Run: func(context.Context) (ActionResult, error) {
			time.Sleep(60 * time.Millisecond) // slowest — finishes last
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"who":"zero"}`)}, nil
		}},
		{Index: 1, Run: func(context.Context) (ActionResult, error) {
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"who":"one"}`)}, nil
		}},
		{Index: 2, Run: func(context.Context) (ActionResult, error) {
			time.Sleep(20 * time.Millisecond)
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"who":"two"}`)}, nil
		}},
	}
	outcomes := ToolBatchExecutor{}.RunParallel(context.Background(), tasks)
	if len(outcomes) != 3 {
		t.Fatalf("outcomes=%+v", outcomes)
	}
	want := []string{`{"who":"zero"}`, `{"who":"one"}`, `{"who":"two"}`}
	for i, outcome := range outcomes {
		if outcome.Index != i || outcome.Err != nil || string(outcome.Result.Output) != want[i] {
			t.Fatalf("outcome[%d]=%+v", i, outcome)
		}
	}
}

func TestToolBatchExecutorRunParallelReportsPartialFailure(t *testing.T) {
	failure := errors.New("infrastructure failure")
	tasks := []BatchTask{
		{Index: 0, Run: func(context.Context) (ActionResult, error) {
			return ActionResult{}, failure
		}},
		{Index: 1, Run: func(context.Context) (ActionResult, error) {
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"value":"1"}`)}, nil
		}},
		{Index: 2, Run: func(context.Context) (ActionResult, error) {
			return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"value":"2"}`)}, nil
		}},
	}
	outcomes := ToolBatchExecutor{}.RunParallel(context.Background(), tasks)
	if len(outcomes) != 3 {
		t.Fatalf("outcomes=%+v", outcomes)
	}
	if !errors.Is(outcomes[0].Err, failure) {
		t.Fatalf("outcome[0]=%+v", outcomes[0])
	}
	if outcomes[1].Err != nil || outcomes[1].Result.Status != ActionSucceeded || outcomes[2].Err != nil || outcomes[2].Result.Status != ActionSucceeded {
		t.Fatalf("siblings of a failed task must still report their own success: outcomes=%+v", outcomes)
	}
}

func TestToolBatchExecutorRunParallelHandlesEmptyBatch(t *testing.T) {
	outcomes := ToolBatchExecutor{}.RunParallel(context.Background(), nil)
	if len(outcomes) != 0 {
		t.Fatalf("outcomes=%+v", outcomes)
	}
}
