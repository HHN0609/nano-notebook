package sourceadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"
)

func TestEvaluatePrivateUploadIsNotApplicable(t *testing.T) {
	report, err := Evaluate(DefaultPolicy(), EvaluationInput{
		Profile:    Profile{InputKind: "file", ContentSHA256: sha256Fixture("private")},
		Extraction: completeExtraction(),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Status != StatusNotApplicable || report.Score != nil {
		t.Fatalf("report=%+v want not_applicable without score", report)
	}
	assertReason(t, report.Reasons, ReasonExternalVerificationNotApplicable)
}

func TestEvaluatePublicSourcePassesOnExactURLAndCompleteExtraction(t *testing.T) {
	report, err := Evaluate(DefaultPolicy(), EvaluationInput{
		Profile: Profile{
			InputKind:     "url",
			Title:         "Nano Source Admission Design",
			FinalURL:      "https://example.com/reports/admission?utm_source=newsletter",
			ContentSHA256: sha256Fixture("public"),
		},
		Extraction: completeExtraction(),
		Searches: []SearchObservation{{
			Query: `"Nano Source Admission Design"`,
			Results: []SearchResult{{
				Title: "Nano Source Admission Design",
				URL:   "https://example.com/reports/admission",
				Rank:  1,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Status != StatusPassed || !report.ExactIdentityMatch {
		t.Fatalf("report=%+v want passed exact identity", report)
	}
	if math.Abs(report.SignalCoverage-0.70) > 0.000001 || report.Score == nil || math.Abs(*report.Score-1) > 0.000001 {
		t.Fatalf("coverage=%v score=%v want 0.70/1", report.SignalCoverage, report.Score)
	}
	assertReason(t, report.Reasons, ReasonExactURLMatch)
}

func TestEvaluateSearchUnavailableRequiresReviewWithoutLoweringScore(t *testing.T) {
	report, err := Evaluate(DefaultPolicy(), EvaluationInput{
		Profile: Profile{
			InputKind:     "url",
			Title:         "A Niche Public Working Paper",
			FinalURL:      "https://research.example/paper",
			ContentSHA256: sha256Fixture("niche"),
		},
		Extraction:  completeExtraction(),
		SearchError: ReasonExternalVerificationUnavailable,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Status != StatusReviewRequired || report.SignalCoverage != 0.30 || report.Score == nil || *report.Score != 1 {
		t.Fatalf("report=%+v want review with extraction-only score", report)
	}
	assertReason(t, report.Reasons, ReasonExternalVerificationUnavailable)
	assertReason(t, report.Reasons, ReasonSignalCoverageInsufficient)
}

func TestEvaluateTitleSimilarityCannotSatisfyExactIdentity(t *testing.T) {
	report, err := Evaluate(DefaultPolicy(), EvaluationInput{
		Profile: Profile{
			InputKind:     "url",
			Title:         "Nano Source Admission Design",
			FinalURL:      "https://publisher.example/report",
			ContentSHA256: sha256Fixture("title-only"),
		},
		Extraction: completeExtraction(),
		Searches: []SearchObservation{{
			Query: `"Nano Source Admission Design"`,
			Results: []SearchResult{{
				Title: "Nano Source Admission Design",
				URL:   "https://aggregator.example/nano-admission",
				Rank:  1,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Status != StatusReviewRequired || report.ExactIdentityMatch {
		t.Fatalf("report=%+v want review without exact identity", report)
	}
	assertReason(t, report.Reasons, ReasonExactIdentityRequired)
}

func completeExtraction() ExtractionObservation {
	return ExtractionObservation{CoverageStatus: "complete", TotalRunes: 1200, BlockCount: 8}
}

func sha256Fixture(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:])
}

func assertReason(t *testing.T, reasons []ReasonCode, want ReasonCode) {
	t.Helper()
	for _, reason := range reasons {
		if reason == want {
			return
		}
	}
	t.Fatalf("reasons=%v missing %q", reasons, want)
}
