package sourceadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"golang.org/x/net/publicsuffix"
)

type Status string

const (
	StatusPassed         Status = "passed"
	StatusReviewRequired Status = "review_required"
	StatusNotApplicable  Status = "not_applicable"
)

type ReasonCode string

const (
	ReasonExtractionComplete                ReasonCode = "extraction_complete"
	ReasonExtractionPartial                 ReasonCode = "extraction_partial"
	ReasonExactURLMatch                     ReasonCode = "exact_url_match"
	ReasonExactIdentifierMatch              ReasonCode = "exact_identifier_match"
	ReasonExternalReferenceFound            ReasonCode = "external_reference_found"
	ReasonExternalVerificationUnavailable   ReasonCode = "external_verification_unavailable"
	ReasonExternalVerificationNotApplicable ReasonCode = "external_verification_not_applicable"
	ReasonExactIdentityRequired             ReasonCode = "exact_identity_required"
	ReasonSignalCoverageInsufficient        ReasonCode = "signal_coverage_insufficient"
	ReasonScoreBelowThreshold               ReasonCode = "score_below_threshold"
)

type Policy struct {
	ID              string           `json:"id"`
	Weights         ComponentWeights `json:"weights"`
	MinimumCoverage float64          `json:"minimum_coverage"`
	MinimumScore    float64          `json:"minimum_score"`
	MaxQueries      int              `json:"max_queries"`
	ResultsPerQuery int              `json:"results_per_query"`
}

func DefaultPolicy() Policy {
	return Policy{
		ID:              "source-admission-v1",
		Weights:         ComponentWeights{Provenance: 0.40, Extraction: 0.30, ExternalCorroboration: 0.20, Freshness: 0.10},
		MinimumCoverage: 0.70, MinimumScore: 0.75, MaxQueries: maxAdmissionQueries, ResultsPerQuery: 5,
	}
}

type ComponentWeights struct {
	Provenance            float64 `json:"provenance"`
	Extraction            float64 `json:"extraction"`
	ExternalCorroboration float64 `json:"external_corroboration"`
	Freshness             float64 `json:"freshness"`
}

type ExtractionObservation struct {
	CoverageStatus        string  `json:"coverage_status"`
	TotalRunes            int     `json:"total_runes"`
	BlockCount            int     `json:"block_count"`
	GapCount              int     `json:"gap_count"`
	InvalidCharacterRatio float64 `json:"invalid_character_ratio"`
	RepeatedBlockRatio    float64 `json:"repeated_block_ratio"`
}

type SearchObservation struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Rank        int    `json:"rank"`
}

type EvaluationInput struct {
	Profile          Profile               `json:"profile"`
	Extraction       ExtractionObservation `json:"extraction"`
	Searches         []SearchObservation   `json:"searches"`
	SearchError      ReasonCode            `json:"search_error,omitempty"`
	ProviderID       string                `json:"provider_id,omitempty"`
	ProviderAttempts int                   `json:"provider_attempts"`
}

type ComponentScores struct {
	Provenance            *float64 `json:"provenance,omitempty"`
	Extraction            *float64 `json:"extraction,omitempty"`
	ExternalCorroboration *float64 `json:"external_corroboration,omitempty"`
	Freshness             *float64 `json:"freshness,omitempty"`
}

type Report struct {
	ID                 string          `json:"id"`
	PolicyID           string          `json:"policy_id"`
	PolicySHA256       string          `json:"policy_sha256"`
	Status             Status          `json:"status"`
	Score              *float64        `json:"score,omitempty"`
	SignalCoverage     float64         `json:"signal_coverage"`
	ExactIdentityMatch bool            `json:"exact_identity_match"`
	Components         ComponentScores `json:"components"`
	Reasons            []ReasonCode    `json:"reasons"`
}

func Evaluate(policy Policy, input EvaluationInput) (Report, error) {
	if err := validateEvaluation(policy, input); err != nil {
		return Report{}, err
	}
	extractionScore := scoreExtraction(input.Extraction)
	report := Report{
		PolicyID:       policy.ID,
		SignalCoverage: policy.Weights.Extraction,
		Components:     ComponentScores{Extraction: floatPointer(extractionScore)},
		Reasons:        []ReasonCode{ReasonExtractionComplete},
	}
	if input.Extraction.CoverageStatus == "partial" {
		report.Reasons[0] = ReasonExtractionPartial
	}
	if input.Profile.InputKind == "file" {
		report.Status = StatusNotApplicable
		report.Reasons = append(report.Reasons, ReasonExternalVerificationNotApplicable)
		return finalizeReport(policy, input, report)
	}

	provenance, provenanceObserved, exactURL, exactIdentifier, externalDomains := scoreSearchIdentity(input.Profile, input.Searches)
	weightedScore := policy.Weights.Extraction * extractionScore
	if provenanceObserved {
		report.Components.Provenance = floatPointer(provenance)
		report.SignalCoverage += policy.Weights.Provenance
		weightedScore += policy.Weights.Provenance * provenance
	}
	if len(externalDomains) > 0 {
		external := math.Min(1, float64(len(externalDomains))/2)
		report.Components.ExternalCorroboration = floatPointer(external)
		report.SignalCoverage += policy.Weights.ExternalCorroboration
		weightedScore += policy.Weights.ExternalCorroboration * external
		report.Reasons = append(report.Reasons, ReasonExternalReferenceFound)
	}
	if exactURL {
		report.Reasons = append(report.Reasons, ReasonExactURLMatch)
	}
	if exactIdentifier {
		report.Reasons = append(report.Reasons, ReasonExactIdentifierMatch)
	}
	report.ExactIdentityMatch = exactURL || exactIdentifier
	if input.SearchError != "" {
		report.Reasons = append(report.Reasons, input.SearchError)
	}
	report.SignalCoverage = rounded(report.SignalCoverage)
	score := rounded(weightedScore / report.SignalCoverage)
	report.Score = &score
	if !report.ExactIdentityMatch {
		report.Reasons = append(report.Reasons, ReasonExactIdentityRequired)
	}
	if report.SignalCoverage < policy.MinimumCoverage {
		report.Reasons = append(report.Reasons, ReasonSignalCoverageInsufficient)
	}
	if score < policy.MinimumScore {
		report.Reasons = append(report.Reasons, ReasonScoreBelowThreshold)
	}
	if report.ExactIdentityMatch && report.SignalCoverage >= policy.MinimumCoverage && score >= policy.MinimumScore {
		report.Status = StatusPassed
	} else {
		report.Status = StatusReviewRequired
	}
	return finalizeReport(policy, input, report)
}

func validateEvaluation(policy Policy, input EvaluationInput) error {
	if _, err := PolicySHA256(policy); err != nil {
		return errors.New("invalid Source Admission policy")
	}
	if input.Profile.InputKind != "file" && input.Profile.InputKind != "url" {
		return errors.New("invalid Source Admission profile")
	}
	if input.Extraction.TotalRunes < 1 || input.Extraction.BlockCount < 1 || input.Extraction.GapCount < 0 ||
		(input.Extraction.CoverageStatus != "complete" && input.Extraction.CoverageStatus != "partial") ||
		(input.Extraction.CoverageStatus == "complete" && input.Extraction.GapCount != 0) ||
		input.Extraction.InvalidCharacterRatio < 0 || input.Extraction.InvalidCharacterRatio > 1 ||
		input.Extraction.RepeatedBlockRatio < 0 || input.Extraction.RepeatedBlockRatio > 1 || len(input.Searches) > policy.MaxQueries {
		return errors.New("invalid Source Admission observations")
	}
	for _, search := range input.Searches {
		if len(search.Results) > policy.ResultsPerQuery {
			return errors.New("invalid Source Admission observations")
		}
	}
	if input.ProviderAttempts < 0 || input.ProviderAttempts > policy.MaxQueries*2 {
		return errors.New("invalid Source Admission observations")
	}
	return nil
}

func PolicySHA256(policy Policy) (string, error) {
	weightTotal := policy.Weights.Provenance + policy.Weights.Extraction + policy.Weights.ExternalCorroboration + policy.Weights.Freshness
	if strings.TrimSpace(policy.ID) == "" || policy.MinimumCoverage <= 0 || policy.MinimumCoverage > 1 ||
		policy.MinimumScore <= 0 || policy.MinimumScore > 1 || policy.MaxQueries < 1 || policy.MaxQueries > maxAdmissionQueries ||
		policy.ResultsPerQuery < 1 || policy.ResultsPerQuery > 10 || math.Abs(weightTotal-1) > 0.000001 ||
		policy.Weights.Provenance <= 0 || policy.Weights.Extraction <= 0 || policy.Weights.ExternalCorroboration <= 0 || policy.Weights.Freshness <= 0 {
		return "", errors.New("invalid Source Admission policy")
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func finalizeReport(policy Policy, input EvaluationInput, report Report) (Report, error) {
	policyHash, err := PolicySHA256(policy)
	if err != nil {
		return Report{}, err
	}
	report.PolicyID = policy.ID
	report.PolicySHA256 = policyHash
	report.ID = ""
	canonical, err := json.Marshal(struct {
		PolicySHA256 string          `json:"policy_sha256"`
		Input        EvaluationInput `json:"input"`
		Report       Report          `json:"report"`
	}{policyHash, input, report})
	if err != nil {
		return Report{}, err
	}
	digest := sha256.Sum256(canonical)
	report.ID = "sar_" + hex.EncodeToString(digest[:16])
	return report, nil
}

func scoreExtraction(observation ExtractionObservation) float64 {
	coverage := 1.0
	if observation.CoverageStatus == "partial" {
		coverage = math.Max(0.5, 1-float64(observation.GapCount)/float64(observation.BlockCount+observation.GapCount))
	}
	characters := math.Max(0, 1-observation.InvalidCharacterRatio*5)
	uniqueBlocks := math.Max(0, 1-observation.RepeatedBlockRatio)
	return rounded(0.60*coverage + 0.20*characters + 0.20*uniqueBlocks)
}

func scoreSearchIdentity(profile Profile, searches []SearchObservation) (float64, bool, bool, bool, []string) {
	canonicalSourceURLs := make(map[string]struct{}, 2)
	for _, raw := range []string{profile.OriginalURL, profile.FinalURL} {
		if identity, err := source.CanonicalURLIdentity(raw); err == nil {
			canonicalSourceURLs[identity] = struct{}{}
		}
	}
	sourceDomain := registrableDomain(profile.FinalURL)
	if sourceDomain == "" {
		sourceDomain = registrableDomain(profile.OriginalURL)
	}
	exactURL := false
	exactIdentifier := false
	titleMatch := false
	externalDomains := make(map[string]struct{})
	for _, search := range searches {
		for _, result := range search.Results {
			canonicalResult, canonicalErr := source.CanonicalURLIdentity(result.URL)
			_, sameURL := canonicalSourceURLs[canonicalResult]
			if canonicalErr == nil && sameURL {
				exactURL = true
			}
			identifierMatch := resultMatchesStableIdentifier(result, profile.StableIdentifiers)
			if identifierMatch {
				exactIdentifier = true
			}
			matchesTitle := normalizedIdentityText(result.Title) != "" && normalizedIdentityText(result.Title) == normalizedIdentityText(profile.Title)
			if matchesTitle {
				titleMatch = true
			}
			if matchesTitle || identifierMatch {
				domain := registrableDomain(result.URL)
				if domain != "" && domain != sourceDomain {
					externalDomains[domain] = struct{}{}
				}
			}
		}
	}
	observed := exactURL || exactIdentifier || titleMatch
	score := 0.0
	switch {
	case exactURL || exactIdentifier:
		score = 1
	case titleMatch:
		score = 0.60
	}
	domains := make([]string, 0, len(externalDomains))
	for domain := range externalDomains {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return score, observed, exactURL, exactIdentifier, domains
}

func resultMatchesStableIdentifier(result SearchResult, identifiers []StableIdentifier) bool {
	haystack := strings.ToLower(strings.Join([]string{result.Title, result.URL, result.Description}, "\n"))
	for _, identifier := range identifiers {
		value := strings.ToLower(strings.TrimSpace(identifier.Value))
		if len(value) >= 6 && strings.Contains(haystack, value) {
			return true
		}
	}
	return false
}

func normalizedIdentityText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, strings.TrimSpace(value))
}

func registrableDomain(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return domain
}

func floatPointer(value float64) *float64 {
	value = rounded(value)
	return &value
}

func rounded(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}
