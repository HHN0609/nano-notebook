package rageval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/rageval"
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

func TestRetrievalSuiteValidationRejectsBlankQuestionOrExpectedSets(t *testing.T) {
	suite := retrievalSuite()
	if err := suite.Validate(); err != nil {
		t.Fatal(err)
	}
	suite.Cases[0].Question = " "
	if err := suite.Validate(); !errors.Is(err, rageval.ErrRetrievalSuiteInvalid) {
		t.Fatalf("Validate = %v, want ErrRetrievalSuiteInvalid", err)
	}
	suite = retrievalSuite()
	suite.Cases[0].ExpectedEvidenceSets = nil
	if err := suite.Validate(); !errors.Is(err, rageval.ErrRetrievalSuiteInvalid) {
		t.Fatalf("Validate = %v, want ErrRetrievalSuiteInvalid", err)
	}
}

func TestRetrievalGridGeneratesTheConfirmed81Combinations(t *testing.T) {
	grid := retrievalGrid()
	if err := grid.Validate(); err != nil {
		t.Fatal(err)
	}
	combinations := grid.Combinations()
	if len(combinations) != 81 {
		t.Fatalf("combinations = %d, want 81", len(combinations))
	}
	seen := make(map[rageval.RetrievalSearchOverride]bool)
	for _, override := range combinations {
		if override.DenseCandidates <= 0 || override.SparseCandidates <= 0 || override.RRFK <= 0 || override.RerankCandidates <= 0 {
			t.Fatalf("invalid override = %+v", override)
		}
		if seen[override] {
			t.Fatalf("duplicate override = %+v", override)
		}
		seen[override] = true
	}
}

func TestRetrievalGridGenerates48RRFOnlyCombinationsWithFixedOutputLimit(t *testing.T) {
	grid := rageval.RetrievalGrid{
		Mode: "rrf_only", DenseCandidates: []int{5, 10, 20, 40},
		SparseCandidates: []int{5, 10, 20, 40}, RRFK: []int{30, 60, 100},
		FusedCandidates: 20,
	}
	if err := grid.Validate(); err != nil {
		t.Fatal(err)
	}
	combinations := grid.Combinations()
	if len(combinations) != 48 {
		t.Fatalf("combinations = %d, want 48", len(combinations))
	}
	for _, override := range combinations {
		if !override.SkipRerank || override.FusedCandidates != 20 || override.RerankCandidates != 0 {
			t.Fatalf("RRF-only override = %+v", override)
		}
	}
}

func TestRetrievalGridRejectsEmptyOrInvalidValues(t *testing.T) {
	grid := retrievalGrid()
	grid.DenseCandidates = nil
	if err := grid.Validate(); !errors.Is(err, rageval.ErrRetrievalGridInvalid) {
		t.Fatalf("Validate = %v, want ErrRetrievalGridInvalid", err)
	}
	grid = retrievalGrid()
	grid.RRFK = []int{0}
	if err := grid.Validate(); !errors.Is(err, rageval.ErrRetrievalGridInvalid) {
		t.Fatalf("Validate = %v, want ErrRetrievalGridInvalid", err)
	}
}

func TestRunRetrievalSweepComputesRecallMRRAndStageLatencyAverages(t *testing.T) {
	suite := retrievalSuite()
	suite.Cases = []rageval.RetrievalCase{
		{
			ID: "en-1", Question: "find unit one", Language: "en", DatasetID: "msmarco-passage",
			SourceRef:            "msmarco:passage:1",
			ExpectedEvidenceSets: [][]string{{"unit-one"}},
		},
		{
			ID: "zh-1", Question: "查找第二段", Language: "zh", DatasetID: "dureader-retrieval",
			SourceRef:            "dureader-retrieval:passage:1",
			ExpectedEvidenceSets: [][]string{{"unit-two"}},
		},
	}
	executor := &retrievalExecutorStub{
		results: map[string]retrieval.SearchResult{
			"en-1": retrievalSearchResult([]retrieval.EvidenceCandidate{
				{ID: "chunk-a", UnitRefs: []retrieval.UnitRef{{UnitID: "unit-one"}}},
				{ID: "chunk-b", UnitRefs: []retrieval.UnitRef{{UnitID: "other"}}},
			}, 100, 200, 250, 50, 300, 400),
			"zh-1": retrievalSearchResult([]retrieval.EvidenceCandidate{
				{ID: "chunk-c", UnitRefs: []retrieval.UnitRef{{UnitID: "other"}}},
				{ID: "chunk-d", UnitRefs: []retrieval.UnitRef{{UnitID: "unit-two"}}},
			}, 150, 250, 310, 60, 350, 450),
		},
	}

	report, err := rageval.RunRetrievalSweep(context.Background(), suite, retrievalGrid(), executor)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "completed" || len(report.GridResults) != 81 || len(report.Cases) != 162 {
		t.Fatalf("report summary = %+v", report)
	}
	row := report.GridResults[0]
	if row.Recall != 1 || row.MRR != 0.75 || row.CompletedCases != 2 || row.FailedCases != 0 {
		t.Fatalf("grid row = %+v", row)
	}
	if row.RecallAt5 != 1 || row.RecallAt10 != 1 || row.RecallAt20 != 1 || row.MRRAt20 != 0.75 {
		t.Fatalf("cutoff metrics = %+v", row)
	}
	if len(row.Datasets) != 2 || row.Datasets[0].DatasetID != "dureader-retrieval" || row.Datasets[1].DatasetID != "msmarco-passage" {
		t.Fatalf("dataset metrics = %+v", row.Datasets)
	}
	if row.RRFLatencyMilliseconds.Mean != 280 || row.RRFLatencyMilliseconds.P50 != 250 || row.RRFLatencyMilliseconds.P95 != 310 {
		t.Fatalf("RRF latency summary = %+v", row.RRFLatencyMilliseconds)
	}
	if row.DenseDurationMilliseconds != 125 || row.BM25DurationMilliseconds != 225 ||
		row.FuseDurationMilliseconds != 55 || row.EvidenceLoadDurationMilliseconds != 325 ||
		row.RerankDurationMilliseconds != 425 {
		t.Fatalf("grid row latencies = %+v", row)
	}
}

func TestRunRetrievalSweepSelectsWinnerByMacroQuality(t *testing.T) {
	suite := retrievalSuite()
	grid := rageval.RetrievalGrid{
		Mode: "rrf_only", DenseCandidates: []int{5, 10}, SparseCandidates: []int{10},
		RRFK: []int{60}, FusedCandidates: 20,
	}
	executor := retrievalExecutorFunc(func(_ context.Context, _ rageval.RetrievalCase, override rageval.RetrievalSearchOverride) (retrieval.SearchResult, error) {
		unitID := "other"
		if override.DenseCandidates == 10 {
			unitID = "unit-one"
		}
		return retrievalSearchResult([]retrieval.EvidenceCandidate{{ID: "chunk", UnitRefs: []retrieval.UnitRef{{UnitID: unitID}}}}, 1, 1, 2, 1, 1, 0), nil
	})
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, grid, executor)
	if err != nil {
		t.Fatal(err)
	}
	if report.Winner == nil || report.Winner.DenseCandidates != 10 || report.Winner.MacroRecallAt20 != 1 || report.RerankInvocationCount != 0 {
		t.Fatalf("winner = %+v", report.Winner)
	}
}

func TestRunRetrievalSweepReportsCandidateScoresAndExpectedMatch(t *testing.T) {
	suite := retrievalSuite()
	executor := &retrievalExecutorStub{
		results: map[string]retrieval.SearchResult{
			"case-1": retrievalSearchResult([]retrieval.EvidenceCandidate{
				{ID: "chunk-a", RerankScore: 0.91, UnitRefs: []retrieval.UnitRef{{UnitID: "unit-one"}}},
				{ID: "chunk-b", RerankScore: 0.12, UnitRefs: []retrieval.UnitRef{{UnitID: "distractor-unit"}}},
			}, 0, 0, 0, 0, 0, 0),
		},
	}
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, retrievalGrid(), executor)
	if err != nil {
		t.Fatal(err)
	}
	scores := report.Cases[0].CandidateScores
	if len(scores) != 2 ||
		scores[0].ChunkID != "chunk-a" || scores[0].RerankScore != 0.91 || !scores[0].ExpectedMatch ||
		scores[1].ChunkID != "chunk-b" || scores[1].RerankScore != 0.12 || scores[1].ExpectedMatch {
		t.Fatalf("candidate scores = %+v", scores)
	}
}

func TestRunRetrievalSweepRecordsSearchErrorsWithoutFailingTheReport(t *testing.T) {
	suite := retrievalSuite()
	executor := &retrievalExecutorStub{
		results: map[string]retrieval.SearchResult{"case-1": retrievalSearchResult(nil, 0, 0, 0, 0, 0, 0)},
		err:     errors.New("search failed"),
	}
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, retrievalGrid(), executor)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "completed_with_errors" || report.FailedCombinations != 81 || report.CompletedCombinations != 0 {
		t.Fatalf("report = %+v", report)
	}
	for _, row := range report.GridResults {
		if row.CompletedCases != 0 || row.FailedCases != 1 {
			t.Fatalf("grid row = %+v", row)
		}
	}
}

func TestRetrievalReportCSVHasTheConfirmedSweepColumns(t *testing.T) {
	suite := retrievalSuite()
	executor := &retrievalExecutorStub{
		results: map[string]retrieval.SearchResult{"case-1": retrievalSearchResult(nil, 0, 0, 0, 0, 0, 0)},
	}
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, retrievalGrid(), executor)
	if err != nil {
		t.Fatal(err)
	}
	csv, err := report.CSV()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if len(lines) != 82 {
		t.Fatalf("csv lines = %d, want header + 81 rows", len(lines))
	}
	if lines[0] != "dense_candidates,sparse_candidates,rrf_k,rerank_candidates,completed_cases,failed_cases,recall_at_5,recall_at_10,recall_at_20,mrr_at_20,macro_recall_at_20,macro_mrr_at_20,dense_mean_ms,dense_p50_ms,dense_p95_ms,bm25_mean_ms,bm25_p50_ms,bm25_p95_ms,rrf_mean_ms,rrf_p50_ms,rrf_p95_ms,evidence_load_mean_ms,evidence_load_p50_ms,evidence_load_p95_ms" {
		t.Fatalf("header = %q", lines[0])
	}
}

func TestRetrievalReportJSONIsValidAndKeepsCaseLevelDetail(t *testing.T) {
	suite := retrievalSuite()
	executor := &retrievalExecutorStub{
		results: map[string]retrieval.SearchResult{"case-1": retrievalSearchResult(nil, 0, 0, 0, 0, 0, 0)},
	}
	report, err := rageval.RunRetrievalSweep(context.Background(), suite, retrievalGrid(), executor)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) || !strings.Contains(string(encoded), `"case_id"`) || !strings.Contains(string(encoded), `"retrieved_evidence_ids"`) {
		t.Fatalf("report JSON missing case detail: %s", encoded)
	}
}

func TestNewRetrievalLiveExecutorRejectsInvalidManifest(t *testing.T) {
	if _, err := rageval.NewRetrievalLiveExecutor(nil, nil, nil, rageval.RetrievalSourceManifest{SchemaVersion: 1}); err == nil {
		t.Fatal("NewRetrievalLiveExecutor accepted an invalid manifest")
	}
}

func retrievalSuite() rageval.RetrievalSuite {
	return rageval.RetrievalSuite{
		SchemaVersion: 1,
		ID:            "retrieval-test-v1",
		Cases: []rageval.RetrievalCase{{
			ID: "case-1", Question: "find the expected unit", Language: "en", DatasetID: "msmarco-passage",
			SourceRef: "msmarco:passage:1", ExpectedEvidenceSets: [][]string{{"unit-one"}},
		}},
	}
}

func retrievalGrid() rageval.RetrievalGrid {
	return rageval.RetrievalGrid{
		DenseCandidates:  []int{20, 40, 80},
		SparseCandidates: []int{20, 40, 80},
		RRFK:             []int{30, 60, 100},
		RerankCandidates: []int{10, 20, 40},
	}
}

type retrievalExecutorStub struct {
	results map[string]retrieval.SearchResult
	err     error
}

type retrievalExecutorFunc func(context.Context, rageval.RetrievalCase, rageval.RetrievalSearchOverride) (retrieval.SearchResult, error)

func (f retrievalExecutorFunc) Search(ctx context.Context, evalCase rageval.RetrievalCase, override rageval.RetrievalSearchOverride) (retrieval.SearchResult, error) {
	return f(ctx, evalCase, override)
}

func (s *retrievalExecutorStub) Search(_ context.Context, evalCase rageval.RetrievalCase, _ rageval.RetrievalSearchOverride) (retrieval.SearchResult, error) {
	if s.err != nil {
		return retrieval.SearchResult{}, s.err
	}
	return s.results[evalCase.ID], nil
}

func retrievalSearchResult(candidates []retrieval.EvidenceCandidate, dense, bm25, rrf, fuse, load, rerank int64) retrieval.SearchResult {
	return retrieval.SearchResult{
		Candidates: candidates,
		Diagnostics: retrieval.SearchDiagnostics{
			Dense:        retrieval.SearchStageDiagnostics{Completed: true, DurationNanoseconds: dense * 1e6},
			BM25:         retrieval.SearchStageDiagnostics{Completed: true, DurationNanoseconds: bm25 * 1e6},
			RRF:          retrieval.SearchStageDiagnostics{Completed: true, DurationNanoseconds: rrf * 1e6},
			Fused:        retrieval.SearchStageDiagnostics{Completed: true, DurationNanoseconds: fuse * 1e6},
			EvidenceLoad: retrieval.SearchStageDiagnostics{Completed: true, DurationNanoseconds: load * 1e6},
			Rerank:       retrieval.SearchStageDiagnostics{Completed: rerank > 0, DurationNanoseconds: rerank * 1e6},
		},
	}
}
