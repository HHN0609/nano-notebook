package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

type researchSourceImporterStub struct {
	request ResearchSourceImportRequest
	result  ResearchSourceImportResult
	err     error
}

func (s *researchSourceImporterStub) ImportResearchPDF(_ context.Context, request ResearchSourceImportRequest) (ResearchSourceImportResult, error) {
	s.request = request
	return s.result, s.err
}

func TestSaveURLAsSourceUsesOnlyTrustedAttemptAuthorityAndReturnsBoundedLifecycleState(t *testing.T) {
	backend := &researchSourceImporterStub{result: ResearchSourceImportResult{
		SourceID: "src_pdf", ProcessingJobID: "srcjob_pdf", State: "processing",
		Searchable: false, Reused: false, FinalURL: "https://cdn.example.com/paper.pdf",
	}}
	action := NewSaveURLAsSourceAction(backend)
	request := ActionRequest{
		ActionID:   "decision:2/action:1",
		Definition: agentcatalog.Reference{Identity: "research.executor", Version: 9},
		Attempt:    Attempt{RunID: "run_pdf", JobID: "job_pdf", AttemptNo: 3, LeaseToken: "lease_pdf"},
		Input:      json.RawMessage(`{"url":"https://example.com/paper.pdf"}`),
	}
	result, err := action.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ActionSucceeded || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	if backend.request.URL != "https://example.com/paper.pdf" || backend.request.ActionID != request.ActionID ||
		backend.request.Attempt != request.Attempt {
		t.Fatalf("trusted request=%+v", backend.request)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output) != 6 || string(output["source_id"]) != `"src_pdf"` ||
		string(output["processing_job_id"]) != `"srcjob_pdf"` ||
		string(output["state"]) != `"processing"` || string(output["searchable"]) != "false" ||
		string(output["reused"]) != "false" || string(output["final_url"]) != `"https://cdn.example.com/paper.pdf"` {
		t.Fatalf("output=%s", result.Output)
	}
	for _, forbidden := range [][]byte{[]byte("%PDF-"), []byte(`"markdown"`), []byte(`"document_handle"`), []byte(`"object_key"`)} {
		if bytes.Contains(result.Output, forbidden) {
			t.Fatalf("output leaked %q: %s", forbidden, result.Output)
		}
	}
}

func TestSaveURLAsSourceIsCrashReplaySafeAndRejectsModelSelectedAuthority(t *testing.T) {
	action := NewSaveURLAsSourceAction(&researchSourceImporterStub{})
	policy, ok := action.(CrashReplayPolicy)
	if !ok || !policy.CrashReplaySafe() {
		t.Fatal("save_url_as_source must replay an incomplete accepted Action")
	}
	for _, input := range []string{
		`{}`,
		`{"url":"file:///tmp/paper.pdf"}`,
		`{"url":"https://user:password@example.com/paper.pdf"}`,
		`{"url":"https://example.com/paper.pdf#page=2"}`,
		`{"url":"https://example.com/paper.pdf","title":"forged"}`,
		`{"url":"https://example.com/paper.pdf","notebook_id":"notebook_forged"}`,
		`{"url":"https://example.com/paper.pdf","source_id":"src_forged"}`,
	} {
		if err := action.ValidateInput(json.RawMessage(input)); err == nil {
			t.Fatalf("accepted input=%s", input)
		}
	}
}

func TestSaveURLAsSourceMapsBoundedDomainFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "unsafe", err: webreader.ErrUnsafeDestination, code: "source_import_unsafe_destination"},
		{name: "too large", err: webreader.ErrResponseTooLarge, code: "source_import_response_too_large"},
		{name: "type mismatch", err: webreader.ErrDocumentTypeMismatch, code: "source_import_document_type_mismatch"},
		{name: "unsupported", err: webreader.ErrUnsupportedType, code: "source_import_unsupported_type"},
		{name: "quota", err: source.ErrQuotaReached, code: "source_import_quota_reached"},
		{name: "unavailable", err: ErrResearchSourceImportUnavailable, code: "source_import_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewSaveURLAsSourceAction(&researchSourceImporterStub{err: test.err}).Execute(
				context.Background(),
				ActionRequest{
					ActionID:   "decision:1/action:0",
					Definition: agentcatalog.Reference{Identity: "research.executor", Version: 9},
					Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"},
					Input:      json.RawMessage(`{"url":"https://example.com/paper.pdf"}`),
				},
			)
			if err != nil || result.Status != ActionDomainError || result.ErrorCode != test.code || len(result.Output) != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestSaveURLAsSourceRequiresCompleteExecutionIdentity(t *testing.T) {
	action := NewSaveURLAsSourceAction(&researchSourceImporterStub{})
	_, err := action.Execute(context.Background(), ActionRequest{
		ActionID:   "decision:1/action:0",
		Definition: agentcatalog.Reference{Identity: "research.executor", Version: 9},
		Attempt:    Attempt{RunID: "run"},
		Input:      json.RawMessage(`{"url":"https://example.com/paper.pdf"}`),
	})
	if !errors.Is(err, ErrResearchSourceImportUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
