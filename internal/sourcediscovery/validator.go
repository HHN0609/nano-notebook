package sourcediscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/fetcher"
	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
)

type candidateSnapshotFetcher interface {
	Fetch(context.Context, string) (fetcher.Snapshot, error)
}

type candidateContentExtractor interface {
	Extract(context.Context, source.Source, []byte, string) (normalize.Artifact, error)
}

type ImportabilityValidatorConfig struct {
	ExtractionConfigID string
	MaxBytes           int64
	MaxNormalizedRunes int
}

type ImportabilityValidator struct {
	fetcher   candidateSnapshotFetcher
	extractor candidateContentExtractor
	config    ImportabilityValidatorConfig
}

func NewImportabilityValidator(snapshotFetcher candidateSnapshotFetcher, extractor candidateContentExtractor, config ImportabilityValidatorConfig) *ImportabilityValidator {
	return &ImportabilityValidator{fetcher: snapshotFetcher, extractor: extractor, config: config}
}

func (v *ImportabilityValidator) Validate(ctx context.Context, rawURL string) bool {
	if v == nil || v.fetcher == nil || v.extractor == nil || v.config.MaxBytes <= 0 || v.config.MaxNormalizedRunes <= 0 || strings.TrimSpace(v.config.ExtractionConfigID) == "" {
		return false
	}
	snapshot, err := v.fetcher.Fetch(ctx, rawURL)
	if err != nil || len(snapshot.Payload) == 0 || int64(len(snapshot.Payload)) > v.config.MaxBytes {
		return false
	}
	digest := sha256.Sum256(snapshot.Payload)
	if !strings.EqualFold(strings.TrimSpace(snapshot.ContentSHA256), hex.EncodeToString(digest[:])) {
		return false
	}
	format, ok := source.FormatForMediaType(snapshot.MediaType)
	if !ok {
		return false
	}
	parsed, err := url.Parse(snapshot.FinalURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	artifact, err := v.extractor.Extract(ctx, source.Source{
		ID: "discovery-preflight", InputKind: "url", Title: parsed.Hostname(), Format: format,
		MediaType: snapshot.MediaType, ByteSize: int64(len(snapshot.Payload)), ContentSHA256: strings.ToLower(snapshot.ContentSHA256),
		FinalURL: snapshot.FinalURL,
	}, snapshot.Payload, v.config.ExtractionConfigID)
	if err != nil || normalize.Validate(artifact) != nil || artifact.Coverage.TotalRunes > v.config.MaxNormalizedRunes {
		return false
	}
	return true
}
