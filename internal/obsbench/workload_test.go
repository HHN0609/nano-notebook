package obsbench_test

import (
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestReferenceWorkloadV1HasStableHundredRootCycle(t *testing.T) {
	workload := obsbench.ReferenceWorkloadV1()
	if workload.Version != "agent-run-reference-v1" {
		t.Fatalf("Version=%q", workload.Version)
	}
	if workload.CycleSize() != 100 {
		t.Fatalf("CycleSize=%d, want 100", workload.CycleSize())
	}

	want := map[obsbench.Scenario]int{
		obsbench.ScenarioDirectAnswer:  50,
		obsbench.ScenarioSingleAction:  30,
		obsbench.ScenarioTwoActions:    10,
		obsbench.ScenarioDelegation:    5,
		obsbench.ScenarioRetryRecovery: 5,
	}
	got := make(map[obsbench.Scenario]int, len(want))
	for index := 0; index < workload.CycleSize(); index++ {
		scenario, err := workload.ScenarioAt(uint64(index))
		if err != nil {
			t.Fatalf("ScenarioAt(%d): %v", index, err)
		}
		got[scenario]++
	}
	for scenario, count := range want {
		if got[scenario] != count {
			t.Errorf("scenario %q count=%d, want %d", scenario, got[scenario], count)
		}
	}

	for index := uint64(0); index < 100; index++ {
		first, err := workload.ScenarioAt(index)
		if err != nil {
			t.Fatal(err)
		}
		repeated, err := workload.ScenarioAt(index + 100)
		if err != nil {
			t.Fatal(err)
		}
		if first != repeated {
			t.Fatalf("cycle changed at %d: %q != %q", index, first, repeated)
		}
	}
}

func TestReferenceRootIdentityIsDeterministicAndScenarioBound(t *testing.T) {
	workload := obsbench.ReferenceWorkloadV1()
	first, err := workload.RootIdentity("stage-a-primary", 94)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := workload.RootIdentity("stage-a-primary", 94)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated {
		t.Fatalf("identity changed: %#v != %#v", first, repeated)
	}
	if first.Scenario != obsbench.ScenarioDelegation || first.Ordinal != 94 {
		t.Fatalf("identity scenario=%q ordinal=%d", first.Scenario, first.Ordinal)
	}
	if first.TraceID == "" || first.RunID == "" || first.ChatID == "" || first.NotebookID == "" {
		t.Fatalf("identity is incomplete: %#v", first)
	}
	if strings.Contains(first.TraceID, "stage-a-primary") || strings.Contains(first.RunID, "stage-a-primary") {
		t.Fatalf("identity leaked raw seed: %#v", first)
	}

	next, err := workload.RootIdentity("stage-a-primary", 95)
	if err != nil {
		t.Fatal(err)
	}
	if next.TraceID == first.TraceID || next.RunID == first.RunID || next.ChatID == first.ChatID {
		t.Fatalf("distinct roots shared identity: first=%#v next=%#v", first, next)
	}
	if next.Scenario != obsbench.ScenarioRetryRecovery {
		t.Fatalf("next scenario=%q", next.Scenario)
	}
}
