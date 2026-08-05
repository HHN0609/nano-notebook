package agenteval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ActionFailureCandidate is one already-flagged domain_error at a single
// Action within a decision — raw material for a human to review and
// decide whether it deserves a hand-labeled DecisionCase. Never
// auto-converted into a case.
type ActionFailureCandidate struct {
	RunID             string          `json:"run_id"`
	DecisionNo        int             `json:"decision_no"`
	ActionIndex       int             `json:"action_index"`
	ChosenActionName  string          `json:"chosen_action_name"`
	ChosenActionInput json.RawMessage `json:"chosen_action_input"`
	ErrorCode         string          `json:"error_code"`
	RunStatus         string          `json:"run_status"`
	OccurredAt        time.Time       `json:"occurred_at"`
}

// DiscoverActionResultFailures lists the most recent Action-level
// domain_error Checkpoints, joined back to the sibling action_proposal
// Checkpoint (same run_id+decision_no) to recover which Action name/input
// the model actually chose. A decision batch can propose several Actions
// and only one might have failed, so the sibling proposal's actions array
// is indexed by THIS row's action_index, not assumed 1:1.
func DiscoverActionResultFailures(ctx context.Context, pool *pgxpool.Pool, limit int) ([]ActionFailureCandidate, error) {
	if limit < 1 {
		limit = 50
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select ar.run_id, ar.decision_no, ar.action_index,
			ap.payload->'actions'->ar.action_index->>'name',
			ap.payload->'actions'->ar.action_index->'input',
			coalesce(ar.payload->>'error_code',''),
			r.status,
			ar.created_at
		from agent_run_checkpoints ar
		join agent_run_checkpoints ap
			on ap.run_id = ar.run_id and ap.decision_no = ar.decision_no and ap.kind = 'action_proposal'
		join agent_runs r on r.id = ar.run_id
		where ar.kind = 'action_result' and ar.payload->>'status' = 'domain_error'
		order by ar.created_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]ActionFailureCandidate, 0)
	for rows.Next() {
		var candidate ActionFailureCandidate
		if err := rows.Scan(
			&candidate.RunID, &candidate.DecisionNo, &candidate.ActionIndex,
			&candidate.ChosenActionName, &candidate.ChosenActionInput,
			&candidate.ErrorCode, &candidate.RunStatus, &candidate.OccurredAt,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return candidates, nil
}

// TerminalRunFailure is one Run that terminalized as 'failed' — a coarser
// discovery signal than ActionFailureCandidate (no per-Action detail),
// useful when the failure happened before any Action was even proposed.
type TerminalRunFailure struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	ErrorCode  string    `json:"error_code"`
	OccurredAt time.Time `json:"occurred_at"`
}

func DiscoverTerminalRunFailures(ctx context.Context, pool *pgxpool.Pool, limit int) ([]TerminalRunFailure, error) {
	if limit < 1 {
		limit = 50
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select id, status, coalesce(error_code,''), created_at
		from agent_runs
		where status = 'failed'
		order by created_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	failures := make([]TerminalRunFailure, 0)
	for rows.Next() {
		var failure TerminalRunFailure
		if err := rows.Scan(&failure.RunID, &failure.Status, &failure.ErrorCode, &failure.OccurredAt); err != nil {
			return nil, err
		}
		failures = append(failures, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return failures, nil
}
