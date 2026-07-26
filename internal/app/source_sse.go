package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/chat"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/jackc/pgx/v5"
)

type notebookSourcesProjection struct {
	Sources   []memberSource `json:"sources"`
	SourceIDs []string       `json:"source_ids"`
}

func (s *Server) streamSourceDiscovery(w http.ResponseWriter, r *http.Request, userID, sessionID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	wake, unsubscribe := s.discoveryHub.subscribe(sessionID)
	defer unsubscribe()
	if _, err := s.sourceDiscoveryProjection(r.Context(), userID, sessionID); err != nil {
		if errors.Is(err, sourcediscovery.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "error.discovery_not_found")
		} else {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		}
		return
	}
	streamFullSnapshots(w, r, "discovery", wake, func(ctx context.Context) (any, error) {
		session, err := s.sourceDiscoveryProjection(ctx, userID, sessionID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"session": session}, nil
	})
}

func (s *Server) sourceDiscoveryProjection(ctx context.Context, userID, sessionID string) (sourcediscovery.Session, error) {
	var session sourcediscovery.Session
	err := s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		var readErr error
		session, readErr = sourcediscovery.NewStore(tx).GetSession(ctx, sessionID)
		return readErr
	})
	return session, err
}

func (s *Server) streamNotebookSources(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	chatID := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	wake, unsubscribe := s.sourceHub.subscribe(notebookID)
	defer unsubscribe()
	if _, err := s.notebookSourcesProjection(r.Context(), userID, notebookID, chatID); err != nil {
		if errors.Is(err, source.ErrNotFound) || errors.Is(err, chat.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
		} else {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		}
		return
	}
	streamFullSnapshots(w, r, "sources", wake, func(ctx context.Context) (any, error) {
		return s.notebookSourcesProjection(ctx, userID, notebookID, chatID)
	})
}

func (s *Server) notebookSourcesProjection(ctx context.Context, userID, notebookID, chatID string) (notebookSourcesProjection, error) {
	projection := notebookSourcesProjection{Sources: make([]memberSource, 0), SourceIDs: make([]string, 0)}
	err := s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		items, err := source.NewStore(tx).ListForNotebook(ctx, notebookID)
		if err != nil {
			return err
		}
		for _, item := range items {
			projection.Sources = append(projection.Sources, sourceForMember(item))
		}
		if chatID == "" {
			return nil
		}
		chatStore := chat.NewStore(tx)
		item, err := chatStore.GetPrivate(ctx, userID, chatID)
		if err != nil {
			return err
		}
		if item.NotebookID != notebookID {
			return chat.ErrNotFound
		}
		projection.SourceIDs, err = chatStore.SelectedSourceIDs(ctx, userID, chatID)
		return err
	})
	return projection, err
}

func streamFullSnapshots(w http.ResponseWriter, r *http.Request, eventName string, wake <-chan struct{}, load func(context.Context) (any, error)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "error.internal")
		return
	}
	projection, err := load(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeProjectionEvent(w, eventName, projection); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-wake:
			projection, err := load(r.Context())
			if err != nil || writeProjectionEvent(w, eventName, projection) != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeProjectionEvent(w io.Writer, eventName string, projection any) error {
	payload, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, payload)
	return err
}
