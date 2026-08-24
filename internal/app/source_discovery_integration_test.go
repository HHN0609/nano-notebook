package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
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
		      ('dscand_import_yes','dsc_import',1,'Import Article','https://import.example/article','import.example/article','Import',true);
	`); err != nil {
		t.Fatal(err)
	}
	remote := &recordingSourceReader{page: webreader.Page{
		Title: "Reader Article", FinalURL: "https://import.example/article",
		Content: "# Imported article\n\nCleaned Reader Markdown.", Engine: "lightweight", WordCount: 5,
	}}
	api.server = app.NewServer(newConfiguredServerConfig(app.Config{
		CookieSecure: false, SourceReader: remote, SourceSnapshots: objectstore.NewMemoryStore(),
	}), api.db)
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
	var sourceTitle string
	if err := api.db.Pool().QueryRow(ctx, `select title from source_sources where id=$1`, linkedSourceID).Scan(&sourceTitle); err != nil {
		t.Fatal(err)
	}
	if sourceTitle != "Import Article" {
		t.Fatalf("Source title = %q, want imported candidate title", sourceTitle)
	}

	if _, err := api.db.Pool().Exec(ctx, `update source_sources set title='import.example' where id=$1`, linkedSourceID); err != nil {
		t.Fatal(err)
	}
	listed := api.getWithCookie(t, "/api/v1/notebooks/"+notebookID+"/sources", owner)
	var listedBody struct {
		Sources []struct {
			Title string `json:"title"`
		} `json:"sources"`
	}
	decodeBody(t, listed, &listedBody)
	if len(listedBody.Sources) != 1 || listedBody.Sources[0].Title != "Import Article" {
		t.Fatalf("historical Source titles = %+v, want candidate title", listedBody.Sources)
	}
	if _, err := api.db.Pool().Exec(ctx, `update source_sources set title='My renamed source' where id=$1`, linkedSourceID); err != nil {
		t.Fatal(err)
	}
	listed = api.getWithCookie(t, "/api/v1/notebooks/"+notebookID+"/sources", owner)
	decodeBody(t, listed, &listedBody)
	if len(listedBody.Sources) != 1 || listedBody.Sources[0].Title != "My renamed source" {
		t.Fatalf("renamed Source titles = %+v, want user title", listedBody.Sources)
	}
}

func TestSourceDiscoveryDropsCandidateWhenImportAdmissionFails(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "discovery-import-failure@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-import-failure")
	ownerID := sourceTestUserID(t, api, "discovery-import-failure@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_import_failure", JobID: "dscjob_import_failure", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "blocked source",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update source_discovery_sessions set status='ready',completed_at=now() where id='dsc_import_failure'`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `update source_discovery_jobs set status='succeeded' where id='dscjob_import_failure'`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_discovery_candidates(id,session_id,ordinal,title,canonical_url,display_url,snippet,selected)
		values('dscand_import_failure','dsc_import_failure',0,'Blocked','https://blocked.example','blocked.example','Blocked',true)
	`); err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(newConfiguredServerConfig(app.Config{
		CookieSecure: false, SourceReader: &recordingSourceReader{err: webreader.ErrUnsafeDestination},
		SourceSnapshots: objectstore.NewMemoryStore(),
	}), api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t,
		"/api/v1/source-discovery-sessions/dsc_import_failure/imports", map[string]any{},
		owner, csrf, csrf.Value, "discovery-import-failure-1",
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	var candidateCount int
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from source_discovery_candidates where id='dscand_import_failure'`).Scan(&candidateCount); err != nil {
		t.Fatal(err)
	}
	if candidateCount != 0 {
		t.Fatalf("failed candidate count=%d", candidateCount)
	}
}

func TestSourceDiscoveryImportDeduplicatesNormalizedRedirectTargetAcrossSessions(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "discovery-redirect-dedupe@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-redirect-dedupe")
	ownerID := sourceTestUserID(t, api, "discovery-redirect-dedupe@example.com")
	ctx := context.Background()
	for index, rawURL := range []string{"https://first.example/article", "https://second.example/redirect"} {
		sessionID := fmt.Sprintf("dsc_redirect_%d", index)
		jobID := fmt.Sprintf("dscjob_redirect_%d", index)
		candidateID := fmt.Sprintf("dscand_redirect_%d", index)
		if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
			_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
				ID: sessionID, JobID: jobID, NotebookID: notebookID, UserID: ownerID,
				Origin: sourcediscovery.OriginManual, Query: "redirected material",
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := api.db.Pool().Exec(ctx, `update source_discovery_sessions set status='ready',completed_at=now() where id=$1`, sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := api.db.Pool().Exec(ctx, `update source_discovery_jobs set status='succeeded' where id=$1`, jobID); err != nil {
			t.Fatal(err)
		}
		if _, err := api.db.Pool().Exec(ctx, `
			insert into source_discovery_candidates(id,session_id,ordinal,title,canonical_url,display_url,snippet,selected)
			values($1,$2,0,'Same article',$3,'example/article','same',true)
		`, candidateID, sessionID, rawURL); err != nil {
			t.Fatal(err)
		}
	}
	remote := &recordingSourceReader{page: webreader.Page{
		Title: "Article", FinalURL: "https://Final.Example/article?utm_source=brave&b=2&a=1",
		Content: "# Article\n\nThis cleaned article has enough meaningful primary content for source processing and retrieval.",
		Engine:  "lightweight", WordCount: 14,
	}}
	objects := objectstore.NewMemoryStore()
	api.server = app.NewServer(newConfiguredServerConfig(app.Config{CookieSecure: false, SourceReader: remote, SourceSnapshots: objects}), api.db)
	api.handler = api.server.Handler()

	var sourceIDs []string
	for index := range 2 {
		response := api.postJSONWithCookieAndCSRF(t,
			fmt.Sprintf("/api/v1/source-discovery-sessions/dsc_redirect_%d/imports", index), map[string]any{},
			owner, csrf, csrf.Value, fmt.Sprintf("redirect-batch-%d", index),
		)
		if response.Code != http.StatusAccepted {
			t.Fatalf("import %d status=%d body=%s", index, response.Code, response.Body.String())
		}
		var body struct {
			Outcomes []struct {
				SourceID string `json:"source_id"`
			} `json:"outcomes"`
		}
		decodeBody(t, response, &body)
		if len(body.Outcomes) != 1 || body.Outcomes[0].SourceID == "" {
			t.Fatalf("import %d outcomes=%+v", index, body.Outcomes)
		}
		sourceIDs = append(sourceIDs, body.Outcomes[0].SourceID)
	}
	if sourceIDs[0] != sourceIDs[1] {
		t.Fatalf("redirect Sources=%v, want one Source", sourceIDs)
	}
	var sourceCount, jobCount int
	var finalIdentity string
	if err := api.db.Pool().QueryRow(ctx, `select count(*),max(final_url_identity) from source_sources where notebook_id=$1`, notebookID).Scan(&sourceCount, &finalIdentity); err != nil {
		t.Fatal(err)
	}
	if err := api.db.Pool().QueryRow(ctx, `select count(*) from source_processing_jobs where notebook_id=$1`, notebookID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 || jobCount != 1 || finalIdentity != "https://final.example/article?a=1&b=2" {
		t.Fatalf("source count=%d jobs=%d final identity=%q", sourceCount, jobCount, finalIdentity)
	}
	stored, err := objects.List(ctx, "sources/", "", 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored snapshots=%+v err=%v", stored, err)
	}
}

func TestSourceDiscoveryRejectsForeignOriginChat(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-origin-owner@example.com")
	other := api.register(t, "discovery-origin-other@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-origin-owner")
	otherNotebookID := createSourceTestNotebook(t, api, other, "discovery-origin-other")
	ownerID := sourceTestUserID(t, api, "discovery-origin-owner@example.com")
	otherID := sourceTestUserID(t, api, "discovery-origin-other@example.com")
	var foreignChatID string
	if err := api.db.WithRequestPrincipal(context.Background(), otherID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `insert into chat_chats(id,notebook_id,creator_user_id,title) values('chat_foreign_origin',$1,$2,'Foreign') returning id`, otherNotebookID, otherID).Scan(&foreignChatID)
	}); err != nil {
		t.Fatal(err)
	}
	err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(context.Background(), sourcediscovery.CreateSessionCommand{
			ID: "dsc_foreign_origin", JobID: "dscjob_foreign_origin", NotebookID: notebookID, UserID: ownerID,
			OriginChatID: &foreignChatID, Origin: sourcediscovery.OriginManual, Query: "film",
		})
		return err
	})
	if !errors.Is(err, sourcediscovery.ErrInvalid) {
		t.Fatalf("foreign origin chat error=%v, want ErrInvalid", err)
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
		      ('dscand_select_2','dsc_selection',1,'Second','https://second.example','second.example','Second'),
		      ('dscand_select_failed','dsc_selection',2,'Failed','https://failed.example','failed.example','Failed');
		update source_discovery_candidates set status='import_failed',selected=false where id='dscand_select_failed';
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

	response = api.patchJSONWithCookie(t, "/api/v1/source-discovery-sessions/dsc_selection/selection",
		map[string]any{"candidate_ids": []string{"dscand_select_failed"}}, owner,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("failed candidate selection status = %d, body = %s", response.Code, response.Body.String())
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
	if session.Status != sourcediscovery.StatusReady || session.Summary == nil || *session.Summary == "" || len(session.Candidates) != 1 || session.Candidates[0].Title != "Film Guide" {
		t.Fatalf("processed Session = %+v", session)
	}
}

func TestSourceDiscoveryProcessorPublishesOnlyValidatedCandidates(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-validation@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-validation")
	ownerID := sourceTestUserID(t, api, "discovery-validation@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_validation", JobID: "dscjob_validation", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "validated sources",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	provider := &stubWebSearchProvider{candidates: []websearch.Candidate{
		{Title: "Usable", URL: "https://usable.example/article", DisplayURL: "usable.example/article", Rank: 1},
		{Title: "Blocked", URL: "https://blocked.example/article", DisplayURL: "blocked.example/article", Rank: 2},
	}}
	validator := &stubDiscoveryCandidateValidator{accepted: map[string]bool{"https://usable.example/article": true}}
	processor := sourcediscovery.NewProcessorWithValidator(
		api.db.Pool(), sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second), provider, validator,
	)
	if processed, err := processor.ProcessNext(ctx); err != nil || !processed {
		t.Fatalf("ProcessNext processed=%v err=%v", processed, err)
	}
	var session sourcediscovery.Session
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).GetSession(ctx, "dsc_validation")
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(session.Candidates) != 1 || session.Candidates[0].Title != "Usable" {
		t.Fatalf("validated candidates = %+v", session.Candidates)
	}
	if len(validator.urls) != 2 {
		t.Fatalf("validated URLs = %+v", validator.urls)
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

type stubDiscoveryCandidateValidator struct {
	accepted map[string]bool
	urls     []string
}

func (v *stubDiscoveryCandidateValidator) Validate(_ context.Context, rawURL string) bool {
	v.urls = append(v.urls, rawURL)
	return v.accepted[rawURL]
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

func TestSourceDiscoveryFailedSessionCanBeRetried(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-retry@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-retry")
	created := api.postJSONWithCookie(t, "/api/v1/notebooks/"+notebookID+"/source-discovery-sessions", map[string]any{"query": "film editing"}, owner, "")
	var body struct {
		Session sourcediscovery.Session `json:"session"`
	}
	decodeBody(t, created, &body)
	if _, err := api.db.Pool().Exec(context.Background(), `update source_discovery_sessions set status='failed',error_code='discovery_timeout',completed_at=now() where id=$1`, body.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `update source_discovery_jobs set status='failed',last_error_code='discovery_timeout' where session_id=$1`, body.Session.ID); err != nil {
		t.Fatal(err)
	}
	retried := api.postJSONWithCookie(t, "/api/v1/source-discovery-sessions/"+body.Session.ID+"/retry", map[string]any{}, owner, "retry-discovery")
	if retried.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
	}
	replayed := api.postJSONWithCookie(t, "/api/v1/source-discovery-sessions/"+body.Session.ID+"/retry", map[string]any{}, owner, "retry-discovery")
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("retry replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	lease, ok, err := sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second).Claim(context.Background())
	if err != nil || !ok || lease.SessionID != body.Session.ID || lease.Query != "film editing" {
		t.Fatalf("retry lease=%+v ok=%v err=%v", lease, ok, err)
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

func TestSourceDiscoveryCompletionMarksExistingNotebookURLImported(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-existing-url@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-existing-url")
	ownerID := sourceTestUserID(t, api, "discovery-existing-url@example.com")
	ctx := context.Background()
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_sources(
			id,notebook_id,input_kind,format,title,media_type,byte_size,content_sha256,original_object_key,
			origin_url,final_url,origin_url_identity,final_url_identity,state
		) values('src_existing_web',$1,'url','html','Existing','text/html',1,$2,'sources/existing/original',
			'https://example.com/article','https://example.com/article','https://example.com/article','https://example.com/article','ready')
	`, notebookID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_existing_web", JobID: "dscjob_existing_web", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "existing web",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := sourcediscovery.NewQueue(api.db.Pool(), 30*time.Second).Claim(ctx)
	if err != nil || !ok {
		t.Fatalf("lease=%+v ok=%v err=%v", lease, ok, err)
	}
	tx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		t.Fatal(err)
	}
	if err := sourcediscovery.NewStore(tx).CompleteSearch(ctx, sourcediscovery.CompleteSearchCommand{
		SessionID: lease.SessionID, JobID: lease.ID, LeaseToken: lease.LeaseToken,
		Candidates: []sourcediscovery.DiscoveredCandidate{{ID: "dscand_existing_web", Title: "Existing", URL: "https://example.com/article?utm_source=brave", DisplayURL: "example.com/article"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var selected bool
	var sourceID *string
	if err := api.db.Pool().QueryRow(ctx, `select status,selected,source_id from source_discovery_candidates where id='dscand_existing_web'`).Scan(&status, &selected, &sourceID); err != nil {
		t.Fatal(err)
	}
	if status != "imported" || selected || sourceID == nil || *sourceID != "src_existing_web" {
		t.Fatalf("candidate status=%s selected=%v source=%v", status, selected, sourceID)
	}
}
