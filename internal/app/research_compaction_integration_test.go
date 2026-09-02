package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

type researchCompactionModel struct {
	calls                  int
	requests               []models.ModelRequest
	capsuleConclusionBytes int
	sawTodoStep            bool
}

func TestResearchV10DecisionRequestRetainsCompletedTodoStepBeforeCompaction(t *testing.T) {
	api := newTestAPI(t)
	claimed, _, _, _ := admitResearchExecutionForRelease(t, api, "research-todo-context-v10@example.com", "nano.default@17")
	ctx := context.Background()
	runtime, err := agent.NewResearchRuntime(api.db.Pool(), promptcatalog.MustLoadEmbedded())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "rewrite_todo_list", Input: json.RawMessage(`{"items":["read evidence","write report"]}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), proposal)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agent.RewriteTodoList(agent.TodoSnapshot{}, false, execution.InputMessageID,
		[]string{"read evidence", "write report"}, stored.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.NewActionResultCheckpoint(1, 0, "decision:1/action:0", agent.ActionResult{Status: agent.ActionSucceeded, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), result); err != nil {
		t.Fatal(err)
	}
	prefix, err := runtime.LoadCheckpointPrefix(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtime.BuildDecisionRequest(ctx, execution, prefix, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundCall, foundSucceededResult := false, false
	for _, message := range request.Messages {
		for _, call := range message.ActionCalls {
			if call.Name == "rewrite_todo_list" && strings.Contains(string(call.Input), "read evidence") {
				foundCall = true
			}
		}
		if message.Role == models.RoleAction && message.ActionCallID == "decision:1/action:0" && strings.Contains(message.Content, `"status":"succeeded"`) {
			foundSucceededResult = true
		}
	}
	if !foundCall || !foundSucceededResult {
		t.Fatalf("completed TODO step missing from pre-compaction request: call=%t result=%t messages=%+v", foundCall, foundSucceededResult, request.Messages)
	}
}

func (m *researchCompactionModel) Decide(_ context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
	m.calls++
	m.requests = append(m.requests, request)
	if request.InvocationPolicy.Temperature == nil || *request.InvocationPolicy.Temperature != 0 || request.InvocationPolicy.MaxOutputTokens != 16_384 || len(request.Messages) != 2 {
		return models.ModelOutcome{}, fmt.Errorf("invalid compaction invocation")
	}
	if !strings.Contains(request.Messages[1].Content, `"name":"web_search"`) ||
		(!strings.Contains(request.Messages[1].Content, "decision-1-") && !strings.Contains(request.Messages[1].Content, "decision-2-") && !strings.Contains(request.Messages[1].Content, "small-1")) {
		return models.ModelOutcome{}, fmt.Errorf("compaction input lost retained Tool name or complete input")
	}
	if strings.Contains(request.Messages[1].Content, "TODO private marker") && strings.Contains(request.Messages[1].Content, "rewrite_todo_list") {
		m.sawTodoStep = true
	}
	if strings.Contains(request.Messages[1].Content, "<agent_status") {
		return models.ModelOutcome{}, fmt.Errorf("compaction input included ephemeral Agent Status")
	}
	var input struct {
		Steps []struct {
			DecisionNo         int `json:"decision_no"`
			StartCheckpointSeq int `json:"start_checkpoint_seq"`
			EndCheckpointSeq   int `json:"end_checkpoint_seq"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil || len(input.Steps) == 0 {
		return models.ModelOutcome{}, fmt.Errorf("invalid compaction input: %w", err)
	}
	var output any
	if m.calls == 1 {
		capsules := make([]map[string]any, 0, len(input.Steps))
		for _, step := range input.Steps {
			conclusions := []string{}
			for remaining := m.capsuleConclusionBytes; remaining > 0; {
				chunk := min(remaining, 1_000)
				conclusions = append(conclusions, strings.Repeat("c", chunk))
				remaining -= chunk
			}
			capsules = append(capsules, map[string]any{
				"schema_version": "nano.research-capsule@1", "decision_no": step.DecisionNo,
				"start_checkpoint_seq": step.StartCheckpointSeq, "end_checkpoint_seq": step.EndCheckpointSeq,
				"objective_advanced": fmt.Sprintf("Completed Research Step %d", step.DecisionNo),
				"conclusions":        conclusions, "decisions": []string{}, "constraints": []string{}, "durable_refs": []string{},
				"contradictions": []string{}, "verification": []string{}, "follow_up": []string{},
			})
		}
		output = map[string]any{"schema_version": "nano.research-capsules@1", "capsules": capsules}
	} else if m.calls == 2 {
		first, last := input.Steps[0], input.Steps[len(input.Steps)-1]
		output = map[string]any{
			"schema_version": "nano.research-task-memory@1", "first_decision_no": first.DecisionNo,
			"last_decision_no": last.DecisionNo, "start_checkpoint_seq": first.StartCheckpointSeq, "end_checkpoint_seq": last.EndCheckpointSeq,
			"goal": "Complete the accepted Research Plan", "phase": "execution",
			"conclusions": []string{}, "decisions": []string{}, "constraints": []string{}, "durable_refs": []string{},
			"contradictions": []string{}, "failed_paths": []string{}, "verification": []string{}, "report_state": []string{}, "follow_up": []string{},
		}
	} else {
		return models.ModelOutcome{}, fmt.Errorf("unexpected compaction call %d", m.calls)
	}
	encoded, _ := json.Marshal(output)
	return models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: string(encoded)}}}, nil
}

func TestResearchV10ThresholdCompactionPersistsTwoLayersWithoutChangingCheckpoints(t *testing.T) {
	api := newTestAPI(t)
	claimed, sessionID, _, _ := admitResearchExecutionForRelease(t, api, "research-compaction-v10@example.com", "nano.default@17")
	ctx := context.Background()
	runtime, err := agent.NewResearchRuntime(api.db.Pool(), promptcatalog.MustLoadEmbedded())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil || execution.AgentConfigID != "research.executor@10" {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	todoInput := json.RawMessage(`{"items":["Investigate TODO private marker"]}`)
	todoProposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "rewrite_todo_list", Input: todoInput,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	storedTodoProposal, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), todoProposal)
	if err != nil {
		t.Fatal(err)
	}
	todoSnapshot, err := agent.RewriteTodoList(agent.TodoSnapshot{}, false, execution.InputMessageID,
		[]string{"Investigate TODO private marker"}, storedTodoProposal.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	todoOutput, err := json.Marshal(todoSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	todoResult := agent.ActionResult{Status: agent.ActionSucceeded, Output: todoOutput}
	todoCheckpoint, err := agent.NewActionResultCheckpoint(1, 0, "decision:1/action:0", todoResult)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), todoCheckpoint); err != nil {
		t.Fatal(err)
	}
	prefix := agent.CheckpointPrefix{}
	inputPadding := strings.Repeat("i", 64_000)
	resultPadding := strings.Repeat("r", 64_000)
	for decision := 2; decision <= 46; decision++ {
		proposal, err := agent.NewProposalCheckpoint(decision, models.ActionProposalBatch{Actions: []models.ActionProposal{{
			Name: "web_search", Input: json.RawMessage(fmt.Sprintf(`{"queries":["decision-%d-%s"]}`, decision, inputPadding)),
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), proposal); err != nil {
			t.Fatal(err)
		}
		result, err := agent.NewActionResultCheckpoint(decision, 0, fmt.Sprintf("decision:%d/action:0", decision), agent.ActionResult{
			Status: agent.ActionSucceeded, Output: json.RawMessage(fmt.Sprintf(`{"padding":"result-%d-%s"}`, decision, resultPadding)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), result); err != nil {
			t.Fatal(err)
		}
		prefix, err = runtime.LoadCheckpointPrefix(ctx, attemptFromClaim(claimed))
		if err != nil {
			t.Fatal(err)
		}
	}
	checkpointPayloads := researchCheckpointPayloadSnapshot(t, api.db.Pool(), claimed.RunID)
	model := &researchCompactionModel{}
	request, err := runtime.PrepareDecisionRequest(ctx, execution, prefix, nil, model, "")
	if err != nil {
		t.Fatal(err)
	}
	var capsules, memories, legacyCapsules, legacyRollups int
	if err := api.db.Pool().QueryRow(ctx, `
		select
			(select count(*) from research_archival_capsules where session_id=$1),
			(select count(*) from research_task_memories where session_id=$1),
			(select count(*) from research_step_capsules where session_id=$1),
			(select count(*) from research_rollups where session_id=$1)
	`, sessionID).Scan(&capsules, &memories, &legacyCapsules, &legacyRollups); err != nil {
		t.Fatal(err)
	}
	checkpointPayloadsAfter := researchCheckpointPayloadSnapshot(t, api.db.Pool(), claimed.RunID)
	if model.calls != 2 || !model.sawTodoStep || capsules != 24 || memories != 1 || legacyCapsules != 0 || legacyRollups != 0 || !reflect.DeepEqual(checkpointPayloadsAfter, checkpointPayloads) {
		t.Fatalf("calls=%d capsules=%d memories=%d legacy=%d/%d checkpoint_changed=%t", model.calls, capsules, memories, legacyCapsules, legacyRollups, !reflect.DeepEqual(checkpointPayloadsAfter, checkpointPayloads))
	}
	if request.ContextTelemetry.InputTokens > execution.ModelContext.Budgets.SafeInputTokens || request.ContextTelemetry.BeforeCompactionTokens <= request.ContextTelemetry.AfterCompactionTokens || !request.ContextTelemetry.AgentStatusInjected {
		t.Fatalf("telemetry=%+v budgets=%+v", request.ContextTelemetry, execution.ModelContext.Budgets)
	}
	joined := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		joined = append(joined, message.Content)
		for _, call := range message.ActionCalls {
			joined = append(joined, string(call.Input))
		}
	}
	projected := strings.Join(joined, "\n")
	if strings.Contains(projected, "decision-2-") || strings.Contains(projected, "result-2-") ||
		!strings.Contains(projected, "decision-46-") || !strings.Contains(projected, "result-46-") || !strings.Contains(projected, "research_task_memory") ||
		!strings.Contains(projected, "<agent_status version=\"1\">") || !strings.Contains(projected, "Investigate TODO private marker") {
		t.Fatalf("unexpected compacted projection markers; bytes=%d old=%t/%t recent=%t/%t memory=%t status=%t todo=%t",
			len(projected), strings.Contains(projected, "decision-2-"), strings.Contains(projected, "result-2-"),
			strings.Contains(projected, "decision-46-"), strings.Contains(projected, "result-46-"),
			strings.Contains(projected, "research_task_memory"), strings.Contains(projected, "<agent_status version=\"1\">"),
			strings.Contains(projected, "Investigate TODO private marker"))
	}
	for index, compactorRequest := range model.requests {
		encoded, _ := json.Marshal(compactorRequest)
		if strings.Contains(string(encoded), "<agent_status") {
			t.Fatalf("compactor request %d included ephemeral Agent Status", index+1)
		}
	}
}

func TestResearchV10CompactionAcceptsCandidateWithoutCheckingGain(t *testing.T) {
	api := newTestAPI(t)
	claimed, sessionID, _, _ := admitResearchExecutionForRelease(t, api, "research-compaction-rollback@example.com", "nano.default@17")
	ctx := context.Background()
	runtime, err := agent.NewResearchRuntime(api.db.Pool(), promptcatalog.MustLoadEmbedded())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	prefix := agent.CheckpointPrefix{}
	for decision := 1; decision <= 4; decision++ {
		proposal, _ := agent.NewProposalCheckpoint(decision, models.ActionProposalBatch{Actions: []models.ActionProposal{{
			Name: "web_search", Input: json.RawMessage(fmt.Sprintf(`{"queries":["small-%d"]}`, decision)),
		}}})
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), proposal); err != nil {
			t.Fatal(err)
		}
		result, _ := agent.NewActionResultCheckpoint(decision, 0, fmt.Sprintf("decision:%d/action:0", decision), agent.ActionResult{
			Status: agent.ActionSucceeded, Output: json.RawMessage(fmt.Sprintf(`{"padding":"%s"}`, strings.Repeat("x", 512))),
		})
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), result); err != nil {
			t.Fatal(err)
		}
	}
	prefix, err = runtime.LoadCheckpointPrefix(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	execution.ModelContext.Budgets.CompactionTriggerTokens = 1
	execution.ModelContext.Policy.KeepRecentTokens = 100
	model := &researchCompactionModel{capsuleConclusionBytes: 6_000}
	request, err := runtime.PrepareDecisionRequest(ctx, execution, prefix, nil, model, "")
	if err != nil {
		t.Fatal(err)
	}
	var artifacts, failures int
	if err := api.db.Pool().QueryRow(ctx, `
		select
			(select count(*) from research_archival_capsules where session_id=$1) +
			(select count(*) from research_task_memories where session_id=$1),
			(select count(*) from research_compaction_failures where session_id=$1)
	`, sessionID).Scan(&artifacts, &failures); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || artifacts != 3 || failures != 0 || request.ContextTelemetry.AfterCompactionTokens <= request.ContextTelemetry.BeforeCompactionTokens {
		t.Fatalf("calls=%d artifacts=%d failures=%d telemetry=%+v", model.calls, artifacts, failures, request.ContextTelemetry)
	}
	t.Logf("accepted larger candidate: before=%d after=%d artifacts=%d failures=%d",
		request.ContextTelemetry.BeforeCompactionTokens, request.ContextTelemetry.AfterCompactionTokens, artifacts, failures)
}

func TestResearchV10TaskMemoryStillOverSafeBudgetIsNotPersisted(t *testing.T) {
	api := newTestAPI(t)
	claimed, sessionID, _, _ := admitResearchExecutionForRelease(t, api, "research-compaction-safe-budget@example.com", "nano.default@17")
	ctx := context.Background()
	runtime, err := agent.NewResearchRuntime(api.db.Pool(), promptcatalog.MustLoadEmbedded())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runtime.Load(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	for decision := 1; decision <= 4; decision++ {
		inputBytes, resultBytes := 128_000, 256_000
		if decision == 4 {
			// The exact suffix alone remains above the test's safe budget. The
			// three older Steps still drive Layer 3 to its final safe-budget
			// recount after Layer 2 is accepted.
			inputBytes, resultBytes = 1_000_000, 1_000_000
		}
		proposal, proposalErr := agent.NewProposalCheckpoint(decision, models.ActionProposalBatch{Actions: []models.ActionProposal{{
			Name: "web_search", Input: json.RawMessage(fmt.Sprintf(`{"queries":["decision-%d-%s"]}`, decision, strings.Repeat("i", inputBytes))),
		}}})
		if proposalErr != nil {
			t.Fatal(proposalErr)
		}
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), proposal); err != nil {
			t.Fatal(err)
		}
		result, resultErr := agent.NewActionResultCheckpoint(decision, 0, fmt.Sprintf("decision:%d/action:0", decision), agent.ActionResult{
			Status: agent.ActionSucceeded,
			Output: json.RawMessage(fmt.Sprintf(`{"padding":"result-%d-%s"}`, decision, strings.Repeat("r", resultBytes))),
		})
		if resultErr != nil {
			t.Fatal(resultErr)
		}
		if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(claimed), result); err != nil {
			t.Fatal(err)
		}
	}
	prefix, err := runtime.LoadCheckpointPrefix(ctx, attemptFromClaim(claimed))
	if err != nil {
		t.Fatal(err)
	}
	checkpointPayloads := researchCheckpointPayloadSnapshot(t, api.db.Pool(), claimed.RunID)
	execution.ModelContext.Budgets.CompactionTriggerTokens = 1
	execution.ModelContext.Budgets.SafeInputTokens = 320_000
	execution.ModelContext.Policy.KeepRecentTokens = 32_768
	model := &researchCompactionModel{}
	if _, err := runtime.PrepareDecisionRequest(ctx, execution, prefix, nil, model, ""); !errors.Is(err, agent.ErrContextBudgetExceeded) {
		t.Fatalf("err=%v", err)
	}
	var capsules, memories, failures, beforeTokens, afterTokens int
	var reason string
	if err := api.db.Pool().QueryRow(ctx, `
		select
			(select count(*) from research_archival_capsules where session_id=$1),
			(select count(*) from research_task_memories where session_id=$1),
			(select count(*) from research_compaction_failures where session_id=$1),
			coalesce((select reason_code from research_compaction_failures where session_id=$1 order by created_at desc limit 1),''),
			coalesce((select before_tokens from research_compaction_failures where session_id=$1 order by created_at desc limit 1),0),
			coalesce((select after_tokens from research_compaction_failures where session_id=$1 order by created_at desc limit 1),0)
	`, sessionID).Scan(&capsules, &memories, &failures, &reason, &beforeTokens, &afterTokens); err != nil {
		t.Fatal(err)
	}
	checkpointPayloadsAfter := researchCheckpointPayloadSnapshot(t, api.db.Pool(), claimed.RunID)
	if model.calls != 2 || capsules != 3 || memories != 0 || failures != 1 || reason != "safe_budget_exceeded" || !reflect.DeepEqual(checkpointPayloadsAfter, checkpointPayloads) {
		t.Fatalf("calls=%d capsules=%d memories=%d failures=%d reason=%s tokens=%d/%d checkpoint_changed=%t", model.calls, capsules, memories, failures, reason, beforeTokens, afterTokens, !reflect.DeepEqual(checkpointPayloadsAfter, checkpointPayloads))
	}
}

func researchCheckpointPayloadSnapshot(t *testing.T, pool *pgxpool.Pool, runID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		select sequence_no,payload::text from agent_run_checkpoints where run_id=$1 order by sequence_no
	`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var sequence int
		var payload string
		if err := rows.Scan(&sequence, &payload); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprintf("%d:%s", sequence, payload))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
