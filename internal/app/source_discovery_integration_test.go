package app_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
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

	tx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		t.Fatal(err)
	}
	err = sourcediscovery.NewStore(tx).CompleteSearch(ctx, sourcediscovery.CompleteSearchCommand{
		SessionID: "dsc_results",
		Summary:   "Practical guides and production references.",
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
