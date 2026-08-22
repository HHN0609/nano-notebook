package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/evidence"
	"github.com/huangxinxinyu/nano-notebook/internal/fetcher"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourceadmission"
	"github.com/jackc/pgx/v5"
	xhtml "golang.org/x/net/html"
)

type memberSource struct {
	ID            string                   `json:"id"`
	NotebookID    string                   `json:"notebook_id"`
	Title         string                   `json:"title"`
	Format        source.Format            `json:"format"`
	ByteSize      int64                    `json:"byte_size"`
	State         string                   `json:"state"`
	FailureReason string                   `json:"failure_reason,omitempty"`
	OpenAction    sourceOpenAction         `json:"open_action"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
	Admission     *source.AdmissionSummary `json:"admission,omitempty"`
}

type sourceOpenAction struct {
	Kind      string `json:"kind"`
	Href      string `json:"href,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

func sourceForMember(item source.Source) memberSource {
	state := "processing"
	if item.State == source.StateReady {
		state = "ready"
	} else if item.State == source.StateFailed {
		state = "failed"
	}
	result := memberSource{
		ID: item.ID, NotebookID: item.NotebookID, Title: item.Title, Format: item.Format,
		ByteSize: item.ByteSize, State: state, OpenAction: sourceOpenActionFor(item, state), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		Admission: item.Admission,
	}
	if state == "failed" {
		result.FailureReason = source.SafeFailureReason(item.FailureCode)
	}
	return result
}

func sourceOpenActionFor(item source.Source, state string) sourceOpenAction {
	if state != "ready" {
		return sourceOpenAction{Kind: "none"}
	}
	if item.InputKind == "url" {
		href, err := canonicalSourceURL(item.FinalURL)
		if err == nil {
			return sourceOpenAction{Kind: "external", Href: href}
		}
		return sourceOpenAction{Kind: "none"}
	}
	if item.InputKind == "file" {
		if mediaType := inlineOriginalMediaType(item.Format, item.MediaType); mediaType != "" {
			return sourceOpenAction{
				Kind: "inline_original", Href: "/api/v1/sources/" + item.ID + "/original-asset", MediaType: mediaType,
			}
		}
	}
	return sourceOpenAction{Kind: "none"}
}

func inlineOriginalMediaType(format source.Format, mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	allowed := false
	switch format {
	case source.FormatTXT:
		allowed = mediaType == "text/plain"
	case source.FormatMarkdown:
		allowed = mediaType == "text/markdown"
	case source.FormatPDF:
		allowed = mediaType == "application/pdf"
	case source.FormatMP3:
		allowed = mediaType == "audio/mpeg"
	case source.FormatWAV:
		allowed = mediaType == "audio/wav" || mediaType == "audio/x-wav"
	case source.FormatM4A:
		allowed = mediaType == "audio/mp4" || mediaType == "audio/x-m4a"
	case source.FormatPNG:
		allowed = mediaType == "image/png"
	case source.FormatJPEG:
		allowed = mediaType == "image/jpeg"
	case source.FormatWebP:
		allowed = mediaType == "image/webp"
	}
	if allowed {
		return mediaType
	}
	return ""
}

func (s *Server) notebookSources(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	var items []source.Source
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var listErr error
		items, listErr = source.NewStore(tx).ListForNotebook(r.Context(), notebookID)
		return listErr
	})
	if errors.Is(err, source.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	result := make([]memberSource, 0, len(items))
	for _, item := range items {
		result = append(result, sourceForMember(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": result})
}

func (s *Server) createURLSource(w http.ResponseWriter, r *http.Request, userID, notebookID string) {
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
	var requestBody struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &requestBody) {
		return
	}
	requestURL, err := canonicalSourceURL(requestBody.URL)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "error.source_url_invalid")
		return
	}
	created, reused, err := s.importURLSource(r.Context(), userID, notebookID, key, requestURL, "")
	if errors.Is(err, source.ErrIdempotencyMismatch) {
		writeError(w, r, http.StatusConflict, "idempotency_mismatch", "error.idempotency_mismatch")
		return
	}
	if errors.Is(err, source.ErrAdmissionInProgress) {
		writeError(w, r, http.StatusConflict, "source_admission_in_progress", "error.source_admission_in_progress")
		return
	}
	if errors.Is(err, source.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.notebook_not_found")
		return
	}
	switch {
	case errors.Is(err, fetcher.ErrUnsafeDestination):
		writeError(w, r, http.StatusUnprocessableEntity, "unsafe_destination", "error.source_url_unsafe")
		return
	case errors.Is(err, fetcher.ErrResponseTooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, "source_too_large", "error.source_too_large")
		return
	case errors.Is(err, fetcher.ErrUnsupportedType):
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_source", "error.source_unsupported")
		return
	case errors.Is(err, errSourceInvalidSnapshot):
		writeError(w, r, http.StatusBadGateway, "source_fetch_failed", "error.source_fetch_failed")
		return
	case errors.Is(err, errSourceObjectWrite):
		writeError(w, r, http.StatusServiceUnavailable, "source_store_unavailable", "error.source_store_unavailable")
		return
	case errors.Is(err, source.ErrQuotaReached):
		writeError(w, r, http.StatusConflict, "quota_reached", "error.source_quota")
		return
	case err != nil:
		writeError(w, r, http.StatusBadGateway, "source_fetch_failed", "error.source_fetch_failed")
		return
	}
	status := http.StatusCreated
	if reused {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"source": sourceForMember(created)})
}

var (
	errSourceInvalidSnapshot = errors.New("invalid Source snapshot")
	errSourceObjectWrite     = errors.New("Source snapshot object write failed")
)

func (s *Server) importURLSource(ctx context.Context, userID, notebookID, key, requestURL, preferredTitle string) (source.Source, bool, error) {
	admissionID, err := newOpaqueID("urladm")
	if err != nil {
		return source.Source{}, false, err
	}
	sourceID, err := newOpaqueID("src")
	if err != nil {
		return source.Source{}, false, err
	}
	requestDigest := sourceURLRequestHash(notebookID, requestURL)
	var admission source.URLAdmission
	var reused bool
	err = s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		var beginErr error
		admission, reused, beginErr = source.NewStore(tx).BeginURLAdmission(ctx, source.BeginURLAdmissionCommand{
			ID: admissionID, SourceID: sourceID, NotebookID: notebookID, IdempotencyKey: key,
			RequestHash: requestDigest, RequestURL: requestURL,
		})
		return beginErr
	})
	if err != nil {
		return source.Source{}, false, err
	}
	if reused {
		var existing source.Source
		err = s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
			var lookupErr error
			existing, lookupErr = source.NewStore(tx).SourceByID(ctx, admission.SourceID)
			return lookupErr
		})
		return existing, true, err
	}

	snapshot, err := s.cfg.SourceFetcher.Fetch(ctx, requestURL)
	if err != nil {
		s.failURLAdmission(ctx, userID, admission.ID, "fetch_failed")
		return source.Source{}, false, err
	}
	digest := sha256.Sum256(snapshot.Payload)
	if len(snapshot.Payload) == 0 || int64(len(snapshot.Payload)) > 100*1024*1024 ||
		!strings.EqualFold(snapshot.ContentSHA256, hex.EncodeToString(digest[:])) {
		s.failURLAdmission(ctx, userID, admission.ID, "invalid_snapshot")
		return source.Source{}, false, errSourceInvalidSnapshot
	}
	format, ok := source.FormatForMediaType(snapshot.MediaType)
	if !ok {
		s.failURLAdmission(ctx, userID, admission.ID, "unsupported_type")
		return source.Source{}, false, fetcher.ErrUnsupportedType
	}
	objectKey := "sources/" + admission.SourceID + "/original/" + strings.ToLower(snapshot.ContentSHA256)
	if err := s.cfg.SourceSnapshots.Put(ctx, objectKey, snapshot.Payload); err != nil {
		s.failURLAdmission(ctx, userID, admission.ID, "object_write_failed")
		return source.Source{}, false, errSourceObjectWrite
	}
	jobID, err := newOpaqueID("srcjob")
	if err != nil {
		return source.Source{}, false, err
	}
	title := boundedSourceTitle(preferredTitle)
	if title == "" && format == source.FormatHTML {
		title = boundedSourceTitle(htmlSnapshotTitle(snapshot.Payload))
	}
	if title == "" {
		title = boundedSourceTitle(snapshot.FinalURL)
	}
	if title == "" {
		title = "Web source"
	}
	var created source.Source
	var finalizedReused bool
	err = s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		var finalizeErr error
		created, finalizedReused, finalizeErr = source.NewStore(tx).FinalizeURLAdmission(ctx, source.FinalizeURLAdmissionCommand{
			AdmissionID: admission.ID, ProcessingJobID: jobID, Title: title, Format: format,
			MediaType: snapshot.MediaType, ByteSize: int64(len(snapshot.Payload)),
			ContentSHA256: strings.ToLower(snapshot.ContentSHA256), OriginalObjectKey: objectKey,
			FinalURL: snapshot.FinalURL, CompletedAt: time.Now().UTC().Truncate(time.Microsecond),
		})
		return finalizeErr
	})
	if err == nil && finalizedReused {
		_ = s.cfg.SourceSnapshots.Delete(ctx, objectKey)
	}
	return created, finalizedReused, err
}

func boundedSourceTitle(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 255 {
		runes = runes[:255]
	}
	return string(runes)
}

func htmlSnapshotTitle(payload []byte) string {
	if len(payload) == 0 || !utf8.Valid(payload) {
		return ""
	}
	document, err := xhtml.Parse(strings.NewReader(string(payload)))
	if err != nil {
		return ""
	}
	var find func(*xhtml.Node) string
	find = func(node *xhtml.Node) string {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "title") {
			var text strings.Builder
			var collect func(*xhtml.Node)
			collect = func(current *xhtml.Node) {
				if current.Type == xhtml.TextNode {
					text.WriteString(current.Data)
				}
				for child := current.FirstChild; child != nil; child = child.NextSibling {
					collect(child)
				}
			}
			collect(node)
			return text.String()
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if title := find(child); title != "" {
				return title
			}
		}
		return ""
	}
	return find(document)
}

func canonicalSourceURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return "", source.ErrInvalidInput
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func sourceURLRequestHash(notebookID, requestURL string) string {
	canonical, _ := json.Marshal(struct {
		NotebookID string `json:"notebook_id"`
		URL        string `json:"url"`
	}{NotebookID: notebookID, URL: requestURL})
	return requestHash(canonical)
}

func (s *Server) failURLAdmission(ctx context.Context, userID, admissionID, errorCode string) {
	_ = s.db.WithRequestPrincipal(ctx, userID, func(tx pgx.Tx) error {
		return source.NewStore(tx).FailURLAdmission(ctx, admissionID, errorCode, time.Now().UTC().Truncate(time.Microsecond))
	})
}

func (s *Server) sourceByID(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "error.session_expired")
		return
	}
	remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/sources/"), "/")
	parts := strings.Split(remainder, "/")
	if parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "retry" && parts[1] != "viewer-asset" && parts[1] != "original-asset" && parts[1] != "admission" && parts[1] != "admission-review") {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "viewer-asset" {
		s.writeSourceViewerAsset(w, r, user.ID, parts[0])
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "original-asset" {
		s.writeSourceOriginalAsset(w, r, user.ID, parts[0])
		return
	}
	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "admission" {
		var admission sourceadmission.StoredAssessment
		err := s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			var readErr error
			admission, readErr = sourceadmission.NewMemberStore(tx).Detail(r.Context(), parts[0])
			return readErr
		})
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"admission": admission})
		} else if errors.Is(err, sourceadmission.ErrAdmissionNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		} else {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		}
		return
	}
	if r.Method == http.MethodGet && len(parts) == 1 {
		var view evidence.SourceView
		err := s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			var readErr error
			view, readErr = evidence.NewReader(tx).SourceView(r.Context(), parts[0])
			return readErr
		})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{"source": view})
		case errors.Is(err, evidence.ErrSourceNotFound):
			writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		case errors.Is(err, evidence.ErrSourceNotReady):
			writeError(w, r, http.StatusConflict, "source_not_ready", "error.source_not_ready")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		}
		return
	}
	if !validCSRF(r) {
		writeError(w, r, http.StatusForbidden, "csrf_required", "error.csrf_required")
		return
	}

	var err error
	switch {
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "admission-review":
		var req struct {
			ReportID string                         `json:"report_id"`
			Decision sourceadmission.ReviewDecision `json:"decision"`
			Note     string                         `json:"note"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		var review sourceadmission.Review
		err = s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			var reviewErr error
			review, _, reviewErr = sourceadmission.NewMemberStore(tx).Review(r.Context(), sourceadmission.ReviewCommand{
				SourceID: parts[0], ReportID: req.ReportID, Decision: req.Decision, Note: req.Note,
			})
			return reviewErr
		})
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"review": review})
			return
		}
	case r.Method == http.MethodPatch && len(parts) == 1:
		var req struct {
			Title string `json:"title"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		var renamed source.Source
		err = s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			var renameErr error
			renamed, renameErr = source.NewStore(tx).Rename(r.Context(), parts[0], req.Title)
			return renameErr
		})
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"source": sourceForMember(renamed)})
			return
		}
	case r.Method == http.MethodPost && len(parts) == 2:
		err = s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			return source.NewStore(tx).RetryFailed(r.Context(), parts[0])
		})
		if err == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"source_id": parts[0], "state": "processing"})
			return
		}
	case r.Method == http.MethodDelete && len(parts) == 1:
		purgeID, idErr := newOpaqueID("srcpurge")
		if idErr != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
			return
		}
		err = s.db.WithRequestPrincipal(r.Context(), user.ID, func(tx pgx.Tx) error {
			_, removeErr := source.NewStore(tx).Remove(r.Context(), parts[0], purgeID)
			return removeErr
		})
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "error.method_not_allowed")
		return
	}

	if errors.Is(err, source.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	}
	if errors.Is(err, sourceadmission.ErrAdmissionNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	}
	if errors.Is(err, sourceadmission.ErrReviewConflict) {
		writeError(w, r, http.StatusConflict, "source_admission_review_conflict", "error.source_state_conflict")
		return
	}
	if errors.Is(err, sourceadmission.ErrInvalidReview) {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "error.source_invalid")
		return
	}
	if errors.Is(err, source.ErrStateConflict) {
		writeError(w, r, http.StatusConflict, "source_state_conflict", "error.source_state_conflict")
		return
	}
	if errors.Is(err, source.ErrInvalidInput) {
		writeError(w, r, http.StatusBadRequest, "validation_failed", "error.source_invalid")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
	}
}

func (s *Server) writeSourceOriginalAsset(w http.ResponseWriter, r *http.Request, userID, sourceID string) {
	if s.cfg.SourceSnapshots == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	}
	var item source.Source
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var readErr error
		item, readErr = source.NewStore(tx).SourceByID(r.Context(), sourceID)
		return readErr
	})
	mediaType := inlineOriginalMediaType(item.Format, item.MediaType)
	if err != nil || item.State != source.StateReady || item.InputKind != "file" || mediaType == "" ||
		item.ByteSize < 1 || item.ByteSize > 100*1024*1024 || len(item.ContentSHA256) != 64 || strings.TrimSpace(item.OriginalObjectKey) == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	}
	payload, err := s.cfg.SourceSnapshots.Get(r.Context(), item.OriginalObjectKey, item.ByteSize)
	digest := sha256.Sum256(payload)
	if err != nil || int64(len(payload)) != item.ByteSize || !strings.EqualFold(hex.EncodeToString(digest[:]), item.ContentSHA256) {
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", `inline; filename="`+safeInlineFilename(item.Title)+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func safeInlineFilename(title string) string {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\\", "/"))
	if index := strings.LastIndex(title, "/"); index >= 0 {
		title = title[index+1:]
	}
	title = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f || character == '"' || character == '\\' {
			return -1
		}
		return character
	}, title)
	if title == "" || title == "." || title == ".." {
		return "source"
	}
	return title
}

func (s *Server) writeSourceViewerAsset(w http.ResponseWriter, r *http.Request, userID, sourceID string) {
	if s.cfg.SourceSnapshots == nil {
		writeError(w, r, http.StatusServiceUnavailable, "source_unavailable", "error.source_unavailable")
		return
	}
	var asset evidence.ViewerAsset
	ordinal := 1
	if value := r.URL.Query().Get("ordinal"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 500 {
			writeError(w, r, http.StatusBadRequest, "validation_failed", "error.source_invalid")
			return
		}
		ordinal = parsed
	}
	err := s.db.WithRequestPrincipal(r.Context(), userID, func(tx pgx.Tx) error {
		var readErr error
		asset, readErr = evidence.NewReader(tx).ViewerAsset(r.Context(), sourceID, ordinal)
		return readErr
	})
	switch {
	case errors.Is(err, evidence.ErrSourceNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "error.source_not_found")
		return
	case errors.Is(err, evidence.ErrSourceNotReady), errors.Is(err, evidence.ErrViewerUnsupported):
		writeError(w, r, http.StatusConflict, "viewer_unavailable", "error.source_not_ready")
		return
	case errors.Is(err, evidence.ErrEvidenceUnavailable):
		writeError(w, r, http.StatusGone, "source_unavailable", "error.source_unavailable")
		return
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "internal", "error.internal")
		return
	}
	payload, err := s.cfg.SourceSnapshots.Get(r.Context(), asset.ObjectKey, asset.ByteSize)
	digest := sha256.Sum256(payload)
	if err != nil || int64(len(payload)) != asset.ByteSize || hex.EncodeToString(digest[:]) != asset.ContentSHA256 {
		writeError(w, r, http.StatusGone, "source_unavailable", "error.source_unavailable")
		return
	}
	mediaType := map[source.Format]string{
		source.FormatPNG: "image/png", source.FormatJPEG: "image/jpeg", source.FormatWebP: "image/webp",
		source.FormatPDF: "image/png", source.FormatPPTX: "image/png",
	}[asset.Format]
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", `inline; filename="`+asset.Filename+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
