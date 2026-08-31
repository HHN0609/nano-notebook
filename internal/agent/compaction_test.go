package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type compactionDecisionModelFunc func(context.Context, models.ModelRequest) (models.ModelOutcome, error)

func (f compactionDecisionModelFunc) Decide(ctx context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
	return f(ctx, request)
}

func TestContextSummaryPreservesThinkingInvocation(t *testing.T) {
	var captured models.ModelRequest
	model := compactionDecisionModelFunc(func(_ context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
		captured = request
		return models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "summary"}}}, nil
	})
	execution := Execution{
		Model: "aliyun/qwen-plus",
		ModelInvocation: models.ModelInvocationPolicy{
			Timeout: 200 * time.Second, EnableThinking: true,
		},
		ModelContext: agentcatalog.ResolvedModelContextPolicy{Policy: agentcatalog.ModelContextPolicy{SummaryMaxOutputTokens: 4096}},
	}
	_, err := generateContextSummary(context.Background(), model, execution, []ContextUnit{{
		Kind: ContextUnitUserMessage, MessageID: "msg_old",
		Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "old research state"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !captured.InvocationPolicy.EnableThinking || captured.InvocationPolicy.Timeout != 200*time.Second ||
		captured.InvocationPolicy.MaxOutputTokens != 4096 {
		t.Fatalf("compaction invocation=%+v", captured.InvocationPolicy)
	}
}

func TestSelectCompactionCutNeverSplitsAgentStep(t *testing.T) {
	units := []ContextUnit{
		{Kind: ContextUnitUserMessage, MessageID: "msg_old", Messages: []models.ModelMessage{{Role: models.RoleUser, Content: strings.Repeat("old", 100)}}},
		{Kind: ContextUnitAgentStep, MessageID: "msg_old", RunID: "run_old", DecisionNo: 1, Messages: []models.ModelMessage{
			{Role: models.RoleAssistant, ActionCalls: []models.ModelActionCall{{ID: "a", Name: "tool"}, {ID: "b", Name: "tool"}}},
			{Role: models.RoleAction, ActionCallID: "a", Content: strings.Repeat("a", 600)},
			{Role: models.RoleAction, ActionCallID: "b", Content: strings.Repeat("b", 600)},
		}},
		{Kind: ContextUnitUserMessage, MessageID: "msg_current", Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "current"}}},
	}
	cut, err := SelectCompactionCut(units, 100)
	if err != nil {
		t.Fatal(err)
	}
	if cut != 2 {
		t.Fatalf("cut=%d want boundary after whole multi-Action Step", cut)
	}
	if units[cut-1].Kind != ContextUnitAgentStep || len(units[cut-1].Messages) != 3 {
		t.Fatalf("cut split Step: %+v", units[cut-1])
	}
}

func TestCompactionSummaryInputCapsOnlyTemporaryToolSerialization(t *testing.T) {
	original := strings.Repeat("evidence", 600)
	units := []ContextUnit{{Kind: ContextUnitAgentStep, RunID: "run_tool", DecisionNo: 1, Messages: []models.ModelMessage{{
		Role: models.RoleAction, ActionCallID: "decision:1/action:0", Content: original,
	}}}}
	serialized, err := serializeCompactionInput(units)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(serialized, "truncated for summary input") || len([]rune(serialized)) >= len([]rune(original)) {
		t.Fatalf("temporary summary input was not capped: %d", len([]rune(serialized)))
	}
	if units[0].Messages[0].Content != original {
		t.Fatal("durable/projected Action Result was mutated")
	}
}

func TestSingleOversizedCurrentUnitFailsClosed(t *testing.T) {
	_, err := SelectCompactionCut([]ContextUnit{{
		Kind: ContextUnitUserMessage, MessageID: "msg_large",
		Messages: []models.ModelMessage{{Role: models.RoleUser, Content: strings.Repeat("x", 1000)}},
	}}, 100)
	if !errors.Is(err, ErrContextBudgetExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestCompactionIdentityConvergesDespiteDifferentSummaryText(t *testing.T) {
	left := ContextCompaction{PredecessorID: "cmp_old", SummarizedThrough: "message:a", SuffixStart: "message:b", Summary: "one"}
	right := left
	right.Summary = "different nondeterministic wording"
	if compactionIdentity("chat_a", left, strings.Repeat("a", 64)) != compactionIdentity("chat_a", right, strings.Repeat("a", 64)) {
		t.Fatal("same predecessor and cut produced different idempotency identities")
	}
}

func TestApplyContextCompactionUsesSummaryAndExactSuffix(t *testing.T) {
	units := []ContextUnit{
		{Kind: ContextUnitUserMessage, MessageID: "msg_old", Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "old"}}},
		{Kind: ContextUnitAgentStep, MessageID: "msg_old", RunID: "run_old", DecisionNo: 1, Messages: []models.ModelMessage{{Role: models.RoleAssistant, Content: "old answer"}}},
		{Kind: ContextUnitUserMessage, MessageID: "msg_new", Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "new"}}},
	}
	compaction := ContextCompaction{
		Summary: "goal and decisions", SummarizedThrough: ContextUnitKey(units[0]), SuffixStart: ContextUnitKey(units[1]),
	}
	projected, err := ApplyContextCompaction(units, compaction)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 3 || projected[0].Messages[0].Role != models.RoleUser || projected[0].Messages[0].Content != "<summary>goal and decisions</summary>" ||
		projected[1].Messages[0].Content != "old answer" || projected[2].Messages[0].Content != "new" {
		t.Fatalf("projected=%+v", projected)
	}
}
