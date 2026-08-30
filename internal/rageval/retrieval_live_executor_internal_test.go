package rageval

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
)

func TestRRFOnlyOverrideMapsToAgentWithoutReranker(t *testing.T) {
	got := retrievalAgentOverrides(RetrievalSearchOverride{
		DenseCandidates: 10, SparseCandidates: 20, RRFK: 30,
		FusedCandidates: 20, SkipRerank: true,
	})
	want := agent.RetrievalSearchOverrides{
		DenseCandidates: 10, SparseCandidates: 20, RRFK: 30,
		RerankCandidates: 20, SkipRerank: true,
	}
	if got != want {
		t.Fatalf("agent overrides = %+v, want %+v", got, want)
	}
}
