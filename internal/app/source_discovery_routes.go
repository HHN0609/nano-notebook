package app

import (
	"errors"
	"net/http"
	"strings"

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
	sessionID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/source-discovery-sessions/"), "/")
	if r.Method != http.MethodGet || sessionID == "" || strings.Contains(sessionID, "/") {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
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
