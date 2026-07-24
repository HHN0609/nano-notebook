package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/fetcher"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5"
)

func TestSourceDiscoverySessionIsPrivateDurableAndMaintainerOnly(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-owner@example.com")
	api.register(t, "discovery-viewer@example.com")
	api.register(t, "discovery-intruder@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-private")
	ownerID := sourceTestUserID(t, api, "discovery-owner@example.com")
	viewerID := sourceTestUserID(t, api, "discovery-viewer@example.com")
	intruderID := sourceTestUserID(t, api, "discovery-intruder@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `
		insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'viewer')
	`, notebookID, viewerID); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var created sourcediscovery.Session
	err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		var err error
		created, err = sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID:         "dsc_private",
			JobID:      "dscjob_private",
			NotebookID: notebookID,
			UserID:     ownerID,
			Origin:     sourcediscovery.OriginManual,
			Query:      "how to make a film",
		})
		return err
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if created.Status != sourcediscovery.StatusSearching || created.Query != "how to make a film" {
		t.Fatalf("created Session = %+v", created)
	}
	var jobStatus string
	if err := api.db.Pool().QueryRow(ctx, `select status from source_discovery_jobs where id='dscjob_private'`).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "queued" {
		t.Fatalf("job status = %q, want queued", jobStatus)
	}

	for name, userID := range map[string]string{"viewer": viewerID, "intruder": intruderID} {
		t.Run(name+" cannot read private session", func(t *testing.T) {
			err := api.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
				_, err := sourcediscovery.NewStore(tx).GetSession(ctx, created.ID)
				return err
			})
			if !errors.Is(err, sourcediscovery.ErrNotFound) {
				t.Fatalf("GetSession error = %v, want ErrNotFound", err)
			}
		})
	}

	err = api.db.WithRequestPrincipal(ctx, viewerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_viewer", JobID: "dscjob_viewer", NotebookID: notebookID, UserID: viewerID,
			Origin: sourcediscovery.OriginManual, Query: "film",
		})
		return err
	})
	if !errors.Is(err, sourcediscovery.ErrForbidden) {
		t.Fatalf("viewer CreateSession error = %v, want ErrForbidden", err)
	}
}

func TestSourceDiscoveryImportsPersistedSelectionThroughURLSourcePipeline(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "discovery-import@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-import")
	ownerID := sourceTestUserID(t, api, "discovery-import@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_import", JobID: "dscjob_import", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "film",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		update source_discovery_sessions set status='ready',completed_at=now() where id='dsc_import';
		update source_discovery_jobs set status='succeeded' where id='dscjob_import';
		insert into source_discovery_candidates(id,session_id,ordinal,title,canonical_url,display_url,snippet,selected)
		values('dscand_import_skip','dsc_import',0,'Skip','https://skip.example','skip.example','Skip',false),
		      ('dscand_import_yes','dsc_import',1,'Import','https://import.example/article','import.example/article','Import',true);
	`); err != nil {
		t.Fatal(err)
	}
	payload := []byte("<main>immutable imported article</main>")
	digest := sha256.Sum256(payload)
	remote := &recordingSourceFetcher{snapshot: fetcher.Snapshot{
		FinalURL: "https://import.example/article", MediaType: "text/html", Payload: payload,
		ContentSHA256: hex.EncodeToString(digest[:]),
	}}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, SourceFetcher: remote, SourceSnapshots: objectstore.NewMemoryStore(),
	}, api.db)
	api.handler = api.server.Handler()

	response := api.postJSONWithCookieAndCSRF(t,
		"/api/v1/source-discovery-sessions/dsc_import/imports", map[string]any{},
		owner, csrf, csrf.Value, "discovery-import-batch-1",
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Outcomes []struct {
			CandidateID string `json:"candidate_id"`
			Status      string `json:"status"`
			SourceID    string `json:"source_id"`
		} `json:"outcomes"`
	}
	decodeBody(t, response, &body)
	if len(body.Outcomes) != 1 || body.Outcomes[0].CandidateID != "dscand_import_yes" ||
		body.Outcomes[0].Status != "imported" || body.Outcomes[0].SourceID == "" || remote.calls != 1 {
		t.Fatalf("import outcomes = %+v, fetch calls = %d", body.Outcomes, remote.calls)
	}
	var candidateStatus, linkedSourceID string
	if err := api.db.Pool().QueryRow(ctx, `
		select status,source_id from source_discovery_candidates where id='dscand_import_yes'
	`).Scan(&candidateStatus, &linkedSourceID); err != nil {
		t.Fatal(err)
	}
	if candidateStatus != "imported" || linkedSourceID != body.Outcomes[0].SourceID {
		t.Fatalf("candidate status/source = %q/%q", candidateStatus, linkedSourceID)
	}
}

func TestSourceDiscoverySelectionReplacementPersists(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-selection@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-selection")
	ownerID := sourceTestUserID(t, api, "discovery-selection@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_selection", JobID: "dscjob_selection", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "film",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		update source_discovery_sessions set status='ready',completed_at=now() where id='dsc_selection';
		update source_discovery_jobs set status='succeeded' where id='dscjob_selection';
		insert into source_discovery_candidates(id,session_id,ordinal,title,canonical_url,display_url,snippet)
		values('dscand_select_1','dsc_selection',0,'First','https://first.example','first.example','First'),
		      ('dscand_select_2','dsc_selection',1,'Second','https://second.example','second.example','Second');
	`); err != nil {
		t.Fatal(err)
	}

	response := api.patchJSONWithCookie(t, "/api/v1/source-discovery-sessions/dsc_selection/selection",
		map[string]any{"candidate_ids": []string{"dscand_select_2"}}, owner,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("selection status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Session sourcediscovery.Session `json:"session"`
	}
	decodeBody(t, response, &body)
	if body.Session.Candidates[0].Selected || !body.Session.Candidates[1].Selected {
		t.Fatalf("selection = %+v", body.Session.Candidates)
	}
}

func TestSourceDiscoveryProcessorExecutesQueuedBraveCompatibleSearch(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-processor@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-processor")
	ownerID := sourceTestUserID(t, api, "discovery-processor@example.com")
	ctx := context.Background()
	err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_processor", JobID: "dscjob_processor", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "film production",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	provider := &stubWebSearchProvider{candidates: []websearch.Candidate{{
		Title: "Film Guide", URL: "https://example.com/film", DisplayURL: "example.com/film",
		Description: "A practical guide.", Rank: 1,
	}}}
	processor := sourcediscovery.NewProcessor(
		api.db.Pool(), sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second), provider,
	)
	processed, err := processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !processed {
		t.Fatal("ProcessNext reported no queued work")
	}
	if len(provider.requests) != 1 || provider.requests[0].Query != "film production" || provider.requests[0].Count != 10 {
		t.Fatalf("provider requests = %+v", provider.requests)
	}

	var session sourcediscovery.Session
	err = api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		var err error
		session, err = sourcediscovery.NewStore(tx).GetSession(ctx, "dsc_processor")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != sourcediscovery.StatusReady || len(session.Candidates) != 1 || session.Candidates[0].Title != "Film Guide" {
		t.Fatalf("processed Session = %+v", session)
	}
}

func TestSourceDiscoveryProcessorPersistsSafeProviderFailure(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-failure@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-failure")
	ownerID := sourceTestUserID(t, api, "discovery-failure@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_failure", JobID: "dscjob_failure", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "film production",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	processor := sourcediscovery.NewProcessor(
		api.db.Pool(), sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second),
		&stubWebSearchProvider{err: websearch.ErrRateLimited},
	)
	processed, err := processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !processed {
		t.Fatal("ProcessNext reported no queued work")
	}
	var session sourcediscovery.Session
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		var err error
		session, err = sourcediscovery.NewStore(tx).GetSession(ctx, "dsc_failure")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if session.Status != sourcediscovery.StatusFailed || session.ErrorCode == nil || *session.ErrorCode != "discovery_rate_limited" {
		t.Fatalf("failed Session = %+v", session)
	}
}

type stubWebSearchProvider struct {
	requests   []websearch.Request
	candidates []websearch.Candidate
	err        error
}

func (p *stubWebSearchProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	return p.candidates, p.err
}

func (api *testAPI) patchJSONWithCookie(t *testing.T, path string, payload map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	csrf := api.csrfBySession[cookie.Value]
	request.Header.Set("X-CSRF-Token", csrf.Value)
	request.AddCookie(cookie)
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	return response
}

func TestSourceDiscoveryCreateAndRestoreHTTP(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-http-owner@example.com")
	viewer := api.register(t, "discovery-http-viewer@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-http")
	viewerID := sourceTestUserID(t, api, "discovery-http-viewer@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `
		insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'viewer')
	`, notebookID, viewerID); err != nil {
		t.Fatal(err)
	}

	created := api.postJSONWithCookie(t,
		"/api/v1/notebooks/"+notebookID+"/source-discovery-sessions",
		map[string]any{"query": "  how to make a film  "}, owner, "",
	)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var createBody struct {
		Session sourcediscovery.Session `json:"session"`
	}
	decodeBody(t, created, &createBody)
	if createBody.Session.Query != "how to make a film" || createBody.Session.Status != sourcediscovery.StatusSearching {
		t.Fatalf("created Session = %+v", createBody.Session)
	}

	restored := api.getWithCookie(t,
		"/api/v1/notebooks/"+notebookID+"/source-discovery-sessions/latest", owner,
	)
	if restored.Code != http.StatusOK {
		t.Fatalf("latest status = %d, body = %s", restored.Code, restored.Body.String())
	}
	var restoreBody struct {
		Session sourcediscovery.Session `json:"session"`
	}
	decodeBody(t, restored, &restoreBody)
	if restoreBody.Session.ID != createBody.Session.ID {
		t.Fatalf("restored Session = %+v, want %s", restoreBody.Session, createBody.Session.ID)
	}

	viewerCreate := api.postJSONWithCookie(t,
		"/api/v1/notebooks/"+notebookID+"/source-discovery-sessions",
		map[string]any{"query": "film"}, viewer, "",
	)
	if viewerCreate.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, body = %s", viewerCreate.Code, viewerCreate.Body.String())
	}
}

func TestSourceDiscoveryCompletionCanonicalizesDeduplicatesAndSelectsCandidates(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-results@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-results")
	ownerID := sourceTestUserID(t, api, "discovery-results@example.com")
	ctx := context.Background()

	err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_results", JobID: "dscjob_results", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "film production",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	queue := sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second)
	lease, ok, err := queue.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || lease.SessionID != "dsc_results" || lease.Query != "film production" {
		t.Fatalf("claimed Lease = %+v, ok = %t", lease, ok)
	}
	staleTx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleTx.Exec(ctx, `set local role nano_worker`); err != nil {
		t.Fatal(err)
	}
	staleErr := sourcediscovery.NewStore(staleTx).CompleteSearch(ctx, sourcediscovery.CompleteSearchCommand{
		SessionID: "dsc_results", JobID: lease.ID, LeaseToken: "00000000-0000-0000-0000-000000000000",
	})
	_ = staleTx.Rollback(ctx)
	if !errors.Is(staleErr, sourcediscovery.ErrLeaseLost) {
		t.Fatalf("stale CompleteSearch error = %v, want ErrLeaseLost", staleErr)
	}

	tx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		t.Fatal(err)
	}
	err = sourcediscovery.NewStore(tx).CompleteSearch(ctx, sourcediscovery.CompleteSearchCommand{
		SessionID: "dsc_results", JobID: lease.ID, LeaseToken: lease.LeaseToken,
		Summary: "Practical guides and production references.",
		Candidates: []sourcediscovery.DiscoveredCandidate{
			{ID: "dscand_first", Title: "Film Guide", URL: "https://Example.COM/guide?utm_source=brave#intro", DisplayURL: "example.com/guide", Snippet: "A guide.", ProviderRank: 1},
			{ID: "dscand_duplicate", Title: "Duplicate", URL: "https://example.com/guide", DisplayURL: "example.com/guide", Snippet: "Same URL.", ProviderRank: 2},
			{ID: "dscand_second", Title: "Lighting", URL: "https://lighting.example/tutorial", DisplayURL: "lighting.example/tutorial", Snippet: "Lighting.", ProviderRank: 3},
		},
	})
	if err != nil {
		t.Fatalf("CompleteSearch: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var session sourcediscovery.Session
	err = api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		var err error
		session, err = sourcediscovery.NewStore(tx).GetSession(ctx, "dsc_results")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != sourcediscovery.StatusReady || session.CompletedAt == nil || session.Summary == nil {
		t.Fatalf("completed Session = %+v", session)
	}
	if len(session.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want two canonical URLs", session.Candidates)
	}
	if got, want := session.Candidates[0].CanonicalURL, "https://example.com/guide"; got != want {
		t.Fatalf("canonical URL = %q, want %q", got, want)
	}
	if !session.Candidates[0].Selected || !session.Candidates[1].Selected {
		t.Fatalf("Candidates must default selected: %+v", session.Candidates)
	}
	if session.Candidates[0].Ordinal != 0 || session.Candidates[1].Ordinal != 1 {
		t.Fatalf("Candidate ordinals = %+v", session.Candidates)
	}
}
