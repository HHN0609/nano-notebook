package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/studio"
	"github.com/jackc/pgx/v5"
)

var studioLocalePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})?$`)

var (
	errStudioForbidden       = errors.New("Studio Output mutation forbidden")
	errStudioNotebookMissing = errors.New("Studio Output Notebook not found")
)

func (s *Server) notebookStudioOutputs(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	switch r.Method {
	case http.MethodGet:
		s.listStudioOutputs(w, r, userID, notebookID)
	case http.MethodPost:
		s.createStudioOutput(w, r, userID, notebookID)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
	}
}

func (s *Server) listStudioOutputs(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	var outputs []studio.Output
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		if err := requireStudioMembership(r.Context(), tx, userID, notebookID, false); err != nil {
			return err
		}
		var err error
		outputs, err = studio.NewStore(tx).List(r.Context(), notebookID)
		return err
	})
	if errors.Is(err, errStudioNotebookMissing) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outputs": outputs})
}

func (s *Server) createStudioOutput(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		writeError(w, r, http.StatusBadRequest, "idempotency_required", "error.idempotency_required")
		return
	}
	var request struct {
		Kind      string   `json:"kind"`
		Locale    string   `json:"locale"`
		SourceIDs []string `json:"source_ids"`
	}
	if !readJSON(w, r, &request) {
		return
	}
	kind, err := studio.ParseKind(request.Kind)
	if err != nil || !studioLocalePattern.MatchString(request.Locale) || len(request.SourceIDs) < 1 || len(request.SourceIDs) > 50 {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "error.studio_output_invalid")
		return
	}
	seen := make(map[string]bool, len(request.SourceIDs))
	for index, sourceID := range request.SourceIDs {
		request.SourceIDs[index] = strings.TrimSpace(sourceID)
		if request.SourceIDs[index] == "" || seen[request.SourceIDs[index]] {
			writeError(w, r, http.StatusBadRequest, "validation_failed", "error.studio_output_invalid")
			return
		}
		seen[request.SourceIDs[index]] = true
	}
	binding, ok := s.studioAgents[kind]
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "studio_unavailable", "error.studio_unavailable")
		return
	}
	canonical, _ := json.Marshal(struct {
		NotebookID string   `json:"notebook_id"`
		Kind       string   `json:"kind"`
		Locale     string   `json:"locale"`
		SourceIDs  []string `json:"source_ids"`
	}{notebookID, kind.String(), request.Locale, request.SourceIDs})
	hash := requestHash(canonical)
	outputID, idErr := newOpaqueID("out")
	if idErr != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	runID, idErr := newOpaqueID("run")
	if idErr != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	jobID, idErr := newOpaqueID("job")
	if idErr != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	var output studio.Output
	err = s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `select pg_advisory_xact_lock(hashtextextended($1,0))`, "studio:"+userID+":"+key); err != nil {
			return err
		}
		if err := requireStudioMembership(r.Context(), tx, userID, notebookID, true); err != nil {
			return err
		}
		store := studio.NewStore(tx)
		if existing, found, err := store.Idempotent(r.Context(), userID, key, hash); err != nil {
			return err
		} else if found {
			output = existing
			return nil
		}
		manifest, err := json.Marshal(map[string]any{
			"agent_release": binding.Release.String(), "kind": kind.String(), "locale": request.Locale,
			"selected_source_count": len(request.SourceIDs), "notebook_id": notebookID, "output_id": outputID,
		})
		if err != nil {
			return err
		}
		deadline := time.Now().Add(s.cfg.AgentRun.Deadline)
		treeID := "tree_" + runID
		if _, err := tx.Exec(r.Context(), `
			insert into agent_trees(id,absolute_deadline,model_call_limit,action_limit,context_byte_limit,result_byte_limit,context_bytes_consumed)
			values($1,$2,$3,$4,$5,$6,$7)
		`, treeID, deadline, binding.Definition.Limits.ModelCalls, binding.Definition.Limits.Actions,
			binding.Definition.Limits.ContextBytes, binding.Definition.Limits.ResultBytes, len(manifest)); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			insert into agent_runs(
				id,user_id,status,runtime_kind,tree_id,definition_identity,definition_version,definition_sha256,
				executor_identity,model_policy_identity,model_policy_version,model_policy_sha256,provider_model,parent_context_manifest
			) values($1,$2,'queued','configured',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)
		`, runID, userID, treeID, binding.Definition.Identity, binding.Definition.Version, binding.Definition.SHA256,
			binding.Definition.Executor, binding.Policy.Identity, binding.Policy.Version, binding.Policy.SHA256,
			binding.Policy.ProviderModel, string(manifest)); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			insert into studio_outputs(id,notebook_id,created_by_user_id,kind,locale,root_agent_run_id,selected_source_count,status,idempotency_key,request_hash)
			values($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9)
		`, outputID, notebookID, userID, kind.String(), request.Locale, runID, len(request.SourceIDs), key, hash); err != nil {
			return err
		}
		if result, err := tx.Exec(r.Context(), `update agent_trees set root_agent_run_id=$2,updated_at=now() where id=$1 and root_agent_run_id is null`, treeID, runID); err != nil {
			return err
		} else if result.RowsAffected() != 1 {
			return errors.New("Studio Agent Tree root was not pinned")
		}
		agentStore := agent.NewStore(tx)
		if err := agentStore.PinEvidenceSet(r.Context(), runID, userID, request.SourceIDs); err != nil {
			return fmt.Errorf("pin Studio evidence: %w", err)
		}
		if err := jobs.NewStore(tx).CreateAgentRun(r.Context(), jobID, runID); err != nil {
			return fmt.Errorf("create Studio job: %w", err)
		}
		if err := agent.StartRunTraceInTx(r.Context(), tx, runID, binding.Policy.ProviderModel, binding.Definition.Reference().String(), nil); err != nil {
			return fmt.Errorf("start Studio trace: %w", err)
		}
		if result, err := tx.Exec(r.Context(), `update agent_runs set user_id=null where id=$1 and runtime_kind='configured' and user_id=$2`, runID, userID); err != nil {
			return err
		} else if result.RowsAffected() != 1 {
			return errors.New("Studio Agent Run ownership was not finalized")
		}
		if _, err := tx.Exec(r.Context(), `select pg_notify('nano_agent_jobs',$1)`, jobID); err != nil {
			return err
		}
		output, err = store.Get(r.Context(), outputID)
		if err != nil {
			return fmt.Errorf("reload Studio Output: %w", err)
		}
		return nil
	})
	switch {
	case errors.Is(err, errStudioForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "error.forbidden")
	case errors.Is(err, errStudioNotebookMissing):
		writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
	case errors.Is(err, studio.ErrIdempotencyMismatch):
		writeError(w, r, http.StatusConflict, "idempotency_mismatch", "error.idempotency_mismatch")
	case errors.Is(err, agent.ErrEvidenceSetInvalid):
		writeError(w, r, http.StatusConflict, "evidence_set_invalid", "error.evidence_set_invalid")
	case err != nil:
		slog.ErrorContext(r.Context(), "Studio Output admission failed", "error", err, "notebook_id", notebookID, "kind", kind)
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"output": output})
	}
}

func (s *Server) studioOutputByID(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "error.session_expired")
		return
	}
	outputID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/studio-outputs/"), "/")
	if outputID == "" || strings.Contains(outputID, "/") {
		writeError(w, r, http.StatusNotFound, "not_found", "error.studio_output_not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		var output studio.Output
		err := s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			var err error
			output, err = studio.NewStore(tx).Get(r.Context(), outputID)
			return err
		})
		if errors.Is(err, studio.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "error.studio_output_not_found")
		} else if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"output": output})
		}
	case http.MethodDelete:
		if !validCSRF(r) {
			writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
			return
		}
		err := s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			output, err := studio.NewStore(tx).Get(r.Context(), outputID)
			if err != nil {
				return err
			}
			if err := requireStudioMembership(r.Context(), tx, user.ID, output.NotebookID, true); err != nil {
				return err
			}
			return studio.NewStore(tx).Delete(r.Context(), outputID)
		})
		if errors.Is(err, errStudioForbidden) {
			writeError(w, r, http.StatusForbidden, "forbidden", "error.forbidden")
		} else if errors.Is(err, studio.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "error.studio_output_not_found")
		} else if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
	}
}

func requireStudioMembership(ctx context.Context, tx pgx.Tx, _ string, notebookID string, maintain bool) error {
	var readable bool
	if err := tx.QueryRow(ctx, `select nano_has_notebook_capability($1,'source.read')`, notebookID).Scan(&readable); err != nil {
		return err
	}
	if !readable {
		return errStudioNotebookMissing
	}
	if !maintain {
		return nil
	}
	var mutable bool
	if err := tx.QueryRow(ctx, `select nano_has_notebook_capability($1,'source.maintain')`, notebookID).Scan(&mutable); err != nil {
		return err
	}
	if !mutable {
		return errStudioForbidden
	}
	return nil
}
