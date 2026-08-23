package sourceadmission

import "testing"

func TestEvaluateProducesStablePolicyAndReportIdentities(t *testing.T) {
	policy := DefaultPolicy()
	input := EvaluationInput{
		Profile: Profile{
			InputKind:      "url",
			Title:          "Nano Source Admission Design",
			FinalURL:       "https://example.com/admission",
			ContentSHA256:  sha256Fixture("content"),
			ArtifactSHA256: sha256Fixture("artifact"),
		},
		Extraction: completeExtraction(),
		Searches: []SearchObservation{{Results: []SearchResult{{
			Title: "Nano Source Admission Design", URL: "https://example.com/admission", Rank: 1,
		}}}},
	}

	first, err := Evaluate(policy, input)
	if err != nil {
		t.Fatalf("Evaluate first: %v", err)
	}
	second, err := Evaluate(policy, input)
	if err != nil {
		t.Fatalf("Evaluate second: %v", err)
	}
	if first.ID == "" || len(first.PolicySHA256) != 64 || first.PolicyID != policy.ID {
		t.Fatalf("report identity incomplete: %+v", first)
	}
	if first.ID != second.ID || first.PolicySHA256 != second.PolicySHA256 {
		t.Fatalf("identities are not deterministic: first=%+v second=%+v", first, second)
	}

	input.Profile.ArtifactSHA256 = sha256Fixture("changed-artifact")
	changed, err := Evaluate(policy, input)
	if err != nil {
		t.Fatalf("Evaluate changed: %v", err)
	}
	if changed.ID == first.ID {
		t.Fatal("report identity did not bind the Artifact hash")
	}
}

func TestPolicyHashChangesWhenThresholdChanges(t *testing.T) {
	policy := DefaultPolicy()
	first, err := PolicySHA256(policy)
	if err != nil {
		t.Fatalf("PolicySHA256 first: %v", err)
	}
	policy.MinimumScore = 0.80
	second, err := PolicySHA256(policy)
	if err != nil {
		t.Fatalf("PolicySHA256 second: %v", err)
	}
	if first == second {
		t.Fatal("policy hash did not bind the threshold")
	}
}
