package sourcejobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLeaseLost          = errors.New("Source processing lease lost")
	ErrTransitionConflict = errors.New("Source state transition conflict")
)

type Lease struct {
	ID             string
	SourceID       string
	NotebookID     string
	AttemptNo      int
	LeaseToken     string
	LeaseExpiresAt time.Time
}

type Queue struct {
	pool          *pgxpool.Pool
	leaseDuration time.Duration
}

func NewQueue(pool *pgxpool.Pool, leaseDuration time.Duration) *Queue {
	return &Queue{pool: pool, leaseDuration: leaseDuration}
}

func (q *Queue) Claim(ctx context.Context) (Lease, bool, error) {
	if q == nil || q.pool == nil || q.leaseDuration <= 0 {
		return Lease{}, false, errors.New("invalid Source processing Queue")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Lease{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return Lease{}, false, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return Lease{}, false, err
	}
	exhaustedRows, err := tx.Query(ctx, `
		with exhausted as (
			update source_processing_jobs
			set status='failed', lease_token=null, lease_expires_at=null,
				last_error_code='retry_exhausted', updated_at=$1
			where status='running' and lease_expires_at <= $1 and attempt_no >= 3
			returning source_id
		)
		update source_sources s
		set state='failed', updated_at=$1
		from exhausted e
		where s.id=e.source_id
		returning s.notebook_id
	`, now)
	if err != nil {
		return Lease{}, false, err
	}
	exhaustedNotebookIDs := make([]string, 0)
	for exhaustedRows.Next() {
		var notebookID string
		if err := exhaustedRows.Scan(&notebookID); err != nil {
			exhaustedRows.Close()
			return Lease{}, false, err
		}
		exhaustedNotebookIDs = append(exhaustedNotebookIDs, notebookID)
	}
	if err := exhaustedRows.Err(); err != nil {
		exhaustedRows.Close()
		return Lease{}, false, err
	}
	exhaustedRows.Close()
	for _, notebookID := range exhaustedNotebookIDs {
		if err := realtime.NotifyNotebookSources(ctx, tx, notebookID); err != nil {
			return Lease{}, false, err
		}
	}
	if _, err := tx.Exec(ctx, `
		update source_processing_jobs
		set status='queued', lease_token=null, lease_expires_at=null, available_at=$1, updated_at=$1
		where status='running' and lease_expires_at <= $1 and attempt_no < 3
	`, now); err != nil {
		return Lease{}, false, err
	}

	var jobID string
	err = tx.QueryRow(ctx, `
		select id
		from source_processing_jobs
		where status='queued' and available_at <= $1 and attempt_no < 3
		order by available_at, created_at, id
		for update skip locked
		limit 1
	`, now).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Lease{}, false, err
		}
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, err
	}
	token := uuid.NewString()
	expiresAt := now.Add(q.leaseDuration)
	var lease Lease
	err = tx.QueryRow(ctx, `
		update source_processing_jobs
		set status='running', attempt_no=attempt_no+1, lease_token=$2::uuid,
			lease_expires_at=$3, updated_at=$1
		where id=$4
		returning id, source_id, notebook_id, attempt_no, lease_token::text, lease_expires_at
	`, now, token, expiresAt, jobID).Scan(
		&lease.ID, &lease.SourceID, &lease.NotebookID, &lease.AttemptNo,
		&lease.LeaseToken, &lease.LeaseExpiresAt,
	)
	if err != nil {
		return Lease{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Lease{}, false, err
	}
	return lease, true, nil
}

func (q *Queue) Advance(ctx context.Context, jobID, leaseToken string, expected, next source.State) error {
	if !validTransition(expected, next) {
		return ErrTransitionConflict
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return err
	}
	var sourceID string
	var current source.State
	err = tx.QueryRow(ctx, `
		select s.id, s.state
		from source_processing_jobs j
		join source_sources s on s.id=j.source_id
		where j.id=$1 and j.status='running' and j.lease_token=$2::uuid and j.lease_expires_at > now()
		for update of j, s
	`, jobID, leaseToken).Scan(&sourceID, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if current != expected {
		return ErrTransitionConflict
	}
	if _, err := tx.Exec(ctx, `update source_sources set state=$2, updated_at=now() where id=$1`, sourceID, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (q *Queue) Renew(ctx context.Context, jobID, leaseToken string) (time.Time, error) {
	if q == nil || q.pool == nil || q.leaseDuration <= 0 {
		return time.Time{}, errors.New("invalid Source processing Queue")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return time.Time{}, err
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&databaseNow); err != nil {
		return time.Time{}, err
	}
	expiresAt := databaseNow.Add(q.leaseDuration)
	err = tx.QueryRow(ctx, `
		update source_processing_jobs
		set lease_expires_at=$3, updated_at=now()
		where id=$1 and status='running' and lease_token=$2::uuid and lease_expires_at > now()
		returning lease_expires_at
	`, jobID, leaseToken, expiresAt).Scan(&expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrLeaseLost
	}
	if err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

func (q *Queue) Complete(ctx context.Context, jobID, leaseToken string) error {
	return q.finish(ctx, jobID, leaseToken, true, "")
}

func (q *Queue) CompleteEvidence(ctx context.Context, jobID, leaseToken, revisionID string) error {
	if q == nil || q.pool == nil || strings.TrimSpace(revisionID) == "" {
		return errors.New("invalid Source Evidence completion")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return err
	}
	var sourceID, notebookID string
	var current source.State
	err = tx.QueryRow(ctx, `
		select s.id, s.state, s.notebook_id
		from source_processing_jobs j
		join source_sources s on s.id=j.source_id
		where j.id=$1 and j.status='running' and j.lease_token=$2::uuid and j.lease_expires_at > now()
		for update of j, s
	`, jobID, leaseToken).Scan(&sourceID, &current, &notebookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if current != source.StateVerifying {
		return ErrTransitionConflict
	}
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('retrieval-index-promotion', 0))`); err != nil {
		return err
	}
	var revisionSourceID, revisionStatus string
	err = tx.QueryRow(ctx, `
		select source_id, status from source_evidence_revisions where id=$1 for update
	`, revisionID).Scan(&revisionSourceID, &revisionStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTransitionConflict
	}
	if err != nil {
		return err
	}
	if revisionSourceID != sourceID || revisionStatus != "building" {
		return ErrTransitionConflict
	}
	var verifiedProjection bool
	if err := tx.QueryRow(ctx, `
		select exists(
			select 1
			from retrieval_source_index_builds b
			join retrieval_index_versions v on v.id=b.index_version_id
			where b.revision_id=$1 and b.source_id=$2 and b.status='verified' and v.status='active'
		)
	`, revisionID, sourceID).Scan(&verifiedProjection); err != nil {
		return err
	}
	if !verifiedProjection {
		return ErrTransitionConflict
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		update source_evidence_revisions
		set status='superseded'
		where source_id=$1 and status='active'
	`, sourceID); err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `
		update source_evidence_revisions
		set status='active', activated_at=$2
		where id=$1 and source_id=$3 and status='building'
	`, revisionID, now, sourceID)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return ErrTransitionConflict
	}
	if _, err := tx.Exec(ctx, `update source_sources set state='ready', updated_at=$2 where id=$1`, sourceID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into chat_source_selections(chat_id,source_id,selected,explicit,updated_at)
		select distinct session.origin_chat_id,$1,true,false,$2::timestamptz
		from source_discovery_candidates candidate
		join source_discovery_sessions session on session.id=candidate.session_id
		join chat_chats chat on chat.id=session.origin_chat_id
		where candidate.source_id=$1 and candidate.status='imported'
		  and session.origin_chat_id is not null
		  and chat.notebook_id=session.notebook_id
		  and chat.creator_user_id=session.user_id
		on conflict(chat_id,source_id) do update
		set selected=true,updated_at=excluded.updated_at
		where chat_source_selections.explicit=false
	`, sourceID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update source_processing_jobs
		set status='succeeded', lease_token=null, lease_expires_at=null, last_error_code=null, updated_at=$2
		where id=$1
	`, jobID, now); err != nil {
		return err
	}
	if err := realtime.NotifyNotebookSources(ctx, tx, notebookID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (q *Queue) Fail(ctx context.Context, jobID, leaseToken, errorCode string) error {
	errorCode = strings.TrimSpace(errorCode)
	if !validErrorCode(errorCode) {
		return errors.New("invalid Source processing error code")
	}
	return q.finish(ctx, jobID, leaseToken, false, errorCode)
}

func (q *Queue) finish(ctx context.Context, jobID, leaseToken string, succeeded bool, errorCode string) error {
	if q == nil || q.pool == nil {
		return errors.New("invalid Source processing Queue")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return err
	}
	var sourceID, notebookID, inputKind, originalObjectKey string
	var discoveryUserID *string
	var current source.State
	err = tx.QueryRow(ctx, `
		select s.id, s.state, s.notebook_id, s.input_kind, s.original_object_key,
			(select session.user_id
			 from source_discovery_candidates candidate
			 join source_discovery_sessions session on session.id=candidate.session_id
			 where candidate.source_id=s.id
			 order by candidate.updated_at desc,candidate.id limit 1)
		from source_processing_jobs j
		join source_sources s on s.id=j.source_id
		where j.id=$1 and j.status='running' and j.lease_token=$2::uuid and j.lease_expires_at > now()
		for update of j, s
	`, jobID, leaseToken).Scan(&sourceID, &current, &notebookID, &inputKind, &originalObjectKey, &discoveryUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if succeeded && current != source.StateVerifying {
		return ErrTransitionConflict
	}
	nextSource := source.StateFailed
	nextJob := "failed"
	var persistedError any = errorCode
	if succeeded {
		nextSource = source.StateReady
		nextJob = "succeeded"
		persistedError = nil
	}
	if _, err := tx.Exec(ctx, `update source_sources set state=$2, updated_at=now() where id=$1`, sourceID, nextSource); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update source_processing_jobs
		set status=$2, lease_token=null, lease_expires_at=null, last_error_code=$3, updated_at=now()
		where id=$1
	`, jobID, nextJob, persistedError); err != nil {
		return err
	}
	if !succeeded && inputKind == "url" && discoveryUserID != nil {
		if err := purgeDiscoveredURLSource(ctx, tx, sourceID, notebookID, *discoveryUserID, originalObjectKey); err != nil {
			return err
		}
	}
	if err := realtime.NotifyNotebookSources(ctx, tx, notebookID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func purgeDiscoveredURLSource(ctx context.Context, tx pgx.Tx, sourceID, notebookID, userID, originalObjectKey string) error {
	objectKeys := []string{originalObjectKey}
	rows, err := tx.Query(ctx, `
		select artifact_object_key from source_evidence_revisions where source_id=$1 order by revision_no
	`, sourceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var objectKey string
		if err := rows.Scan(&objectKey); err != nil {
			rows.Close()
			return err
		}
		objectKeys = append(objectKeys, objectKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		select object_key from source_viewer_artifacts where source_id=$1 order by revision_id,ordinal
	`, sourceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var objectKey string
		if err := rows.Scan(&objectKey); err != nil {
			rows.Close()
			return err
		}
		objectKeys = append(objectKeys, objectKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	type projectionScope struct {
		NotebookID     string `json:"notebook_id"`
		SourceID       string `json:"source_id"`
		RevisionID     string `json:"revision_id"`
		IndexVersionID string `json:"index_version_id"`
	}
	projectionScopes := make([]projectionScope, 0)
	rows, err = tx.Query(ctx, `
		select notebook_id,source_id,revision_id,index_version_id
		from retrieval_source_index_builds where source_id=$1 order by index_version_id,revision_id
	`, sourceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var scope projectionScope
		if err := rows.Scan(&scope.NotebookID, &scope.SourceID, &scope.RevisionID, &scope.IndexVersionID); err != nil {
			rows.Close()
			return err
		}
		projectionScopes = append(projectionScopes, scope)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	objectManifest, err := json.Marshal(objectKeys)
	if err != nil {
		return err
	}
	projectionManifest, err := json.Marshal(projectionScopes)
	if err != nil {
		return err
	}
	discoverySessionIDs := make([]string, 0)
	rows, err = tx.Query(ctx, `
		select distinct session_id from source_discovery_candidates where source_id=$1 order by session_id
	`, sourceID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return err
		}
		discoverySessionIDs = append(discoverySessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `delete from source_discovery_candidates where source_id=$1`, sourceID); err != nil {
		return err
	}
	for _, sessionID := range discoverySessionIDs {
		if err := realtime.NotifySourceDiscovery(ctx, tx, sessionID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		insert into source_purge_jobs(
			id,source_id,notebook_id,created_by_user_id,original_object_key,object_keys,projection_scopes,state
		) values($1,$2,$3,$4,$5,$6,$7,'pending')
	`, "srcpurge_"+uuid.NewString(), sourceID, notebookID, userID, originalObjectKey, objectManifest, projectionManifest); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `delete from source_sources where id=$1`, sourceID)
	return err
}

func validErrorCode(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validTransition(expected, next source.State) bool {
	return (expected == source.StateUploaded && next == source.StateValidating) ||
		(expected == source.StateValidating && next == source.StateNormalizing) ||
		(expected == source.StateNormalizing && next == source.StateSegmenting) ||
		(expected == source.StateSegmenting && next == source.StateIndexing) ||
		(expected == source.StateIndexing && next == source.StateVerifying)
}
