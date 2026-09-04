package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresDiscoverSourcesBackend struct {
	pool     *pgxpool.Pool
	provider websearch.Provider
}

func NewPostgresDiscoverSourcesBackend(pool *pgxpool.Pool, provider websearch.Provider) *PostgresDiscoverSourcesBackend {
	return &PostgresDiscoverSourcesBackend{pool: pool, provider: provider}
}

func (b *PostgresDiscoverSourcesBackend) Discover(ctx context.Context, request DiscoverSourcesRequest) (DiscoverSourcesResult, error) {
	if b == nil || b.pool == nil || b.provider == nil || request.RunID == "" || request.ActionID == "" ||
		request.UserID == "" || request.ChatID == "" || len(request.Queries) < 1 || len(request.Queries) > 3 {
		return DiscoverSourcesResult{}, errors.New("invalid Discover Sources request")
	}
	var notebookID string
	if err := b.pool.QueryRow(ctx, `select notebook_id from chat_chats where id=$1 and creator_user_id=$2`, request.ChatID, request.UserID).Scan(&notebookID); err != nil {
		return DiscoverSourcesResult{}, err
	}
	sessionID := deterministicDiscoverySessionID(request.RunID, request.ActionID)
	query := truncateLeaderRunes(strings.Join(request.Queries, " · "), 500)
	tx, err := b.workerTx(ctx)
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	store := sourcediscovery.NewStore(tx)
	session, err := store.EnsureChatAgentSession(ctx, sourcediscovery.ChatAgentSessionCommand{
		ID: sessionID, NotebookID: notebookID, UserID: request.UserID, OriginChatID: request.ChatID,
		AgentRunID: request.RunID, ActionID: request.ActionID, Query: query,
	})
	if err == nil {
		_, err = tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, request.RunID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	if session.Status != sourcediscovery.StatusSearching {
		return b.result(ctx, sessionID, request.ChatID, session.Status)
	}

	groups := make([][]websearch.Candidate, 0, len(request.Queries))
	for _, query := range request.Queries {
		candidates, searchErr := b.provider.Search(ctx, websearch.Request{Query: query, Count: 10})
		if searchErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return DiscoverSourcesResult{}, contextErr
			}
			return b.fail(ctx, sessionID, request.ChatID, sourcediscovery.SafeProviderError(searchErr))
		}
		if len(candidates) > 10 {
			return b.fail(ctx, sessionID, request.ChatID, "discovery_invalid_response")
		}
		groups = append(groups, candidates)
	}
	candidates := mergeResearchCandidates(groups)
	tx, err = b.workerTx(ctx)
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	store = sourcediscovery.NewStore(tx)
	if err = store.CompleteChatAgentSession(ctx, sessionID, sourcediscovery.SummaryForQuery(query), candidates); err == nil {
		_, err = tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, request.RunID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	return b.result(ctx, sessionID, request.ChatID, sourcediscovery.StatusReady)
}

func (b *PostgresDiscoverSourcesBackend) fail(ctx context.Context, sessionID, chatID, errorCode string) (DiscoverSourcesResult, error) {
	tx, err := b.workerTx(ctx)
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	if err = sourcediscovery.NewStore(tx).FailChatAgentSession(ctx, sessionID, errorCode); err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	return b.result(ctx, sessionID, chatID, sourcediscovery.StatusFailed)
}

func (b *PostgresDiscoverSourcesBackend) result(ctx context.Context, sessionID, chatID string, status sourcediscovery.Status) (DiscoverSourcesResult, error) {
	counts, err := sourcediscovery.NewStore(b.pool).CountsForChat(ctx, sessionID, chatID)
	if err != nil {
		return DiscoverSourcesResult{}, err
	}
	return DiscoverSourcesResult{
		SessionID: sessionID, Status: string(status), NovelCandidateCount: counts.Novel,
		ExistingCandidateCount: counts.Existing, ExistingSelectedCount: counts.ExistingSelected,
	}, nil
}

func (b *PostgresDiscoverSourcesBackend) workerTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func deterministicDiscoverySessionID(runID, actionID string) string {
	return "dss_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(runID+"\x00"+actionID)).String()
}
