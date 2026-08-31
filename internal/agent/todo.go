package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
	TodoCancelled  TodoStatus = "cancelled"
)

type TodoItem struct {
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	Status    TodoStatus `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type TodoSnapshot struct {
	InputMessageID string     `json:"input_message_id"`
	Revision       int        `json:"revision"`
	NextOrdinal    int        `json:"next_ordinal"`
	Items          []TodoItem `json:"items"`
}

type TodoUpdate struct {
	ID     string     `json:"id"`
	Status TodoStatus `json:"status"`
}

type TodoMutationError struct {
	Code string
}

func (e *TodoMutationError) Error() string { return e.Code }

func todoMutationError(code string) error { return &TodoMutationError{Code: code} }

func RewriteTodoList(previous TodoSnapshot, exists bool, inputMessageID string, items []string, at time.Time) (TodoSnapshot, error) {
	if strings.TrimSpace(inputMessageID) == "" || at.IsZero() || len(items) < 1 || len(items) > 20 {
		return TodoSnapshot{}, todoMutationError("todo_invalid_items")
	}
	normalized := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || utf8.RuneCountInString(item) > 500 {
			return TodoSnapshot{}, todoMutationError("todo_invalid_items")
		}
		if _, duplicate := seen[item]; duplicate {
			return TodoSnapshot{}, todoMutationError("todo_invalid_items")
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}

	next := TodoSnapshot{InputMessageID: inputMessageID, Revision: 1, NextOrdinal: 1, Items: make([]TodoItem, 0, len(normalized))}
	if exists {
		if err := validateTodoSnapshot(previous, inputMessageID); err != nil {
			return TodoSnapshot{}, err
		}
		next = cloneTodoSnapshot(previous)
		next.Revision++
		for index := range next.Items {
			if next.Items[index].Status == TodoPending || next.Items[index].Status == TodoInProgress {
				next.Items[index].Status = TodoCancelled
				next.Items[index].UpdatedAt = at
			}
		}
	}
	for _, content := range normalized {
		item := TodoItem{
			ID: fmt.Sprintf("todo_%d", next.NextOrdinal), Content: content, Status: TodoPending,
			CreatedAt: at, UpdatedAt: at,
		}
		next.Items = append(next.Items, item)
		next.NextOrdinal++
	}
	if err := validateTodoSnapshot(next, inputMessageID); err != nil {
		return TodoSnapshot{}, err
	}
	return next, nil
}

func UpdateTodoStatuses(previous TodoSnapshot, revision int, updates []TodoUpdate, at time.Time) (TodoSnapshot, error) {
	if at.IsZero() || len(updates) < 1 || len(updates) > 20 {
		return TodoSnapshot{}, todoMutationError("todo_invalid_updates")
	}
	if err := validateTodoSnapshot(previous, previous.InputMessageID); err != nil {
		return TodoSnapshot{}, err
	}
	if revision != previous.Revision {
		return TodoSnapshot{}, todoMutationError("todo_revision_conflict")
	}
	byID := make(map[string]int, len(previous.Items))
	for index, item := range previous.Items {
		byID[item.ID] = index
	}
	seen := make(map[string]struct{}, len(updates))
	next := cloneTodoSnapshot(previous)
	for _, update := range updates {
		if _, duplicate := seen[update.ID]; duplicate {
			return TodoSnapshot{}, todoMutationError("todo_duplicate_item")
		}
		seen[update.ID] = struct{}{}
		index, ok := byID[update.ID]
		if !ok {
			return TodoSnapshot{}, todoMutationError("todo_item_not_found")
		}
		if !validTodoStatus(update.Status) {
			return TodoSnapshot{}, todoMutationError("todo_invalid_status")
		}
		if next.Items[index].Status != update.Status {
			next.Items[index].Status = update.Status
			next.Items[index].UpdatedAt = at
		}
	}
	active := 0
	for _, item := range next.Items {
		if item.Status == TodoInProgress {
			active++
		}
	}
	if active > 1 {
		return TodoSnapshot{}, todoMutationError("todo_multiple_in_progress")
	}
	next.Revision++
	return next, nil
}

func validateTodoSnapshot(snapshot TodoSnapshot, inputMessageID string) error {
	if snapshot.InputMessageID == "" || snapshot.InputMessageID != inputMessageID || snapshot.Revision < 1 || snapshot.NextOrdinal < 1 || len(snapshot.Items) < 1 {
		return todoMutationError("todo_snapshot_invalid")
	}
	seen := make(map[string]struct{}, len(snapshot.Items))
	active := 0
	maxOrdinal := 0
	for _, item := range snapshot.Items {
		var ordinal int
		if _, err := fmt.Sscanf(item.ID, "todo_%d", &ordinal); err != nil || ordinal < 1 || ordinal >= snapshot.NextOrdinal || strings.TrimSpace(item.Content) == "" || utf8.RuneCountInString(item.Content) > 500 || !validTodoStatus(item.Status) || item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return todoMutationError("todo_snapshot_invalid")
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return todoMutationError("todo_snapshot_invalid")
		}
		seen[item.ID] = struct{}{}
		if ordinal > maxOrdinal {
			maxOrdinal = ordinal
		}
		if item.Status == TodoInProgress {
			active++
		}
	}
	if active > 1 || snapshot.NextOrdinal <= maxOrdinal {
		return todoMutationError("todo_snapshot_invalid")
	}
	return nil
}

func validTodoStatus(status TodoStatus) bool {
	return status == TodoPending || status == TodoInProgress || status == TodoCompleted || status == TodoCancelled
}

func cloneTodoSnapshot(snapshot TodoSnapshot) TodoSnapshot {
	cloned := snapshot
	cloned.Items = append([]TodoItem(nil), snapshot.Items...)
	return cloned
}
