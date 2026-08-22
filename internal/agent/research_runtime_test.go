package agent

import (
	"strings"
	"testing"
)

func TestBuildResearchRoutingDirectivePreventsRepeatedDiscoveryAndReads(t *testing.T) {
	directive := buildResearchRoutingDirective(41, 5, 1,
		[]string{"https://github.com/openai/codex", "https://github.com/anthropics/claude-code"},
		[]string{"Claude Code architecture | Codex architecture", "DeepSeek harness"},
	)
	for _, required := range []string{
		"47 total unique URLs: 41 discovered-only, 5 successfully read, 1 failed",
		"Do not call `read_url` again",
		"https://github.com/openai/codex",
		"Do not repeat these recent `web_search` query batches",
		"prioritize substantive `read_url` calls",
	} {
		if !strings.Contains(directive, required) {
			t.Fatalf("directive missing %q:\n%s", required, directive)
		}
	}
}

func TestCanonicalizeResearchEvidenceClaimsUsesLedgerAuthority(t *testing.T) {
	report := "# 研究报告\n\n共收录 214 个 URL，其中 **32 个为成功读取的一手材料**（远超目标）。\n\n**证据账本状态**：共 214 个 URL，其中 **32 个为成功读取的一手材料**。"
	got, corrections := canonicalizeResearchEvidenceClaims(report, 214, 204, 8, 2)
	for _, required := range []string{
		"系统核验的证据账本（权威）",
		"204 个仅发现、8 个成功读取、2 个读取失败",
		"其中 8 个为成功读取的一手材料",
		"证据账本状态（系统核验）",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("report missing %q:\n%s", required, got)
		}
	}
	if corrections != 3 || strings.Contains(got, "32 个为成功读取") {
		t.Fatalf("corrections=%d report=%s", corrections, got)
	}
}

func TestCombineResearchRollupRetainsPreviousProjection(t *testing.T) {
	got := combineResearchRollup("# Research Rollup\n\nold fact", []string{"# Agent Step 9\n\nnew fact"})
	if !strings.Contains(got, "old fact") || !strings.Contains(got, "new fact") {
		t.Fatalf("rollup lost history:\n%s", got)
	}
}

func TestConsecutiveResearchDuplicateStepsTriggersReportRecovery(t *testing.T) {
	prefix := CheckpointPrefix{Proposals: []AcceptedProposal{
		{DecisionNo: 1, Actions: []AcceptedAction{{Result: &ActionResult{Status: ActionSucceeded, Output: []byte(`{"ok":true}`)}}}},
		{DecisionNo: 2, Actions: []AcceptedAction{{Result: &ActionResult{Status: ActionDomainError, ErrorCode: "research_duplicate_action"}}}},
		{DecisionNo: 3, Actions: []AcceptedAction{{Result: &ActionResult{Status: ActionDomainError, ErrorCode: "research_duplicate_action"}}}},
		{DecisionNo: 4, Actions: []AcceptedAction{{Result: &ActionResult{Status: ActionDomainError, ErrorCode: "research_duplicate_action"}}}},
	}}
	if got := consecutiveResearchDuplicateSteps(prefix); got != 3 {
		t.Fatalf("duplicate steps=%d", got)
	}
}

func TestRewriteResearchReportLinksDowngradesUnreadCitationWithoutDroppingReport(t *testing.T) {
	report := "Read [Codex](https://github.com/openai/codex) and [unread lead](https://example.com/lead)."
	got, removed := rewriteResearchReportLinks(report, map[string]bool{"https://github.com/openai/codex": true})
	if got != "Read [Codex](https://github.com/openai/codex) and unread lead." || len(removed) != 1 || removed[0] != "https://example.com/lead" {
		t.Fatalf("report=%q removed=%v", got, removed)
	}
}

func TestResearchExecutionControlPromptCarriesPinnedBatchContract(t *testing.T) {
	got := researchExecutionControlPrompt(6)
	for _, required := range []string{"at most 6 tool calls", "one to three queries", "exactly one URL"} {
		if !strings.Contains(got, required) {
			t.Fatalf("control prompt missing %q: %s", required, got)
		}
	}
}
