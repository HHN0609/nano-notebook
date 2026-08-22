package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

const ResearchReadURLMaxChars = 120_000

type readURLAction struct {
	reader webreader.Adapter
}

type readURLInput struct {
	URL string `json:"url"`
}

type readURLOutput struct {
	Title     string `json:"title"`
	FinalURL  string `json:"final_url"`
	Markdown  string `json:"markdown"`
	Engine    string `json:"engine"`
	WordCount int    `json:"word_count"`
	Truncated bool   `json:"truncated"`
}

func NewReadURLAction(reader webreader.Adapter) Action {
	return &readURLAction{reader: reader}
}

func (*readURLAction) CrashReplaySafe() bool { return true }

func (*readURLAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "read_url",
		Description: "Fetch one public HTTP or HTTPS URL and return cleaned Markdown for substantive evidence analysis.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["url"],"properties":{"url":{"type":"string","minLength":1,"maxLength":4096,"pattern":"^https?://"}}}`),
	}
}

func (*readURLAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeReadURLInput(raw)
	return err
}

func (a *readURLAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	input, err := decodeReadURLInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if a == nil || a.reader == nil {
		return ActionResult{Status: ActionDomainError, ErrorCode: "read_url_unavailable"}, nil
	}
	page, err := a.reader.Parse(ctx, webreader.Request{URL: input.URL, Format: webreader.FormatMarkdown, MaxChars: ResearchReadURLMaxChars})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return ActionResult{}, contextErr
		}
		return ActionResult{Status: ActionDomainError, ErrorCode: "read_url_failed"}, nil
	}
	payload, err := json.Marshal(readURLOutput{
		Title: page.Title, FinalURL: page.FinalURL, Markdown: page.Content,
		Engine: page.Engine, WordCount: page.WordCount, Truncated: page.Truncated,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func decodeReadURLInput(raw json.RawMessage) (readURLInput, error) {
	var input readURLInput
	if err := decodeExactJSON(raw, &input); err != nil {
		return readURLInput{}, errors.New("invalid read_url input")
	}
	request := webreader.Request{URL: input.URL, Format: webreader.FormatMarkdown, MaxChars: ResearchReadURLMaxChars}
	if err := request.Validate(); err != nil {
		return readURLInput{}, errors.New("invalid read_url input")
	}
	return input, nil
}
