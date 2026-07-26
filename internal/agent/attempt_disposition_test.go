package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

func TestAttemptDispositionClassifiesRetryableTerminalAndAbandonedFailures(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		cause error
		want  AttemptResolution
	}{
		{name: "success", want: AttemptResolution{Disposition: AttemptCompleted}},
		{name: "lease lost", err: ErrLeaseLost, want: AttemptResolution{Disposition: AttemptAbandoned, ErrorCode: AttemptCauseLeaseLost}},
		{name: "cancelled", err: context.Canceled, cause: context.Canceled, want: AttemptResolution{Disposition: AttemptAbandoned, ErrorCode: AttemptCauseCancelled}},
		{name: "model timeout", err: &models.ModelError{Kind: models.ErrorTimeout, Err: context.DeadlineExceeded}, want: AttemptResolution{Disposition: AttemptRetryable, ErrorCode: string(models.ErrorTimeout)}},
		{name: "model unavailable", err: &models.ModelError{Kind: models.ErrorUnavailable, Err: errors.New("down")}, want: AttemptResolution{Disposition: AttemptRetryable, ErrorCode: string(models.ErrorUnavailable)}},
		{name: "model invalid", err: &models.ModelError{Kind: models.ErrorInvalidResponse, Err: errors.New("bad")}, want: AttemptResolution{Disposition: AttemptTerminal, ErrorCode: string(models.ErrorInvalidResponse)}},
		{name: "search timeout", err: websearch.ErrTimeout, want: AttemptResolution{Disposition: AttemptRetryable, ErrorCode: "discovery_timeout"}},
		{name: "search rate", err: websearch.ErrRateLimited, want: AttemptResolution{Disposition: AttemptRetryable, ErrorCode: "discovery_rate_limited"}},
		{name: "search unavailable", err: websearch.ErrUnavailable, want: AttemptResolution{Disposition: AttemptRetryable, ErrorCode: "discovery_unavailable"}},
		{name: "invalid query", err: websearch.ErrInvalidQuery, want: AttemptResolution{Disposition: AttemptTerminal, ErrorCode: "discovery_invalid_query"}},
		{name: "authority lost", err: ErrResearchAuthorityLost, want: AttemptResolution{Disposition: AttemptTerminal, ErrorCode: "research_authority_lost"}},
		{name: "unknown", err: errors.New("secret provider body"), want: AttemptResolution{Disposition: AttemptTerminal, ErrorCode: "agent_execution_failed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyAttempt(test.err, test.cause); got != test.want {
				t.Fatalf("ClassifyAttempt() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAttemptRetryBackoffIsBoundedAndDeterministic(t *testing.T) {
	for attempt, base := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second} {
		got := AttemptRetryBackoff(attempt, "job_one")
		if got < base*3/4 || got > base*5/4 {
			t.Fatalf("AttemptRetryBackoff(%d) = %s, outside jittered bound", attempt, got)
		}
		if again := AttemptRetryBackoff(attempt, "job_one"); again != got {
			t.Fatalf("same Job backoff changed from %s to %s", got, again)
		}
	}
	if got := AttemptRetryBackoff(20, "job_one"); got > 30*time.Second {
		t.Fatalf("bounded backoff = %s", got)
	}
}

func TestAttemptResolutionRejectsUnsafeCodesAndInvalidShapes(t *testing.T) {
	invalid := []AttemptResolution{
		{},
		{Disposition: AttemptCompleted, ErrorCode: "unexpected"},
		{Disposition: AttemptRetryable},
		{Disposition: AttemptTerminal, ErrorCode: "provider secret body"},
		{Disposition: AttemptAbandoned, ErrorCode: "unknown"},
	}
	for _, resolution := range invalid {
		if resolution.Valid() {
			t.Fatalf("resolution %#v should be invalid", resolution)
		}
	}
}
