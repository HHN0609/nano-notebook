package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type TodoActionStateLoader interface {
	LoadTodoActionState(context.Context, Attempt, string) (string, time.Time, TodoSnapshot, bool, error)
}

type PlanMutationPolicy interface {
	IsPlanMutation() bool
}

type rewriteTodoListAction struct{ loader TodoActionStateLoader }
type updateTodoStatusAction struct{ loader TodoActionStateLoader }

type rewriteTodoListInput struct {
	Items []string `json:"items"`
}

type updateTodoStatusInput struct {
	Revision int          `json:"revision"`
	Updates  []TodoUpdate `json:"updates"`
}

func NewRewriteTodoListAction(loader TodoActionStateLoader) Action {
	return &rewriteTodoListAction{loader: loader}
}

func NewUpdateTodoStatusAction(loader TodoActionStateLoader) Action {
	return &updateTodoStatusAction{loader: loader}
}

func (*rewriteTodoListAction) CrashReplaySafe() bool  { return true }
func (*updateTodoStatusAction) CrashReplaySafe() bool { return true }
func (*rewriteTodoListAction) IsPlanMutation() bool   { return true }
func (*updateTodoStatusAction) IsPlanMutation() bool  { return true }

func (*rewriteTodoListAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "rewrite_todo_list",
		Description: "Create or replace the unfinished TODO plan for the current user request while preserving terminal history.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","minItems":1,"maxItems":20,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":500}}}}`),
	}
}

func (*updateTodoStatusAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "update_todo_status",
		Description: "Atomically update one or more TODO item statuses using the current snapshot revision.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["revision","updates"],"properties":{"revision":{"type":"integer","minimum":1},"updates":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","additionalProperties":false,"required":["id","status"],"properties":{"id":{"type":"string","pattern":"^todo_[1-9][0-9]*$"},"status":{"type":"string","enum":["pending","in_progress","completed","cancelled"]}}}}}}`),
	}
}

func (*rewriteTodoListAction) ValidateInput(raw json.RawMessage) error {
	var input rewriteTodoListInput
	if err := decodeExactJSON(raw, &input); err != nil || len(input.Items) < 1 || len(input.Items) > 20 {
		return errors.New("invalid rewrite_todo_list input")
	}
	return nil
}

func (*updateTodoStatusAction) ValidateInput(raw json.RawMessage) error {
	var input updateTodoStatusInput
	if err := decodeExactJSON(raw, &input); err != nil || input.Revision < 1 || len(input.Updates) < 1 || len(input.Updates) > 20 {
		return errors.New("invalid update_todo_status input")
	}
	for _, update := range input.Updates {
		if !strings.HasPrefix(update.ID, "todo_") || !validTodoStatus(update.Status) {
			return errors.New("invalid update_todo_status input")
		}
	}
	return nil
}

func (a *rewriteTodoListAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	var input rewriteTodoListInput
	if err := decodeExactJSON(request.Input, &input); err != nil {
		return ActionResult{}, errors.New("invalid rewrite_todo_list input")
	}
	inputMessageID, proposedAt, previous, exists, err := a.load(ctx, request)
	if err != nil {
		return ActionResult{}, err
	}
	next, err := RewriteTodoList(previous, exists, inputMessageID, input.Items, proposedAt)
	if err != nil {
		return todoActionDomainError(todoMutationCode(err)), nil
	}
	return todoSnapshotResult(next)
}

func (a *updateTodoStatusAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	var input updateTodoStatusInput
	if err := decodeExactJSON(request.Input, &input); err != nil {
		return ActionResult{}, errors.New("invalid update_todo_status input")
	}
	_, proposedAt, previous, exists, err := a.load(ctx, request)
	if err != nil {
		return ActionResult{}, err
	}
	if !exists {
		return todoActionDomainError("todo_list_missing"), nil
	}
	next, err := UpdateTodoStatuses(previous, input.Revision, input.Updates, proposedAt)
	if err != nil {
		return todoActionDomainError(todoMutationCode(err)), nil
	}
	return todoSnapshotResult(next)
}

func (a *rewriteTodoListAction) load(ctx context.Context, request ActionRequest) (string, time.Time, TodoSnapshot, bool, error) {
	if a == nil || a.loader == nil {
		return "", time.Time{}, TodoSnapshot{}, false, errors.New("TODO state loader is unavailable")
	}
	return a.loader.LoadTodoActionState(ctx, request.Attempt, request.ActionID)
}

func (a *updateTodoStatusAction) load(ctx context.Context, request ActionRequest) (string, time.Time, TodoSnapshot, bool, error) {
	if a == nil || a.loader == nil {
		return "", time.Time{}, TodoSnapshot{}, false, errors.New("TODO state loader is unavailable")
	}
	return a.loader.LoadTodoActionState(ctx, request.Attempt, request.ActionID)
}

func todoSnapshotResult(snapshot TodoSnapshot) (ActionResult, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func todoMutationCode(err error) string {
	var mutationErr *TodoMutationError
	if errors.As(err, &mutationErr) && actionCodePattern.MatchString(mutationErr.Code) {
		return mutationErr.Code
	}
	return "todo_mutation_failed"
}

func todoActionDomainError(code string) ActionResult {
	detail := ActionError{Kind: "domain", Code: code, Message: "The TODO update could not be applied.", Suggestion: "Read the current TODO List in Agent Status and choose a valid next update.", Retryable: true}
	switch code {
	case "todo_list_missing":
		detail.Message = "No TODO list exists for this user request."
		detail.Suggestion = "Call rewrite_todo_list before updating item status."
	case "todo_revision_conflict":
		detail.Message = "The TODO list revision is stale."
		detail.Suggestion = "Use the revision from the current Agent Status and retry once."
	case "todo_item_not_found":
		detail.Message = "The TODO item does not exist."
		detail.Suggestion = "Use an item ID from the current Agent Status."
	case "todo_multiple_in_progress":
		detail.Message = "The update would leave more than one TODO item in progress."
		detail.Suggestion = "Complete, cancel, or pause the current active item in the same update batch."
	case "todo_duplicate_item", "todo_invalid_status", "todo_invalid_updates", "todo_invalid_items":
		detail.Message = "The TODO mutation input is invalid."
		detail.Suggestion = "Use distinct item IDs, allowed statuses, and the documented size limits."
		detail.Retryable = false
	}
	return ActionResult{Status: ActionDomainError, Error: &detail}
}
