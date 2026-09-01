package sourcemap

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrPersistenceConflict = errors.New("Source Map persistence conflict")

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Append(ctx context.Context, input PersistInput) (Record, bool, error) {
	if r == nil || r.pool == nil || input.NotebookID == "" || input.ObjectKey == "" ||
		len(input.Artifact.CanonicalJSON) < 1 || len(input.Artifact.CanonicalJSON) > MaxParserOutput ||
		!validSHA256(input.Artifact.SHA256) {
		return Record{}, false, errors.New("invalid Source Map persistence input")
	}
	expected := recordFromPersistInput(input)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return Record{}, false, err
	}
	var sourceID, notebookID, originalSHA string
	err = tx.QueryRow(ctx, `
		select revision.source_id,revision.notebook_id,source.content_sha256
		from source_evidence_revisions revision
		join source_sources source on source.id=revision.source_id and source.notebook_id=revision.notebook_id
		where revision.id=$1 and revision.status in ('building','active')
	`, expected.RevisionID).Scan(&sourceID, &notebookID, &originalSHA)
	if err != nil {
		return Record{}, false, err
	}
	if sourceID != expected.SourceID || notebookID != expected.NotebookID || originalSHA != input.Artifact.Map.OriginalSHA256 {
		return Record{}, false, ErrPersistenceConflict
	}
	tag, err := tx.Exec(ctx, `
		insert into source_maps(
			id,source_id,notebook_id,revision_id,original_sha256,artifact_object_key,
			artifact_sha256,artifact_bytes,parser_identity,parser_version,parser_policy_id,
			navigation_kind,confidence,page_count,entry_count
		) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		on conflict(revision_id,parser_policy_id) do nothing
	`, expected.ID, expected.SourceID, expected.NotebookID, expected.RevisionID, input.Artifact.Map.OriginalSHA256,
		expected.ObjectKey, expected.ArtifactSHA256, expected.ArtifactBytes, expected.ParserIdentity, expected.ParserVersion,
		expected.ParserPolicyID, expected.NavigationKind, expected.Confidence, expected.PageCount, expected.EntryCount)
	if err != nil {
		return Record{}, false, err
	}
	created := tag.RowsAffected() == 1
	actual, err := loadRecord(ctx, tx, expected.RevisionID, expected.ParserPolicyID)
	if err != nil {
		return Record{}, false, err
	}
	if actual != expected {
		return Record{}, false, ErrPersistenceConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, false, err
	}
	return actual, !created, nil
}

func loadRecord(ctx context.Context, tx pgx.Tx, revisionID, policyID string) (Record, error) {
	var value Record
	err := tx.QueryRow(ctx, `
		select id,source_id,notebook_id,revision_id,artifact_object_key,artifact_sha256,artifact_bytes,
			parser_identity,parser_version,parser_policy_id,navigation_kind,confidence,page_count,entry_count
		from source_maps where revision_id=$1 and parser_policy_id=$2
	`, revisionID, policyID).Scan(&value.ID, &value.SourceID, &value.NotebookID, &value.RevisionID,
		&value.ObjectKey, &value.ArtifactSHA256, &value.ArtifactBytes, &value.ParserIdentity, &value.ParserVersion,
		&value.ParserPolicyID, &value.NavigationKind, &value.Confidence, &value.PageCount, &value.EntryCount)
	return value, err
}
