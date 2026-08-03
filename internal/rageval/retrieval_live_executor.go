package rageval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/qdrantstore"
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RetrievalSourceManifest struct {
	SchemaVersion  int                   `json:"schema_version"`
	IndexVersionID string                `json:"index_version_id"`
	UserID         string                `json:"user_id"`
	NotebookID     string                `json:"notebook_id"`
	Cases          []RetrievalSourceCase `json:"cases"`
}

type RetrievalSourceCase struct {
	CaseID             string `json:"case_id"`
	SourceID           string `json:"source_id"`
	EvidenceRevisionID string `json:"evidence_revision_id"`
	UserID             string `json:"user_id,omitempty"`
	NotebookID         string `json:"notebook_id,omitempty"`
}

type retrievalModel interface {
	Embed(context.Context, models.EmbeddingRequest) (models.EmbeddingOutcome, error)
	Rerank(context.Context, models.RerankRequest) (models.RerankOutcome, error)
}

type cachedRetrievalModel struct {
	next       retrievalModel
	mu         sync.Mutex
	embeddings map[string]models.EmbeddingOutcome
}

func (m *cachedRetrievalModel) Embed(ctx context.Context, request models.EmbeddingRequest) (models.EmbeddingOutcome, error) {
	key := fmt.Sprintf("%s\x00%d\x00%s", request.Model, request.Dimensions, strings.Join(request.Inputs, "\x00"))
	m.mu.Lock()
	outcome, ok := m.embeddings[key]
	m.mu.Unlock()
	if ok {
		return outcome, nil
	}
	outcome, err := m.next.Embed(ctx, request)
	if err != nil {
		return models.EmbeddingOutcome{}, err
	}
	m.mu.Lock()
	if m.embeddings == nil {
		m.embeddings = make(map[string]models.EmbeddingOutcome)
	}
	m.embeddings[key] = outcome
	m.mu.Unlock()
	return outcome, nil
}

func (m *cachedRetrievalModel) Rerank(ctx context.Context, request models.RerankRequest) (models.RerankOutcome, error) {
	return m.next.Rerank(ctx, request)
}

type RetrievalLiveExecutor struct {
	pool              *pgxpool.Pool
	service           *agent.EvidenceSearchService
	manifest          RetrievalSourceManifest
	caseByID          map[string]RetrievalSourceCase
	attemptByNotebook map[string]agent.Attempt
	closed            bool
}

func NewRetrievalLiveExecutor(pool *pgxpool.Pool, vectors *qdrantstore.Client, model retrievalModel, manifest RetrievalSourceManifest) (*RetrievalLiveExecutor, error) {
	if pool == nil || vectors == nil || model == nil || manifest.SchemaVersion != 1 ||
		strings.TrimSpace(manifest.IndexVersionID) == "" || strings.TrimSpace(manifest.UserID) == "" ||
		strings.TrimSpace(manifest.NotebookID) == "" || len(manifest.Cases) == 0 {
		return nil, errors.New("invalid retrieval sweep source manifest")
	}
	caseByID := make(map[string]RetrievalSourceCase, len(manifest.Cases))
	for _, item := range manifest.Cases {
		if strings.TrimSpace(item.CaseID) == "" || strings.TrimSpace(item.SourceID) == "" ||
			strings.TrimSpace(item.EvidenceRevisionID) == "" {
			return nil, errors.New("invalid retrieval sweep source Case")
		}
		if _, duplicate := caseByID[item.CaseID]; duplicate {
			return nil, errors.New("duplicated retrieval sweep source Case")
		}
		caseByID[item.CaseID] = item
	}
	return &RetrievalLiveExecutor{
		pool: pool, manifest: manifest, caseByID: caseByID,
		service:           agent.NewEvidenceSearchService(pool, vectors, &cachedRetrievalModel{next: model, embeddings: make(map[string]models.EmbeddingOutcome)}),
		attemptByNotebook: make(map[string]agent.Attempt),
	}, nil
}

func (e *RetrievalLiveExecutor) Search(ctx context.Context, evalCase RetrievalCase, override RetrievalSearchOverride) (retrieval.SearchResult, error) {
	if e == nil || e.pool == nil || e.service == nil {
		return retrieval.SearchResult{}, errors.New("nil retrieval sweep Executor")
	}
	sourceCase, ok := e.caseByID[evalCase.ID]
	if !ok {
		return retrieval.SearchResult{}, fmt.Errorf("retrieval sweep Source for Case %q is missing", evalCase.ID)
	}
	notebookID := retrievalCaseNotebookID(e.manifest, sourceCase)
	attempt, admitted := e.attemptByNotebook[notebookID]
	if !admitted {
		var err error
		attempt, err = e.admitNotebook(ctx, notebookID)
		if err != nil {
			return retrieval.SearchResult{}, err
		}
	}
	for _, activeAttempt := range e.attemptByNotebook {
		if err := e.refreshLease(ctx, activeAttempt); err != nil {
			return retrieval.SearchResult{}, err
		}
	}
	return e.service.SearchEvidenceWithOverrides(ctx, attempt, evalCase.Question, agent.RetrievalSearchOverrides{
		DenseCandidates: override.DenseCandidates, SparseCandidates: override.SparseCandidates,
		RRFK: override.RRFK, RerankCandidates: override.RerankCandidates,
	})
}

func (e *RetrievalLiveExecutor) refreshLease(ctx context.Context, attempt agent.Attempt) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		update agent_jobs set lease_expires_at=now()+interval '10 minutes',updated_at=now()
		where run_id=$1 and id=$2 and status='running'
	`, attempt.RunID, attempt.JobID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("retrieval sweep Job lease was lost")
	}
	runTag, err := tx.Exec(ctx, `
		update agent_runs set deadline_at=greatest(deadline_at,now()+interval '10 minutes'),updated_at=now()
		where id=$1 and status='running'
	`, attempt.RunID)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 {
		return errors.New("retrieval sweep Run deadline refresh lost authority")
	}
	return tx.Commit(ctx)
}

func (e *RetrievalLiveExecutor) Close(ctx context.Context) error {
	if e == nil || e.pool == nil || e.closed {
		return nil
	}
	e.closed = true
	for _, attempt := range e.attemptByNotebook {
		tx, err := e.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, `
			update agent_jobs set status='failed',finished_at=now(),lease_token=null,lease_expires_at=null,updated_at=now()
			where run_id=$1 and status='running'
		`, attempt.RunID); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, `
			update agent_runs set status='failed',error_code='retrieval_sweep_completed_without_agent',finished_at=now(),updated_at=now()
			where id=$1 and status='running'
		`, attempt.RunID); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *RetrievalLiveExecutor) admitNotebook(ctx context.Context, notebookID string) (agent.Attempt, error) {
	config := PinnedConfig{
		ComposerModel: "retrieval-sweep", PromptVersion: "retrieval-sweep-v1", AgentConfigID: "retrieval-sweep-v1",
	}
	var userID string
	sourceIDs := make([]string, 0)
	for _, item := range e.caseByID {
		if retrievalCaseNotebookID(e.manifest, item) != notebookID {
			continue
		}
		if userID == "" {
			userID = retrievalCaseUserID(e.manifest, item)
		}
		sourceIDs = append(sourceIDs, item.SourceID)
	}
	if userID == "" || len(sourceIDs) == 0 {
		return agent.Attempt{}, errors.New("retrieval sweep notebook has no pinned Sources")
	}
	_, attempt, err := admitRetrievalRun(ctx, e.pool, userID, notebookID, "notebook-"+notebookID, "retrieval sweep", sourceIDs, config)
	if err != nil {
		return agent.Attempt{}, fmt.Errorf("admit retrieval sweep Run for notebook %s: %w", notebookID, err)
	}
	e.attemptByNotebook[notebookID] = attempt
	return attempt, nil
}

func retrievalCaseNotebookID(manifest RetrievalSourceManifest, item RetrievalSourceCase) string {
	if strings.TrimSpace(item.NotebookID) != "" {
		return item.NotebookID
	}
	return manifest.NotebookID
}

func retrievalCaseUserID(manifest RetrievalSourceManifest, item RetrievalSourceCase) string {
	if strings.TrimSpace(item.UserID) != "" {
		return item.UserID
	}
	return manifest.UserID
}

func admitRetrievalRun(ctx context.Context, pool *pgxpool.Pool, userID, notebookID, caseID, question string, sourceIDs []string, config PinnedConfig) (string, agent.Attempt, error) {
	runID := "evalrun_" + uuid.NewString()
	chatID := "evalchat_" + uuid.NewString()
	messageID := "evalmsg_" + uuid.NewString()
	jobID := "evaljob_" + uuid.NewString()
	leaseToken := uuid.NewString()
	traceScope, err := agent.NewTraceScope(agent.DiscardTraceSink{})
	if err != nil {
		return "", agent.Attempt{}, err
	}
	defer traceScope.Rollback()
	traceContext := agent.ContextWithTraceScope(ctx, traceScope)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", agent.Attempt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `set local role nano_worker`); err != nil {
		return "", agent.Attempt{}, err
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from notebook_memberships where notebook_id=$1 and user_id=$2)`, notebookID, userID).Scan(&authorized); err != nil {
		return "", agent.Attempt{}, err
	}
	if !authorized {
		return "", agent.Attempt{}, errors.New("retrieval sweep principal is not a Notebook member")
	}
	if _, err := tx.Exec(ctx, `insert into chat_chats(id,notebook_id,creator_user_id,title) values($1,$2,$3,$4)`, chatID, notebookID, userID, "Retrieval Sweep: "+caseID); err != nil {
		return "", agent.Attempt{}, err
	}
	if _, err := tx.Exec(ctx, `insert into chat_messages(id,chat_id,role,content) values($1,$2,'user',$3)`, messageID, chatID, question); err != nil {
		return "", agent.Attempt{}, err
	}
	runConfig := agent.RunConfig{
		ID: config.AgentConfigID, ActionDecisionLimit: 1, FinalDecisionLimit: 1, ActionLimit: 1, ActionBatchLimit: 1,
		ActionResultByteLimit: 16 * 1024, ActionResultsByteLimit: 64 * 1024, Deadline: 10 * time.Minute,
	}
	store := agent.NewStore(tx)
	if err := store.CreateQueued(ctx, runID, userID, chatID, messageID, config.ComposerModel, config.PromptVersion, "UTC", runConfig); err != nil {
		return "", agent.Attempt{}, err
	}
	// PinEvidenceSet resolves the active Index Version; sweep evaluates the
	// current baseline instead of a candidate built for promotion.
	if err := store.PinEvidenceSet(ctx, runID, userID, sourceIDs); err != nil {
		return "", agent.Attempt{}, err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_jobs(id,kind,run_id,status,attempt_no,lease_token,lease_expires_at,started_at)
		values($1,'agent_run',$2,'running',1,$3::uuid,now()+interval '10 minutes',now())
	`, jobID, runID, leaseToken); err != nil {
		return "", agent.Attempt{}, err
	}
	runTag, err := tx.Exec(ctx, `update agent_runs set status='running',started_at=now(),updated_at=now() where id=$1 and status='queued'`, runID)
	if err != nil {
		return "", agent.Attempt{}, err
	}
	if runTag.RowsAffected() != 1 {
		return "", agent.Attempt{}, errors.New("retrieval sweep Run admission lost authority")
	}
	if err := agent.StartRunTraceInTx(traceContext, tx, runID, config.ComposerModel, config.PromptVersion, nil); err != nil {
		return "", agent.Attempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", agent.Attempt{}, err
	}
	_ = traceScope.PublishAfterCommit(traceContext)
	return runID, agent.Attempt{JobID: jobID, RunID: runID, AttemptNo: 1, LeaseToken: leaseToken}, nil
}
