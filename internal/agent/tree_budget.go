package agent

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type treeBudgetCharge struct {
	ModelCalls   int
	Actions      int
	ContextBytes int
	ResultBytes  int
}

func chargeConfiguredTreeInTx(ctx context.Context, tx pgx.Tx, runID string, charge treeBudgetCharge) error {
	if tx == nil || runID == "" || charge.ModelCalls < 0 || charge.Actions < 0 || charge.ContextBytes < 0 || charge.ResultBytes < 0 {
		return errors.New("invalid Agent Tree budget charge")
	}
	if charge == (treeBudgetCharge{}) {
		return nil
	}
	var configured bool
	if err := tx.QueryRow(ctx, `select runtime_kind='configured' from agent_runs where id=$1`, runID).Scan(&configured); err != nil {
		return err
	}
	if !configured {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		update agent_trees
		set model_calls_consumed=model_calls_consumed+$2,
			actions_consumed=actions_consumed+$3,
			context_bytes_consumed=context_bytes_consumed+$4,
			result_bytes_consumed=result_bytes_consumed+$5,
			updated_at=now()
		where id=(select tree_id from agent_runs where id=$1)
		  and model_calls_consumed+$2<=model_call_limit
		  and actions_consumed+$3<=action_limit
		  and context_bytes_consumed+$4<=context_byte_limit
		  and result_bytes_consumed+$5<=result_byte_limit
	`, runID, charge.ModelCalls, charge.Actions, charge.ContextBytes, charge.ResultBytes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("Agent Tree budget exhausted")
	}
	return nil
}
