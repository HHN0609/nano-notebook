package obsbench

import (
	"errors"
	"math"
	"time"
)

// ArrivalSchedule is an immutable open-loop root-Run schedule.
type ArrivalSchedule struct {
	rate     float64
	count    uint64
	interval time.Duration
}

// NewArrivalSchedule fixes offered demand independently of completion latency.
func NewArrivalSchedule(rootRunsPerSecond float64, count uint64) (ArrivalSchedule, error) {
	if rootRunsPerSecond <= 0 || math.IsNaN(rootRunsPerSecond) || math.IsInf(rootRunsPerSecond, 0) || count == 0 {
		return ArrivalSchedule{}, errors.New("observability benchmark arrival schedule is invalid")
	}
	interval := time.Duration(float64(time.Second) / rootRunsPerSecond)
	if interval < time.Nanosecond {
		return ArrivalSchedule{}, errors.New("observability benchmark arrival rate exceeds nanosecond resolution")
	}
	return ArrivalSchedule{rate: rootRunsPerSecond, count: count, interval: interval}, nil
}

func (s ArrivalSchedule) Offset(ordinal uint64) time.Duration {
	return time.Duration(ordinal) * s.interval
}

func (s ArrivalSchedule) Count() uint64 { return s.count }

func (s ArrivalSchedule) Rate() float64 { return s.rate }
