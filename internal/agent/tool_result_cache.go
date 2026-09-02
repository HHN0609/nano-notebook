package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ToolResultInline       = "inline"
	ToolResultExternalized = "externalized"
	ToolResultNotCached    = "not_cached"
	ToolResultReadTool     = "read_tool_result"
)

var ErrToolResultExpired = errors.New("tool_result_expired")

var (
	ErrToolResultUnauthorized    = errors.New("tool_result_unauthorized")
	ErrToolResultCorrupt         = errors.New("tool_result_corrupt")
	ErrToolResultInvalidOffset   = errors.New("tool_result_invalid_offset")
	ErrToolResultInvalidPageSize = errors.New("tool_result_invalid_page_size")
)

type ToolResultCachePolicy struct {
	TTL               time.Duration
	MaximumValueBytes int
}

type ToolResultScope struct {
	UserID   string
	ChatID   string
	RunID    string
	ActionID string
	ToolName string
}

type ToolResultEnvelope struct {
	SchemaVersion int       `json:"schema_version"`
	ResultRef     string    `json:"result_ref"`
	UserID        string    `json:"user_id"`
	ChatID        string    `json:"chat_id"`
	RunID         string    `json:"run_id"`
	ActionID      string    `json:"action_id"`
	ToolName      string    `json:"tool_name"`
	MediaType     string    `json:"media_type"`
	Encoding      string    `json:"encoding"`
	ResultBytes   int       `json:"result_bytes"`
	SHA256        string    `json:"sha256"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Body          []byte    `json:"body"`
}

type ToolResultStore interface {
	Put(context.Context, ToolResultEnvelope, time.Duration) error
	Get(context.Context, string) (ToolResultEnvelope, error)
}

type ToolResultRangeStore interface {
	ReadRange(context.Context, string, int, int) (ToolResultEnvelope, []byte, error)
}

type ToolResultProjection struct {
	ActionID     string `json:"action_id,omitempty"`
	ContentState string `json:"content_state"`
	Preview      string `json:"preview"`
	ResultRef    string `json:"result_ref,omitempty"`
	ResultBytes  int    `json:"result_bytes"`
	SHA256       string `json:"sha256"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	ReadTool     string `json:"read_tool,omitempty"`
	NextOffset   int    `json:"next_offset,omitempty"`
	Complete     bool   `json:"complete"`
	Notice       string `json:"notice,omitempty"`
}

type ToolResultExternalization struct {
	State string
	Err   error
}

type ToolResultExternalizer struct {
	Store        ToolResultStore
	Policy       ToolResultCachePolicy
	Now          func() time.Time
	NewResultRef func() (string, error)
}

type ToolResultPage struct {
	ResultRef   string `json:"result_ref"`
	Offset      int    `json:"offset"`
	Content     string `json:"content"`
	NextOffset  int    `json:"next_offset"`
	Complete    bool   `json:"complete"`
	ResultBytes int    `json:"result_bytes"`
	ExpiresAt   string `json:"expires_at"`
	Notice      string `json:"notice,omitempty"`
}

type ToolResultReader struct {
	Store              ToolResultStore
	MaximumPageBytes   int
	MaximumOutputBytes int
	Now                func() time.Time
}

func (r ToolResultReader) Read(ctx context.Context, scope ToolResultScope, resultRef string, offset, maxBytes int) (ToolResultPage, error) {
	if r.Store == nil || !strings.HasPrefix(resultRef, "tr_") {
		return ToolResultPage{}, ErrToolResultExpired
	}
	pageLimit := maxBytes
	if pageLimit <= 0 || pageLimit > r.MaximumPageBytes {
		pageLimit = r.MaximumPageBytes
	}
	if pageLimit < utf8.UTFMax || r.MaximumPageBytes < utf8.UTFMax {
		return ToolResultPage{}, ErrToolResultInvalidPageSize
	}
	var envelope ToolResultEnvelope
	var rangedBody []byte
	rangeStore, useRange := r.Store.(ToolResultRangeStore)
	var err error
	if useRange {
		envelope, rangedBody, err = rangeStore.ReadRange(ctx, resultRef, offset, pageLimit+utf8.UTFMax-1)
	} else {
		envelope, err = r.Store.Get(ctx, resultRef)
	}
	if err != nil {
		if errors.Is(err, ErrToolResultExpired) {
			return ToolResultPage{}, ErrToolResultExpired
		}
		return ToolResultPage{}, err
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	if !now.Before(envelope.ExpiresAt) {
		return ToolResultPage{}, ErrToolResultExpired
	}
	if envelope.SchemaVersion != 1 || envelope.ResultRef != resultRef || envelope.UserID != scope.UserID ||
		envelope.ChatID != scope.ChatID || envelope.RunID != scope.RunID {
		return ToolResultPage{}, ErrToolResultUnauthorized
	}
	if offset < 0 || offset > envelope.ResultBytes {
		return ToolResultPage{}, ErrToolResultInvalidOffset
	}
	var content []byte
	if useRange {
		minimum := pageLimit
		if remaining := envelope.ResultBytes - offset; remaining < minimum {
			minimum = remaining
		}
		if envelope.ResultBytes < 0 || len(envelope.SHA256) != sha256.Size*2 || len(rangedBody) < minimum {
			return ToolResultPage{}, ErrToolResultCorrupt
		}
		end := pageLimit
		if end > len(rangedBody) {
			end = len(rangedBody)
		}
		for end > 0 && !utf8.Valid(rangedBody[:end]) {
			end--
		}
		if end == 0 && offset < envelope.ResultBytes {
			return ToolResultPage{}, ErrToolResultInvalidOffset
		}
		content = rangedBody[:end]
	} else {
		if envelope.ResultBytes != len(envelope.Body) || hashPayload(envelope.Body) != envelope.SHA256 {
			return ToolResultPage{}, ErrToolResultCorrupt
		}
		if !utf8.Valid(envelope.Body[offset:]) {
			return ToolResultPage{}, ErrToolResultInvalidOffset
		}
		end := offset + pageLimit
		if end > len(envelope.Body) {
			end = len(envelope.Body)
		}
		for end > offset && !utf8.Valid(envelope.Body[offset:end]) {
			end--
		}
		if end == offset && offset < len(envelope.Body) {
			return ToolResultPage{}, ErrToolResultInvalidPageSize
		}
		content = envelope.Body[offset:end]
	}
	nextOffset := offset + len(content)
	page := ToolResultPage{
		ResultRef: resultRef, Offset: offset, Content: string(content),
		NextOffset: nextOffset, Complete: nextOffset == envelope.ResultBytes, ResultBytes: envelope.ResultBytes,
		ExpiresAt: envelope.ExpiresAt.UTC().Format(time.RFC3339),
	}
	page.Notice = toolResultPageNotice(page.ResultRef, page.Offset, page.NextOffset, page.ResultBytes, page.Complete)
	return page, nil
}

func (e *ToolResultExternalizer) Externalize(ctx context.Context, scope ToolResultScope, result ActionResult, inlineByteLimit int) (ActionResult, ToolResultExternalization) {
	if result.Status != ActionSucceeded || inlineByteLimit < 1 {
		return result, ToolResultExternalization{State: ToolResultInline}
	}
	visibleBytes, err := modelVisibleToolResultBytes(scope.ActionID, result.Output)
	if err != nil {
		return result, ToolResultExternalization{State: ToolResultInline, Err: err}
	}
	if visibleBytes <= inlineByteLimit {
		return result, ToolResultExternalization{State: ToolResultInline}
	}
	now := time.Now().UTC()
	if e != nil && e.Now != nil {
		now = e.Now().UTC()
	}
	hash := sha256.Sum256(result.Output)
	sha := hex.EncodeToString(hash[:])
	projection := ToolResultProjection{
		ActionID: scope.ActionID, ContentState: ToolResultNotCached,
		ResultBytes: len(result.Output), SHA256: sha,
	}
	fallback := func(err error) (ActionResult, ToolResultExternalization) {
		projection = boundedToolResultPreview(result.Output, projection, inlineByteLimit)
		encoded, marshalErr := json.Marshal(projection)
		if marshalErr != nil {
			return result, ToolResultExternalization{State: ToolResultInline, Err: marshalErr}
		}
		copyResult := result
		copyResult.Output = encoded
		return copyResult, ToolResultExternalization{State: ToolResultNotCached, Err: err}
	}
	if e == nil || e.Store == nil || e.Policy.TTL <= 0 || e.Policy.MaximumValueBytes < 1 || len(result.Output) > e.Policy.MaximumValueBytes {
		return fallback(errors.New("Tool Result cache is unavailable or result exceeds maximum value size"))
	}
	newReference := e.NewResultRef
	if newReference == nil {
		newReference = newOpaqueToolResultReference
	}
	resultRef, err := newReference()
	if err != nil || !strings.HasPrefix(resultRef, "tr_") {
		if err == nil {
			err = errors.New("invalid Tool Result reference")
		}
		return fallback(err)
	}
	expiresAt := now.Add(e.Policy.TTL)
	envelope := ToolResultEnvelope{
		SchemaVersion: 1, ResultRef: resultRef,
		UserID: scope.UserID, ChatID: scope.ChatID, RunID: scope.RunID,
		ActionID: scope.ActionID, ToolName: scope.ToolName,
		MediaType: "application/json", Encoding: "json",
		ResultBytes: len(result.Output), SHA256: sha,
		CreatedAt: now, ExpiresAt: expiresAt, Body: append([]byte(nil), result.Output...),
	}
	if err := e.Store.Put(ctx, envelope, e.Policy.TTL); err != nil {
		return fallback(err)
	}
	projection.ContentState = ToolResultExternalized
	projection.ResultRef = resultRef
	projection.ExpiresAt = expiresAt.Format(time.RFC3339)
	projection.ReadTool = ToolResultReadTool
	projection = boundedToolResultPreview(result.Output, projection, inlineByteLimit)
	encoded, err := json.Marshal(projection)
	if err != nil {
		return fallback(err)
	}
	copyResult := result
	copyResult.Output = encoded
	return copyResult, ToolResultExternalization{State: ToolResultExternalized}
}

func boundedToolResultPreview(body []byte, projection ToolResultProjection, limit int) ToolResultProjection {
	if limit < 1 {
		return projection
	}
	text := string(body)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		middle := (low + high + 1) / 2
		candidate := projection
		candidate.Preview = string(runes[:middle])
		candidate.NextOffset = len([]byte(candidate.Preview))
		candidate.Complete = candidate.NextOffset == len(body)
		candidate.Notice = toolResultPageNotice(candidate.ResultRef, 0, candidate.NextOffset, len(body), candidate.Complete)
		encoded, _ := json.Marshal(candidate)
		visibleBytes, sizeErr := modelVisibleToolResultBytes(candidate.ActionID, encoded)
		if sizeErr == nil && visibleBytes <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}
	projection.Preview = string(runes[:low])
	projection.NextOffset = len([]byte(projection.Preview))
	projection.Complete = projection.NextOffset == len(body)
	projection.Notice = toolResultPageNotice(projection.ResultRef, 0, projection.NextOffset, len(body), projection.Complete)
	return projection
}

func modelVisibleToolResultBytes(actionID string, output json.RawMessage) (int, error) {
	canonical, err := CanonicalJSONObject(output)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(actionResultCheckpointPayload{
		ActionID: actionID, Status: ActionSucceeded, Output: canonical,
	})
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

func toolResultPageNotice(resultRef string, offset, nextOffset, resultBytes int, complete bool) string {
	if complete || nextOffset <= offset {
		return ""
	}
	if resultRef == "" {
		return fmt.Sprintf("[Showing bytes %d-%d of %d. Cached continuation is unavailable; reissue the original tool call.]", offset, nextOffset-1, resultBytes)
	}
	return fmt.Sprintf("[Showing bytes %d-%d of %d. Use read_tool_result(result_ref=%q, offset=%d) to continue.]", offset, nextOffset-1, resultBytes, resultRef, nextOffset)
}

func newOpaqueToolResultReference() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate Tool Result reference: %w", err)
	}
	return "tr_" + hex.EncodeToString(bytes), nil
}
