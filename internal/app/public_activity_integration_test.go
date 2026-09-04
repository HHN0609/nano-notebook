package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/jackc/pgx/v5"
)

func TestRunProjectionShowsOnlyPendingPublicActivitiesAndDurableTiming(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "public-activity@example.com")
	runID := admitRunForLeaseTest(t, api, sessionCookie, csrfCookie, chatID, "0190cdd2-5f2d-7ad8-b3f5-1b588788d101")
	ctx := context.Background()
	claimed, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(ctx)
	if err != nil || !ok || claimed.RunID != runID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	attempt := attemptFromClaim(claimed)
	runtime := agent.NewPostgresRuntime(api.db.Pool(), "", nil)
	proposal, err := agent.NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{
		{Name: "read_url", Input: json.RawMessage(`{"url":"https://user:secret@example.com/report?q=private#fragment"}`)},
		{Name: "discover_sources", Input: json.RawMessage(`{"queries":["official release notes"]}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attempt, proposal); err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := api.db.Pool().QueryRow(ctx, `select creator_user_id from chat_chats where id=$1`, chatID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var projection agent.RunProjection
	if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		var projectionErr error
		projection, projectionErr = agent.NewStore(tx).ProjectionForUser(ctx, userID, runID)
		return projectionErr
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Run.StartedAt == nil || projection.Run.FinishedAt != nil || len(projection.Run.Activities) != 2 {
		t.Fatalf("run=%+v", projection.Run)
	}
	if projection.Run.Activities[0].Kind != "reading_webpage" || projection.Run.Activities[0].Detail != "example.com/report" ||
		projection.Run.Activities[1].Kind != "discovering_sources" || projection.Run.Activities[1].Detail != "official release notes" {
		t.Fatalf("activities=%+v", projection.Run.Activities)
	}
	encoded, err := json.Marshal(projection.Run.Activities)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"read_url", "discover_sources", "secret", "private", "fragment", "decision:1", "run_"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("Run projection leaked %q: %s", forbidden, encoded)
		}
	}

	completed := agent.ActionResult{Status: agent.ActionSucceeded, Output: json.RawMessage(`{"title":"Public title","document_handle":"rdoc_0123456789abcdef0123456789abcdef"}`)}
	resultCheckpoint, err := agent.NewActionResultCheckpoint(1, 0, "decision:1/action:0", completed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AppendCheckpoint(ctx, attempt, resultCheckpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		var projectionErr error
		projection, projectionErr = agent.NewStore(tx).ProjectionForUser(ctx, userID, runID)
		return projectionErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(projection.Run.Activities) != 1 || projection.Run.Activities[0].Kind != "discovering_sources" {
		t.Fatalf("remaining activities=%+v", projection.Run.Activities)
	}
}
