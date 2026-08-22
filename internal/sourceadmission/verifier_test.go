package sourceadmission

import (
	"context"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type recordingSearchProvider struct {
	requests []websearch.Request
	results  []websearch.Candidate
	err      error
}

func (provider *recordingSearchProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	provider.requests = append(provider.requests, request)
	return append([]websearch.Candidate(nil), provider.results...), provider.err
}

func TestVerifierNeverSearchesPrivateUpload(t *testing.T) {
	provider := &recordingSearchProvider{err: errors.New("must not be called")}
	verifier, err := NewVerifier(provider, DefaultVerifierConfig("test-search"))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	artifact := normalizedTextFixture(t, "src_private", "Private notebook material")

	assessment, err := verifier.Verify(context.Background(), Profile{
		InputKind: "file", Title: "private.txt", ContentSHA256: sha256Fixture("private"), ArtifactSHA256: artifact.SHA256,
	}, artifact)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(provider.requests) != 0 || assessment.Report.Status != StatusNotApplicable || assessment.ProviderAttempts != 0 {
		t.Fatalf("requests=%v assessment=%+v", provider.requests, assessment)
	}
}

func TestVerifierExecutesAtMostThreeDeterministicQueries(t *testing.T) {
	provider := &recordingSearchProvider{results: []websearch.Candidate{{
		Title: "A Deterministic Source Admission System", URL: "https://example.com/report", Rank: 1,
	}}}
	verifier, err := NewVerifier(provider, DefaultVerifierConfig("test-search"))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	artifact := normalizedTextFixture(t, "src_public", "DOI 10.1234/nano.2026.7\n\nPrimary content")

	assessment, err := verifier.Verify(context.Background(), Profile{
		InputKind: "url", Title: "A Deterministic Source Admission System", Publisher: "Nano Research Lab",
		FinalURL: "https://example.com/report", ContentSHA256: sha256Fixture("public"), ArtifactSHA256: artifact.SHA256,
	}, artifact)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(provider.requests) != 3 || assessment.ProviderAttempts != 3 || len(assessment.Input.Searches) != 3 {
		t.Fatalf("requests=%v assessment=%+v", provider.requests, assessment)
	}
	for _, request := range provider.requests {
		if request.Count != DefaultPolicy().ResultsPerQuery {
			t.Fatalf("request=%+v has wrong result bound", request)
		}
	}
	if assessment.Report.Status != StatusPassed {
		t.Fatalf("report=%+v want passed", assessment.Report)
	}
}

func TestVerifierDegradesRetryableProviderFailureToReview(t *testing.T) {
	provider := &recordingSearchProvider{err: websearch.ErrRateLimited}
	config := DefaultVerifierConfig("test-search")
	config.MaxAttemptsPerQuery = 2
	verifier, err := NewVerifier(provider, config)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	artifact := normalizedTextFixture(t, "src_niche", "Niche public working paper")

	assessment, err := verifier.Verify(context.Background(), Profile{
		InputKind: "url", Title: "Niche Public Working Paper", FinalURL: "https://example.com/niche",
		ContentSHA256: sha256Fixture("niche"), ArtifactSHA256: artifact.SHA256,
	}, artifact)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(provider.requests) != 2 || assessment.ProviderAttempts != 2 || assessment.Report.Status != StatusReviewRequired {
		t.Fatalf("requests=%v assessment=%+v", provider.requests, assessment)
	}
	assertReason(t, assessment.Report.Reasons, ReasonExternalVerificationUnavailable)
}

func TestVerifierPropagatesCancellationWithoutPublishingAssessment(t *testing.T) {
	provider := &recordingSearchProvider{err: context.Canceled}
	verifier, err := NewVerifier(provider, DefaultVerifierConfig("test-search"))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	artifact := normalizedTextFixture(t, "src_cancelled", "Cancellation source content")

	_, err = verifier.Verify(context.Background(), Profile{
		InputKind: "url", Title: "Cancellation Source Content", FinalURL: "https://example.com/cancelled",
		ContentSHA256: sha256Fixture("cancelled"), ArtifactSHA256: artifact.SHA256,
	}, artifact)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify error=%v want context.Canceled", err)
	}
}

func normalizedTextFixture(t *testing.T, sourceID, text string) normalize.Artifact {
	t.Helper()
	artifact, err := normalize.Text(normalize.Input{
		SourceID: sourceID, ExtractionConfigID: "extract-v1", Format: "txt", Payload: []byte(text),
	})
	if err != nil {
		t.Fatalf("normalize.Text: %v", err)
	}
	return artifact
}
