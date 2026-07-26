package sourcediscovery

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Lease struct {
	ID             string
	SessionID      string
	Query          string
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
		return Lease{}, false, errors.New("invalid Source Discovery Queue")
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Lease{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return Lease{}, false, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return Lease{}, false, err
	}
	exhaustedRows, err := tx.Query(ctx, `
		with exhausted as (
			update source_discovery_jobs
			set status='failed',lease_token=null,lease_expires_at=null,last_error_code='retry_exhausted',updated_at=$1
			where status='running' and lease_expires_at <= $1 and attempt_no >= 3
			returning session_id
		)
		update source_discovery_sessions s
		set status='failed',error_code='discovery_unavailable',completed_at=$1,updated_at=$1
		from exhausted e where s.id=e.session_id and s.status='searching'
		returning s.id
	`, now)
	if err != nil {
		return Lease{}, false, err
	}
	exhaustedSessionIDs := make([]string, 0)
	for exhaustedRows.Next() {
		var sessionID string
		if err := exhaustedRows.Scan(&sessionID); err != nil {
			exhaustedRows.Close()
			return Lease{}, false, err
		}
		exhaustedSessionIDs = append(exhaustedSessionIDs, sessionID)
	}
	if err := exhaustedRows.Err(); err != nil {
		exhaustedRows.Close()
		return Lease{}, false, err
	}
	exhaustedRows.Close()
	for _, sessionID := range exhaustedSessionIDs {
		if err := realtime.NotifySourceDiscovery(ctx, tx, sessionID); err != nil {
			return Lease{}, false, err
		}
	}
	if _, err := tx.Exec(ctx, `
		update source_discovery_jobs
		set status='queued',lease_token=null,lease_expires_at=null,available_at=$1,updated_at=$1
		where status='running' and lease_expires_at <= $1 and attempt_no < 3
	`, now); err != nil {
		return Lease{}, false, err
	}
	var jobID string
	if err := tx.QueryRow(ctx, `
		select j.id
		from source_discovery_jobs j join source_discovery_sessions s on s.id=j.session_id
		where j.status='queued' and j.available_at <= $1 and j.attempt_no < 3 and s.status='searching'
		order by j.available_at,j.created_at,j.id
		for update of j skip locked limit 1
	`, now).Scan(&jobID); errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Lease{}, false, err
		}
		return Lease{}, false, nil
	} else if err != nil {
		return Lease{}, false, err
	}
	token := uuid.NewString()
	expiresAt := now.Add(q.leaseDuration)
	var lease Lease
	if err := tx.QueryRow(ctx, `
		update source_discovery_jobs j
		set status='running',attempt_no=attempt_no+1,lease_token=$2::uuid,lease_expires_at=$3,updated_at=$1
		from source_discovery_sessions s
		where j.id=$4 and s.id=j.session_id
		returning j.id,j.session_id,s.query,j.attempt_no,j.lease_token::text,j.lease_expires_at
	`, now, token, expiresAt, jobID).Scan(
		&lease.ID, &lease.SessionID, &lease.Query, &lease.AttemptNo, &lease.LeaseToken, &lease.LeaseExpiresAt,
	); err != nil {
		return Lease{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Lease{}, false, err
	}
	return lease, true, nil
}
