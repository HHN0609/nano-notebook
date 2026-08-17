package obsbench_test

import (
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestArrivalScheduleIsIndependentOfCompletionLatency(t *testing.T) {
	schedule, err := obsbench.NewArrivalSchedule(2.5, 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond, 1600 * time.Millisecond}
	for index := range want {
		if got := schedule.Offset(uint64(index)); got != want[index] {
			t.Fatalf("Offset(%d)=%s, want %s", index, got, want[index])
		}
	}
	if schedule.Count() != 5 || schedule.Rate() != 2.5 {
		t.Fatalf("schedule count=%d rate=%v", schedule.Count(), schedule.Rate())
	}
}
