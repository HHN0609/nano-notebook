package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/jackc/pgx/v5"
)

func TestThresholdCompactionPersistsSummaryWithoutRewritingHistory(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "context-compaction@example.com")
	ctx := context.Background()
	modelCalls := 0
	var compactedMessages []models.ModelMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []models.ModelMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelCalls++
		content := "first final"
		if strings.Contains(request.Messages[0].Content, "Summarize the supplied older Agent context") {
			content = "The first request established the durable prior goal."
		} else if modelCalls > 1 {
			content = "second final"
			compactedMessages = append([]models.ModelMessage(nil), request.Messages...)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + mustJSONString(t, content) + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`))
	}))
	defer upstream.Close()

	traceSink := &capturingDirectTraceSink{}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil, agent.WithTraceSink(traceSink))
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewController(runtime, models.NewBifrostClient(upstream.URL, upstream.Client(), 2048), registry)

	firstMessageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788d001"
	firstRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID, firstMessageID, "small first request")
	firstClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || firstClaim.RunID != firstRunID {
		t.Fatalf("first claim=%+v ok=%t err=%v", firstClaim, ok, err)
	}
	if err := controller.Execute(ctx, attemptFromClaim(firstClaim)); err != nil {
		t.Fatal(err)
	}
	largeHistory := strings.Repeat("x", 60_000)
	if _, err := api.db.Pool().Exec(ctx, `update chat_messages set content=$2 where id=$1`, firstMessageID, largeHistory); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		messageID := "msg_compaction_history_" + string(rune('a'+index))
		if _, err := api.db.Pool().Exec(ctx, `
			insert into public.chat_messages(id,chat_id,role,content,created_at)
			values($1,$2,'user',$3,clock_timestamp()+($4 * interval '1 microsecond'))
		`, messageID, chatID, strings.Repeat(string(rune('a'+index)), 60_000), index); err != nil {
			t.Fatal(err)
		}
	}
	var checkpointCountBefore int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id=$1`, firstRunID).Scan(&checkpointCountBefore); err != nil {
		t.Fatal(err)
	}

	secondMessageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788d002"
	secondRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID, secondMessageID, "continue from the first request")
	secondClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || secondClaim.RunID != secondRunID {
		t.Fatalf("second claim=%+v ok=%t err=%v", secondClaim, ok, err)
	}
	if err := controller.Execute(ctx, attemptFromClaim(secondClaim)); err != nil {
		t.Fatal(err)
	}

	if len(compactedMessages) != 3 || compactedMessages[1].Role != models.RoleUser ||
		!strings.HasPrefix(compactedMessages[1].Content, "<summary>") ||
		compactedMessages[2].Content != "continue from the first request" {
		t.Fatalf("compacted model context=%+v", compactedMessages)
	}
	var compactions, beforeTokens, afterTokens int
	var trigger, summarizedThrough, suffixStart string
	if err := api.db.Pool().QueryRow(ctx, `
		select count(*),min(before_tokens),min(after_tokens),min(trigger_reason),min(summarized_through),min(suffix_start)
		from agent_context_compactions where chat_id=$1
	`, chatID).Scan(&compactions, &beforeTokens, &afterTokens, &trigger, &summarizedThrough, &suffixStart); err != nil {
		t.Fatal(err)
	}
	if compactions != 1 || beforeTokens <= 98_304 || afterTokens >= beforeTokens || trigger != agent.CompactionTriggerThreshold ||
		!strings.HasPrefix(summarizedThrough, "message:msg_compaction_history_") || suffixStart != "message:"+secondMessageID {
		t.Fatalf("compaction count=%d before=%d after=%d trigger=%q through=%q suffix=%q", compactions, beforeTokens, afterTokens, trigger, summarizedThrough, suffixStart)
	}
	compactionTraceRecords := 0
	compactionTraceKinds := make(map[string]struct{})
	for _, envelope := range traceSink.envelopes {
		if envelope.Trace.RunID == secondRunID && envelope.Record.Name == agent.TraceSpanContextCompaction {
			compactionTraceRecords++
			compactionTraceKinds[string(envelope.Record.Kind)] = struct{}{}
		}
	}
	if compactionTraceRecords != 2 || len(compactionTraceKinds) != 2 {
		t.Fatalf("Compaction trace records/kinds=%d/%d want start+end", compactionTraceRecords, len(compactionTraceKinds))
	}
	var storedHistory string
	var checkpointCountAfter int
	if err := api.db.Pool().QueryRow(ctx, `select content from chat_messages where id=$1`, firstMessageID).Scan(&storedHistory); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id=$1`, firstRunID).Scan(&checkpointCountAfter); err != nil {
		t.Fatal(err)
	}
	if storedHistory != largeHistory || checkpointCountAfter != checkpointCountBefore {
		t.Fatalf("raw authority changed: content=%d checkpoints=%d/%d", len(storedHistory), checkpointCountBefore, checkpointCountAfter)
	}
	api.register(t, "context-compaction-other@example.com")
	var ownerID, otherID string
	if err := api.db.Pool().QueryRow(ctx, `select creator_user_id from chat_chats where id=$1`, chatID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select id from identity_users where canonical_email='context-compaction-other@example.com'`).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	visible := func(userID string) int {
		t.Helper()
		count := -1
		if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select count(*) from agent_context_compactions where chat_id=$1`, chatID).Scan(&count)
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if ownerVisible, otherVisible := visible(ownerID), visible(otherID); ownerVisible != 1 || otherVisible != 0 {
		t.Fatalf("Compaction RLS owner/other=%d/%d", ownerVisible, otherVisible)
	}
}

func TestNextRunReceivesCompleteCrossRunToolHistoryOnce(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "cross-run-context@example.com")
	ctx := context.Background()
	callNo := 0
	type providerToolCall struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type providerMessage struct {
		Role       string             `json:"role"`
		Content    string             `json:"content"`
		ToolCalls  []providerToolCall `json:"tool_calls"`
		ToolCallID string             `json:"tool_call_id"`
	}
	var secondRunContext []providerMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providerMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		callNo++
		w.Header().Set("Content-Type", "application/json")
		switch callNo {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"provider-a","type":"function","function":{"name":"calculate","arguments":"{\"operation\":\"add\",\"operands\":[\"1\",\"1\"]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"provider-b","type":"function","function":{"name":"calculate","arguments":"{\"operation\":\"add\",\"operands\":[\"2\",\"2\"]}"}}]},"finish_reason":"tool_calls"}]}`))
		case 3:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"first final"},"finish_reason":"stop"}]}`))
		case 4:
			secondRunContext = append([]providerMessage(nil), request.Messages...)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"second final"},"finish_reason":"stop"}]}`))
		default:
			t.Fatalf("unexpected model call %d", callNo)
		}
	}))
	defer upstream.Close()

	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewController(
		agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil),
		models.NewBifrostClient(upstream.URL, upstream.Client(), 2048), registry,
	)
	firstRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID,
		"0190cdd2-5f2d-7ad8-b3f5-1b588788d011", "calculate twice")
	firstClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || firstClaim.RunID != firstRunID {
		t.Fatalf("first claim=%+v ok=%t err=%v", firstClaim, ok, err)
	}
	if err := controller.Execute(ctx, attemptFromClaim(firstClaim)); err != nil {
		t.Fatal(err)
	}
	secondRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID,
		"0190cdd2-5f2d-7ad8-b3f5-1b588788d012", "what happened next?")
	secondClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || secondClaim.RunID != secondRunID {
		t.Fatalf("second claim=%+v ok=%t err=%v", secondClaim, ok, err)
	}
	if err := controller.Execute(ctx, attemptFromClaim(secondClaim)); err != nil {
		t.Fatal(err)
	}
	wantRoles := []string{"system", "user", "assistant", "tool", "assistant", "tool", "assistant", "user"}
	if len(secondRunContext) != len(wantRoles) {
		t.Fatalf("second Run context=%+v", secondRunContext)
	}
	for index, want := range wantRoles {
		if secondRunContext[index].Role != want {
			t.Fatalf("role[%d]=%q want %q", index, secondRunContext[index].Role, want)
		}
	}
	if secondRunContext[2].ToolCalls[0].ID != "decision:1/action:0" || secondRunContext[3].ToolCallID != "decision:1/action:0" ||
		secondRunContext[4].ToolCalls[0].ID != "decision:2/action:0" || secondRunContext[5].ToolCallID != "decision:2/action:0" ||
		secondRunContext[6].Content != "first final" || secondRunContext[7].Content != "what happened next?" {
		t.Fatalf("cross-Run causal context=%+v", secondRunContext)
	}
	finalCount := 0
	for _, message := range secondRunContext {
		if message.Content == "first final" {
			finalCount++
		}
	}
	if finalCount != 1 {
		t.Fatalf("first final projected %d times", finalCount)
	}
}

func TestLaterRunClosesCancelledPartialBatchBeforeModelCall(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "partial-batch-reconciliation@example.com")
	ctx := context.Background()
	firstRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID,
		"0190cdd2-5f2d-7ad8-b3f5-1b588788d021", "start a calculation")
	firstClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || firstClaim.RunID != firstRunID {
		t.Fatalf("first claim=%+v ok=%t err=%v", firstClaim, ok, err)
	}
	proposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "calculate", Input: json.RawMessage(`{"operation":"add","operands":["4","5"]}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil)
	if _, err := runtime.AppendCheckpoint(ctx, attemptFromClaim(firstClaim), proposal); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_runs set status='cancelled',finished_at=now(),updated_at=now() where id=$1`, firstRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update agent_jobs set status='cancelled',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where run_id=$1`, firstRunID); err != nil {
		t.Fatal(err)
	}

	type providerMessage struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	}
	var projected []providerMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providerMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		projected = append([]providerMessage(nil), request.Messages...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"continued safely"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	secondRunID := admitMessageForCompactionTest(t, api, sessionCookie, csrfCookie, chatID,
		"0190cdd2-5f2d-7ad8-b3f5-1b588788d022", "continue safely")
	secondClaim, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || secondClaim.RunID != secondRunID {
		t.Fatalf("second claim=%+v ok=%t err=%v", secondClaim, ok, err)
	}
	registry, err := agent.NewActionRegistry(agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewController(runtime, models.NewBifrostClient(upstream.URL, upstream.Client(), 2048), registry)
	if err := controller.Execute(ctx, attemptFromClaim(secondClaim)); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 5 || projected[2].Role != "assistant" || projected[3].Role != "tool" ||
		projected[3].ToolCallID != "decision:1/action:0" || !strings.Contains(projected[3].Content, agent.ErrorActionInterrupted) ||
		projected[4].Content != "continue safely" {
		t.Fatalf("reconciled context=%+v", projected)
	}
	var resultCount int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from agent_run_checkpoints where run_id=$1 and kind='action_result'`, firstRunID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 {
		t.Fatalf("durable closing Results=%d", resultCount)
	}
}

func admitMessageForCompactionTest(t *testing.T, api *testAPI, sessionCookie, csrfCookie *http.Cookie, chatID, messageID, content string) string {
	t.Helper()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": content,
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &body)
	return body.RunID
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
