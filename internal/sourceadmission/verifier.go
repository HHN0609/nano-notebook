package sourceadmission

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type VerifierConfig struct {
	Policy              Policy
	ProviderID          string
	MaxAttemptsPerQuery int
	QueryTimeout        time.Duration
}

func DefaultVerifierConfig(providerID string) VerifierConfig {
	return VerifierConfig{
		Policy: DefaultPolicy(), ProviderID: strings.TrimSpace(providerID),
		MaxAttemptsPerQuery: 2, QueryTimeout: 5 * time.Second,
	}
}

type Assessment struct {
	Report           Report          `json:"report"`
	Input            EvaluationInput `json:"input"`
	Queries          []string        `json:"queries"`
	ProviderID       string          `json:"provider_id"`
	ProviderAttempts int             `json:"provider_attempts"`
}

type Verifier struct {
	provider websearch.Provider
	config   VerifierConfig
}

func NewVerifier(provider websearch.Provider, config VerifierConfig) (*Verifier, error) {
	if _, err := PolicySHA256(config.Policy); err != nil || strings.TrimSpace(config.ProviderID) == "" ||
		config.MaxAttemptsPerQuery < 1 || config.MaxAttemptsPerQuery > 2 || config.QueryTimeout <= 0 {
		return nil, errors.New("invalid Source Admission Verifier")
	}
	return &Verifier{provider: provider, config: config}, nil
}

func (verifier *Verifier) Verify(ctx context.Context, profile Profile, artifact normalize.Artifact) (Assessment, error) {
	if verifier == nil {
		return Assessment{}, errors.New("invalid Source Admission Verifier")
	}
	extraction, err := ObserveExtraction(artifact)
	if err != nil {
		return Assessment{}, err
	}
	profile.ArtifactSHA256 = artifact.SHA256
	profile.StableIdentifiers = mergeStableIdentifiers(
		profile.StableIdentifiers,
		ExtractStableIdentifiers(profile.Title, profile.FinalURL, artifact.Text),
	)
	input := EvaluationInput{Profile: profile, Extraction: extraction, ProviderID: verifier.config.ProviderID}
	queries := BuildQueries(profile)
	assessment := Assessment{Queries: append([]string(nil), queries...), ProviderID: verifier.config.ProviderID}
	if profile.InputKind == "url" {
		if verifier.provider == nil {
			input.SearchError = ReasonExternalVerificationUnavailable
		} else {
			seenResults := make(map[string]struct{})
			for _, query := range queries {
				results, attempts, searchErr := verifier.search(ctx, query)
				assessment.ProviderAttempts += attempts
				if searchErr != nil {
					if errors.Is(searchErr, context.Canceled) || errors.Is(searchErr, context.DeadlineExceeded) {
						return Assessment{}, searchErr
					}
					if isDegradedSearchError(searchErr) {
						input.SearchError = ReasonExternalVerificationUnavailable
						continue
					}
					return Assessment{}, searchErr
				}
				input.Searches = append(input.Searches, SearchObservation{
					Query: query, Results: normalizeSearchResults(results, seenResults, verifier.config.Policy.ResultsPerQuery),
				})
			}
		}
	}
	input.ProviderAttempts = assessment.ProviderAttempts
	report, err := Evaluate(verifier.config.Policy, input)
	if err != nil {
		return Assessment{}, err
	}
	assessment.Report = report
	assessment.Input = input
	return assessment, nil
}

func (verifier *Verifier) search(ctx context.Context, query string) ([]websearch.Candidate, int, error) {
	for attempt := 1; attempt <= verifier.config.MaxAttemptsPerQuery; attempt++ {
		queryCtx, cancel := context.WithTimeout(ctx, verifier.config.QueryTimeout)
		results, err := verifier.provider.Search(queryCtx, websearch.Request{Query: query, Count: verifier.config.Policy.ResultsPerQuery})
		cancel()
		if err == nil {
			return results, attempt, nil
		}
		if ctx.Err() != nil {
			return nil, attempt, context.Cause(ctx)
		}
		if errors.Is(err, context.Canceled) {
			return nil, attempt, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) && attempt < verifier.config.MaxAttemptsPerQuery {
			continue
		}
		if !retryableSearchError(err) || attempt == verifier.config.MaxAttemptsPerQuery {
			return nil, attempt, err
		}
	}
	return nil, verifier.config.MaxAttemptsPerQuery, websearch.ErrUnavailable
}

func retryableSearchError(err error) bool {
	return errors.Is(err, websearch.ErrTimeout) || errors.Is(err, websearch.ErrRateLimited) || errors.Is(err, websearch.ErrUnavailable)
}

func isDegradedSearchError(err error) bool {
	return retryableSearchError(err) || errors.Is(err, websearch.ErrNotConfigured) || errors.Is(err, websearch.ErrInvalidResponse) || errors.Is(err, context.DeadlineExceeded)
}

func normalizeSearchResults(candidates []websearch.Candidate, seen map[string]struct{}, limit int) []SearchResult {
	results := make([]SearchResult, 0, min(len(candidates), limit))
	for _, candidate := range candidates {
		canonical, err := source.CanonicalURLIdentity(candidate.URL)
		if err != nil {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		rank := candidate.Rank
		if rank < 1 {
			rank = len(results) + 1
		}
		results = append(results, SearchResult{
			Title: boundedRunes(candidate.Title, 512), URL: boundedRunes(canonical, 4096),
			Description: boundedRunes(candidate.Description, 2048), Rank: rank,
		})
		if len(results) == limit {
			break
		}
	}
	return results
}

func mergeStableIdentifiers(groups ...[]StableIdentifier) []StableIdentifier {
	seen := make(map[string]StableIdentifier)
	for _, group := range groups {
		for _, identifier := range group {
			kind := strings.ToLower(strings.TrimSpace(identifier.Kind))
			value := strings.ToLower(strings.TrimSpace(identifier.Value))
			if stableIdentifierPriority(kind) == 100 || value == "" {
				continue
			}
			seen[kind+"\x00"+value] = StableIdentifier{Kind: kind, Value: value}
		}
	}
	merged := make([]StableIdentifier, 0, len(seen))
	for _, identifier := range seen {
		merged = append(merged, identifier)
	}
	if strongest, ok := strongestStableIdentifier(merged); ok {
		remaining := make([]StableIdentifier, 0, len(merged))
		remaining = append(remaining, strongest)
		for _, identifier := range merged {
			if identifier != strongest {
				remaining = append(remaining, identifier)
			}
		}
		return remaining
	}
	return merged
}

func boundedRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
