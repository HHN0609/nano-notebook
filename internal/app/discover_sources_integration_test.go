package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type discoverSourcesProvider struct {
	requests []websearch.Request
	results  map[string][]websearch.Candidate
}

func (p *discoverSourcesProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	return p.results[request.Query], nil
}

type selectiveDiscoveryValidator struct{ rejected string }

func (v selectiveDiscoveryValidator) Validate(_ context.Context, rawURL string) bool {
	return rawURL != v.rejected
}

func TestDiscoverSourcesToolPersistsDeduplicatedSessionAndReplaysWithoutResearchChild(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "discover-tool@example.com")
	ctx := context.Background()
	var notebookID, userID string
	if err := api.db.Pool().QueryRow(ctx, `select notebook_id,creator_user_id from chat_chats where id=$1`, chatID).Scan(&notebookID, &userID); err != nil {
		t.Fatal(err)
	}
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788d001", "content": "What changed this week?", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admit status=%d body=%s", admitted.Code, admitted.Body.String())
	}
	var admission struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admission)
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_sources(
			id,notebook_id,input_kind,format,title,media_type,byte_size,content_sha256,original_object_key,
			origin_url,final_url,origin_url_identity,final_url_identity,state
		) values('src_discover_existing',$1,'url','html','Existing source','text/html',1,$2,'sources/discover-existing/original',
			'https://example.com/existing','https://example.com/existing','https://example.com/existing','https://example.com/existing','ready')
	`, notebookID, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into chat_source_selections(chat_id,source_id,selected,explicit)
		values($1,'src_discover_existing',true,true)
	`, chatID); err != nil {
		t.Fatal(err)
	}

	provider := &discoverSourcesProvider{results: map[string][]websearch.Candidate{
		"recent changes": {
			{Title: "Existing source", URL: "https://example.com/existing?utm_source=search", DisplayURL: "example.com/existing", Description: "already here", Rank: 1},
			{Title: "Novel source", URL: "https://novel.example/report", DisplayURL: "novel.example/report", Description: "new", Rank: 2},
		},
		"official update": {
			{Title: "Novel duplicate", URL: "https://novel.example/report#section", DisplayURL: "novel.example/report", Description: "same", Rank: 1},
			{Title: "Rejected", URL: "https://blocked.example/report", DisplayURL: "blocked.example/report", Description: "blocked", Rank: 2},
		},
	}}
	backend := agent.NewPostgresDiscoverSourcesBackend(api.db.Pool(), provider, selectiveDiscoveryValidator{rejected: "https://blocked.example/report"})
	request := agent.DiscoverSourcesRequest{
		RunID: admission.RunID, ActionID: "decision:1/action:1", UserID: userID, ChatID: chatID,
		Queries: []string{"recent changes", "official update"},
	}
	result, err := backend.Discover(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || result.SessionID == "" || result.NovelCandidateCount != 1 ||
		result.ExistingCandidateCount != 1 || result.ExistingSelectedCount != 1 {
		t.Fatalf("result=%+v", result)
	}
	if len(provider.requests) != 2 || provider.requests[0].Count != 10 || provider.requests[1].Count != 10 {
		t.Fatalf("provider requests=%+v", provider.requests)
	}

	replayed, err := backend.Discover(ctx, request)
	if err != nil || replayed != result {
		t.Fatalf("replayed=%+v err=%v want=%+v", replayed, err, result)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("replay called provider again: %+v", provider.requests)
	}

	var origin, status, linkedSession string
	var candidateCount int
	if err := api.db.Pool().QueryRow(ctx, `
		select session.origin,session.status,run.discovery_session_id,
			(select count(*) from source_discovery_candidates candidate where candidate.session_id=session.id)
		from source_discovery_sessions session join agent_runs run on run.id=$2
		where session.id=$1
	`, result.SessionID, admission.RunID).Scan(&origin, &status, &linkedSession, &candidateCount); err != nil {
		t.Fatal(err)
	}
	if origin != "chat_agent" || status != "ready" || linkedSession != result.SessionID || candidateCount != 2 {
		t.Fatalf("session origin=%s status=%s linked=%s candidates=%d", origin, status, linkedSession, candidateCount)
	}
}
