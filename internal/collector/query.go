package collector

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
)

var (
	ErrTraceNotFound     = errors.New("Collector Trace not found")
	ErrProjectionPending = errors.New("Collector Trace projection is pending")
	ErrReplayNotFound    = errors.New("Collector Replay not found")
	ErrReplayExpired     = errors.New("Collector Replay expired")
	ErrReplayUnavailable = errors.New("Collector Replay unavailable")
)

type TraceListQuery struct {
	StartedAfterUnixNano  *int64
	StartedBeforeUnixNano *int64
	IdentityExact         string
	IdentityPrefix        string
	AgentName             string
	ModelName             string
	Status                string
	Active                *bool
	Cursor                string
	PageSize              int
}

type TraceListItem struct {
	Summary          TraceSummary `json:"summary"`
	CommittedThrough int          `json:"committed_sequence"`
	ProjectedThrough int          `json:"projected_sequence"`
	ProjectionLagged bool         `json:"projection_lagged"`
}

type TraceListResult struct {
	Items      []TraceListItem `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type OpaqueReplay struct {
	AttachmentID string               `json:"attachment_id"`
	TraceID      agentobs.TraceID     `json:"trace_id"`
	SpanID       agentobs.SpanID      `json:"span_id"`
	Class        replay.Class         `json:"class"`
	Sealed       replay.SealedPayload `json:"sealed"`
}

type traceCursor struct {
	StartedAtUnixNano int64            `json:"started_at_unix_nano"`
	TraceID           agentobs.TraceID `json:"trace_id"`
}

func encodeTraceCursor(cursor traceCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeTraceCursor(value string) (traceCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(payload) > 256 {
		return traceCursor{}, errors.New("Collector Trace cursor is invalid")
	}
	var cursor traceCursor
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.StartedAtUnixNano == 0 || cursor.TraceID == "" || len(cursor.TraceID) > 128 {
		return traceCursor{}, errors.New("Collector Trace cursor is invalid")
	}
	return cursor, nil
}
