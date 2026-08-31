package agent

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRewriteTodoListCreatesDeterministicSnapshot(t *testing.T) {
	at := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	got, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{
		" Inspect the current implementation ",
		"Implement the selected change",
		"Run focused verification",
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	want := TodoSnapshot{
		InputMessageID: "msg_1", Revision: 1, NextOrdinal: 4,
		Items: []TodoItem{
			{ID: "todo_1", Content: "Inspect the current implementation", Status: TodoPending, CreatedAt: at, UpdatedAt: at},
			{ID: "todo_2", Content: "Implement the selected change", Status: TodoPending, CreatedAt: at, UpdatedAt: at},
			{ID: "todo_3", Content: "Run focused verification", Status: TodoPending, CreatedAt: at, UpdatedAt: at},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
	again, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{
		"Inspect the current implementation", "Implement the selected change", "Run focused verification",
	}, at)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("deterministic replay = %#v, %v", again, err)
	}
}

func TestRewriteTodoListPreservesTerminalItemsAndCancelsUnfinished(t *testing.T) {
	created := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	changed := created.Add(2 * time.Minute)
	previous := TodoSnapshot{
		InputMessageID: "msg_1", Revision: 2, NextOrdinal: 5,
		Items: []TodoItem{
			{ID: "todo_1", Content: "done", Status: TodoCompleted, CreatedAt: created, UpdatedAt: created.Add(time.Minute)},
			{ID: "todo_2", Content: "active", Status: TodoInProgress, CreatedAt: created, UpdatedAt: created.Add(time.Minute)},
			{ID: "todo_3", Content: "waiting", Status: TodoPending, CreatedAt: created, UpdatedAt: created},
			{ID: "todo_4", Content: "old cancelled", Status: TodoCancelled, CreatedAt: created, UpdatedAt: created.Add(time.Minute)},
		},
	}
	got, err := RewriteTodoList(previous, true, "msg_1", []string{"new path"}, changed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 3 || got.NextOrdinal != 6 || len(got.Items) != 5 {
		t.Fatalf("snapshot metadata = revision %d next %d items %d", got.Revision, got.NextOrdinal, len(got.Items))
	}
	if got.Items[0] != previous.Items[0] || got.Items[3] != previous.Items[3] {
		t.Fatalf("terminal items changed: %#v", got.Items)
	}
	for _, index := range []int{1, 2} {
		if got.Items[index].Status != TodoCancelled || !got.Items[index].UpdatedAt.Equal(changed) {
			t.Fatalf("unfinished item %d = %#v", index, got.Items[index])
		}
	}
	if item := got.Items[4]; item.ID != "todo_5" || item.Content != "new path" || item.Status != TodoPending || !item.CreatedAt.Equal(changed) || !item.UpdatedAt.Equal(changed) {
		t.Fatalf("new item = %#v", item)
	}
}

func TestUpdateTodoStatusAtomicallyCompletesAndStarts(t *testing.T) {
	created := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	changed := created.Add(time.Minute)
	previous, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{"first", "second"}, created)
	if err != nil {
		t.Fatal(err)
	}
	started, err := UpdateTodoStatuses(previous, 1, []TodoUpdate{{ID: "todo_1", Status: TodoInProgress}}, changed)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := UpdateTodoStatuses(started, 2, []TodoUpdate{
		{ID: "todo_1", Status: TodoCompleted},
		{ID: "todo_2", Status: TodoInProgress},
	}, changed.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if finished.Revision != 3 || finished.Items[0].Status != TodoCompleted || finished.Items[1].Status != TodoInProgress {
		t.Fatalf("finished snapshot = %#v", finished)
	}
	if !finished.Items[0].UpdatedAt.Equal(changed.Add(time.Minute)) || !finished.Items[1].UpdatedAt.Equal(changed.Add(time.Minute)) {
		t.Fatalf("changed timestamps = %#v", finished.Items)
	}
}

func TestTodoMutationsRejectInvalidInputWithoutSuccessor(t *testing.T) {
	at := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	snapshot, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{"first", "second"}, at)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		fn   func() error
		code string
	}{
		{name: "stale revision", fn: func() error {
			_, err := UpdateTodoStatuses(snapshot, 0, []TodoUpdate{{ID: "todo_1", Status: TodoCompleted}}, at)
			return err
		}, code: "todo_revision_conflict"},
		{name: "duplicate id", fn: func() error {
			_, err := UpdateTodoStatuses(snapshot, 1, []TodoUpdate{{ID: "todo_1", Status: TodoCompleted}, {ID: "todo_1", Status: TodoCancelled}}, at)
			return err
		}, code: "todo_duplicate_item"},
		{name: "unknown id", fn: func() error {
			_, err := UpdateTodoStatuses(snapshot, 1, []TodoUpdate{{ID: "todo_9", Status: TodoCompleted}}, at)
			return err
		}, code: "todo_item_not_found"},
		{name: "multiple active", fn: func() error {
			_, err := UpdateTodoStatuses(snapshot, 1, []TodoUpdate{{ID: "todo_1", Status: TodoInProgress}, {ID: "todo_2", Status: TodoInProgress}}, at)
			return err
		}, code: "todo_multiple_in_progress"},
		{name: "duplicate rewrite", fn: func() error {
			_, err := RewriteTodoList(snapshot, true, "msg_1", []string{"same", " same "}, at)
			return err
		}, code: "todo_invalid_items"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mutationErr *TodoMutationError
			if err := test.fn(); !errors.As(err, &mutationErr) || mutationErr.Code != test.code {
				t.Fatalf("error = %v, want TodoMutationError %q", err, test.code)
			}
		})
	}
}
