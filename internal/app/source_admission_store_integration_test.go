package app_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/evidence"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourceadmission"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcejobs"
	"github.com/huangxinxinyu/nano-notebook/internal/sourceprocessing"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

func TestSourceAdmissionReviewApprovesPinnedReportAndResumesWithoutSearch(t *testing.T) {
	api := newTestAPI(t)
	owner, ownerCSRF := api.registerWithCSRF(t, "source-admission-review-owner@example.com")
	viewer, viewerCSRF := api.registerWithCSRF(t, "source-admission-review-viewer@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "source-admission-review")
	ownerID := sourceTestUserID(t, api, "source-admission-review-owner@example.com")
	viewerID := sourceTestUserID(t, api, "source-admission-review-viewer@example.com")
	if _, err := api.db.Pool().Exec(context.Background(), `
		insert into notebook_memberships(notebook_id,user_id,role) values($1,$2,'viewer')
	`, notebookID, viewerID); err != nil {
		t.Fatal(err)
	}
	payload := []byte("A public Source held for a member review before retrieval projection.")
	objectKey := seedProcessableSource(t, api, ownerID, notebookID, "src_admission_review", "srcjob_admission_review", source.FormatTXT, payload)
	identity, err := source.CanonicalURLIdentity("https://publisher.example/review")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `
		update source_sources set input_kind='url',title='Review This Source',
			origin_url='https://publisher.example/review',final_url='https://publisher.example/review',
			origin_url_identity=$2,final_url_identity=$2 where id=$1
	`, "src_admission_review", identity); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(context.Background(), objectKey, payload); err != nil {
		t.Fatal(err)
	}
	queue := sourcejobs.NewQueue(api.db.Pool(), time.Minute)
	lease, ok, err := queue.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("Claim=%+v ok=%t err=%v", lease, ok, err)
	}
	provider := &stubWebSearchProvider{}
	projection := newRecordingEvidenceProjection(t, api)
	processor := sourceprocessing.NewProcessor(
		api.db.Pool(), queue, evidence.NewPublisher(api.db.Pool(), objects), objects, projection,
		sourceprocessing.Config{ExtractionConfigID: "extract-text-v1", MaxSourceBytes: 1 << 20, MaxNormalizedRunes: 10_000},
	).WithAdmission(configuredAdmissionService(t, api, provider, sourceadmission.ModeEnforcement))
	if err := processor.ProcessLease(context.Background(), lease); err != nil {
		t.Fatalf("first ProcessLease: %v", err)
	}
	assertSourceJobState(t, api, "src_admission_review", "srcjob_admission_review", source.StateQualifying, "succeeded", "")
	if len(provider.requests) != 1 {
		t.Fatalf("initial Web Search requests=%d want=1", len(provider.requests))
	}
	listed := api.getWithCookie(t, "/api/v1/notebooks/"+notebookID+"/sources", viewer)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"admission":{"report_id":"sar_`) ||
		!strings.Contains(listed.Body.String(), `"status":"review_required"`) {
		t.Fatalf("Source list omitted compact admission summary: status=%d body=%s", listed.Code, listed.Body.String())
	}

	detail := api.getWithCookie(t, "/api/v1/sources/src_admission_review/admission", viewer)
	if detail.Code != http.StatusOK {
		t.Fatalf("viewer admission detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var detailBody struct {
		Admission struct {
			Report struct {
				ID string `json:"id"`
			} `json:"report"`
		} `json:"admission"`
	}
	decodeBody(t, detail, &detailBody)
	reportID := detailBody.Admission.Report.ID
	if reportID == "" {
		t.Fatal("admission detail omitted report id")
	}

	viewerReview := api.postJSONWithCookieAndCSRF(t, "/api/v1/sources/src_admission_review/admission-review", map[string]any{
		"report_id": reportID, "decision": "approve", "note": "I checked the publisher.",
	}, viewer, viewerCSRF, viewerCSRF.Value, "")
	if viewerReview.Code != http.StatusNotFound {
		t.Fatalf("viewer review status=%d body=%s", viewerReview.Code, viewerReview.Body.String())
	}
	stale := api.postJSONWithCookieAndCSRF(t, "/api/v1/sources/src_admission_review/admission-review", map[string]any{
		"report_id": "sar_00000000000000000000000000000000", "decision": "approve",
	}, owner, ownerCSRF, ownerCSRF.Value, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale review status=%d body=%s", stale.Code, stale.Body.String())
	}
	approved := api.postJSONWithCookieAndCSRF(t, "/api/v1/sources/src_admission_review/admission-review", map[string]any{
		"report_id": reportID, "decision": "approve", "note": "Publisher identity verified.",
	}, owner, ownerCSRF, ownerCSRF.Value, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	assertSourceJobState(t, api, "src_admission_review", "srcjob_admission_review", source.StateQualifying, "queued", "")

	resumed, ok, err := queue.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("resumed Claim=%+v ok=%t err=%v", resumed, ok, err)
	}
	if err := processor.ProcessLease(context.Background(), resumed); err != nil {
		t.Fatalf("resumed ProcessLease: %v", err)
	}
	assertSourceJobState(t, api, "src_admission_review", "srcjob_admission_review", source.StateReady, "succeeded", "")
	if len(provider.requests) != 1 || projection.builds != 1 || projection.verifications != 1 {
		t.Fatalf("resume repeated Search or skipped projection: searches=%d projection=%+v", len(provider.requests), projection)
	}
}

func TestSourceAdmissionStorePublishesImmutableReportIdempotently(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "source-admission-store@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "source-admission-store")
	ownerID := sourceTestUserID(t, api, "source-admission-store@example.com")
	seedSourceProcessingJob(t, api, ownerID, notebookID, "src_admission_store", "srcjob_admission_store", "a")
	const leaseToken = "00000000-0000-0000-0000-000000000091"
	if _, err := api.db.Pool().Exec(context.Background(), `update source_sources set state='qualifying' where id='src_admission_store'`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `
		update source_processing_jobs set status='running',attempt_no=1,lease_token=$1::uuid,
			lease_expires_at=now()+interval '1 minute' where id='srcjob_admission_store'
	`, leaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `
		insert into source_evidence_revisions(
			id,source_id,notebook_id,revision_no,extraction_config_id,artifact_schema_version,
			artifact_object_key,artifact_sha256,status
		) values(
			'evr_admission_store','src_admission_store',$1,1,'extract-v1','nano.normalized-source.v1',
			'sources/src_admission_store/evidence/normalized.json',$2,'building'
		)
	`, notebookID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}

	policy := sourceadmission.DefaultPolicy()
	input := sourceadmission.EvaluationInput{
		Profile: sourceadmission.Profile{
			InputKind: "file", Title: "private.txt", ContentSHA256: strings.Repeat("a", 64), ArtifactSHA256: strings.Repeat("b", 64),
		},
		Extraction: sourceadmission.ExtractionObservation{CoverageStatus: "complete", TotalRunes: 120, BlockCount: 2},
		ProviderID: "not-configured",
	}
	report, err := sourceadmission.Evaluate(policy, input)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	assessment := sourceadmission.Assessment{Report: report, Input: input, ProviderID: input.ProviderID}
	command := sourceadmission.PublishCommand{
		Lease: sourcejobs.Lease{
			ID: "srcjob_admission_store", SourceID: "src_admission_store", NotebookID: notebookID,
			LeaseToken: leaseToken, LeaseExpiresAt: time.Now().Add(time.Minute), AttemptNo: 1,
		},
		RevisionID: "evr_admission_store", Mode: sourceadmission.ModeShadow, Policy: policy, Assessment: assessment,
	}
	store := sourceadmission.NewStore(api.db.Pool())
	first, created, err := store.Publish(context.Background(), command)
	if err != nil {
		t.Fatalf("Publish first: %v", err)
	}
	if !created || first.Report.ID != report.ID || first.Mode != sourceadmission.ModeShadow {
		t.Fatalf("first=%+v created=%t", first, created)
	}
	second, created, err := store.Publish(context.Background(), command)
	if err != nil {
		t.Fatalf("Publish second: %v", err)
	}
	if created || second.Report.ID != first.Report.ID {
		t.Fatalf("second=%+v created=%t", second, created)
	}

	loaded, ok, err := store.Current(context.Background(), "src_admission_store", "evr_admission_store", report.PolicySHA256)
	if err != nil || !ok || loaded.Report.ID != report.ID || loaded.Input.Profile.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("Current=%+v ok=%t err=%v", loaded, ok, err)
	}

	var sourceState source.State
	if err := api.db.Pool().QueryRow(context.Background(), `select state from source_sources where id='src_admission_store'`).Scan(&sourceState); err != nil {
		t.Fatal(err)
	}
	if sourceState != source.StateQualifying {
		t.Fatalf("source state=%q want qualifying", sourceState)
	}
}

func TestSourceProcessingPersistsShadowAdmissionBeforeProjection(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "source-admission-processing@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "source-admission-processing")
	ownerID := sourceTestUserID(t, api, "source-admission-processing@example.com")
	payload := []byte("A private Source is admitted without public Web Search.")
	objectKey := seedProcessableSource(
		t, api, ownerID, notebookID, "src_admission_processing", "srcjob_admission_processing", source.FormatTXT, payload,
	)
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(context.Background(), objectKey, payload); err != nil {
		t.Fatal(err)
	}
	queue := sourcejobs.NewQueue(api.db.Pool(), time.Minute)
	lease, ok, err := queue.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("Claim=%+v ok=%t err=%v", lease, ok, err)
	}
	projection := &admissionCheckingProjection{
		t: t, poolSourceID: "src_admission_processing", api: api,
		recordingEvidenceProjection: newRecordingEvidenceProjection(t, api),
	}
	processor := sourceprocessing.NewProcessor(
		api.db.Pool(), queue, evidence.NewPublisher(api.db.Pool(), objects), objects, projection,
		sourceprocessing.Config{ExtractionConfigID: "extract-text-v1", MaxSourceBytes: 1 << 20, MaxNormalizedRunes: 10_000},
	)
	if err := processor.ProcessLease(context.Background(), lease); err != nil {
		t.Fatalf("ProcessLease: %v", err)
	}
	assertSourceJobState(t, api, "src_admission_processing", "srcjob_admission_processing", source.StateReady, "succeeded", "")
	var status, mode, providerID string
	var score *float64
	if err := api.db.Pool().QueryRow(context.Background(), `
		select status,runtime_mode,assessment_json->>'provider_id',score
		from source_admission_reports where source_id='src_admission_processing'
	`).Scan(&status, &mode, &providerID, &score); err != nil {
		t.Fatal(err)
	}
	if status != "not_applicable" || mode != "shadow" || providerID != "not-configured" || score != nil {
		t.Fatalf("report status=%q mode=%q provider=%q score=%v", status, mode, providerID, score)
	}
}

type admissionCheckingProjection struct {
	t            *testing.T
	api          *testAPI
	poolSourceID string
	*recordingEvidenceProjection
}

func (projection *admissionCheckingProjection) Build(ctx context.Context, command sourceprocessing.ProjectionCommand) error {
	projection.t.Helper()
	var state source.State
	var reportCount int
	if err := projection.api.db.Pool().QueryRow(ctx, `
		select s.state,(select count(*) from source_admission_reports r where r.source_id=s.id)
		from source_sources s where s.id=$1
	`, projection.poolSourceID).Scan(&state, &reportCount); err != nil {
		projection.t.Fatal(err)
	}
	if state != source.StateQualifying || reportCount != 1 {
		projection.t.Fatalf("projection observed state=%q reports=%d want qualifying/1", state, reportCount)
	}
	return projection.recordingEvidenceProjection.Build(ctx, command)
}

var _ sourceprocessing.Projection = (*admissionCheckingProjection)(nil)

func TestPublicSourceAdmissionUsesBoundedSearchInShadowMode(t *testing.T) {
	api, notebookID, objects, queue, lease := publicAdmissionProcessingFixture(t, "shadow")
	provider := &stubWebSearchProvider{candidates: []websearch.Candidate{{
		Title: "Nano Public Admission Report", URL: "https://publisher.example/admission", Rank: 1,
	}}}
	projection := newRecordingEvidenceProjection(t, api)
	processor := sourceprocessing.NewProcessor(
		api.db.Pool(), queue, evidence.NewPublisher(api.db.Pool(), objects), objects, projection,
		sourceprocessing.Config{ExtractionConfigID: "extract-text-v1", MaxSourceBytes: 1 << 20, MaxNormalizedRunes: 10_000},
	).WithAdmission(configuredAdmissionService(t, api, provider, sourceadmission.ModeShadow))
	if err := processor.ProcessLease(context.Background(), lease); err != nil {
		t.Fatalf("ProcessLease: %v", err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Count != sourceadmission.DefaultPolicy().ResultsPerQuery {
		t.Fatalf("search requests=%+v", provider.requests)
	}
	assertSourceJobState(t, api, "src_admission_shadow", "srcjob_admission_shadow", source.StateReady, "succeeded", "")
	var status, mode string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select status,runtime_mode from source_admission_reports
		where source_id='src_admission_shadow' and notebook_id=$1
	`, notebookID).Scan(&status, &mode); err != nil {
		t.Fatal(err)
	}
	if status != "passed" || mode != "shadow" || projection.builds != 1 {
		t.Fatalf("status=%q mode=%q projection=%+v", status, mode, projection)
	}
}

func TestEnforcementModeHoldsReviewRequiredSourceBeforeProjection(t *testing.T) {
	api, _, objects, queue, lease := publicAdmissionProcessingFixture(t, "enforcement")
	provider := &stubWebSearchProvider{}
	projection := newRecordingEvidenceProjection(t, api)
	processor := sourceprocessing.NewProcessor(
		api.db.Pool(), queue, evidence.NewPublisher(api.db.Pool(), objects), objects, projection,
		sourceprocessing.Config{ExtractionConfigID: "extract-text-v1", MaxSourceBytes: 1 << 20, MaxNormalizedRunes: 10_000},
	).WithAdmission(configuredAdmissionService(t, api, provider, sourceadmission.ModeEnforcement))
	if err := processor.ProcessLease(context.Background(), lease); err != nil {
		t.Fatalf("ProcessLease: %v", err)
	}
	assertSourceJobState(t, api, "src_admission_enforcement", "srcjob_admission_enforcement", source.StateQualifying, "succeeded", "")
	if projection.builds != 0 || projection.verifications != 0 {
		t.Fatalf("review-required Source reached projection: %+v", projection)
	}
	var status, mode string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select status,runtime_mode from source_admission_reports where source_id='src_admission_enforcement'
	`).Scan(&status, &mode); err != nil {
		t.Fatal(err)
	}
	if status != "review_required" || mode != "enforcement" {
		t.Fatalf("status=%q mode=%q", status, mode)
	}
}

func TestSourceAdmissionRetryReusesImmutableReportWithoutSearchingAgain(t *testing.T) {
	api, _, objects, queue, lease := publicAdmissionProcessingFixture(t, "retry")
	provider := &stubWebSearchProvider{candidates: []websearch.Candidate{{
		Title: "Nano Public Admission Report", URL: "https://publisher.example/admission", Rank: 1,
	}}}
	projection := newRecordingEvidenceProjection(t, api)
	projection.buildError = errors.New("projection temporarily unavailable")
	processor := sourceprocessing.NewProcessor(
		api.db.Pool(), queue, evidence.NewPublisher(api.db.Pool(), objects), objects, projection,
		sourceprocessing.Config{ExtractionConfigID: "extract-text-v1", MaxSourceBytes: 1 << 20, MaxNormalizedRunes: 10_000},
	).WithAdmission(configuredAdmissionService(t, api, provider, sourceadmission.ModeShadow))
	if err := processor.ProcessLease(context.Background(), lease); err == nil {
		t.Fatal("first ProcessLease unexpectedly succeeded")
	}
	assertSourceJobState(t, api, "src_admission_retry", "srcjob_admission_retry", source.StateQualifying, "running", "")
	if len(provider.requests) != 1 {
		t.Fatalf("first attempt searches=%d want=1", len(provider.requests))
	}
	projection.buildError = nil
	if err := processor.ProcessLease(context.Background(), lease); err != nil {
		t.Fatalf("retry ProcessLease: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("retry repeated Web Search: requests=%+v", provider.requests)
	}
	var reportCount int
	if err := api.db.Pool().QueryRow(context.Background(), `
		select count(*) from source_admission_reports where source_id='src_admission_retry'
	`).Scan(&reportCount); err != nil {
		t.Fatal(err)
	}
	if reportCount != 1 {
		t.Fatalf("reports=%d want=1", reportCount)
	}
}

func publicAdmissionProcessingFixture(
	t *testing.T,
	suffix string,
) (*testAPI, string, *objectstore.MemoryStore, *sourcejobs.Queue, sourcejobs.Lease) {
	t.Helper()
	api := newTestAPI(t)
	email := "source-admission-" + suffix + "@example.com"
	owner := api.register(t, email)
	notebookID := createSourceTestNotebook(t, api, owner, "source-admission-"+suffix)
	ownerID := sourceTestUserID(t, api, email)
	sourceID := "src_admission_" + suffix
	jobID := "srcjob_admission_" + suffix
	payload := []byte("A public Source with mechanically verifiable identity.")
	objectKey := seedProcessableSource(t, api, ownerID, notebookID, sourceID, jobID, source.FormatTXT, payload)
	identity, err := source.CanonicalURLIdentity("https://publisher.example/admission")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(context.Background(), `
		update source_sources set input_kind='url',title='Nano Public Admission Report',
			origin_url='https://publisher.example/admission',final_url='https://publisher.example/admission',
			origin_url_identity=$2,final_url_identity=$2 where id=$1
	`, sourceID, identity); err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemoryStore()
	if err := objects.Put(context.Background(), objectKey, payload); err != nil {
		t.Fatal(err)
	}
	queue := sourcejobs.NewQueue(api.db.Pool(), time.Minute)
	lease, ok, err := queue.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("Claim=%+v ok=%t err=%v", lease, ok, err)
	}
	return api, notebookID, objects, queue, lease
}

func configuredAdmissionService(
	t *testing.T,
	api *testAPI,
	provider websearch.Provider,
	mode sourceadmission.Mode,
) *sourceadmission.Service {
	t.Helper()
	verifier, err := sourceadmission.NewVerifier(provider, sourceadmission.DefaultVerifierConfig("test-search"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := sourceadmission.NewService(sourceadmission.NewStore(api.db.Pool()), verifier, mode)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
