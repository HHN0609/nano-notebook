package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestProjectChatLanePreservesCrossRunStepsAndDeduplicatesFinal(t *testing.T) {
	run1 := chatLaneRunFromPending(t, "run_1", []PendingCheckpoint{
		mustProposal(t, 1, "tool_a", "tool_b"),
		mustResult(t, 1, 0, ActionSucceeded, `{"value":"a"}`, ""),
		mustResult(t, 1, 1, ActionDomainError, ``, "tool_b_failed"),
		mustProposal(t, 2, "tool_c"),
		mustResult(t, 2, 0, ActionSucceeded, `{"value":"c"}`, ""),
		mustFinal(t, 3, "answer one"),
	})
	// The publication is deliberately present too. The Final checkpoint is
	// authoritative and must make this compatibility value disappear.
	run1.LegacyPublishedFinal = "answer one"
	run2 := chatLaneRunFromPending(t, "run_2", []PendingCheckpoint{
		mustProposal(t, 1, "tool_d"),
		mustResult(t, 1, 0, ActionSucceeded, `{"value":"d"}`, ""),
	})

	units, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{
		{MessageID: "msg_1", Content: "question one", Runs: []ChatLaneRun{run1}},
		{MessageID: "msg_2", Content: "question two", Runs: []ChatLaneRun{run2}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := FlattenContextUnits(units)
	roles := make([]models.ModelRole, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	wantRoles := []models.ModelRole{
		models.RoleUser,
		models.RoleAssistant, models.RoleAction, models.RoleAction,
		models.RoleAssistant, models.RoleAction,
		models.RoleAssistant,
		models.RoleUser,
		models.RoleAssistant, models.RoleAction,
	}
	if len(roles) != len(wantRoles) {
		t.Fatalf("roles=%v", roles)
	}
	for index := range wantRoles {
		if roles[index] != wantRoles[index] {
			t.Fatalf("roles[%d]=%q want %q", index, roles[index], wantRoles[index])
		}
	}
	finals := 0
	for _, message := range messages {
		if message.Role == models.RoleAssistant && message.Content == "answer one" {
			finals++
		}
	}
	if finals != 1 {
		t.Fatalf("published final projected %d times", finals)
	}
	if messages[2].ActionCallID != "decision:1/action:0" || messages[3].ActionCallID != "decision:1/action:1" {
		t.Fatalf("first batch result order=%q,%q", messages[2].ActionCallID, messages[3].ActionCallID)
	}
}

func TestProjectChatLaneOrdersParallelResultsByProposal(t *testing.T) {
	proposal := mustProposal(t, 1, "tool_a", "tool_b", "tool_c")
	// Durable commit order is B, C, A.
	run := chatLaneRunFromPending(t, "run_parallel", []PendingCheckpoint{
		proposal,
		mustResult(t, 1, 1, ActionSucceeded, `{"value":"b"}`, ""),
		mustResult(t, 1, 2, ActionSucceeded, `{"value":"c"}`, ""),
		mustResult(t, 1, 0, ActionSucceeded, `{"value":"a"}`, ""),
	})
	units, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{{
		MessageID: "msg_parallel", Content: "parallel", Runs: []ChatLaneRun{run},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := FlattenContextUnits(units)
	for index, want := range []string{"decision:1/action:0", "decision:1/action:1", "decision:1/action:2"} {
		if got := messages[index+2].ActionCallID; got != want {
			t.Fatalf("result %d=%q want %q", index, got, want)
		}
	}
}

func TestProjectChatLaneRejectsOrphanActionCall(t *testing.T) {
	run := chatLaneRunFromPending(t, "run_incomplete", []PendingCheckpoint{
		mustProposal(t, 1, "tool_a", "tool_b"),
		mustResult(t, 1, 0, ActionSucceeded, `{"value":"a"}`, ""),
	})
	_, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{{
		MessageID: "msg_incomplete", Content: "incomplete", Runs: []ChatLaneRun{run},
	}}}, nil)
	if err == nil {
		t.Fatal("expected incomplete historical Action batch to fail closed")
	}
}

func TestProjectChatLaneUsesLegacyFinalOnlyWithoutCheckpoint(t *testing.T) {
	units, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{{
		MessageID: "msg_legacy", Content: "legacy question", Runs: []ChatLaneRun{{
			RunID: "run_legacy", LegacyPublishedFinal: "legacy answer",
		}},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := FlattenContextUnits(units)
	if len(messages) != 2 || messages[1].Role != models.RoleAssistant || messages[1].Content != "legacy answer" {
		t.Fatalf("messages=%+v", messages)
	}
}

func TestProjectChatLaneRetryHasNoCopiedCheckpointsButSeesSourceRunSteps(t *testing.T) {
	source := chatLaneRunFromPending(t, "run_source", []PendingCheckpoint{
		mustProposal(t, 1, "tool_a"),
		mustResult(t, 1, 0, ActionDomainError, ``, "source_failed"),
	})
	retry := ChatLaneRun{RunID: "run_retry"}
	units, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{{
		MessageID: "msg_retry", Content: "retry this", Runs: []ChatLaneRun{source, retry},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := FlattenContextUnits(units)
	if len(retry.Checkpoints) != 0 || len(messages) != 3 || messages[0].Role != models.RoleUser ||
		messages[1].Role != models.RoleAssistant || messages[2].Role != models.RoleAction ||
		!strings.Contains(messages[2].Content, "source_failed") {
		t.Fatalf("retry checkpoints=%d messages=%+v", len(retry.Checkpoints), messages)
	}
}

func chatLaneRunFromPending(t *testing.T, runID string, pending []PendingCheckpoint) ChatLaneRun {
	t.Helper()
	checkpoints := make([]Checkpoint, 0, len(pending))
	for index, checkpoint := range pending {
		checkpoints = append(checkpoints, Checkpoint{SequenceNo: index + 1, PendingCheckpoint: checkpoint})
	}
	return ChatLaneRun{RunID: runID, Checkpoints: checkpoints}
}

func mustProposal(t *testing.T, decisionNo int, names ...string) PendingCheckpoint {
	t.Helper()
	actions := make([]models.ActionProposal, 0, len(names))
	for _, name := range names {
		actions = append(actions, models.ActionProposal{Name: name, Input: json.RawMessage(`{"query":"x"}`)})
	}
	checkpoint, err := NewProposalCheckpoint(decisionNo, models.ActionProposalBatch{Actions: actions})
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func mustResult(t *testing.T, decisionNo, actionIndex int, status ActionResultStatus, output, errorCode string) PendingCheckpoint {
	t.Helper()
	result := ActionResult{Status: status, ErrorCode: errorCode}
	if output != "" {
		result.Output = json.RawMessage(output)
	}
	checkpoint, err := NewActionResultCheckpoint(decisionNo, actionIndex, "decision:"+itoa(decisionNo)+"/action:"+itoa(actionIndex), result)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func mustFinal(t *testing.T, decisionNo int, text string) PendingCheckpoint {
	t.Helper()
	checkpoint, err := NewFinalDraftCheckpoint(decisionNo, models.FinalDraft{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
