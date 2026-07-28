package studio

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound            = errors.New("Studio Output not found")
	ErrIdempotencyMismatch = errors.New("Studio Output idempotency mismatch")
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Output struct {
	ID          string          `json:"id"`
	NotebookID  string          `json:"notebook_id"`
	Kind        Kind            `json:"kind"`
	Locale      string          `json:"locale"`
	Title       *string         `json:"title"`
	RunID       string          `json:"run_id"`
	SourceCount int             `json:"source_count"`
	Status      string          `json:"status"`
	ErrorCode   *string         `json:"error_code,omitempty"`
	Artifact    json.RawMessage `json:"artifact,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Store struct{ db DBTX }

func NewStore(db DBTX) *Store { return &Store{db: db} }

func (s *Store) Idempotent(ctx context.Context, userID, key, requestHash string) (Output, bool, error) {
	var output Output
	var storedHash string
	err := scanOutput(s.db.QueryRow(ctx, outputSelect+`
		where created_by_user_id=$1 and idempotency_key=$2`, userID, key), &output, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Output{}, false, nil
	}
	if err != nil {
		return Output{}, false, err
	}
	if storedHash != requestHash {
		return Output{}, false, ErrIdempotencyMismatch
	}
	return output, true, nil
}

func (s *Store) Get(ctx context.Context, outputID string) (Output, error) {
	var output Output
	err := scanOutput(s.db.QueryRow(ctx, outputSelect+` where id=$1`, outputID), &output, nil)
	if errors.Is(err, pgx.ErrNoRows) {
		return Output{}, ErrNotFound
	}
	return output, err
}

func (s *Store) List(ctx context.Context, notebookID string) ([]Output, error) {
	rows, err := s.db.Query(ctx, outputSelect+` where notebook_id=$1 order by created_at desc,id desc limit 100`, notebookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	outputs := make([]Output, 0)
	for rows.Next() {
		var output Output
		if err := scanOutput(rows, &output, nil); err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	return outputs, rows.Err()
}

func (s *Store) Delete(ctx context.Context, outputID string) error {
	result, err := s.db.Exec(ctx, `
		with target as (
			select root_agent_run_id from studio_outputs where id=$1 for update
		), cancelled_run as (
			update agent_runs set status='cancelled',error_code=null,finished_at=coalesce(finished_at,now()),updated_at=now()
			where id=(select root_agent_run_id from target) and status in ('queued','running') returning id
		), cancelled_job as (
			update agent_jobs set status='cancelled',lease_token=null,lease_expires_at=null,
				finished_at=coalesce(finished_at,now()),updated_at=now()
			where run_id=(select root_agent_run_id from target) and status in ('queued','running','waiting') returning id
		)
		delete from studio_outputs where id=$1
	`, outputID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const outputSelect = `
	select id,notebook_id,kind,locale,title,root_agent_run_id,selected_source_count,status,error_code,
		artifact,created_at,started_at,finished_at,updated_at,request_hash
	from studio_outputs`

type rowScanner interface{ Scan(...any) error }

func scanOutput(row rowScanner, output *Output, requestHash *string) error {
	var artifact []byte
	var storedHash string
	if err := row.Scan(&output.ID, &output.NotebookID, &output.Kind, &output.Locale, &output.Title, &output.RunID,
		&output.SourceCount, &output.Status, &output.ErrorCode, &artifact, &output.CreatedAt, &output.StartedAt,
		&output.FinishedAt, &output.UpdatedAt, &storedHash); err != nil {
		return err
	}
	if len(artifact) > 0 {
		output.Artifact = append(json.RawMessage(nil), artifact...)
	}
	if requestHash != nil {
		*requestHash = storedHash
	}
	return nil
}
