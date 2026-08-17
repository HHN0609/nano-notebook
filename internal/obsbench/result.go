package obsbench

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Stage identifies one controlled architecture in the A/B/C comparison.
type Stage string

const (
	StageDirectPostgres  Stage = "A_direct_postgres"
	StageKafkaPostgres   Stage = "B_kafka_postgres"
	StageKafkaClickHouse Stage = "C_kafka_clickhouse"
)

// Latency contains the reportable latency percentiles in milliseconds.
type Latency struct {
	P50Milliseconds float64 `json:"p50_ms"`
	P95Milliseconds float64 `json:"p95_ms"`
	P99Milliseconds float64 `json:"p99_ms"`
}

// LevelResult is the evidence summary for one offered root-Run rate.
type LevelResult struct {
	OfferedRootRunsPerSecond  float64 `json:"offered_root_runs_per_second"`
	AchievedRootRunsPerSecond float64 `json:"achieved_root_runs_per_second"`
	RecordsPerSecond          float64 `json:"records_per_second"`
	MiBPerSecond              float64 `json:"mib_per_second"`
	AcceptanceLatency         Latency `json:"acceptance_latency"`
	ProductQueryLatency       Latency `json:"product_query_latency"`
	ScheduledArrivals         uint64  `json:"scheduled_arrivals"`
	LateArrivals              uint64  `json:"late_arrivals"`
	ValidRecords              uint64  `json:"valid_records"`
	LostRecords               uint64  `json:"lost_records"`
}

// Result is the versioned, machine-readable summary of one benchmark level.
type Result struct {
	SchemaVersion   int         `json:"schema_version"`
	Stage           Stage       `json:"stage"`
	WorkloadVersion string      `json:"workload_version"`
	ManifestSHA256  string      `json:"manifest_sha256"`
	StartedAt       time.Time   `json:"started_at"`
	Level           LevelResult `json:"level"`
}

// Validate rejects a result that cannot support the accepted headline claims.
func (r Result) Validate() error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("unsupported observability benchmark result schema %d", r.SchemaVersion)
	}
	if r.Stage != StageDirectPostgres && r.Stage != StageKafkaPostgres && r.Stage != StageKafkaClickHouse {
		return fmt.Errorf("unknown observability benchmark stage %q", r.Stage)
	}
	if strings.TrimSpace(r.WorkloadVersion) == "" || r.StartedAt.IsZero() {
		return errors.New("observability benchmark result identity is incomplete")
	}
	digest, err := hex.DecodeString(r.ManifestSHA256)
	if err != nil || len(digest) != 32 {
		return errors.New("observability benchmark manifest SHA-256 is invalid")
	}
	if !finitePositive(r.Level.OfferedRootRunsPerSecond) || !finiteNonNegative(r.Level.AchievedRootRunsPerSecond) ||
		!finiteNonNegative(r.Level.RecordsPerSecond) || !finiteNonNegative(r.Level.MiBPerSecond) {
		return errors.New("observability benchmark rates must be finite and non-negative with a positive offered rate")
	}
	if r.Level.AchievedRootRunsPerSecond > r.Level.OfferedRootRunsPerSecond*1.001 {
		return errors.New("achieved root Run rate exceeds the offered rate")
	}
	if r.Level.ScheduledArrivals == 0 || r.Level.LateArrivals > r.Level.ScheduledArrivals || r.Level.ValidRecords == 0 {
		return errors.New("observability benchmark counters are inconsistent")
	}
	if r.Level.LostRecords != 0 {
		return fmt.Errorf("observability benchmark has %d lost records", r.Level.LostRecords)
	}
	if err := validateLatency("acceptance", r.Level.AcceptanceLatency); err != nil {
		return err
	}
	return validateLatency("product query", r.Level.ProductQueryLatency)
}

func validateLatency(name string, latency Latency) error {
	if !finiteNonNegative(latency.P50Milliseconds) || !finiteNonNegative(latency.P95Milliseconds) || !finiteNonNegative(latency.P99Milliseconds) ||
		latency.P50Milliseconds > latency.P95Milliseconds || latency.P95Milliseconds > latency.P99Milliseconds {
		return fmt.Errorf("%s latency must satisfy finite p50 <= p95 <= p99", name)
	}
	return nil
}

func finitePositive(value float64) bool {
	return value > 0 && finiteNonNegative(value)
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
