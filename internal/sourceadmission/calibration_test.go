package sourceadmission

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestFrozenCalibrationSuitePinsDefaultPolicyDecisions(t *testing.T) {
	payload, err := os.ReadFile("../../evals/source-admission/v1.json")
	if err != nil {
		t.Fatalf("read frozen calibration suite: %v", err)
	}
	var suite struct {
		SchemaVersion int    `json:"schema_version"`
		PolicySHA256  string `json:"policy_sha256"`
		Cases         []struct {
			ID       string          `json:"id"`
			Input    EvaluationInput `json:"input"`
			Expected struct {
				Status             Status   `json:"status"`
				Score              *float64 `json:"score"`
				SignalCoverage     float64  `json:"signal_coverage"`
				ExactIdentityMatch bool     `json:"exact_identity_match"`
			} `json:"expected"`
		} `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		t.Fatalf("decode frozen calibration suite: %v", err)
	}
	policy := DefaultPolicy()
	policySHA256, err := PolicySHA256(policy)
	if err != nil {
		t.Fatal(err)
	}
	if suite.SchemaVersion != 1 || suite.PolicySHA256 != policySHA256 || len(suite.Cases) < 5 {
		t.Fatalf("invalid calibration identity: version=%d policy=%q cases=%d", suite.SchemaVersion, suite.PolicySHA256, len(suite.Cases))
	}
	for _, calibrationCase := range suite.Cases {
		t.Run(calibrationCase.ID, func(t *testing.T) {
			report, err := Evaluate(policy, calibrationCase.Input)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if report.Status != calibrationCase.Expected.Status || report.ExactIdentityMatch != calibrationCase.Expected.ExactIdentityMatch ||
				math.Abs(report.SignalCoverage-calibrationCase.Expected.SignalCoverage) > 0.000001 || !sameOptionalScore(report.Score, calibrationCase.Expected.Score) {
				t.Fatalf("report=%+v expected=%+v", report, calibrationCase.Expected)
			}
		})
	}
}

func sameOptionalScore(actual, expected *float64) bool {
	if actual == nil || expected == nil {
		return actual == nil && expected == nil
	}
	return math.Abs(*actual-*expected) <= 0.000001
}
