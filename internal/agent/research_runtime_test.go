package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
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
	if strings.Contains(got, "系统核验的证据账本（权威）") {
		t.Fatalf("code-owned coverage telemetry was injected into the report body: %s", got)
	}
}

func TestCanonicalizeResearchEvidenceClaimsDoesNotAddOperationalBoilerplate(t *testing.T) {
	got, corrections := canonicalizeResearchEvidenceClaims("# Decision\n\nAdopt A for the pilot.", 40, 25, 12, 3)
	if corrections != 0 || got != "# Decision\n\nAdopt A for the pilot." {
		t.Fatalf("corrections=%d report=%q", corrections, got)
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

func TestPlanResearchDuplicateRecoverySuggestsUnreadEvidenceWithoutSchemaDeadlock(t *testing.T) {
	definitions := []models.ActionDefinition{
		{Name: "web_search"},
		{Name: "read_url", InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)},
		{Name: "search_evidence"},
	}
	recovery := planResearchDuplicateRecovery(1, []string{
		"https://github.com/openai/codex",
		"https://docs.anthropic.com/en/docs/claude-code",
	}, definitions)
	if len(recovery.definitions) != len(definitions) {
		t.Fatalf("definitions=%+v", recovery.definitions)
	}
	if strings.Contains(string(recovery.definitions[1].InputSchema), "enum") {
		t.Fatalf("recovery schema created a fatal URL enum: %s", recovery.definitions[1].InputSchema)
	}
	for _, required := range []string{"fresh unread", "https://github.com/openai/codex", "https://docs.anthropic.com/en/docs/claude-code"} {
		if !strings.Contains(recovery.directive, required) {
			t.Fatalf("directive missing %q: %s", required, recovery.directive)
		}
	}
}

func TestPlanResearchDuplicateRecoveryKeepsFreshEvidenceAvailableAfterManyStalledSteps(t *testing.T) {
	definitions := []models.ActionDefinition{{Name: "web_search"}, {Name: "read_url"}}
	recovery := planResearchDuplicateRecovery(3, []string{"https://example.com/still-unread"}, definitions)
	if len(recovery.definitions) != len(definitions) {
		t.Fatalf("recovery=%+v", recovery)
	}
	if !strings.Contains(recovery.directive, "https://example.com/still-unread") {
		t.Fatalf("recovery directive=%q", recovery.directive)
	}
}

func TestResearchDuplicateRecoveryOmitsExactRecentSteps(t *testing.T) {
	if includeExactResearchSuffix([]models.ActionDefinition{{Name: "read_url"}}, 1) {
		t.Fatal("duplicate recovery retained the exact suffix that caused the model to copy attempted URLs")
	}
	if !includeExactResearchSuffix([]models.ActionDefinition{{Name: "read_url"}}, 0) {
		t.Fatal("ordinary tool-capable research lost its exact suffix")
	}
	if includeExactResearchSuffix(nil, 0) {
		t.Fatal("reporting request exposed raw tool steps")
	}
}

func TestSelectUnattemptedResearchURLsExcludesEveryPriorReadProposal(t *testing.T) {
	candidates := []string{
		"https://example.com/already-read",
		"https://example.com/fresh-one",
		"https://example.com/already-failed",
		"https://example.com/fresh-two",
	}
	attempted := map[string]bool{
		"https://example.com/already-read":   true,
		"https://example.com/already-failed": true,
	}
	got := selectUnattemptedResearchURLs(candidates, attempted, 2)
	if strings.Join(got, ",") != "https://example.com/fresh-one,https://example.com/fresh-two" {
		t.Fatalf("selected=%v", got)
	}
}

func TestResearchFinalOnlyPromptClosesToolUse(t *testing.T) {
	for _, required := range []string{"Tool use is closed", "Final", "complete report"} {
		if !strings.Contains(researchFinalOnlyPrompt(), required) {
			t.Fatalf("final-only prompt missing %q", required)
		}
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

func TestResearchRecoveryAllowsChoiceBeyondOneActionBatch(t *testing.T) {
	if got, want := researchRecoveryCandidateLimit(6), 24; got != want {
		t.Fatalf("candidate limit=%d want=%d", got, want)
	}
	runtime := &ResearchRuntime{}
	if got, want := runtime.InvalidModelResponseRetryLimit(), 5; got != want {
		t.Fatalf("invalid response retries=%d want=%d", got, want)
	}
}

func TestResearchFinalProjectionHidesDiscoveredOnlyLedgerURLs(t *testing.T) {
	if !includeResearchLedgerURL("read", false) || !includeResearchLedgerURL("failed", false) {
		t.Fatal("Final projection dropped a read or failed evidence boundary")
	}
	if includeResearchLedgerURL("discovered", false) {
		t.Fatal("Final projection exposed a discovered-only URL")
	}
	if !includeResearchLedgerURL("discovered", true) {
		t.Fatal("tool-capable projection hid discovery candidates")
	}
}

func TestResearchCompletionSignalRequiresAssembledArtifact(t *testing.T) {
	for _, value := range []string{"Final", "done.", "已完成！"} {
		if !isResearchCompletionSignal(value) {
			t.Fatalf("completion signal %q was accepted as a report", value)
		}
	}
	for _, value := range []string{"# Decision\n\nAdopt checkpointed sections.", "完成情况：建议采用分段写作。"} {
		if isResearchCompletionSignal(value) {
			t.Fatalf("complete report %q was rejected as a signal", value)
		}
	}
}
