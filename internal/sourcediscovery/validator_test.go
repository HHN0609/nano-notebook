package sourcediscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/fetcher"
	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
)

func TestImportabilityValidatorRequiresFetchableExtractableContent(t *testing.T) {
	payload := []byte("A useful, readable source document.")
	digest := sha256.Sum256(payload)
	validSnapshot := fetcher.Snapshot{
		FinalURL: "https://example.com/guide", MediaType: "text/plain", Payload: payload,
		ContentSHA256: hex.EncodeToString(digest[:]),
	}
	tests := []struct {
		name      string
		fetcher   *validatorFetcher
		extractor candidateExtractor
		want      bool
	}{
		{name: "valid", fetcher: &validatorFetcher{snapshot: validSnapshot}, extractor: textCandidateExtractor, want: true},
		{name: "fetch failure", fetcher: &validatorFetcher{err: errors.New("blocked")}, extractor: textCandidateExtractor},
		{name: "empty payload", fetcher: &validatorFetcher{snapshot: fetcher.Snapshot{FinalURL: "https://example.com/empty", MediaType: "text/plain", ContentSHA256: hex.EncodeToString(sha256.New().Sum(nil))}}, extractor: textCandidateExtractor},
		{name: "invalid proof", fetcher: &validatorFetcher{snapshot: fetcher.Snapshot{FinalURL: validSnapshot.FinalURL, MediaType: validSnapshot.MediaType, Payload: payload, ContentSHA256: "wrong"}}, extractor: textCandidateExtractor},
		{name: "unsupported type", fetcher: &validatorFetcher{snapshot: fetcher.Snapshot{FinalURL: validSnapshot.FinalURL, MediaType: "application/octet-stream", Payload: payload, ContentSHA256: validSnapshot.ContentSHA256}}, extractor: textCandidateExtractor},
		{name: "extraction failure", fetcher: &validatorFetcher{snapshot: validSnapshot}, extractor: func(context.Context, source.Source, []byte, string) (normalize.Artifact, error) {
			return normalize.Artifact{}, errors.New("unreadable")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := NewImportabilityValidator(test.fetcher, test.extractor, ImportabilityValidatorConfig{
				ExtractionConfigID: "discovery-preflight-v1", MaxBytes: 1 << 20, MaxNormalizedRunes: 10_000,
			})
			if got := validator.Validate(context.Background(), "https://example.com/guide"); got != test.want {
				t.Fatalf("Validate()=%v want=%v", got, test.want)
			}
		})
	}
}

type validatorFetcher struct {
	snapshot fetcher.Snapshot
	err      error
}

func (f *validatorFetcher) Fetch(context.Context, string) (fetcher.Snapshot, error) {
	return f.snapshot, f.err
}

type candidateExtractor func(context.Context, source.Source, []byte, string) (normalize.Artifact, error)

func (f candidateExtractor) Extract(ctx context.Context, item source.Source, payload []byte, configID string) (normalize.Artifact, error) {
	return f(ctx, item, payload, configID)
}

func textCandidateExtractor(_ context.Context, item source.Source, payload []byte, configID string) (normalize.Artifact, error) {
	return normalize.Text(normalize.Input{SourceID: item.ID, ExtractionConfigID: configID, Format: string(item.Format), Payload: payload})
}
