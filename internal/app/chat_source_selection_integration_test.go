package app_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcejobs"
)

func TestChatSourceSelectionDefaultsThenPersistsReplacementAndDrivesAdmission(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "chat-selection@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "chat-selection")
	installReadyEvidenceSetFixture(t, api, notebookID, "src_initial_ready", "evr_initial_ready", "src_later_ready", "evr_later_ready")
	if _, err := api.db.Pool().Exec(context.Background(), `update source_sources set state='uploaded' where id='src_later_ready'`); err != nil {
		t.Fatal(err)
	}

	created := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "chat-selection-create")
	if created.Code != http.StatusCreated {
		t.Fatalf("create Chat status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, created, &createdBody)
	chatID := createdBody.Chat.ID

	assertChatSelection(t, api, owner, chatID, []string{"src_initial_ready"})
	if _, err := api.db.Pool().Exec(context.Background(), `update source_sources set state='ready' where id='src_later_ready'`); err != nil {
		t.Fatal(err)
	}
	assertChatSelection(t, api, owner, chatID, []string{"src_initial_ready"})

	replaced := api.patchJSONWithCookie(t, "/api/v1/chats/"+chatID+"/source-selection", map[string]any{
		"source_ids": []string{"src_later_ready"},
	}, owner)
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace selection status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	assertChatSelection(t, api, owner, chatID, []string{"src_later_ready"})

	messageID := "0190cdd2-5f2d-7ad8-b3f5-1b588788c0b1"
	admitted := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": messageID, "content": "Use the persisted source selection.", "time_zone": "Asia/Shanghai",
	}, owner, csrf, csrf.Value, "")
	if admitted.Code != http.StatusAccepted {
		t.Fatalf("admit status=%d body=%s", admitted.Code, admitted.Body.String())
	}
	var admittedBody struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, admitted, &admittedBody)
	var pinned string
	if err := api.db.Pool().QueryRow(context.Background(), `select source_id from agent_run_evidence_set where run_id=$1`, admittedBody.RunID).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned != "src_later_ready" {
		t.Fatalf("pinned source=%q", pinned)
	}
}

func TestDiscoveryImportedSourceAutoSelectsOnlyOriginChatAtReadyAndHonorsExplicitDeselection(t *testing.T) {
	api := newTestAPI(t)
	owner, csrf := api.registerWithCSRF(t, "origin-chat-selection@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "origin-chat-selection")
	first := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "origin-chat-first")
	second := api.postJSONWithCookieAndCSRF(t, "/api/v1/notebooks/"+notebookID+"/chats", map[string]any{}, owner, csrf, csrf.Value, "origin-chat-second")
	var firstBody, secondBody struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	decodeBody(t, first, &firstBody)
	decodeBody(t, second, &secondBody)

	ctx := context.Background()
	ownerID := sourceTestUserID(t, api, "origin-chat-selection@example.com")
	seedSourceProcessingJob(t, api, ownerID, notebookID, "src_origin_select", "srcjob_origin_select", "8")
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_discovery_sessions(id,notebook_id,user_id,origin_chat_id,origin,query,status,completed_at)
		values('dsc_origin_select',$1,$2,$3,'manual','origin source','ready',now())
	`, notebookID, ownerID, firstBody.Chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		insert into source_discovery_candidates(id,session_id,ordinal,title,canonical_url,display_url,snippet,status,source_id)
		values('dscand_origin_select','dsc_origin_select',0,'Origin','https://example.com/origin','example.com/origin','','imported','src_origin_select')
	`); err != nil {
		t.Fatal(err)
	}
	installBuildingEvidenceProjection(t, api, notebookID, "src_origin_select", "evr_origin_select")
	queue := sourcejobs.NewQueue(api.db.Pool(), time.Minute)
	lease, ok, err := queue.Claim(ctx)
	if err != nil || !ok || lease.SourceID != "src_origin_select" {
		t.Fatalf("claim=%+v ok=%v err=%v", lease, ok, err)
	}
	advanceSourceToVerifying(t, queue, lease)
	if err := queue.CompleteEvidence(ctx, lease.ID, lease.LeaseToken, "evr_origin_select"); err != nil {
		t.Fatal(err)
	}
	assertChatSelection(t, api, owner, firstBody.Chat.ID, []string{"src_origin_select"})
	assertChatSelection(t, api, owner, secondBody.Chat.ID, []string{})

	cleared := api.patchJSONWithCookie(t, "/api/v1/chats/"+firstBody.Chat.ID+"/source-selection", map[string]any{"source_ids": []string{}}, owner)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear selection status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	if _, err := api.db.Pool().Exec(ctx, `update source_sources set state='verifying' where id='src_origin_select'; update source_processing_jobs set status='running',attempt_no=1,lease_token='00000000-0000-0000-0000-000000000008',lease_expires_at=now()+interval '1 minute' where id='srcjob_origin_select'; update source_evidence_revisions set status='building',activated_at=null where id='evr_origin_select'`); err != nil {
		t.Fatal(err)
	}
	if err := queue.CompleteEvidence(ctx, "srcjob_origin_select", "00000000-0000-0000-0000-000000000008", "evr_origin_select"); err != nil {
		t.Fatal(err)
	}
	assertChatSelection(t, api, owner, firstBody.Chat.ID, []string{})
}

func assertChatSelection(t *testing.T, api *testAPI, owner *http.Cookie, chatID string, want []string) {
	t.Helper()
	response := api.getWithCookie(t, "/api/v1/chats/"+chatID+"/source-selection", owner)
	if response.Code != http.StatusOK {
		t.Fatalf("selection status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		SourceIDs []string `json:"source_ids"`
	}
	decodeBody(t, response, &body)
	if !reflect.DeepEqual(body.SourceIDs, want) {
		t.Fatalf("selection=%v want=%v", body.SourceIDs, want)
	}
}

func installBuildingEvidenceProjection(t *testing.T, api *testAPI, notebookID, sourceID, revisionID string) {
	t.Helper()
	ctx := context.Background()
	const indexConfig = `{"chunk":{"max_runes":512,"overlap_runes":64,"preserve_heading_context":true},"analyzer_id":"nano-mixed-v1","bm25_k1":1.2,"bm25_b":0.75,"bm25_average_document_length":128,"embedding_model":"embed-test","embedding_dimensions":3,"embedding_profile_id":"gemini-retrieval-v1","dense_candidates":8,"sparse_candidates":8,"rrf_k":60,"reranker_id":"rerank-test","rerank_candidates":8,"degradation_policy_id":"strict-v1"}`
	if _, err := api.db.Pool().Exec(ctx, `update retrieval_index_versions set status='retired' where status='active'`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `insert into retrieval_index_versions(id,config_json,config_sha256,status,promoted_by_eval_run_id,promoted_at) values('riv_origin_active',$1::jsonb,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','active','eval_origin',now()) on conflict(id) do update set status='active',promoted_at=now()`, indexConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `insert into source_evidence_revisions(id,source_id,notebook_id,revision_no,extraction_config_id,artifact_schema_version,artifact_object_key,artifact_sha256,status) values($1,$2,$3,1,'html-primary-v2','nano.normalized-source.v1','sources/origin/evidence','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','building')`, revisionID, sourceID, notebookID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `insert into retrieval_source_index_builds(revision_id,index_version_id,source_id,notebook_id,expected_points,projection_sha256,status,verified_at) values($1,'riv_origin_active',$2,$3,1,'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc','verified',now())`, revisionID, sourceID, notebookID); err != nil {
		t.Fatal(err)
	}
}

func advanceSourceToVerifying(t *testing.T, queue *sourcejobs.Queue, lease sourcejobs.Lease) {
	t.Helper()
	for _, transition := range [][2]source.State{{source.StateUploaded, source.StateValidating}, {source.StateValidating, source.StateNormalizing}, {source.StateNormalizing, source.StateSegmenting}, {source.StateSegmenting, source.StateQualifying}, {source.StateQualifying, source.StateIndexing}, {source.StateIndexing, source.StateVerifying}} {
		if err := queue.Advance(context.Background(), lease.ID, lease.LeaseToken, transition[0], transition[1]); err != nil {
			t.Fatal(err)
		}
	}
}
