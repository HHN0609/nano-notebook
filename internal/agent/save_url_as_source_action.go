package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

var ErrResearchSourceImportUnavailable = errors.New("Research Source import is unavailable")

type ResearchSourceImportRequest struct {
	URL      string
	ActionID string
	Attempt  Attempt
}

type ResearchSourceImportResult struct {
	SourceID        string
	ProcessingJobID string
	State           string
	Searchable      bool
	Reused          bool
	FinalURL        string
}

type ResearchSourceImporter interface {
	ImportResearchPDF(context.Context, ResearchSourceImportRequest) (ResearchSourceImportResult, error)
}

type saveURLAsSourceAction struct {
	backend ResearchSourceImporter
}

type saveURLAsSourceInput struct {
	URL string `json:"url"`
}

type saveURLAsSourceOutput struct {
	SourceID        string `json:"source_id"`
	ProcessingJobID string `json:"processing_job_id"`
	State           string `json:"state"`
	Searchable      bool   `json:"searchable"`
	Reused          bool   `json:"reused"`
	FinalURL        string `json:"final_url"`
}

func NewSaveURLAsSourceAction(backend ResearchSourceImporter) Action {
	return &saveURLAsSourceAction{backend: backend}
}

func (*saveURLAsSourceAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "save_url_as_source",
		Description: "Import one substantive public PDF as a permanent Notebook Source. Accepted imports process asynchronously and are not searchable until Ready.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["url"],"properties":{"url":{"type":"string","minLength":1,"maxLength":4096,"pattern":"^https?://"}}}`),
	}
}

func (*saveURLAsSourceAction) CrashReplaySafe() bool { return true }

func (*saveURLAsSourceAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeSaveURLAsSourceInput(raw)
	return err
}

func (a *saveURLAsSourceAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	input, err := decodeSaveURLAsSourceInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if a == nil || a.backend == nil || !stableActionIDPattern.MatchString(request.ActionID) ||
		request.Definition.Identity != "research.executor" || request.Definition.Version < 9 ||
		request.Attempt.RunID == "" || request.Attempt.JobID == "" || request.Attempt.LeaseToken == "" || request.Attempt.AttemptNo < 1 {
		return ActionResult{}, ErrResearchSourceImportUnavailable
	}
	imported, err := a.backend.ImportResearchPDF(ctx, ResearchSourceImportRequest{
		URL: input.URL, ActionID: request.ActionID, Attempt: request.Attempt,
	})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return ActionResult{}, contextErr
		}
		return ActionResult{Status: ActionDomainError, ErrorCode: classifyResearchSourceImportError(err)}, nil
	}
	payload, err := json.Marshal(saveURLAsSourceOutput{
		SourceID: imported.SourceID, ProcessingJobID: imported.ProcessingJobID, State: imported.State,
		Searchable: imported.Searchable, Reused: imported.Reused, FinalURL: imported.FinalURL,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func decodeSaveURLAsSourceInput(raw json.RawMessage) (saveURLAsSourceInput, error) {
	var input saveURLAsSourceInput
	if err := decodeExactJSON(raw, &input); err != nil {
		return saveURLAsSourceInput{}, errors.New("invalid save_url_as_source input")
	}
	request := webreader.Request{URL: input.URL, Format: webreader.FormatMarkdown, MaxChars: webreader.MaxContentChars}
	if err := request.Validate(); err != nil {
		return saveURLAsSourceInput{}, errors.New("invalid save_url_as_source input")
	}
	return input, nil
}

func classifyResearchSourceImportError(err error) string {
	switch {
	case errors.Is(err, webreader.ErrUnsafeDestination):
		return "source_import_unsafe_destination"
	case errors.Is(err, webreader.ErrResponseTooLarge):
		return "source_import_response_too_large"
	case errors.Is(err, webreader.ErrDocumentTypeMismatch):
		return "source_import_document_type_mismatch"
	case errors.Is(err, webreader.ErrUnsupportedType):
		return "source_import_unsupported_type"
	case errors.Is(err, source.ErrQuotaReached):
		return "source_import_quota_reached"
	case errors.Is(err, ErrResearchSourceImportUnavailable):
		return "source_import_unavailable"
	default:
		return "source_import_failed"
	}
}
