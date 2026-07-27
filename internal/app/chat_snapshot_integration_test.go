package app_test

import (
	"context"
	"net/http"
	"testing"
)

func TestChatSnapshotRestoresDurableMessagesAndTheNewestRunForEachInput(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "chat-snapshot@example.com")
	const messageID = "0190cdd2-5f2d-7ad8-b3f5-1b588788c006"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id":      messageID,
		"content": "Restore this durable message.",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admission status = %d, body = %s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)

	ctx := context.Background()
	if _, err := api.db.Pool().Exec(ctx, `
		update agent_runs
		set status = 'cancelled', finished_at = now(), updated_at = now()
		where id = $1`, admittedBody.RunID); err != nil {
		t.Fatal(err)
	}
	const retryRunID = "run_snapshot_retry"
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_runs(id, user_id, chat_id, input_message_id, status, model, prompt_version, created_at)
		select $1, user_id, chat_id, input_message_id, 'queued', model, prompt_version, created_at + interval '1 second'
		from agent_runs where id = $2`, retryRunID, admittedBody.RunID); err != nil {
		t.Fatal(err)
	}

	snapshot := api.getWithCookie(t, "/api/v1/chats/"+chatID, sessionCookie)
	if snapshot.Code != http.StatusOK {
		t.Fatalf("chat snapshot status = %d, body = %s", snapshot.Code, snapshot.Body.String())
	}
	var body struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
		Messages []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Runs []struct {
			ID             string `json:"id"`
			InputMessageID string `json:"input_message_id"`
			Status         string `json:"status"`
		} `json:"runs"`
	}
	decodeBody(t, snapshot, &body)
	if body.Chat.ID != chatID || len(body.Messages) != 1 || body.Messages[0].ID != messageID || body.Messages[0].Role != "user" || body.Messages[0].Content != "Restore this durable message." {
		t.Fatalf("unexpected durable snapshot: %+v", body)
	}
	if len(body.Runs) != 1 || body.Runs[0].ID != retryRunID || body.Runs[0].InputMessageID != messageID || body.Runs[0].Status != "queued" {
		t.Fatalf("Run projections = %+v, want newest queued Run %q for input %q", body.Runs, retryRunID, messageID)
	}
}

func TestChatSnapshotOrdersLatestRunsByInputMessageChronology(t *testing.T) {
	api, sessionCookie, _, chatID := newChatFixture(t, "chat-snapshot-run-order@example.com")
	ctx := context.Background()

	var userID, notebookID string
	if err := api.db.Pool().QueryRow(ctx, `
		select creator_user_id, notebook_id
		from chat_chats
		where id = $1`, chatID).Scan(&userID, &notebookID); err != nil {
		t.Fatal(err)
	}

	const (
		olderMessageID = "ffffffff-ffff-4fff-bfff-ffffffffffff"
		newerMessageID = "00000000-0000-4000-8000-000000000001"
		olderSessionID = "dss_snapshot_older"
		newerSessionID = "dss_snapshot_newer"
	)
	if _, err := api.db.Pool().Exec(ctx, `
		insert into chat_messages(id, chat_id, role, content, created_at) values
			($1, $3, 'user', 'older request', now() - interval '2 minutes'),
			($2, $3, 'user', 'newer request', now() - interval '1 minute')
	`, olderMessageID, newerMessageID, chatID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_discovery_sessions(
			id, notebook_id, user_id, origin_chat_id, origin, query, status, created_at, updated_at, completed_at
		) values
			($1, $3, $4, $5, 'research_agent', 'older request', 'ready', now() - interval '2 minutes', now(), now()),
			($2, $3, $4, $5, 'research_agent', 'newer request', 'ready', now() - interval '1 minute', now(), now())
	`, olderSessionID, newerSessionID, notebookID, userID, chatID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into agent_runs(
			id, user_id, chat_id, input_message_id, status, model, prompt_version, discovery_session_id, created_at
		) values
			('run_snapshot_older_initial', $6, $3, $1, 'cancelled', 'test-model', 'test-prompt', $4, now() - interval '2 minutes'),
			('run_snapshot_newer', $6, $3, $2, 'completed', 'test-model', 'test-prompt', $5, now() - interval '1 minute'),
			('run_snapshot_older_retry', $6, $3, $1, 'completed', 'test-model', 'test-prompt', $4, now())
	`, olderMessageID, newerMessageID, chatID, olderSessionID, newerSessionID, userID); err != nil {
		t.Fatal(err)
	}

	snapshot := api.getWithCookie(t, "/api/v1/chats/"+chatID, sessionCookie)
	if snapshot.Code != http.StatusOK {
		t.Fatalf("chat snapshot status = %d, body = %s", snapshot.Code, snapshot.Body.String())
	}
	var body struct {
		Runs []struct {
			ID                 string  `json:"id"`
			InputMessageID     string  `json:"input_message_id"`
			DiscoverySessionID *string `json:"discovery_session_id"`
		} `json:"runs"`
	}
	decodeBody(t, snapshot, &body)
	if len(body.Runs) != 2 {
		t.Fatalf("Run projections = %+v, want one latest Run per input", body.Runs)
	}
	if body.Runs[0].ID != "run_snapshot_older_retry" || body.Runs[0].InputMessageID != olderMessageID {
		t.Fatalf("first Run = %+v, want latest retry for older input", body.Runs[0])
	}
	if body.Runs[1].ID != "run_snapshot_newer" || body.Runs[1].InputMessageID != newerMessageID || body.Runs[1].DiscoverySessionID == nil || *body.Runs[1].DiscoverySessionID != newerSessionID {
		t.Fatalf("last Run = %+v, want newer input with discovery session %q", body.Runs[1], newerSessionID)
	}
}
