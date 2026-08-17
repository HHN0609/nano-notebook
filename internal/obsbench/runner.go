package obsbench

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
)

// EnvelopeSink is the bounded producer surface exercised by a benchmark stage.
type EnvelopeSink interface {
	Offer(context.Context, agentbatch.Envelope) error
	ForceFlush(context.Context) error
}

// WaitUntilFunc waits for one precomputed open-loop arrival target and returns its actual dispatch time.
type WaitUntilFunc func(context.Context, time.Time) (time.Time, error)

// RunnerConfig fixes one rate-level execution.
type RunnerConfig struct {
	Workload               Workload
	Seed                   string
	EventEpoch             time.Time
	Schedule               ArrivalSchedule
	StartAt                time.Time
	MaximumArrivalLateness time.Duration
	WaitUntil              WaitUntilFunc
	Sink                   EnvelopeSink
}

// RunLevelResult reports generated demand before storage reconciliation.
type RunLevelResult struct {
	RootAgentRuns  uint64
	TotalAgentRuns uint64
	Records        uint64
	LateArrivals   uint64
}

// RunLevel dispatches a fixed open-loop schedule into one bounded producer.
func RunLevel(ctx context.Context, config RunnerConfig) (RunLevelResult, error) {
	if config.Workload.CycleSize() == 0 || config.Schedule.Count() == 0 || config.StartAt.IsZero() ||
		config.EventEpoch.IsZero() || config.MaximumArrivalLateness < 0 || config.Sink == nil {
		return RunLevelResult{}, errors.New("observability benchmark runner configuration is incomplete")
	}
	if config.WaitUntil == nil {
		config.WaitUntil = waitUntil
	}
	result := RunLevelResult{}
	for ordinal := uint64(0); ordinal < config.Schedule.Count(); ordinal++ {
		target := config.StartAt.Add(config.Schedule.Offset(ordinal))
		actual, err := config.WaitUntil(ctx, target)
		if err != nil {
			return result, fmt.Errorf("wait for root Run %d: %w", ordinal, err)
		}
		if actual.Sub(target) > config.MaximumArrivalLateness {
			result.LateArrivals++
		}
		fixture, err := config.Workload.BuildRootFixture(config.Seed, ordinal, config.EventEpoch)
		if err != nil {
			return result, fmt.Errorf("build root Run %d: %w", ordinal, err)
		}
		for recordIndex, envelope := range fixture.Envelopes {
			if err := config.Sink.Offer(ctx, envelope); err != nil {
				return result, fmt.Errorf("offer root Run %d record %d: %w", ordinal, recordIndex, err)
			}
		}
		result.RootAgentRuns++
		result.TotalAgentRuns += uint64(fixture.TotalAgentRuns)
		result.Records += uint64(len(fixture.Envelopes))
	}
	if err := config.Sink.ForceFlush(ctx); err != nil {
		return result, fmt.Errorf("flush observability benchmark producer: %w", err)
	}
	return result, nil
}

func waitUntil(ctx context.Context, target time.Time) (time.Time, error) {
	delay := time.Until(target)
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		}
	}
	return time.Now().UTC(), nil
}
