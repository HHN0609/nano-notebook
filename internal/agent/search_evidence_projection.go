package agent

import (
	"context"
	"encoding/json"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

const maxSearchEvidenceModelOutputBytes = 8 * 1024

func (r *PostgresRuntime) loadSearchEvidenceModelCandidates(ctx context.Context, execution Execution, prefix CheckpointPrefix) ([]retrieval.EvidenceCandidate, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, proposal := range prefix.Proposals {
		for _, action := range proposal.Actions {
			if action.Name != "search_evidence" || action.Result == nil || action.Result.Status != ActionSucceeded {
				continue
			}
			result, err := decodeSearchEvidenceResult(action.Result.Output)
			if err != nil {
				return nil, err
			}
			if result.Legacy {
				continue
			}
			for _, reference := range result.Evidence {
				if _, duplicate := seen[reference.ChunkID]; duplicate {
					continue
				}
				seen[reference.ChunkID] = struct{}{}
				ids = append(ids, reference.ChunkID)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	search := &EvidenceSearchService{pool: r.pool}
	var scope pinnedSearchScope
	var err error
	if execution.ReplayOnly {
		scope, err = search.loadPinnedScopeForReplay(ctx, execution.RunID, execution.SelectedSourceCount)
	} else {
		scope, err = search.loadPinnedScope(ctx, execution.Attempt)
	}
	if err != nil {
		return nil, err
	}
	return search.reloadCandidates(ctx, scope, ids)
}

func projectSearchEvidenceForModel(execution Execution, raw json.RawMessage, candidates []retrieval.EvidenceCandidate) (json.RawMessage, error) {
	result, err := decodeSearchEvidenceResult(raw)
	if err != nil {
		return nil, err
	}
	limit := maxSearchEvidenceModelOutputBytes
	if execution.ActionResultByteLimit > 0 && execution.ActionResultByteLimit < limit {
		limit = execution.ActionResultByteLimit
	}
	if result.Legacy {
		return buildLegacySearchEvidenceModelOutput(result, limit)
	}
	if isSourceFirstResearchExecution(execution) {
		return buildSourceFirstSearchEvidenceModelOutput(result, candidates, limit)
	}
	return buildSearchEvidenceModelOutput(result, candidates, limit)
}
