package sourceadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcejobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrReportConflict = errors.New("Source Admission Report conflict")

type Mode string

const (
	ModeShadow      Mode = "shadow"
	ModeEnforcement Mode = "enforcement"
)

type PublishCommand struct {
	Lease      sourcejobs.Lease
	RevisionID string
	Mode       Mode
	Policy     Policy
	Assessment Assessment
}

type StoredAssessment struct {
	Assessment
	SourceID   string    `json:"source_id"`
	NotebookID string    `json:"notebook_id"`
	RevisionID string    `json:"revision_id"`
	Mode       Mode      `json:"mode"`
	CreatedAt  time.Time `json:"created_at"`
	Review     *Review   `json:"review,omitempty"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) Publish(ctx context.Context, command PublishCommand) (StoredAssessment, bool, error) {
	if store == nil || store.pool == nil || strings.TrimSpace(command.Lease.ID) == "" ||
		strings.TrimSpace(command.Lease.SourceID) == "" || strings.TrimSpace(command.Lease.NotebookID) == "" ||
		strings.TrimSpace(command.Lease.LeaseToken) == "" || strings.TrimSpace(command.RevisionID) == "" ||
		(command.Mode != ModeShadow && command.Mode != ModeEnforcement) {
		return StoredAssessment{}, false, errors.New("invalid Source Admission publication")
	}
	if err := validateAssessment(command.Policy, command.Assessment); err != nil {
		return StoredAssessment{}, false, err
	}
	assessmentJSON, err := json.Marshal(command.Assessment)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return StoredAssessment{}, false, err
	}
	var sourceState source.State
	var contentSHA256, artifactSHA256, revisionSourceID string
	err = tx.QueryRow(ctx, `
		select s.state,s.content_sha256,r.artifact_sha256,r.source_id
		from source_processing_jobs j
		join source_sources s on s.id=j.source_id
		join source_evidence_revisions r on r.id=$5
		where j.id=$1 and j.source_id=$2 and j.notebook_id=$3 and j.status='running'
			and j.lease_token=$4::uuid and j.lease_expires_at > now()
		for update of j,s,r
	`, command.Lease.ID, command.Lease.SourceID, command.Lease.NotebookID, command.Lease.LeaseToken, command.RevisionID).Scan(
		&sourceState, &contentSHA256, &artifactSHA256, &revisionSourceID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredAssessment{}, false, sourcejobs.ErrLeaseLost
	}
	if err != nil {
		return StoredAssessment{}, false, err
	}
	if sourceState != source.StateQualifying || revisionSourceID != command.Lease.SourceID ||
		contentSHA256 != command.Assessment.Input.Profile.ContentSHA256 || artifactSHA256 != command.Assessment.Input.Profile.ArtifactSHA256 {
		return StoredAssessment{}, false, ErrReportConflict
	}
	existing, ok, err := currentInTx(ctx, tx, command.Lease.SourceID, command.RevisionID, command.Assessment.Report.PolicySHA256)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	if ok {
		if existing.Report.ID != command.Assessment.Report.ID || existing.Mode != command.Mode {
			return StoredAssessment{}, false, ErrReportConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return StoredAssessment{}, false, err
		}
		return existing, false, nil
	}
	var score any
	if command.Assessment.Report.Score != nil {
		score = *command.Assessment.Report.Score
	}
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		insert into source_admission_reports(
			id,source_id,notebook_id,revision_id,content_sha256,artifact_sha256,policy_id,policy_sha256,
			runtime_mode,status,score,signal_coverage,exact_identity_match,assessment_json
		) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		returning created_at
	`, command.Assessment.Report.ID, command.Lease.SourceID, command.Lease.NotebookID, command.RevisionID,
		contentSHA256, artifactSHA256, command.Assessment.Report.PolicyID, command.Assessment.Report.PolicySHA256,
		command.Mode, command.Assessment.Report.Status, score, command.Assessment.Report.SignalCoverage,
		command.Assessment.Report.ExactIdentityMatch, assessmentJSON).Scan(&createdAt)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredAssessment{}, false, err
	}
	return StoredAssessment{
		Assessment: command.Assessment, SourceID: command.Lease.SourceID, NotebookID: command.Lease.NotebookID,
		RevisionID: command.RevisionID, Mode: command.Mode, CreatedAt: createdAt,
	}, true, nil
}

func (store *Store) Current(ctx context.Context, sourceID, revisionID, policySHA256 string) (StoredAssessment, bool, error) {
	if store == nil || store.pool == nil || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(revisionID) == "" || len(policySHA256) != 64 {
		return StoredAssessment{}, false, errors.New("invalid Source Admission Report lookup")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return StoredAssessment{}, false, err
	}
	stored, ok, err := currentInTx(ctx, tx, sourceID, revisionID, policySHA256)
	if err != nil {
		return StoredAssessment{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredAssessment{}, false, err
	}
	return stored, ok, nil
}

func currentInTx(ctx context.Context, tx pgx.Tx, sourceID, revisionID, policySHA256 string) (StoredAssessment, bool, error) {
	var stored StoredAssessment
	var assessmentJSON []byte
	var reviewID, reviewerID, decision, note *string
	var reviewCreatedAt *time.Time
	err := tx.QueryRow(ctx, `
		select r.notebook_id,r.runtime_mode,r.assessment_json,r.created_at,
			review.id,review.reviewer_user_id,review.decision,review.note,review.created_at
		from source_admission_reports r
		left join source_admission_reviews review on review.report_id=r.id
		where r.source_id=$1 and r.revision_id=$2 and r.policy_sha256=$3
	`, sourceID, revisionID, policySHA256).Scan(
		&stored.NotebookID, &stored.Mode, &assessmentJSON, &stored.CreatedAt,
		&reviewID, &reviewerID, &decision, &note, &reviewCreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredAssessment{}, false, nil
	}
	if err != nil {
		return StoredAssessment{}, false, err
	}
	if err := json.Unmarshal(assessmentJSON, &stored.Assessment); err != nil {
		return StoredAssessment{}, false, err
	}
	stored.SourceID = sourceID
	stored.RevisionID = revisionID
	stored.Review = hydrateReview(reviewID, reviewerID, decision, note, reviewCreatedAt)
	if stored.Review != nil {
		stored.Review.ReportID = stored.Report.ID
	}
	return stored, true, nil
}

func validateAssessment(policy Policy, assessment Assessment) error {
	expected, err := Evaluate(policy, assessment.Input)
	if err != nil {
		return err
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	actualJSON, err := json.Marshal(assessment.Report)
	if err != nil {
		return err
	}
	if !bytes.Equal(expectedJSON, actualJSON) || assessment.ProviderID != assessment.Input.ProviderID ||
		assessment.ProviderAttempts != assessment.Input.ProviderAttempts {
		return ErrReportConflict
	}
	return nil
}
