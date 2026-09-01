package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcemap"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sourceInspectionObjectReader interface {
	Get(context.Context, string, int64) ([]byte, error)
}

type SourceInspectionService struct {
	pool    *pgxpool.Pool
	objects sourceInspectionObjectReader
}

func NewSourceInspectionService(pool *pgxpool.Pool, objects sourceInspectionObjectReader) *SourceInspectionService {
	return &SourceInspectionService{pool: pool, objects: objects}
}

func (s *SourceInspectionService) InspectSource(ctx context.Context, attempt Attempt, sourceID string) (json.RawMessage, error) {
	sourceID = strings.TrimSpace(sourceID)
	if s == nil || s.pool == nil || s.objects == nil || sourceID == "" || attempt.RunID == "" || attempt.JobID == "" ||
		attempt.LeaseToken == "" || attempt.AttemptNo < 1 {
		return nil, ErrSourceInspectionUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return nil, err
	}
	var executorIdentity, definitionIdentity string
	var definitionVersion int
	err = tx.QueryRow(ctx, `
		select run.executor_identity,run.definition_identity,run.definition_version
		from agent_runs run
		join agent_jobs job on job.run_id=run.id
		left join agent_trees tree on tree.id=run.tree_id
		where run.id=$1 and job.id=$2 and job.lease_token=$3::uuid and job.attempt_no=$4
			and run.status='running' and run.output_message_id is null
			and coalesce(run.deadline_at,tree.absolute_deadline)>now()
			and job.status='running' and job.lease_expires_at>now()
	`, attempt.RunID, attempt.JobID, attempt.LeaseToken, attempt.AttemptNo).Scan(&executorIdentity, &definitionIdentity, &definitionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLeaseLost
	}
	if err != nil {
		return nil, err
	}
	if executorIdentity != "research_root" || definitionIdentity != "research.executor" || definitionVersion < 10 {
		return nil, ErrSourceNotInspectable
	}
	var authority sourceInspectionAuthority
	var originalSHA256 string
	err = tx.QueryRow(ctx, `
		select source.id,evidence.evidence_revision_id,source.title,source.media_type,source.content_sha256
		from agent_run_evidence_set evidence
		join source_sources source on source.id=evidence.source_id and source.notebook_id=evidence.notebook_id
			and source.state='ready' and source.format='pdf' and source.media_type='application/pdf'
		join source_evidence_revisions revision on revision.id=evidence.evidence_revision_id
			and revision.source_id=source.id and revision.notebook_id=source.notebook_id and revision.status='active'
		join retrieval_source_index_builds build on build.revision_id=revision.id and build.source_id=source.id
			and build.notebook_id=source.notebook_id and build.index_version_id=evidence.index_version_id and build.status='verified'
		where evidence.run_id=$1 and evidence.source_id=$2
	`, attempt.RunID, sourceID).Scan(&authority.SourceID, &authority.RevisionID, &authority.Title, &authority.MediaType, &originalSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSourceNotInspectable
	}
	if err != nil {
		return nil, err
	}
	var mapID, objectKey, navigationKind, confidence string
	var artifactBytes, pageCount, entryCount int
	err = tx.QueryRow(ctx, `
		select id,artifact_object_key,artifact_sha256,artifact_bytes,navigation_kind,confidence,page_count,entry_count
		from source_maps
		where source_id=$1 and revision_id=$2 and original_sha256=$3 and parser_policy_id=$4
	`, authority.SourceID, authority.RevisionID, originalSHA256, sourcemap.ParserPolicyNoOCR).Scan(
		&mapID, &objectKey, &authority.ArtifactSHA256, &artifactBytes, &navigationKind, &confidence, &pageCount, &entryCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSourceInspectionUnavailable
	}
	if err != nil {
		return nil, err
	}
	units, err := loadSourceInspectionUnits(ctx, tx, authority.SourceID, authority.RevisionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	payload, err := s.objects.Get(ctx, objectKey, int64(artifactBytes))
	if err != nil {
		if errors.Is(err, objectstore.ErrNotFound) || errors.Is(err, objectstore.ErrObjectTooLarge) {
			return nil, ErrSourceInspectionUnavailable
		}
		return nil, err
	}
	sourceMap, err := sourcemap.DecodeArtifact(payload, sourcemap.ArtifactIdentity{
		SourceID: authority.SourceID, RevisionID: authority.RevisionID,
		SHA256: authority.ArtifactSHA256, Bytes: artifactBytes,
	})
	if err != nil || sourceMap.MapID != mapID || string(sourceMap.NavigationKind) != navigationKind ||
		string(sourceMap.Confidence) != confidence || sourceMap.PageCount != pageCount || len(sourceMap.Entries) != entryCount ||
		sourceMap.OriginalSHA256 != originalSHA256 {
		return nil, ErrSourceInspectionUnavailable
	}
	return buildSourceInspectionProjection(authority, sourceMap, units)
}

func loadSourceInspectionUnits(ctx context.Context, tx pgx.Tx, sourceID, revisionID string) ([]sourceInspectionUnit, error) {
	rows, err := tx.Query(ctx, `
		select id,ordinal,kind,text_content,coordinate_json
		from source_evidence_units
		where source_id=$1 and revision_id=$2
		order by ordinal
	`, sourceID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	units := make([]sourceInspectionUnit, 0)
	for rows.Next() {
		var unit sourceInspectionUnit
		var coordinateJSON []byte
		if err := rows.Scan(&unit.ID, &unit.Ordinal, &unit.Kind, &unit.Text, &coordinateJSON); err != nil {
			return nil, err
		}
		if unit.Ordinal != len(units) || strings.TrimSpace(unit.ID) == "" || strings.TrimSpace(unit.Text) == "" || !utf8.ValidString(unit.Text) {
			return nil, fmt.Errorf("%w: invalid Source Evidence Unit", ErrSourceInspectionUnavailable)
		}
		if len(coordinateJSON) > 0 && string(coordinateJSON) != "null" {
			if err := json.Unmarshal(coordinateJSON, &unit.Coordinate); err != nil || strings.TrimSpace(unit.Coordinate.Kind) == "" {
				return nil, fmt.Errorf("%w: invalid Source Evidence coordinate", ErrSourceInspectionUnavailable)
			}
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, ErrSourceInspectionUnavailable
	}
	return units, nil
}

var _ SourceInspectionBackend = (*SourceInspectionService)(nil)
