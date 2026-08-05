package agent

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

func TestRetrievalSearchRequestUsesRequestTimeOverrides(t *testing.T) {
	request := retrievalSearchRequest(
		"what is a galaxy",
		retrieval.Scope{NotebookID: "nb-1", SourceIDs: []string{"src-1"}, RevisionIDs: []string{"evr-1"}},
		20, 40, 10, 60, 0.42,
	)
	if request.Query != "what is a galaxy" || request.Scope.NotebookID != "nb-1" {
		t.Fatalf("query/scope = %+v", request)
	}
	if request.DenseLimit != 20 || request.SparseLimit != 40 || request.RerankLimit != 10 || request.RRFK != 60 ||
		request.RerankRelevanceThreshold != 0.42 {
		t.Fatalf("overrides = %+v", request)
	}
}
