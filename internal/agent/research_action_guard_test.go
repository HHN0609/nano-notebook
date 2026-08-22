package agent

import (
	"encoding/json"
	"testing"
)

func TestResearchActionProposalDuplicateDetectionUsesCanonicalToolAndInput(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"actions":[{"action_id":"decision:1/action:0","index":0,"name":"read_url","input":{"url":"https://example.com/a"}}]}`),
		[]byte(`{"actions":[{"action_id":"decision:2/action:0","index":0,"name":"web_search","input":{"queries":["new"]}}]}`),
		[]byte(`{"actions":[{"action_id":"decision:3/action:0","index":0,"name":"read_url","input":{"url":"https://example.com/a"}}]}`),
	}
	if !hasRepeatedResearchAction(payloads, "read_url", json.RawMessage(`{"url":"https://example.com/a"}`)) {
		t.Fatal("duplicate read_url input was not detected")
	}
	if hasRepeatedResearchAction(payloads, "web_search", json.RawMessage(`{"queries":["new"]}`)) {
		t.Fatal("one accepted web_search input was classified as a duplicate")
	}
}
