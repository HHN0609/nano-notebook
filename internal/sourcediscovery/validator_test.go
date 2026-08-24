package sourcediscovery

import (
	"context"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

func TestImportabilityValidatorRequiresReadableBoundedMarkdown(t *testing.T) {
	validPage := webreader.Page{
		FinalURL: "https://example.com/guide", Title: "Guide",
		Content: "# Guide\n\nA useful, readable source document with enough detail.", Engine: "lightweight", WordCount: 10,
	}
	tests := []struct {
		name   string
		reader *validatorReader
		want   bool
	}{
		{name: "valid", reader: &validatorReader{page: validPage}, want: true},
		{name: "read failure", reader: &validatorReader{err: errors.New("blocked")}},
		{name: "empty content", reader: &validatorReader{page: webreader.Page{FinalURL: validPage.FinalURL, Engine: "lightweight"}}},
		{name: "invalid final url", reader: &validatorReader{page: webreader.Page{FinalURL: "file:///tmp/private", Content: validPage.Content, Engine: "lightweight"}}},
		{name: "truncated", reader: &validatorReader{page: webreader.Page{FinalURL: validPage.FinalURL, Content: validPage.Content, Engine: "lightweight", Truncated: true}}},
		{name: "over normalized budget", reader: &validatorReader{page: webreader.Page{FinalURL: validPage.FinalURL, Content: validPage.Content + validPage.Content, Engine: "lightweight"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := NewImportabilityValidator(test.reader, ImportabilityValidatorConfig{
				ExtractionConfigID: "discovery-preflight-v1", MaxBytes: 1 << 20, MaxNormalizedRunes: 80,
			})
			if got := validator.Validate(context.Background(), "https://example.com/guide"); got != test.want {
				t.Fatalf("Validate()=%v want=%v", got, test.want)
			}
		})
	}
}

type validatorReader struct {
	page webreader.Page
	err  error
}

func (r *validatorReader) Parse(context.Context, webreader.Request) (webreader.Page, error) {
	return r.page, r.err
}
