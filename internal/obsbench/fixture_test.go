package obsbench_test

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestBuildRootFixtureCreatesCompleteDirectAnswerTrace(t *testing.T) {
	workload := obsbench.ReferenceWorkloadV1()
	fixture, err := workload.BuildRootFixture("fixture-v1", 0, time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Identity.Scenario != obsbench.ScenarioDirectAnswer || fixture.TotalAgentRuns != 1 || len(fixture.Envelopes) != 12 {
		t.Fatalf("direct fixture = scenario %q total_runs=%d records=%d", fixture.Identity.Scenario, fixture.TotalAgentRuns, len(fixture.Envelopes))
	}
	seen := make(map[string]bool, len(fixture.Envelopes))
	for index, envelope := range fixture.Envelopes {
		if envelope.Trace.TraceID != agentobs.TraceID(fixture.Identity.TraceID) || envelope.Trace.RunID != fixture.Identity.RunID ||
			envelope.Trace.WorkloadID != fixture.Identity.RunID {
			t.Fatalf("record %d descriptor=%#v identity=%#v", index, envelope.Trace, fixture.Identity)
		}
		if err := envelope.Record.Validate(); err != nil {
			t.Fatalf("record %d invalid: %v (%#v)", index, err, envelope.Record)
		}
		if seen[envelope.Record.IdentityKey] {
			t.Fatalf("duplicate identity %q", envelope.Record.IdentityKey)
		}
		seen[envelope.Record.IdentityKey] = true
	}
	if fixture.Envelopes[0].Record.Kind != agentobs.RecordSpanStarted || fixture.Envelopes[0].Record.Name != "agent.execution" ||
		fixture.Envelopes[len(fixture.Envelopes)-1].Record.Kind != agentobs.RecordSpanEnded ||
		fixture.Envelopes[len(fixture.Envelopes)-1].Record.Status != agentobs.StatusOK {
		t.Fatalf("root boundaries = first %#v last %#v", fixture.Envelopes[0].Record, fixture.Envelopes[len(fixture.Envelopes)-1].Record)
	}
}

func TestBuildRootFixtureMaterializesActionAmplification(t *testing.T) {
	workload := obsbench.ReferenceWorkloadV1()
	base := time.Unix(1_700_000_000, 0).UTC()
	for _, test := range []struct {
		name         string
		ordinal      uint64
		scenario     obsbench.Scenario
		records      int
		actionStarts int
	}{
		{name: "single", ordinal: 50, scenario: obsbench.ScenarioSingleAction, records: 18, actionStarts: 1},
		{name: "two", ordinal: 80, scenario: obsbench.ScenarioTwoActions, records: 21, actionStarts: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, err := workload.BuildRootFixture("fixture-v1", test.ordinal, base)
			if err != nil {
				t.Fatal(err)
			}
			if fixture.Identity.Scenario != test.scenario || len(fixture.Envelopes) != test.records {
				t.Fatalf("scenario=%q records=%d", fixture.Identity.Scenario, len(fixture.Envelopes))
			}
			actionStarts := 0
			for index, envelope := range fixture.Envelopes {
				if err := envelope.Record.Validate(); err != nil {
					t.Fatalf("record %d invalid: %v", index, err)
				}
				if envelope.Record.Kind == agentobs.RecordSpanStarted && envelope.Record.Name == "agent.action" {
					actionStarts++
				}
			}
			if actionStarts != test.actionStarts {
				t.Fatalf("action starts=%d, want %d", actionStarts, test.actionStarts)
			}
		})
	}
}

func TestBuildRootFixtureMaterializesDelegationAndRetryShapes(t *testing.T) {
	workload := obsbench.ReferenceWorkloadV1()
	base := time.Unix(1_700_000_000, 0).UTC()

	delegation, err := workload.BuildRootFixture("fixture-v1", 90, base)
	if err != nil {
		t.Fatal(err)
	}
	traceIDs := make(map[agentobs.TraceID]bool)
	delegationLinks := 0
	for index, envelope := range delegation.Envelopes {
		if err := envelope.Record.Validate(); err != nil {
			t.Fatalf("delegation record %d invalid: %v", index, err)
		}
		traceIDs[envelope.Trace.TraceID] = true
		if envelope.Record.Kind == agentobs.RecordLink && (envelope.Record.Name == "delegates" || envelope.Record.Name == "delegated_from") {
			delegationLinks++
		}
	}
	if delegation.Identity.Scenario != obsbench.ScenarioDelegation || delegation.TotalAgentRuns != 2 || len(traceIDs) != 2 || delegationLinks != 2 {
		t.Fatalf("delegation fixture scenario=%q total_runs=%d traces=%d links=%d", delegation.Identity.Scenario, delegation.TotalAgentRuns, len(traceIDs), delegationLinks)
	}

	retry, err := workload.BuildRootFixture("fixture-v1", 95, base)
	if err != nil {
		t.Fatal(err)
	}
	attemptStarts, recoveryLinks := 0, 0
	for index, envelope := range retry.Envelopes {
		if err := envelope.Record.Validate(); err != nil {
			t.Fatalf("retry record %d invalid: %v", index, err)
		}
		if envelope.Record.Kind == agentobs.RecordSpanStarted && envelope.Record.Name == "nano.job.attempt" {
			attemptStarts++
		}
		if envelope.Record.Kind == agentobs.RecordLink && envelope.Record.Name == "continues" {
			recoveryLinks++
		}
	}
	if retry.Identity.Scenario != obsbench.ScenarioRetryRecovery || retry.TotalAgentRuns != 1 || attemptStarts != 2 || recoveryLinks != 1 {
		t.Fatalf("retry fixture scenario=%q total_runs=%d attempts=%d links=%d", retry.Identity.Scenario, retry.TotalAgentRuns, attemptStarts, recoveryLinks)
	}
}

func TestMaterializedCycleStatsExposeRunAndRecordAmplification(t *testing.T) {
	stats, err := obsbench.ReferenceWorkloadV1().MaterializedCycleStats("fixture-v1", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stats.RootAgentRuns != 100 || stats.TotalAgentRuns != 105 || stats.Records != 1_620 {
		t.Fatalf("cycle stats=%#v", stats)
	}
	if stats.RecordsPerRoot != 16.2 || stats.ChildRunsPerRoot != 0.05 {
		t.Fatalf("cycle amplification=%#v", stats)
	}
}
