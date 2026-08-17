package obsbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ScenarioCount freezes one scenario's cardinality in a workload cycle.
type ScenarioCount struct {
	Scenario Scenario `json:"scenario"`
	Count    int      `json:"count"`
}

// Manifest is the stage-independent logical input identity for an A/B/C run.
type Manifest struct {
	SchemaVersion   int             `json:"schema_version"`
	DatasetID       string          `json:"dataset_id"`
	RootCount       uint64          `json:"root_count"`
	EventEpoch      time.Time       `json:"event_epoch"`
	WorkloadVersion string          `json:"workload_version"`
	Cycle           []ScenarioCount `json:"cycle"`
}

// NewManifest freezes the logical dataset shared by every architecture stage.
func NewManifest(workload Workload, datasetID string, rootCount uint64, eventEpoch time.Time) (Manifest, error) {
	if strings.TrimSpace(datasetID) == "" || rootCount == 0 || eventEpoch.IsZero() || workload.Version == "" || workload.CycleSize() == 0 {
		return Manifest{}, errors.New("observability benchmark manifest is incomplete")
	}
	order := []Scenario{
		ScenarioDirectAnswer,
		ScenarioSingleAction,
		ScenarioTwoActions,
		ScenarioDelegation,
		ScenarioRetryRecovery,
	}
	counts := make(map[Scenario]int, len(order))
	for index := 0; index < workload.CycleSize(); index++ {
		scenario, err := workload.ScenarioAt(uint64(index))
		if err != nil {
			return Manifest{}, err
		}
		counts[scenario]++
	}
	cycle := make([]ScenarioCount, 0, len(order))
	for _, scenario := range order {
		if counts[scenario] > 0 {
			cycle = append(cycle, ScenarioCount{Scenario: scenario, Count: counts[scenario]})
		}
	}
	return Manifest{
		SchemaVersion: 1, DatasetID: strings.TrimSpace(datasetID), RootCount: rootCount,
		EventEpoch: eventEpoch.UTC(), WorkloadVersion: workload.Version, Cycle: cycle,
	}, nil
}

// CanonicalJSON returns stable bytes and their lowercase SHA-256 digest.
func (m Manifest) CanonicalJSON() ([]byte, string, error) {
	if m.SchemaVersion != 1 || strings.TrimSpace(m.DatasetID) == "" || m.RootCount == 0 || m.EventEpoch.IsZero() ||
		strings.TrimSpace(m.WorkloadVersion) == "" || len(m.Cycle) == 0 {
		return nil, "", errors.New("observability benchmark manifest is invalid")
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}
