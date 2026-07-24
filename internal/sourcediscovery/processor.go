package sourcediscovery

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Processor struct {
	pool     *pgxpool.Pool
	queue    *Queue
	provider websearch.Provider
}

func NewProcessor(pool *pgxpool.Pool, queue *Queue, provider websearch.Provider) *Processor {
	return &Processor{pool: pool, queue: queue, provider: provider}
}

func (p *Processor) ProcessNext(ctx context.Context) (bool, error) {
	if p == nil || p.pool == nil || p.queue == nil || p.provider == nil {
		return false, errors.New("invalid Source Discovery Processor")
	}
	lease, ok, err := p.queue.Claim(ctx)
	if err != nil || !ok {
		return ok, err
	}
	results, err := p.provider.Search(ctx, websearch.Request{Query: lease.Query, Count: 10})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return true, err
		}
		tx, beginErr := p.pool.Begin(ctx)
		if beginErr != nil {
			return true, beginErr
		}
		defer tx.Rollback(ctx)
		if _, roleErr := tx.Exec(ctx, `set local role nano_worker`); roleErr != nil {
			return true, roleErr
		}
		if failErr := NewStore(tx).FailSearch(ctx, FailSearchCommand{
			SessionID: lease.SessionID, JobID: lease.ID, LeaseToken: lease.LeaseToken,
			ErrorCode: SafeProviderError(err),
		}); failErr != nil {
			return true, failErr
		}
		return true, tx.Commit(ctx)
	}
	candidates := make([]DiscoveredCandidate, 0, len(results))
	for _, result := range results {
		candidates = append(candidates, DiscoveredCandidate{
			ID: "dscand_" + uuid.NewString(), Title: result.Title, URL: result.URL,
			DisplayURL: result.DisplayURL, Snippet: result.Description, ProviderRank: result.Rank,
		})
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return true, err
	}
	if err := NewStore(tx).CompleteSearch(ctx, CompleteSearchCommand{
		SessionID: lease.SessionID, JobID: lease.ID, LeaseToken: lease.LeaseToken,
		Summary: SummaryForQuery(lease.Query), Candidates: candidates,
	}); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func SafeProviderError(err error) string {
	switch {
	case errors.Is(err, websearch.ErrNotConfigured):
		return "discovery_not_configured"
	case errors.Is(err, websearch.ErrTimeout):
		return "discovery_timeout"
	case errors.Is(err, websearch.ErrRateLimited):
		return "discovery_rate_limited"
	case errors.Is(err, websearch.ErrInvalidResponse):
		return "discovery_invalid_response"
	default:
		return "discovery_unavailable"
	}
}
