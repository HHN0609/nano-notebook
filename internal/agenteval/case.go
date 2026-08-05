// Package agenteval turns real production failures where the Agent's LLM
// decision was wrong (picked the wrong Action, delegation target, or
// arguments — not a code defect) into checked-in regression cases. Scope
// is limited to failures the backend already flagged (a domain_error on an
// action_result Checkpoint, or a terminal agent_runs.status='failed');
// open-ended generation-quality scoring is out of scope.
//
// A DecisionCase replays the exact context a historical decision saw
// (reconstructed live from agent_run_checkpoints via the same production
// code path Controller.Execute uses, not the lossy encrypted Replay
// system) against the CURRENT model, and compares the resulting Action to
// a human-labeled expected one. It performs no writes — it is an
// auditor/replayer, not a Run driver.
//
// v1 only judges single-Action decisions and only Leader chat Runs
// (runtime_kind='legacy_role'); see agent.ErrStudioReplayUnsupported.
package agenteval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ComparisonMode string

const (
	// ComparisonExactCanonicalJSON requires ExpectedActionInput and the
	// replayed Action's Input to be byte-identical after
	// agent.CanonicalJSONObject normalization (key-order independent).
	// Default when ComparisonMode is empty.
	ComparisonExactCanonicalJSON ComparisonMode = "exact_canonical_json"
	// ComparisonRequiredKeysSubset only requires the keys listed in
	// RequiredInputKeys to match; other fields in the replayed Action's
	// Input may vary. An opt-in escape hatch, not the default — the
	// default is deliberately strict so drift is not masked silently.
	ComparisonRequiredKeysSubset ComparisonMode = "required_keys_subset"
)

// DecisionCase pins one historical, already-flagged wrong Agent decision
// to a hand-labeled expected Action, so it can be replayed later against
// the current model/prompt/catalog as a regression guard.
type DecisionCase struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id"`
	DecisionNo  int    `json:"decision_no"`
	Description string `json:"description"`

	// Observed* fields document what actually happened in production —
	// for human context only, never read by the comparator.
	ObservedActionName  string          `json:"observed_action_name,omitempty"`
	ObservedActionInput json.RawMessage `json:"observed_action_input,omitempty"`
	ObservedErrorCode   string          `json:"observed_error_code,omitempty"`

	ExpectedActionName  string          `json:"expected_action_name"`
	ExpectedActionInput json.RawMessage `json:"expected_action_input,omitempty"`
	ComparisonMode      ComparisonMode  `json:"comparison_mode,omitempty"`
	// RequiredInputKeys is only consulted when ComparisonMode is
	// ComparisonRequiredKeysSubset.
	RequiredInputKeys []string `json:"required_input_keys,omitempty"`

	LabeledBy string    `json:"labeled_by"`
	LabeledAt time.Time `json:"labeled_at"`
}

func (c DecisionCase) validate() error {
	if isBlank(c.ID) || isBlank(c.RunID) || isBlank(c.ExpectedActionName) {
		return fmt.Errorf("Decision Case %q is invalid: id, run_id, and expected_action_name are required", c.ID)
	}
	if c.DecisionNo < 1 {
		return fmt.Errorf("Decision Case %q is invalid: decision_no must be >= 1", c.ID)
	}
	switch c.ComparisonMode {
	case "", ComparisonExactCanonicalJSON:
	case ComparisonRequiredKeysSubset:
		if len(c.RequiredInputKeys) == 0 {
			return fmt.Errorf("Decision Case %q is invalid: required_keys_subset mode needs required_input_keys", c.ID)
		}
	default:
		return fmt.Errorf("Decision Case %q is invalid: unknown comparison_mode %q", c.ID, c.ComparisonMode)
	}
	return nil
}

// DecisionSuite is a checked-in collection of DecisionCases, one file per
// suite under evals/agent-decisions/.
type DecisionSuite struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Cases         []DecisionCase `json:"cases"`
}

var ErrDecisionSuiteInvalid = errors.New("Decision Suite is invalid")

func (s DecisionSuite) Validate() error {
	if s.SchemaVersion != 1 || isBlank(s.ID) || len(s.Cases) == 0 {
		return ErrDecisionSuiteInvalid
	}
	seen := make(map[string]struct{}, len(s.Cases))
	for _, evalCase := range s.Cases {
		if err := evalCase.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrDecisionSuiteInvalid, err)
		}
		if _, duplicate := seen[evalCase.ID]; duplicate {
			return fmt.Errorf("%w: duplicate case id %q", ErrDecisionSuiteInvalid, evalCase.ID)
		}
		seen[evalCase.ID] = struct{}{}
	}
	return nil
}

func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}
