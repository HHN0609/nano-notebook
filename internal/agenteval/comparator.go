package agenteval

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type ComparisonResult struct {
	Pass   bool
	Reason string
}

// Compare judges a replayed Action proposal against evalCase's
// hand-labeled expectation. Name must always match exactly; Input is
// judged by evalCase.ComparisonMode (default ComparisonExactCanonicalJSON).
func Compare(evalCase DecisionCase, actual models.ActionProposal) (ComparisonResult, error) {
	if actual.Name != evalCase.ExpectedActionName {
		return ComparisonResult{Reason: fmt.Sprintf("action name = %q, expected %q", actual.Name, evalCase.ExpectedActionName)}, nil
	}
	mode := evalCase.ComparisonMode
	if mode == "" {
		mode = ComparisonExactCanonicalJSON
	}
	switch mode {
	case ComparisonRequiredKeysSubset:
		return compareRequiredKeys(evalCase.RequiredInputKeys, evalCase.ExpectedActionInput, actual.Input)
	default:
		return compareExactCanonical(evalCase.ExpectedActionInput, actual.Input)
	}
}

func compareExactCanonical(expected, actual json.RawMessage) (ComparisonResult, error) {
	expectedCanonical, err := canonicalOrEmptyObject(expected)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("expected_action_input: %w", err)
	}
	actualCanonical, err := canonicalOrEmptyObject(actual)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("replayed Action input: %w", err)
	}
	if bytes.Equal(expectedCanonical, actualCanonical) {
		return ComparisonResult{Pass: true}, nil
	}
	return ComparisonResult{Reason: fmt.Sprintf("input = %s, expected %s", actualCanonical, expectedCanonical)}, nil
}

func compareRequiredKeys(requiredKeys []string, expected, actual json.RawMessage) (ComparisonResult, error) {
	expectedFields, err := decodeObjectFields(expected)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("expected_action_input: %w", err)
	}
	actualFields, err := decodeObjectFields(actual)
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("replayed Action input: %w", err)
	}
	for _, key := range requiredKeys {
		expectedValue, expectedHasKey := expectedFields[key]
		actualValue, actualHasKey := actualFields[key]
		if expectedHasKey != actualHasKey || !bytes.Equal(expectedValue, actualValue) {
			return ComparisonResult{Reason: fmt.Sprintf("required key %q = %s, expected %s", key, actualValue, expectedValue)}, nil
		}
	}
	return ComparisonResult{Pass: true}, nil
}

// canonicalOrEmptyObject treats an omitted/empty Input as the empty JSON
// object, so case authors need not write "{}" for no-argument Actions.
func canonicalOrEmptyObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return agent.CanonicalJSONObject(raw)
}

func decodeObjectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	canonical, err := canonicalOrEmptyObject(raw)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
