package sourcediscovery

import (
	"context"
	"net/url"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

type ImportabilityValidatorConfig struct {
	ExtractionConfigID string
	MaxBytes           int64
	MaxNormalizedRunes int
}

type ImportabilityValidator struct {
	reader webreader.Adapter
	config ImportabilityValidatorConfig
}

func NewImportabilityValidator(reader webreader.Adapter, config ImportabilityValidatorConfig) *ImportabilityValidator {
	return &ImportabilityValidator{reader: reader, config: config}
}

func (v *ImportabilityValidator) Validate(ctx context.Context, rawURL string) bool {
	if v == nil || v.reader == nil || v.config.MaxBytes <= 0 || v.config.MaxNormalizedRunes <= 0 || strings.TrimSpace(v.config.ExtractionConfigID) == "" {
		return false
	}
	page, err := v.reader.Parse(ctx, webreader.Request{
		URL: rawURL, Format: webreader.FormatMarkdown, MaxChars: webreader.MaxContentChars,
	})
	payload := []byte(page.Content)
	if err != nil || len(payload) == 0 || int64(len(payload)) > v.config.MaxBytes || page.Truncated {
		return false
	}
	parsed, err := url.Parse(page.FinalURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	artifact, err := normalize.Text(normalize.Input{
		SourceID: "discovery-preflight", ExtractionConfigID: v.config.ExtractionConfigID,
		Format: webreader.FormatMarkdown, Payload: payload,
	})
	if err != nil || normalize.Validate(artifact) != nil || artifact.Coverage.TotalRunes > v.config.MaxNormalizedRunes {
		return false
	}
	return true
}
