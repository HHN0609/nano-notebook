// Package obsbench defines versioned, deterministic observability benchmark inputs
// and results. It deliberately has no network, database, or clock dependency.
package obsbench

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Scenario is one production-shaped root Agent Run path in a benchmark workload.
type Scenario string

const (
	ScenarioDirectAnswer  Scenario = "direct_answer"
	ScenarioSingleAction  Scenario = "single_action"
	ScenarioTwoActions    Scenario = "two_actions"
	ScenarioDelegation    Scenario = "delegation"
	ScenarioRetryRecovery Scenario = "retry_recovery"
)

type weightedScenario struct {
	scenario Scenario
	count    int
}

// Workload is an immutable versioned scenario cycle.
type Workload struct {
	Version string
	cycle   []Scenario
}

// RootIdentity correlates one synthetic product demand unit across every stage.
type RootIdentity struct {
	Ordinal    uint64
	Scenario   Scenario
	TraceID    string
	RunID      string
	ChatID     string
	NotebookID string
}

// ReferenceWorkloadV1 returns the accepted one-hundred-root reference mix.
func ReferenceWorkloadV1() Workload {
	weights := []weightedScenario{
		{scenario: ScenarioDirectAnswer, count: 50},
		{scenario: ScenarioSingleAction, count: 30},
		{scenario: ScenarioTwoActions, count: 10},
		{scenario: ScenarioDelegation, count: 5},
		{scenario: ScenarioRetryRecovery, count: 5},
	}
	cycle := make([]Scenario, 0, 100)
	for _, weight := range weights {
		for range weight.count {
			cycle = append(cycle, weight.scenario)
		}
	}
	return Workload{Version: "agent-run-reference-v1", cycle: cycle}
}

// CycleSize reports the number of root Runs before the scenario sequence repeats.
func (w Workload) CycleSize() int {
	return len(w.cycle)
}

// ScenarioAt returns the deterministic scenario at a zero-based root Run ordinal.
func (w Workload) ScenarioAt(ordinal uint64) (Scenario, error) {
	if len(w.cycle) == 0 {
		return "", errors.New("observability benchmark workload has no scenarios")
	}
	return w.cycle[ordinal%uint64(len(w.cycle))], nil
}

// RootIdentity returns stable opaque identities for one workload ordinal.
func (w Workload) RootIdentity(seed string, ordinal uint64) (RootIdentity, error) {
	if strings.TrimSpace(seed) == "" {
		return RootIdentity{}, errors.New("observability benchmark identity seed is required")
	}
	scenario, err := w.ScenarioAt(ordinal)
	if err != nil {
		return RootIdentity{}, err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", w.Version, seed, ordinal)))
	opaque := hex.EncodeToString(digest[:16])
	notebookDigest := sha256.Sum256([]byte(w.Version + "\x00" + seed + "\x00notebook"))
	return RootIdentity{
		Ordinal: ordinal, Scenario: scenario,
		TraceID: "trace-" + opaque, RunID: "run-" + opaque, ChatID: "chat-" + opaque,
		NotebookID: "notebook-" + hex.EncodeToString(notebookDigest[:8]),
	}, nil
}
