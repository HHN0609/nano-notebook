package sourceadmission

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrAdmissionNotFound = errors.New("Source Admission Report not found")
	ErrReviewConflict    = errors.New("Source Admission review conflict")
	ErrInvalidReview     = errors.New("invalid Source Admission review")
)

type ReviewDecision string

const (
	ReviewApproved ReviewDecision = "approve"
	ReviewRejected ReviewDecision = "reject"
)

type Review struct {
	ID             string         `json:"id"`
	ReportID       string         `json:"report_id"`
	ReviewerUserID string         `json:"reviewer_user_id"`
	Decision       ReviewDecision `json:"decision"`
	Note           string         `json:"note,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type ReviewCommand struct {
	SourceID string
	ReportID string
	Decision ReviewDecision
	Note     string
}

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type MemberStore struct {
	db DBTX
}

func NewMemberStore(db DBTX) *MemberStore {
	return &MemberStore{db: db}
}

func (store *MemberStore) Detail(ctx context.Context, sourceID string) (StoredAssessment, error) {
	if store == nil || store.db == nil || strings.TrimSpace(sourceID) == "" {
		return StoredAssessment{}, ErrAdmissionNotFound
	}
	var stored StoredAssessment
	var assessmentJSON []byte
	var reviewID, reviewerID, decision, note *string
	var reviewCreatedAt *time.Time
	err := store.db.QueryRow(ctx, `
		select r.source_id,r.notebook_id,r.revision_id,r.runtime_mode,r.assessment_json,r.created_at,
			review.id,review.reviewer_user_id,review.decision,review.note,review.created_at
		from source_admission_reports r
		left join source_admission_reviews review on review.report_id=r.id
		where r.source_id=$1
		order by r.created_at desc,r.id desc limit 1
	`, sourceID).Scan(
		&stored.SourceID, &stored.NotebookID, &stored.RevisionID, &stored.Mode, &assessmentJSON, &stored.CreatedAt,
		&reviewID, &reviewerID, &decision, &note, &reviewCreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredAssessment{}, ErrAdmissionNotFound
	}
	if err != nil {
		return StoredAssessment{}, err
	}
	if err := json.Unmarshal(assessmentJSON, &stored.Assessment); err != nil {
		return StoredAssessment{}, err
	}
	stored.Review = hydrateReview(reviewID, reviewerID, decision, note, reviewCreatedAt)
	if stored.Review != nil {
		stored.Review.ReportID = stored.Report.ID
	}
	return stored, nil
}

func hydrateReview(id, reviewerID, decision, note *string, createdAt *time.Time) *Review {
	if id == nil || reviewerID == nil || decision == nil || note == nil || createdAt == nil {
		return nil
	}
	return &Review{
		ID: *id, ReportID: "", ReviewerUserID: *reviewerID, Decision: ReviewDecision(*decision), Note: *note, CreatedAt: *createdAt,
	}
}

func (store *MemberStore) Review(ctx context.Context, command ReviewCommand) (Review, bool, error) {
	command.SourceID = strings.TrimSpace(command.SourceID)
	command.ReportID = strings.TrimSpace(command.ReportID)
	command.Note = strings.TrimSpace(command.Note)
	if store == nil || store.db == nil || command.SourceID == "" || command.ReportID == "" || len(command.Note) > 500 ||
		(command.Decision != ReviewApproved && command.Decision != ReviewRejected) {
		return Review{}, false, ErrInvalidReview
	}
	var notebookID string
	var canMaintain bool
	err := store.db.QueryRow(ctx, `
		select notebook_id,nano_has_notebook_capability(notebook_id,'source.maintain')
		from source_sources where id=$1
	`, command.SourceID).Scan(&notebookID, &canMaintain)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canMaintain) {
		return Review{}, false, ErrAdmissionNotFound
	}
	if err != nil {
		return Review{}, false, err
	}
	var currentReportID, revisionID, mode, status, sourceState, jobStatus string
	err = store.db.QueryRow(ctx, `
		select r.id,r.revision_id,r.runtime_mode,r.status,s.state,j.status
		from source_sources s
		join lateral (
			select * from source_admission_reports where source_id=s.id order by created_at desc,id desc limit 1
		) r on true
		join source_processing_jobs j on j.source_id=s.id
		where s.id=$1
		for update of s,j
	`, command.SourceID).Scan(&currentReportID, &revisionID, &mode, &status, &sourceState, &jobStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, false, ErrReviewConflict
	}
	if err != nil {
		return Review{}, false, err
	}
	var existing Review
	err = store.db.QueryRow(ctx, `
		select id,report_id,reviewer_user_id,decision,note,created_at
		from source_admission_reviews where report_id=$1
	`, currentReportID).Scan(&existing.ID, &existing.ReportID, &existing.ReviewerUserID, &existing.Decision, &existing.Note, &existing.CreatedAt)
	if err == nil {
		if currentReportID == command.ReportID && existing.Decision == command.Decision {
			return existing, false, nil
		}
		return Review{}, false, ErrReviewConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Review{}, false, err
	}
	if currentReportID != command.ReportID || mode != string(ModeEnforcement) || status != string(StatusReviewRequired) || sourceState != "qualifying" || jobStatus != "succeeded" {
		return Review{}, false, ErrReviewConflict
	}
	reviewID := "sarv_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var review Review
	err = store.db.QueryRow(ctx, `
		insert into source_admission_reviews(
			id,report_id,source_id,notebook_id,revision_id,reviewer_user_id,decision,note
		) values($1,$2,$3,$4,$5,nullif(current_setting('app.principal_id',true),''),$6,$7)
		returning id,report_id,reviewer_user_id,decision,note,created_at
	`, reviewID, command.ReportID, command.SourceID, notebookID, revisionID, command.Decision, command.Note).Scan(
		&review.ID, &review.ReportID, &review.ReviewerUserID, &review.Decision, &review.Note, &review.CreatedAt,
	)
	if err != nil {
		return Review{}, false, err
	}
	if command.Decision == ReviewApproved {
		tag, err := store.db.Exec(ctx, `
			update source_processing_jobs
			set status='queued',attempt_no=0,available_at=now(),lease_token=null,lease_expires_at=null,last_error_code=null,updated_at=now()
			where source_id=$1 and status='succeeded'
		`, command.SourceID)
		if err != nil {
			return Review{}, false, err
		}
		if tag.RowsAffected() != 1 {
			return Review{}, false, ErrReviewConflict
		}
	}
	if err := realtime.NotifyNotebookSources(ctx, store.db, notebookID); err != nil {
		return Review{}, false, err
	}
	return review, true, nil
}
