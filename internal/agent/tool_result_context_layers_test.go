package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestToolResultContextLayerCompressionRatios(t *testing.T) {
	const modelVisibleLimit = 50 * 1024
	rawOutput, err := json.Marshal(map[string]string{
		"title": "Long research evidence",
		"markdown": strings.Repeat(
			"## Evidence section\nPrimary-source detail supports the finding. [source](https://example.com/paper)\n",
			5_000,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	rawResult := ActionResult{Status: ActionSucceeded, Output: rawOutput}
	rawCheckpoint, err := NewActionResultCheckpoint(1, 0, "decision:1/action:0", rawResult)
	if err != nil {
		t.Fatal(err)
	}

	store := &recordingToolResultStore{}
	boundedResult, externalization := testToolResultExternalizer(store).Externalize(context.Background(), ToolResultScope{
		UserID: "user_a", ChatID: "chat_a", RunID: "run_a", ActionID: "decision:1/action:0", ToolName: "read_url",
	}, rawResult, modelVisibleLimit)
	if externalization.State != ToolResultExternalized || externalization.Err != nil || len(store.envelopes) != 1 ||
		!bytes.Equal(store.envelopes[0].Body, rawOutput) {
		t.Fatalf("externalization=%+v envelopes=%d", externalization, len(store.envelopes))
	}
	boundedCheckpoint, err := NewActionResultCheckpoint(1, 0, "decision:1/action:0", boundedResult)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundedCheckpoint.Payload) > modelVisibleLimit {
		t.Fatalf("bounded model-visible result=%d want <=%d", len(boundedCheckpoint.Payload), modelVisibleLimit)
	}

	proposal, err := NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "read_url", Input: json.RawMessage(`{"url":"https://example.com/paper"}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	rawUnits := projectCompressionFixture(t, proposal, rawCheckpoint)
	boundedUnits := projectCompressionFixture(t, proposal, boundedCheckpoint)
	boundedMessages := FlattenContextUnits(boundedUnits)
	if len(boundedMessages) != 2 || boundedMessages[1].Role != models.RoleAction ||
		boundedMessages[1].Content != string(boundedCheckpoint.Payload) {
		t.Fatalf("adjacent projection diverged from checkpoint: %+v", boundedMessages)
	}

	archive := researchArchivalCapsule{
		DecisionNo:  1,
		CapsuleJSON: json.RawMessage(`{"schema_version":"nano.research-capsule@1","decision_no":1,"objective_advanced":"Read the source and retained its conclusion.","conclusions":["The primary source supports the finding."],"decisions":[],"constraints":[],"durable_refs":["https://example.com/paper"],"contradictions":[],"verification":["Full result remains in Redis during its TTL."],"follow_up":[]}`),
	}
	archives := map[int]researchArchivalCapsule{1: archive}
	archivedUnits, err := applyResearchArchivalCapsules(boundedUnits, archives)
	if err != nil {
		t.Fatal(err)
	}
	memoryUnits, err := applyResearchTaskMemories(boundedUnits, archives, []researchTaskMemory{{
		FirstDecisionNo: 1, LastDecisionNo: 1,
		MemoryJSON: json.RawMessage(`{"schema_version":"nano.research-task-memory@1","first_decision_no":1,"last_decision_no":1,"goal":"Finish the evidence-backed report.","phase":"execution","conclusions":["The primary source supports the finding."],"decisions":[],"constraints":[],"durable_refs":["https://example.com/paper"],"contradictions":[],"failed_paths":[],"verification":["Source read completed."],"report_state":[],"follow_up":[]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}

	rawStepBytes := encodedModelMessagesBytes(t, FlattenContextUnits(rawUnits))
	boundedStepBytes := encodedModelMessagesBytes(t, boundedMessages)
	archivedStepBytes := encodedModelMessagesBytes(t, FlattenContextUnits(archivedUnits))
	memoryStepBytes := encodedModelMessagesBytes(t, FlattenContextUnits(memoryUnits))
	if !(rawStepBytes > boundedStepBytes && boundedStepBytes > archivedStepBytes && archivedStepBytes > memoryStepBytes) {
		t.Fatalf("unexpected layer sizes raw=%d bounded=%d archived=%d memory=%d", rawStepBytes, boundedStepBytes, archivedStepBytes, memoryStepBytes)
	}

	t.Logf("tool-result-layer-bytes raw_body=%d raw_action=%d bounded_checkpoint=%d adjacent_action=%d", len(rawOutput), len(rawCheckpoint.Payload), len(boundedCheckpoint.Payload), len(boundedMessages[1].Content))
	t.Logf("research-step-layer-bytes raw=%d bounded=%d archival_capsule=%d task_memory=%d", rawStepBytes, boundedStepBytes, archivedStepBytes, memoryStepBytes)
	t.Logf("research-step-compression-vs-raw bounded=%.2f%% archival_capsule=%.2f%% task_memory=%.2f%%", compressionPercent(rawStepBytes, boundedStepBytes), compressionPercent(rawStepBytes, archivedStepBytes), compressionPercent(rawStepBytes, memoryStepBytes))
}

func projectCompressionFixture(t *testing.T, proposal, result PendingCheckpoint) []ContextUnit {
	t.Helper()
	prefix, err := LoadCheckpointPrefix(context.Background(), []Checkpoint{
		{SequenceNo: 1, PendingCheckpoint: proposal},
		{SequenceNo: 2, PendingCheckpoint: result},
	})
	if err != nil {
		t.Fatal(err)
	}
	units, err := ProjectChatLane(context.Background(), ChatLane{Turns: []ChatLaneTurn{{
		MessageID: "msg_a", Content: "Research the source.", Runs: []ChatLaneRun{{RunID: "run_a", Prefix: &prefix}},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return units[1:]
}

func encodedModelMessagesBytes(t *testing.T, messages []models.ModelMessage) int {
	t.Helper()
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return len(encoded)
}

func compressionPercent(baseline, value int) float64 {
	return (1 - float64(value)/float64(baseline)) * 100
}
