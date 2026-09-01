package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReadToolResultActionReturnsScopedBoundedPage(t *testing.T) {
	envelope := testToolResultEnvelope([]byte(`{"markdown":"decision evidence 🛡️","source":"primary"}`))
	store := &recordingToolResultStore{envelopes: []ToolResultEnvelope{envelope}}
	action := NewReadToolResultAction(ToolResultReader{Store: store, MaximumPageBytes: 16, Now: testToolResultNow})
	input := json.RawMessage(`{"result_ref":"tr_test_reference","offset":0,"max_bytes":4096}`)

	result, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:2/action:0", Input: input, UserID: "user_a", ChatID: "chat_a",
		Attempt: Attempt{RunID: "run_a"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != ActionSucceeded {
		t.Fatalf("result = %#v", result)
	}
	var page ToolResultPage
	if err := json.Unmarshal(result.Output, &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.ResultRef != envelope.ResultRef || page.Offset != 0 || page.NextOffset > 16 || page.Complete {
		t.Fatalf("page = %#v", page)
	}
}

func TestReadToolResultActionReturnsStableExpiredDomainError(t *testing.T) {
	action := NewReadToolResultAction(ToolResultReader{Store: &recordingToolResultStore{}, MaximumPageBytes: 16, Now: testToolResultNow})
	result, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:2/action:0", Input: json.RawMessage(`{"result_ref":"tr_missing_reference"}`),
		UserID: "user_a", ChatID: "chat_a", Attempt: Attempt{RunID: "run_a"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != ActionDomainError || result.ErrorCode != "tool_result_expired" {
		t.Fatalf("result = %#v", result)
	}
}

func TestReadToolResultActionRejectsCrossRunRead(t *testing.T) {
	envelope := testToolResultEnvelope([]byte(`{"ok":true}`))
	action := NewReadToolResultAction(ToolResultReader{
		Store: &recordingToolResultStore{envelopes: []ToolResultEnvelope{envelope}}, MaximumPageBytes: 16, Now: testToolResultNow,
	})
	result, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:2/action:0", Input: json.RawMessage(`{"result_ref":"tr_test_reference"}`),
		UserID: "user_a", ChatID: "chat_a", Attempt: Attempt{RunID: "run_b"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != ActionDomainError || result.ErrorCode != "tool_result_unauthorized" {
		t.Fatalf("result = %#v", result)
	}
}
