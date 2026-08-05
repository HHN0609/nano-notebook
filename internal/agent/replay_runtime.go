package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
)

// ErrStudioReplayUnsupported is returned by ReplayControllerRuntime for a
// Studio ('configured') Run. Studio's forced-first-search decision is
// routed by Controller.Execute through QueryContextRuntime.
// BuildQueryContextRequest (controller.go:166-183,276-), not
// ControllerRuntime.BuildDecisionRequest — replaying it needs a
// structurally different request-builder path this package does not
// implement yet. Studio's second decision (final composition) produces a
// FinalDraft, not an Action, so it is out of scope for action-comparison
// regardless (see internal/agenteval's package doc).
var ErrStudioReplayUnsupported = errors.New("Studio decision replay is not yet supported")

// ReplayControllerRuntime resolves which Action Definition scope runID's
// decisions were made under, for internal/agenteval's offline
// decision-replay tooling. It returns base itself (v1's only supported
// replay runtime) so callers can use both its LoadForReplay (concrete,
// not part of the ControllerRuntime interface) and BuildDecisionRequest
// (interface method) on the same value. chatDefinition is the caller's
// already-resolved Leader chat root Reference (the same one cmd/worker
// passes to NewMCPController) — this package cannot resolve it itself
// without importing internal/app, which imports internal/agent.
//
// v1 supports Leader chat Runs (runtime_kind='legacy_role') only.
func ReplayControllerRuntime(ctx context.Context, base *PostgresRuntime, chatDefinition agentcatalog.Reference, runID string) (*PostgresRuntime, agentcatalog.Reference, error) {
	tx, err := base.workerTx(ctx)
	if err != nil {
		return nil, agentcatalog.Reference{}, err
	}
	defer tx.Rollback(ctx)
	var runtimeKind string
	if err := tx.QueryRow(ctx, `select runtime_kind from agent_runs where id=$1`, runID).Scan(&runtimeKind); err != nil {
		return nil, agentcatalog.Reference{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, agentcatalog.Reference{}, err
	}
	switch runtimeKind {
	case "legacy_role":
		return base, chatDefinition, nil
	case "configured":
		return nil, agentcatalog.Reference{}, ErrStudioReplayUnsupported
	default:
		return nil, agentcatalog.Reference{}, fmt.Errorf("unknown Run runtime_kind %q", runtimeKind)
	}
}
