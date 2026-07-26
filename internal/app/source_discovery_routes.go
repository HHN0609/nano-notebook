package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/fetcher"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/jackc/pgx/v5"
)

func (s *Server) notebookSourceDiscoverySessions(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}
	var request struct {
		Query        string  `json:"query"`
		OriginChatID *string `json:"origin_chat_id"`
	}
	if !readJSON(w, r, &request) {
		return
	}
	sessionID, err := newOpaqueID("dsc")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	jobID, err := newOpaqueID("dscjob")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	var session sourcediscovery.Session
	err = s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var createErr error
		session, createErr = sourcediscovery.NewStore(tx).CreateSession(r.Context(), sourcediscovery.CreateSessionCommand{
			ID: sessionID, JobID: jobID, NotebookID: notebookID, UserID: userID,
			OriginChatID: request.OriginChatID, Origin: sourcediscovery.OriginManual, Query: request.Query,
		})
		return createErr
	})
	switch {
	case errors.Is(err, sourcediscovery.ErrInvalid):
		writeError(w, r, http.StatusBadRequest, "discovery_invalid_query", "error.discovery_invalid_query")
	case errors.Is(err, sourcediscovery.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "discovery_forbidden", "error.discovery_forbidden")
	case errors.Is(err, sourcediscovery.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"session": session})
	}
}

func (s *Server) latestSourceDiscoverySession(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	var session sourcediscovery.Session
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).LatestSession(r.Context(), notebookID)
		return readErr
	})
	if errors.Is(err, sourcediscovery.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) sourceDiscoverySessionByID(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "error.session_expired")
		return
	}
	remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/source-discovery-sessions/"), "/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "events" {
		s.streamSourceDiscovery(w, r, user.ID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "selection" {
		sourceDiscoverySelection(w, r, s, user.ID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "imports" {
		sourceDiscoveryImports(w, r, s, user.ID, parts[0], false)
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "retry" {
		sourceDiscoveryRetry(w, r, s, user.ID, parts[0])
		return
	}
	if r.Method != http.MethodGet || len(parts) != 1 || parts[0] == "" {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	sessionID := parts[0]
	var session sourcediscovery.Session
	err := s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).GetSession(r.Context(), sessionID)
		return readErr
	})
	if errors.Is(err, sourcediscovery.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

type discoveryImportOutcome struct {
	CandidateID string  `json:"candidate_id"`
	Status      string  `json:"status"`
	SourceID    *string `json:"source_id,omitempty"`
	ErrorCode   *string `json:"error_code,omitempty"`
}

func sourceDiscoveryImports(w http.ResponseWriter, r *http.Request, s *Server, userID, sessionID string, retryOnly bool) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		writeError(w, r, http.StatusBadRequest, "idempotency_required", "error.idempotency_required")
		return
	}
	if s.cfg.SourceFetcher == nil || s.cfg.SourceSnapshots == nil {
		writeError(w, r, http.StatusServiceUnavailable, "source_fetch_unavailable", "error.source_fetch_unavailable")
		return
	}
	var session sourcediscovery.Session
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).GetSession(r.Context(), sessionID)
		return readErr
	})
	if errors.Is(err, sourcediscovery.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	outcomes := make([]discoveryImportOutcome, 0, len(session.Candidates))
	for _, candidate := range session.Candidates {
		if !candidate.Selected {
			continue
		}
		if retryOnly && candidate.Status != sourcediscovery.CandidateImportFailed {
			continue
		}
		if candidate.Status == sourcediscovery.CandidateImported && candidate.SourceID != nil {
			outcomes = append(outcomes, discoveryImportOutcome{CandidateID: candidate.ID, Status: "imported", SourceID: candidate.SourceID})
			continue
		}
		var admission sourcediscovery.CandidateImport
		err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
			var beginErr error
			admission, beginErr = sourcediscovery.NewStore(tx).BeginCandidateImport(r.Context(), sessionID, candidate.ID)
			return beginErr
		})
		if err != nil {
			code := "discovery_invalid_state"
			outcomes = append(outcomes, discoveryImportOutcome{CandidateID: candidate.ID, Status: "import_failed", ErrorCode: &code})
			continue
		}
		digest := sha256.Sum256([]byte(key + "\x00" + candidate.ID))
		candidateKey := "discovery:" + hex.EncodeToString(digest[:])
		created, _, importErr := s.importURLSource(r.Context(), userID, admission.NotebookID, candidateKey, admission.URL, admission.Title)
		if importErr != nil {
			code := discoveryImportErrorCode(importErr)
			_ = s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
				return sourcediscovery.NewStore(tx).DropCandidateImport(r.Context(), sessionID, candidate.ID)
			})
			outcomes = append(outcomes, discoveryImportOutcome{CandidateID: candidate.ID, Status: "import_failed", ErrorCode: &code})
			continue
		}
		completeErr := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
			return sourcediscovery.NewStore(tx).CompleteCandidateImport(r.Context(), sessionID, candidate.ID, created.ID)
		})
		if completeErr != nil {
			code := "discovery_import_failed"
			outcomes = append(outcomes, discoveryImportOutcome{CandidateID: candidate.ID, Status: "import_failed", ErrorCode: &code})
			continue
		}
		sourceID := created.ID
		outcomes = append(outcomes, discoveryImportOutcome{CandidateID: candidate.ID, Status: "imported", SourceID: &sourceID})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"outcomes": outcomes})
}

func sourceDiscoveryRetry(w http.ResponseWriter, r *http.Request, s *Server, userID, sessionID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		writeError(w, r, http.StatusBadRequest, "idempotency_required", "error.idempotency_required")
		return
	}
	requestDigest := sha256.Sum256([]byte(sessionID))
	requestHash := hex.EncodeToString(requestDigest[:])
	var replayedSession *sourcediscovery.Session
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var storedHash string
		var storedJSON []byte
		err := tx.QueryRow(r.Context(), `
			select request_hash,response_json from platform_idempotency_keys
			where principal_id=$1 and action='retry_source_discovery' and key=$2
		`, userID, key).Scan(&storedHash, &storedJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if storedHash != requestHash {
			return source.ErrIdempotencyMismatch
		}
		var stored struct {
			Session sourcediscovery.Session `json:"session"`
		}
		if err := json.Unmarshal(storedJSON, &stored); err != nil {
			return err
		}
		replayedSession = &stored.Session
		return nil
	})
	if errors.Is(err, source.ErrIdempotencyMismatch) {
		writeError(w, r, http.StatusConflict, "idempotency_mismatch", "error.idempotency_mismatch")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	if replayedSession != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"session": *replayedSession})
		return
	}
	var session sourcediscovery.Session
	err = s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).GetSession(r.Context(), sessionID)
		return readErr
	})
	if errors.Is(err, sourcediscovery.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	if session.Status == sourcediscovery.StatusReady {
		sourceDiscoveryImports(w, r, s, userID, sessionID, true)
		return
	}
	if session.Status != sourcediscovery.StatusFailed {
		writeError(w, r, http.StatusConflict, "discovery_invalid_state", "error.discovery_invalid_state")
		return
	}
	jobID, err := newOpaqueID("dscjob")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	err = s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		if _, lockErr := tx.Exec(r.Context(), `select pg_advisory_xact_lock(hashtextextended($1,0))`, "retry-source-discovery:"+userID+":"+key); lockErr != nil {
			return lockErr
		}
		var storedHash string
		var storedJSON []byte
		storedErr := tx.QueryRow(r.Context(), `
			select request_hash,response_json from platform_idempotency_keys
			where principal_id=$1 and action='retry_source_discovery' and key=$2
		`, userID, key).Scan(&storedHash, &storedJSON)
		if storedErr == nil {
			if storedHash != requestHash {
				return source.ErrIdempotencyMismatch
			}
			var stored struct {
				Session sourcediscovery.Session `json:"session"`
			}
			if err := json.Unmarshal(storedJSON, &stored); err != nil {
				return err
			}
			session = stored.Session
			return nil
		}
		if !errors.Is(storedErr, pgx.ErrNoRows) {
			return storedErr
		}
		var retryErr error
		session, retryErr = sourcediscovery.NewStore(tx).RetryFailedSession(r.Context(), sessionID, jobID)
		if retryErr != nil {
			return retryErr
		}
		response, marshalErr := json.Marshal(map[string]any{"session": session})
		if marshalErr != nil {
			return marshalErr
		}
		_, insertErr := tx.Exec(r.Context(), `
			insert into platform_idempotency_keys(principal_id,action,key,request_hash,status_code,response_json)
			values($1,'retry_source_discovery',$2,$3,$4,$5::jsonb)
		`, userID, key, requestHash, http.StatusAccepted, string(response))
		return insertErr
	})
	switch {
	case errors.Is(err, source.ErrIdempotencyMismatch):
		writeError(w, r, http.StatusConflict, "idempotency_mismatch", "error.idempotency_mismatch")
	case errors.Is(err, sourcediscovery.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
	case errors.Is(err, sourcediscovery.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "discovery_forbidden", "error.discovery_forbidden")
	case errors.Is(err, sourcediscovery.ErrState):
		writeError(w, r, http.StatusConflict, "discovery_invalid_state", "error.discovery_invalid_state")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"session": session})
	}
}

func discoveryImportErrorCode(err error) string {
	switch {
	case errors.Is(err, fetcher.ErrUnsafeDestination):
		return "unsafe_destination"
	case errors.Is(err, fetcher.ErrResponseTooLarge), errors.Is(err, source.ErrQuotaReached):
		return "limits_exceeded"
	case errors.Is(err, fetcher.ErrUnsupportedType):
		return "unsupported_source"
	case errors.Is(err, errSourceObjectWrite):
		return "source_store_unavailable"
	default:
		return "discovery_import_failed"
	}
}

func sourceDiscoverySelection(w http.ResponseWriter, r *http.Request, s *Server, userID, sessionID string) {
	if r.Method != http.MethodPatch {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}
	var request struct {
		CandidateIDs []string `json:"candidate_ids"`
	}
	if !readJSON(w, r, &request) {
		return
	}
	var session sourcediscovery.Session
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var updateErr error
		session, updateErr = sourcediscovery.NewStore(tx).ReplaceSelection(r.Context(), sessionID, request.CandidateIDs)
		return updateErr
	})
	switch {
	case errors.Is(err, sourcediscovery.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
	case errors.Is(err, sourcediscovery.ErrCandidate), errors.Is(err, sourcediscovery.ErrInvalid):
		writeError(w, r, http.StatusBadRequest, "discovery_candidate_invalid", "error.discovery_candidate_invalid")
	case errors.Is(err, sourcediscovery.ErrState):
		writeError(w, r, http.StatusConflict, "discovery_invalid_state", "error.discovery_invalid_state")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"session": session})
	}
}
