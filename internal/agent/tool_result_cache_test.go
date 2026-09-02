package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type recordingToolResultStore struct {
	envelopes []ToolResultEnvelope
	putErr    error
	getErr    error
}

type recordingRangeToolResultStore struct {
	envelope   ToolResultEnvelope
	fullGets   int
	rangeReads int
}

func (*recordingRangeToolResultStore) Put(context.Context, ToolResultEnvelope, time.Duration) error {
	return nil
}

func (s *recordingRangeToolResultStore) Get(context.Context, string) (ToolResultEnvelope, error) {
	s.fullGets++
	return ToolResultEnvelope{}, errors.New("full Tool Result GET must not be used for a page read")
}

func (s *recordingRangeToolResultStore) ReadRange(_ context.Context, resultRef string, offset, maximumBytes int) (ToolResultEnvelope, []byte, error) {
	s.rangeReads++
	if resultRef != s.envelope.ResultRef || offset < 0 || offset > len(s.envelope.Body) {
		return ToolResultEnvelope{}, nil, ErrToolResultExpired
	}
	end := offset + maximumBytes
	if end > len(s.envelope.Body) {
		end = len(s.envelope.Body)
	}
	metadata := s.envelope
	metadata.Body = nil
	return metadata, append([]byte(nil), s.envelope.Body[offset:end]...), nil
}

func (s *recordingToolResultStore) Put(_ context.Context, envelope ToolResultEnvelope, _ time.Duration) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.envelopes = append(s.envelopes, envelope)
	return nil
}

func (s *recordingToolResultStore) Get(_ context.Context, resultRef string) (ToolResultEnvelope, error) {
	if s.getErr != nil {
		return ToolResultEnvelope{}, s.getErr
	}
	for _, envelope := range s.envelopes {
		if envelope.ResultRef == resultRef {
			return envelope, nil
		}
	}
	return ToolResultEnvelope{}, ErrToolResultExpired
}

func TestToolResultReaderPreservesStoreFailureClassification(t *testing.T) {
	scope := ToolResultScope{UserID: "user_a", ChatID: "chat_a", RunID: "run_a"}
	tests := []struct {
		name     string
		storeErr error
		want     error
	}{
		{name: "missing", storeErr: ErrToolResultExpired, want: ErrToolResultExpired},
		{name: "corrupt", storeErr: ErrToolResultCorrupt, want: ErrToolResultCorrupt},
		{name: "backend outage", storeErr: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := ToolResultReader{
				Store: &recordingToolResultStore{getErr: test.storeErr}, MaximumPageBytes: 16, Now: testToolResultNow,
			}
			if _, err := reader.Read(context.Background(), scope, "tr_test_reference", 0, 16); !errors.Is(err, test.want) {
				t.Fatalf("Read error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestToolResultReaderUsesRangeStoreWithoutLoadingFullBody(t *testing.T) {
	envelope := testToolResultEnvelope([]byte(`{"markdown":"range-backed evidence that is larger than one page"}`))
	store := &recordingRangeToolResultStore{envelope: envelope}
	reader := ToolResultReader{Store: store, MaximumPageBytes: 16, Now: testToolResultNow}
	page, err := reader.Read(context.Background(), ToolResultScope{
		UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID,
	}, envelope.ResultRef, 0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if store.fullGets != 0 || store.rangeReads != 1 || page.Offset != 0 || page.NextOffset > 16 || page.Complete {
		t.Fatalf("fullGets=%d rangeReads=%d page=%+v", store.fullGets, store.rangeReads, page)
	}
}

func TestToolResultExternalizerLeavesSmallResultInline(t *testing.T) {
	store := &recordingToolResultStore{}
	externalizer := testToolResultExternalizer(store)
	result := ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"title":"short","markdown":"small"}`)}

	projected, outcome := externalizer.Externalize(context.Background(), ToolResultScope{
		UserID: "user_a", ChatID: "chat_a", RunID: "run_a", ActionID: "decision:1/action:0", ToolName: "read_url",
	}, result, 1024)

	if outcome.State != ToolResultInline || string(projected.Output) != string(result.Output) {
		t.Fatalf("small result projection = %s, outcome = %#v", projected.Output, outcome)
	}
	if len(store.envelopes) != 0 {
		t.Fatalf("small result wrote %d cache entries", len(store.envelopes))
	}
}

func TestToolResultExternalizerIncludesActionWrapperInInlineDecision(t *testing.T) {
	store := &recordingToolResultStore{}
	externalizer := testToolResultExternalizer(store)
	result := ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"markdown":"` + strings.Repeat("x", 450) + `"}`)}

	projected, outcome := externalizer.Externalize(context.Background(), ToolResultScope{
		UserID: "user_a", ChatID: "chat_a", RunID: "run_a", ActionID: "decision:1/action:0", ToolName: "read_url",
	}, result, 512)

	if outcome.State != ToolResultExternalized || len(store.envelopes) != 1 {
		t.Fatalf("wrapper overflow stayed inline: outcome=%+v writes=%d output_bytes=%d", outcome, len(store.envelopes), len(result.Output))
	}
	checkpoint, err := NewActionResultCheckpoint(1, 0, "decision:1/action:0", projected)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Payload) > 512 {
		t.Fatalf("bounded checkpoint bytes=%d want <=512", len(checkpoint.Payload))
	}
}

func TestToolResultExternalizerCachesOnlyLossyLargeResult(t *testing.T) {
	store := &recordingToolResultStore{}
	externalizer := testToolResultExternalizer(store)
	body := json.RawMessage(`{"title":"paper","markdown":"` + strings.Repeat("evidence-", 200) + `"}`)

	projected, outcome := externalizer.Externalize(context.Background(), ToolResultScope{
		UserID: "user_a", ChatID: "chat_a", RunID: "run_a", ActionID: "decision:4/action:2", ToolName: "read_url",
	}, ActionResult{Status: ActionSucceeded, Output: body}, 512)

	if outcome.State != ToolResultExternalized || len(store.envelopes) != 1 {
		t.Fatalf("large result outcome = %#v, writes = %d", outcome, len(store.envelopes))
	}
	envelope := store.envelopes[0]
	if envelope.ResultRef != "tr_test_reference" || envelope.UserID != "user_a" || envelope.ChatID != "chat_a" ||
		envelope.RunID != "run_a" || envelope.ActionID != "decision:4/action:2" || envelope.ToolName != "read_url" {
		t.Fatalf("cache envelope scope = %#v", envelope)
	}
	if string(envelope.Body) != string(body) || envelope.ResultBytes != len(body) || envelope.SHA256 == "" {
		t.Fatalf("cache envelope body metadata = %#v", envelope)
	}
	if !envelope.ExpiresAt.Equal(time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("expires_at = %s", envelope.ExpiresAt)
	}
	if len(projected.Output) > 512 || strings.Contains(string(projected.Output), string(body)) {
		t.Fatalf("large body leaked into bounded projection: %s", projected.Output)
	}
	var visible ToolResultProjection
	if err := json.Unmarshal(projected.Output, &visible); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	if visible.ContentState != ToolResultExternalized || visible.ResultRef != envelope.ResultRef || visible.ReadTool != "read_tool_result" || visible.ResultBytes != len(body) {
		t.Fatalf("model projection = %#v", visible)
	}
	var continuation struct {
		Preview    string `json:"preview"`
		NextOffset int    `json:"next_offset"`
		Complete   bool   `json:"complete"`
		Notice     string `json:"notice"`
	}
	if err := json.Unmarshal(projected.Output, &continuation); err != nil {
		t.Fatal(err)
	}
	if continuation.NextOffset != len([]byte(continuation.Preview)) || continuation.NextOffset <= 0 || continuation.Complete ||
		!strings.Contains(continuation.Notice, `Use read_tool_result(result_ref="tr_test_reference", offset=`) ||
		!strings.Contains(continuation.Notice, `Showing bytes 0-`) {
		t.Fatalf("projection continuation = %#v", continuation)
	}
}

func TestToolResultExternalizerWriteFailureReturnsNoReadableReference(t *testing.T) {
	store := &recordingToolResultStore{putErr: errors.New("redis unavailable")}
	externalizer := testToolResultExternalizer(store)
	body := json.RawMessage(`{"markdown":"` + strings.Repeat("long-", 300) + `"}`)

	projected, outcome := externalizer.Externalize(context.Background(), ToolResultScope{
		UserID: "user_a", ChatID: "chat_a", RunID: "run_a", ActionID: "decision:1/action:0", ToolName: "read_url",
	}, ActionResult{Status: ActionSucceeded, Output: body}, 384)

	if outcome.State != ToolResultNotCached || outcome.Err == nil {
		t.Fatalf("write failure outcome = %#v", outcome)
	}
	var visible ToolResultProjection
	if err := json.Unmarshal(projected.Output, &visible); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	if visible.ContentState != ToolResultNotCached || visible.ResultRef != "" || visible.ReadTool != "" {
		t.Fatalf("write failure exposed false reference: %#v", visible)
	}
	checkpoint, err := NewActionResultCheckpoint(1, 0, "decision:1/action:0", projected)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Payload) > 384 || !strings.Contains(visible.Notice, "reissue the original tool call") {
		t.Fatalf("write failure projection is not safely bounded/actionable: bytes=%d projection=%#v", len(checkpoint.Payload), visible)
	}
}

func TestToolResultReaderPagesUTF8AndReconstructsExactBody(t *testing.T) {
	store := &recordingToolResultStore{}
	body := []byte(`{"markdown":"视觉攻击🛡️与决策安全","count":3}`)
	envelope := testToolResultEnvelope(body)
	store.envelopes = append(store.envelopes, envelope)
	reader := ToolResultReader{Store: store, MaximumPageBytes: 11, Now: testToolResultNow}
	scope := ToolResultScope{UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID}

	var reconstructed strings.Builder
	offset := 0
	for {
		page, err := reader.Read(context.Background(), scope, envelope.ResultRef, offset, 1000)
		if err != nil {
			t.Fatalf("read offset %d: %v", offset, err)
		}
		if !utf8.ValidString(page.Content) {
			t.Fatalf("invalid UTF-8 page %q", page.Content)
		}
		reconstructed.WriteString(page.Content)
		if page.Complete {
			break
		}
		if page.NextOffset <= offset {
			t.Fatalf("pagination did not advance: %#v", page)
		}
		offset = page.NextOffset
	}
	if reconstructed.String() != string(body) {
		t.Fatalf("reconstructed body = %q, want %q", reconstructed.String(), body)
	}
}

func TestToolResultReaderRejectsScopeMismatchCorruptionAndInvalidOffset(t *testing.T) {
	body := []byte(`{"markdown":"安全🛡️"}`)
	tests := []struct {
		name   string
		mutate func(*ToolResultEnvelope, *ToolResultScope)
		offset int
		want   error
	}{
		{name: "cross user", mutate: func(_ *ToolResultEnvelope, scope *ToolResultScope) { scope.UserID = "user_b" }, want: ErrToolResultUnauthorized},
		{name: "cross chat", mutate: func(_ *ToolResultEnvelope, scope *ToolResultScope) { scope.ChatID = "chat_b" }, want: ErrToolResultUnauthorized},
		{name: "cross run", mutate: func(_ *ToolResultEnvelope, scope *ToolResultScope) { scope.RunID = "run_b" }, want: ErrToolResultUnauthorized},
		{name: "hash mismatch", mutate: func(envelope *ToolResultEnvelope, _ *ToolResultScope) { envelope.Body = append(envelope.Body, '!') }, want: ErrToolResultCorrupt},
		{name: "middle of rune", offset: len(`{"markdown":"安全`) + 1, want: ErrToolResultInvalidOffset},
		{name: "past end", offset: len(body) + 1, want: ErrToolResultInvalidOffset},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := testToolResultEnvelope(body)
			scope := ToolResultScope{UserID: envelope.UserID, ChatID: envelope.ChatID, RunID: envelope.RunID}
			if test.mutate != nil {
				test.mutate(&envelope, &scope)
			}
			store := &recordingToolResultStore{envelopes: []ToolResultEnvelope{envelope}}
			reader := ToolResultReader{Store: store, MaximumPageBytes: 16, Now: testToolResultNow}
			if _, err := reader.Read(context.Background(), scope, envelope.ResultRef, test.offset, 16); !errors.Is(err, test.want) {
				t.Fatalf("Read error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestToolResultReaderNormalizesMissingAndExpiredEntries(t *testing.T) {
	reader := ToolResultReader{Store: &recordingToolResultStore{}, MaximumPageBytes: 16, Now: testToolResultNow}
	scope := ToolResultScope{UserID: "user_a", ChatID: "chat_a", RunID: "run_a"}
	if _, err := reader.Read(context.Background(), scope, "tr_missing", 0, 16); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("missing error = %v", err)
	}
	envelope := testToolResultEnvelope([]byte(`{"ok":true}`))
	envelope.ExpiresAt = testToolResultNow().Add(-time.Second)
	reader.Store = &recordingToolResultStore{envelopes: []ToolResultEnvelope{envelope}}
	if _, err := reader.Read(context.Background(), scope, envelope.ResultRef, 0, 16); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestExpiredToolResultCheckpointStillRecoversWithoutAutomaticRerun(t *testing.T) {
	proposal, err := NewProposalCheckpoint(1, models.ActionProposalBatch{Actions: []models.ActionProposal{{
		Name: "read_url", Input: json.RawMessage(`{"url":"https://example.com/paper"}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	projection, _ := json.Marshal(ToolResultProjection{
		ActionID: "decision:1/action:0", ContentState: ToolResultExternalized,
		ResultRef: "tr_expired_checkpoint", ResultBytes: 100000, SHA256: strings.Repeat("a", 64),
		ExpiresAt: "2026-09-01T12:30:00Z", ReadTool: ToolResultReadTool,
	})
	result, err := NewActionResultCheckpoint(1, 0, "decision:1/action:0", ActionResult{Status: ActionSucceeded, Output: projection})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := LoadCheckpointPrefix(context.Background(), []Checkpoint{
		{SequenceNo: 1, PendingCheckpoint: proposal}, {SequenceNo: 2, PendingCheckpoint: result},
	})
	if err != nil || len(prefix.Proposals) != 1 || prefix.Proposals[0].Actions[0].Result == nil {
		t.Fatalf("checkpoint recovery prefix=%+v err=%v", prefix, err)
	}
	store := &recordingToolResultStore{}
	reader := ToolResultReader{Store: store, MaximumPageBytes: 64}
	if _, err := reader.Read(context.Background(), ToolResultScope{UserID: "user_a", ChatID: "chat_a", RunID: "run_a"}, "tr_expired_checkpoint", 0, 64); !errors.Is(err, ErrToolResultExpired) {
		t.Fatalf("expired recovered reference error=%v", err)
	}
	if len(store.envelopes) != 0 {
		t.Fatal("checkpoint recovery reran or rewrote the original Tool Result")
	}
}

func testToolResultEnvelope(body []byte) ToolResultEnvelope {
	hash := hashPayload(body)
	return ToolResultEnvelope{
		SchemaVersion: 1, ResultRef: "tr_test_reference", UserID: "user_a", ChatID: "chat_a", RunID: "run_a",
		ActionID: "decision:1/action:0", ToolName: "read_url", MediaType: "application/json", Encoding: "json",
		ResultBytes: len(body), SHA256: hash, CreatedAt: testToolResultNow(), ExpiresAt: testToolResultNow().Add(30 * time.Minute), Body: append([]byte(nil), body...),
	}
}

func testToolResultNow() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }

func testToolResultExternalizer(store ToolResultStore) *ToolResultExternalizer {
	return &ToolResultExternalizer{
		Store: store,
		Policy: ToolResultCachePolicy{
			TTL: 30 * time.Minute, MaximumValueBytes: 2 * 1024 * 1024,
		},
		Now:          testToolResultNow,
		NewResultRef: func() (string, error) { return "tr_test_reference", nil },
	}
}
