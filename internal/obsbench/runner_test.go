package obsbench_test

import (
	"context"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestRunLevelDispatchesMaterializedCycleAndFlushes(t *testing.T) {
	schedule, err := obsbench.NewArrivalSchedule(1_000, 100)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1_700_000_100, 0).UTC()
	waiter := &recordingWaiter{}
	sink := &recordingEnvelopeSink{}
	result, err := obsbench.RunLevel(context.Background(), obsbench.RunnerConfig{
		Workload: obsbench.ReferenceWorkloadV1(), Seed: "stage-a-smoke", EventEpoch: time.Unix(1_700_000_000, 0).UTC(),
		Schedule: schedule, StartAt: start, MaximumArrivalLateness: 5 * time.Millisecond,
		WaitUntil: waiter.WaitUntil, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RootAgentRuns != 100 || result.TotalAgentRuns != 105 || result.Records != 1_620 || result.LateArrivals != 0 {
		t.Fatalf("run result=%#v", result)
	}
	if !sink.flushed || len(sink.envelopes) != 1_620 || len(waiter.targets) != 100 {
		t.Fatalf("sink records=%d flushed=%t waits=%d", len(sink.envelopes), sink.flushed, len(waiter.targets))
	}
	if waiter.targets[0] != start || waiter.targets[99] != start.Add(99*time.Millisecond) {
		t.Fatalf("targets first=%s last=%s", waiter.targets[0], waiter.targets[99])
	}
}

type recordingWaiter struct {
	targets []time.Time
}

func (w *recordingWaiter) WaitUntil(_ context.Context, target time.Time) (time.Time, error) {
	w.targets = append(w.targets, target)
	return target, nil
}

type recordingEnvelopeSink struct {
	envelopes []agentbatch.Envelope
	flushed   bool
}

func (s *recordingEnvelopeSink) Offer(_ context.Context, envelope agentbatch.Envelope) error {
	s.envelopes = append(s.envelopes, envelope)
	return nil
}

func (s *recordingEnvelopeSink) ForceFlush(context.Context) error {
	s.flushed = true
	return nil
}
