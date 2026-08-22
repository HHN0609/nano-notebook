package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

type webReaderStub struct {
	page    webreader.Page
	err     error
	request webreader.Request
}

func (s *webReaderStub) Parse(_ context.Context, request webreader.Request) (webreader.Page, error) {
	s.request = request
	return s.page, s.err
}

func TestReadURLActionReturnsCleanMarkdownWithPinnedLimit(t *testing.T) {
	reader := &webReaderStub{page: webreader.Page{
		Title: "Harness", FinalURL: "https://example.com/final", Content: "# Harness\n\nUseful evidence.",
		Engine: "readability", WordCount: 3, Truncated: false,
	}}
	action := NewReadURLAction(reader)
	result, err := action.Execute(context.Background(), ActionRequest{Input: json.RawMessage(`{"url":"https://example.com/article"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || reader.request.MaxChars != ResearchReadURLMaxChars || reader.request.Format != webreader.FormatMarkdown {
		t.Fatalf("result=%+v request=%+v", result, reader.request)
	}
	var output struct {
		Title     string `json:"title"`
		FinalURL  string `json:"final_url"`
		Markdown  string `json:"markdown"`
		Engine    string `json:"engine"`
		WordCount int    `json:"word_count"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Title != "Harness" || output.FinalURL != "https://example.com/final" || output.Markdown != "# Harness\n\nUseful evidence." || output.Engine != "readability" || output.WordCount != 3 || output.Truncated {
		t.Fatalf("output=%+v", output)
	}
}

func TestReadURLActionIsCheckpointReplaySafe(t *testing.T) {
	policy, ok := NewReadURLAction(&webReaderStub{}).(CrashReplayPolicy)
	if !ok || !policy.CrashReplaySafe() {
		t.Fatal("read_url must replay an incomplete checkpoint after a Worker crash")
	}
}

func TestReadURLActionMakesExternalFailureARecoverableToolResult(t *testing.T) {
	action := NewReadURLAction(&webReaderStub{err: errors.New("sidecar unavailable")})
	result, err := action.Execute(context.Background(), ActionRequest{Input: json.RawMessage(`{"url":"https://example.com/article"}`)})
	if err != nil {
		t.Fatalf("external failure escaped Action result: %v", err)
	}
	if result.Status != ActionDomainError || result.ErrorCode != "read_url_failed" {
		t.Fatalf("result=%+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReadURLActionRejectsMutableOrUnsafeInput(t *testing.T) {
	action := NewReadURLAction(&webReaderStub{})
	for _, input := range []string{
		`{"url":"http://user:password@example.com/article"}`,
		`{"url":"https://example.com/article#fragment"}`,
		`{"url":"file:///tmp/article"}`,
		`{"url":"https://example.com","max_chars":250000}`,
	} {
		if err := action.ValidateInput(json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
