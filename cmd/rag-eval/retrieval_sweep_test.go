package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/rageval"
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

func TestRunSweepWritesCSVAndJSONReports(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	gridPath := filepath.Join(dir, "grid.json")
	writeTestJSON(t, suitePath, rageval.RetrievalSuite{
		SchemaVersion: 1, ID: "retrieval-test-v1",
		Cases: []rageval.RetrievalCase{{
			ID: "case-1", Question: "find unit", Language: "en", DatasetID: "msmarco-passage",
			SourceRef: "msmarco:passage:1", ExpectedEvidenceSets: [][]string{{"unit-one"}},
		}},
	})
	writeTestJSON(t, gridPath, rageval.RetrievalGrid{
		DenseCandidates: []int{20}, SparseCandidates: []int{20}, RRFK: []int{60}, RerankCandidates: []int{10},
	})
	prefix := filepath.Join(dir, "sweep", "retrieval")
	var output bytes.Buffer
	if err := runSweepWithExecutor([]string{
		"-suite", suitePath, "-grid", gridPath, "-out-prefix", prefix,
	}, &output, &sweepExecutorStub{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), prefix+".json") || !strings.Contains(output.String(), prefix+".csv") || !strings.Contains(output.String(), prefix+".md") {
		t.Fatalf("output = %q", output.String())
	}
	var report rageval.RetrievalReport
	decodeTestJSON(t, prefix+".json", &report)
	if report.SuiteID != "retrieval-test-v1" || len(report.GridResults) != 1 || len(report.Cases) != 1 {
		t.Fatalf("report = %+v", report)
	}
	csvPayload, err := os.ReadFile(prefix + ".csv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(csvPayload), "dense_candidates,sparse_candidates,rrf_k,rerank_candidates") {
		t.Fatalf("csv = %q", csvPayload)
	}
	markdownPayload, err := os.ReadFile(prefix + ".md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdownPayload), "# Retrieval sweep") || !strings.Contains(string(markdownPayload), "Recall@20") {
		t.Fatalf("markdown = %q", markdownPayload)
	}
}

func TestRunSweepRequiresGridAndOutPrefix(t *testing.T) {
	if err := runSweepWithExecutor(nil, &bytes.Buffer{}, &sweepExecutorStub{}); err == nil {
		t.Fatal("runSweep accepted missing grid and out-prefix")
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

type sweepExecutorStub struct{}

func (*sweepExecutorStub) Search(_ context.Context, _ rageval.RetrievalCase, _ rageval.RetrievalSearchOverride) (retrieval.SearchResult, error) {
	return retrieval.SearchResult{}, nil
}
