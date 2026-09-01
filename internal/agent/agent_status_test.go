package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestObserveAgentStatusProjectsLatestTodoAndCanonicalToolCounts(t *testing.T) {
	firstAt := time.Date(2026, 8, 31, 7, 20, 1, 0, time.UTC)
	secondAt := firstAt.Add(time.Minute)
	initial, err := RewriteTodoList(TodoSnapshot{}, false, "msg_1", []string{"inspect", "implement"}, firstAt)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateTodoStatuses(initial, 1, []TodoUpdate{{ID: "todo_1", Status: TodoInProgress}}, secondAt)
	if err != nil {
		t.Fatal(err)
	}
	prefix1 := acceptedPrefixForStatus(t, []statusStep{
		{name: "rewrite_todo_list", input: `{"items":["inspect","implement"]}`, output: mustStatusJSON(t, initial)},
		{name: "read_url", input: `{"url":"https://example.com","purpose":"inspect"}`, output: `{"ok":true}`},
	})
	prefix2 := acceptedPrefixForStatus(t, []statusStep{
		{name: "read_url", input: `{"purpose":"inspect","url":"https://example.com"}`, output: `{"ok":true}`},
		{name: "update_todo_status", input: `{"revision":1,"updates":[{"id":"todo_1","status":"in_progress"}]}`, output: mustStatusJSON(t, updated)},
	})
	generated := time.Date(2026, 8, 31, 7, 24, 10, 0, time.UTC)
	observation, err := ObserveAgentStatus("msg_1", []CheckpointPrefix{prefix1, prefix2}, generated, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if observation.Todo == nil || observation.Todo.Revision != 2 || observation.Todo.Items[0].Status != TodoInProgress {
		t.Fatalf("TODO observation = %#v", observation.Todo)
	}
	if observation.ToolCalls["read_url"] != 2 || observation.ToolCalls["rewrite_todo_list"] != 1 || observation.ToolCalls["update_todo_status"] != 1 {
		t.Fatalf("tool calls = %#v", observation.ToolCalls)
	}
	if len(observation.ExactRepeats) != 1 || observation.ExactRepeats[0].Name != "read_url" || observation.ExactRepeats[0].Count != 2 {
		t.Fatalf("exact repeats = %#v", observation.ExactRepeats)
	}

	rendered, err := RenderAgentStatus(observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`<agent_status version="1">`,
		"Generated at: 2026-08-31T15:24:10+08:00",
		"Time zone: Asia/Shanghai",
		"TODO List (revision=2):",
		"- [todo_1] in_progress | inspect | created_at=2026-08-31T15:20:01+08:00 | updated_at=2026-08-31T15:21:01+08:00",
		"- read_url: 2",
		"- identical read_url input repeated: 2",
		`</agent_status>`,
	} {
		if !strings.Contains(rendered, fragment) {
			t.Fatalf("status missing %q:\n%s", fragment, rendered)
		}
	}
}

func TestRenderAgentStatusAlwaysProducesTimestampOnlyEnvelope(t *testing.T) {
	observation, err := ObserveAgentStatus("msg_1", nil, time.Date(2026, 8, 31, 7, 24, 10, 0, time.UTC), "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderAgentStatus(observation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "TODO List:") || strings.Contains(rendered, "Tool Calls:") || !strings.Contains(rendered, "Generated at: 2026-08-31T15:24:10+08:00") {
		t.Fatalf("timestamp-only status = %q", rendered)
	}
}

func TestFinalizeDecisionRequestAppendsAgentStatusAsFinalUserMessage(t *testing.T) {
	request := models.ModelRequest{Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: "system"},
		{Role: models.RoleUser, Content: "question"},
		{Role: models.RoleSystem, Content: "recovery"},
	}}
	FinalizeDecisionRequest(&request, "<agent_status version=\"1\">status</agent_status>")
	if len(request.Messages) != 4 || request.Messages[3].Role != models.RoleUser || !strings.HasPrefix(request.Messages[3].Content, "<agent_status") {
		t.Fatalf("finalized messages = %#v", request.Messages)
	}
}

func TestAttachAgentStatusTelemetryRecordsBoundedDerivedCountsIdempotently(t *testing.T) {
	observation := AgentStatusObservation{
		Todo: &TodoSnapshot{Revision: 7, Items: []TodoItem{
			{Status: TodoPending}, {Status: TodoInProgress}, {Status: TodoCompleted}, {Status: TodoCompleted}, {Status: TodoCancelled},
		}},
		ExactRepeats: []ToolInputRepeat{{Count: 2}, {Count: 4}},
	}
	request := models.ModelRequest{}
	rendered := `<agent_status version="1">状态</agent_status>`
	attachAgentStatusTelemetry(&request, observation, rendered)
	attachAgentStatusTelemetry(&request, observation, rendered)
	metadata := request.ContextTelemetry
	if !metadata.AgentStatusInjected || metadata.AgentStatusBytes != len([]byte(rendered)) || metadata.AgentStatusTokens < 1 ||
		metadata.TodoRevision != 7 || metadata.TodoPendingCount != 1 || metadata.TodoInProgressCount != 1 ||
		metadata.TodoCompletedCount != 2 || metadata.TodoCancelledCount != 1 || metadata.MaxToolInputRepeatCount != 4 {
		t.Fatalf("Agent Status telemetry = %+v", metadata)
	}
}

type statusStep struct {
	name   string
	input  string
	output string
}

func acceptedPrefixForStatus(t *testing.T, steps []statusStep) CheckpointPrefix {
	t.Helper()
	checkpoints := make([]Checkpoint, 0, len(steps)*2)
	for index, step := range steps {
		decision := index + 1
		proposal, err := NewProposalCheckpoint(decision, models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: step.name, Input: json.RawMessage(step.input)}}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := NewActionResultCheckpoint(decision, 0, "decision:"+itoa(decision)+"/action:0", ActionResult{Status: ActionSucceeded, Output: json.RawMessage(step.output)})
		if err != nil {
			t.Fatal(err)
		}
		checkpoints = append(checkpoints,
			Checkpoint{SequenceNo: len(checkpoints) + 1, PendingCheckpoint: proposal},
			Checkpoint{SequenceNo: len(checkpoints) + 2, PendingCheckpoint: result},
		)
	}
	prefix, err := LoadCheckpointPrefix(context.Background(), checkpoints)
	if err != nil {
		t.Fatal(err)
	}
	return prefix
}

func mustStatusJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
