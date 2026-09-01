package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestTodoActionsAreOrderedReplaySafePlanMutations(t *testing.T) {
	loader := &todoActionLoaderStub{}
	for _, action := range []Action{NewRewriteTodoListAction(loader), NewUpdateTodoStatusAction(loader)} {
		if action.Definition().Name != "rewrite_todo_list" && action.Definition().Name != "update_todo_status" {
			t.Fatalf("unexpected action %q", action.Definition().Name)
		}
		if policy, ok := action.(CrashReplayPolicy); !ok || !policy.CrashReplaySafe() {
			t.Fatalf("%s is not crash-replay safe", action.Definition().Name)
		}
		if policy, ok := action.(PlanMutationPolicy); !ok || !policy.IsPlanMutation() {
			t.Fatalf("%s is not marked as a plan mutation", action.Definition().Name)
		}
	}
}

func TestRewriteTodoListActionUsesDurableProposalTimeAndPriorSnapshot(t *testing.T) {
	at := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	loader := &todoActionLoaderStub{inputMessageID: "msg_1", proposedAt: at}
	action := NewRewriteTodoListAction(loader)
	result, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:1/action:0", Input: json.RawMessage(`{"items":["inspect","implement"]}`),
		Attempt: Attempt{RunID: "run_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || result.Error != nil {
		t.Fatalf("result = %#v", result)
	}
	var snapshot TodoSnapshot
	if err := json.Unmarshal(result.Output, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.InputMessageID != "msg_1" || snapshot.Revision != 1 || snapshot.Items[0].ID != "todo_1" || !snapshot.Items[0].CreatedAt.Equal(at) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestUpdateTodoStatusActionReturnsSafeActionableDomainErrors(t *testing.T) {
	at := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	action := NewUpdateTodoStatusAction(&todoActionLoaderStub{inputMessageID: "msg_1", proposedAt: at})
	missing, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:1/action:0", Input: json.RawMessage(`{"revision":1,"updates":[{"id":"todo_1","status":"completed"}]}`),
		Attempt: Attempt{RunID: "run_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != ActionDomainError || missing.Error == nil || missing.Error.Code != "todo_list_missing" || missing.Error.Suggestion == "" {
		t.Fatalf("missing-list result = %#v", missing)
	}

	initial, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{"inspect"}, at)
	if err != nil {
		t.Fatal(err)
	}
	action = NewUpdateTodoStatusAction(&todoActionLoaderStub{inputMessageID: "msg_1", proposedAt: at.Add(time.Minute), snapshot: initial, exists: true})
	stale, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:2/action:0", Input: json.RawMessage(`{"revision":2,"updates":[{"id":"todo_1","status":"completed"}]}`),
		Attempt: Attempt{RunID: "run_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != ActionDomainError || stale.Error == nil || stale.Error.Code != "todo_revision_conflict" || !stale.Error.Retryable {
		t.Fatalf("stale result = %#v", stale)
	}
}

func TestTodoActionPropagatesStateLoaderFailureAsHarnessError(t *testing.T) {
	want := errors.New("database unavailable")
	action := NewRewriteTodoListAction(&todoActionLoaderStub{err: want})
	_, err := action.Execute(context.Background(), ActionRequest{ActionID: "decision:1/action:0", Input: json.RawMessage(`{"items":["inspect"]}`)})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

type todoActionLoaderStub struct {
	inputMessageID string
	proposedAt     time.Time
	snapshot       TodoSnapshot
	exists         bool
	err            error
}

func (s *todoActionLoaderStub) LoadTodoActionState(context.Context, Attempt, string) (string, time.Time, TodoSnapshot, bool, error) {
	return s.inputMessageID, s.proposedAt, cloneTodoSnapshot(s.snapshot), s.exists, s.err
}
