package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGroundedSystemPromptDescribesPlainTextSourceMarkerContract(t *testing.T) {
	for _, required := range []string{
		"strongly prefer calling `search_evidence` before answering",
		"with an older topic",
		"[source:<source_id>]",
		"ordinary plain text",
		"`discover_sources`",
		"same Action batch",
		"do not wait for an evidence-sufficiency gate",
		"at most once per Run",
		"left Discovery panel",
	} {
		if !strings.Contains(GroundedSystemPrompt, required) {
			t.Fatalf("grounded prompt is missing %q", required)
		}
	}
	for _, forbidden := range []string{"delegate.research.source-discovery.v1", "must be only JSON", "You must always use search_evidence"} {
		if strings.Contains(GroundedSystemPrompt, forbidden) {
			t.Fatalf("grounded prompt still contains %q", forbidden)
		}
	}
}

func TestBareSystemPromptStronglyPrefersPublicSourceDiscovery(t *testing.T) {
	for _, required := range []string{
		"strongly prefer calling `discover_sources` before answering",
		"at most once per Run",
		"left Discovery panel",
		"semantic judgment",
		"call `rewrite_todo_list` before substantive work",
		"call `update_todo_status` immediately",
		"Read Agent Status before every decision",
	} {
		if !strings.Contains(BareSystemPrompt, required) {
			t.Fatalf("bare prompt is missing %q", required)
		}
	}
	for _, forbidden := range []string{"delegate.research.source-discovery.v1"} {
		if strings.Contains(BareSystemPrompt, forbidden) {
			t.Fatalf("bare prompt still contains stale phrase %q", forbidden)
		}
	}
}

func TestComposerPromptTraceRefsUseRetrievalFirstImmutableVersions(t *testing.T) {
	bare := composerPromptTraceRef(BarePromptVersion)
	grounded := composerPromptTraceRef(GroundedPromptVersion)
	if bare.Identity != "agent.chat-composer-bare" || bare.Version != 4 {
		t.Fatalf("bare trace ref = %+v", bare)
	}
	if grounded.Identity != "agent.chat-composer-grounded" || grounded.Version != 5 {
		t.Fatalf("grounded trace ref = %+v", grounded)
	}
}

func TestGroundedRequiredActionDependsOnDurableSearchAttempt(t *testing.T) {
	tests := []struct {
		name   string
		prefix CheckpointPrefix
		want   string
	}{
		{name: "no search", want: "search_evidence"},
		{name: "complete empty search", prefix: groundedSearchPrefix(t, true, false, nil)},
		{name: "degraded empty search", prefix: groundedSearchPrefix(t, false, true, nil)},
		{name: "compact evidence manifest", prefix: groundedCompactSearchPrefix(t, false, true, []map[string]any{{
			"chunk_id": "chunk_a", "source_id": "src_a", "evidence_revision_id": "evr_a",
		}})},
		{name: "legacy citeable evidence", prefix: groundedSearchPrefix(t, false, true, []map[string]any{{
			"source_id": "src_a", "evidence_revision_id": "evr_a", "evidence_ranges": []map[string]any{{
				"unit_id": "unit_a", "start_rune": 2, "end_rune": 9,
			}},
		}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := groundedRequiredAction(tt.prefix)
			if err != nil || got != tt.want {
				t.Fatalf("required action=%q err=%v, want %q", got, err, tt.want)
			}
		})
	}
}

func groundedCompactSearchPrefix(t *testing.T, completeEmpty, degraded bool, evidence []map[string]any) CheckpointPrefix {
	t.Helper()
	output, err := json.Marshal(map[string]any{
		"result_version": SearchEvidenceResultVersion,
		"complete_empty": completeEmpty,
		"degraded":       degraded,
		"degradations":   []string{},
		"evidence":       evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := ActionResult{Status: ActionSucceeded, Output: output}
	return CheckpointPrefix{Proposals: []AcceptedProposal{{DecisionNo: 1, Actions: []AcceptedAction{{
		ActionID: "decision:1/action:0", Index: 0, Name: "search_evidence", Result: &result,
	}}}}}
}

func groundedSearchPrefix(t *testing.T, completeEmpty, degraded bool, evidence []map[string]any) CheckpointPrefix {
	t.Helper()
	output, err := json.Marshal(map[string]any{
		"complete_empty": completeEmpty,
		"degraded":       degraded,
		"evidence":       evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := ActionResult{Status: ActionSucceeded, Output: output}
	return CheckpointPrefix{Proposals: []AcceptedProposal{{DecisionNo: 1, Actions: []AcceptedAction{{
		ActionID: "decision:1/action:0", Index: 0, Name: "search_evidence", Result: &result,
	}}}}}
}
