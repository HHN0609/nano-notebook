package rageval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

var (
	ErrRetrievalSuiteInvalid = errors.New("RAG retrieval Suite is invalid")
	ErrRetrievalGridInvalid  = errors.New("RAG retrieval sweep grid is invalid")
)

type RetrievalCase struct {
	ID                   string     `json:"id"`
	Question             string     `json:"question"`
	Language             string     `json:"language"`
	DatasetID            string     `json:"dataset_id"`
	SourceRef            string     `json:"source_ref"`
	ExpectedEvidenceSets [][]string `json:"expected_evidence_sets"`
}

type RetrievalSuite struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Cases         []RetrievalCase `json:"cases"`
}

func (s RetrievalSuite) Validate() error {
	if s.SchemaVersion != 1 || strings.TrimSpace(s.ID) == "" || len(s.Cases) == 0 {
		return ErrRetrievalSuiteInvalid
	}
	ids := make(map[string]struct{}, len(s.Cases))
	for _, evalCase := range s.Cases {
		if strings.TrimSpace(evalCase.ID) == "" || strings.TrimSpace(evalCase.Question) == "" ||
			strings.TrimSpace(evalCase.Language) == "" || strings.TrimSpace(evalCase.DatasetID) == "" ||
			strings.TrimSpace(evalCase.SourceRef) == "" || len(evalCase.ExpectedEvidenceSets) == 0 {
			return ErrRetrievalSuiteInvalid
		}
		if _, duplicate := ids[evalCase.ID]; duplicate {
			return ErrRetrievalSuiteInvalid
		}
		ids[evalCase.ID] = struct{}{}
		for _, set := range evalCase.ExpectedEvidenceSets {
			if len(set) == 0 || hasBlankOrDuplicate(set) {
				return ErrRetrievalSuiteInvalid
			}
		}
	}
	return nil
}

func (s RetrievalSuite) SHA256() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type RetrievalSearchOverride struct {
	DenseCandidates  int  `json:"dense_candidates"`
	SparseCandidates int  `json:"sparse_candidates"`
	RRFK             int  `json:"rrf_k"`
	RerankCandidates int  `json:"rerank_candidates"`
	FusedCandidates  int  `json:"fused_candidates,omitempty"`
	SkipRerank       bool `json:"skip_rerank,omitempty"`
}

type RetrievalGrid struct {
	Mode             string `json:"mode,omitempty"`
	DenseCandidates  []int  `json:"dense_candidates"`
	SparseCandidates []int  `json:"sparse_candidates"`
	RRFK             []int  `json:"rrf_k"`
	RerankCandidates []int  `json:"rerank_candidates"`
	FusedCandidates  int    `json:"fused_candidates,omitempty"`
}

func (g RetrievalGrid) Validate() error {
	if !validSweepValues(g.DenseCandidates) || !validSweepValues(g.SparseCandidates) || !validSweepValues(g.RRFK) {
		return ErrRetrievalGridInvalid
	}
	if g.Mode == "rrf_only" {
		if g.FusedCandidates <= 0 || len(g.RerankCandidates) != 0 {
			return ErrRetrievalGridInvalid
		}
		return nil
	}
	if g.Mode != "" || !validSweepValues(g.RerankCandidates) || g.FusedCandidates != 0 {
		return ErrRetrievalGridInvalid
	}
	return nil
}

func (g RetrievalGrid) Combinations() []RetrievalSearchOverride {
	if g.Mode == "rrf_only" {
		result := make([]RetrievalSearchOverride, 0, len(g.DenseCandidates)*len(g.SparseCandidates)*len(g.RRFK))
		for _, dense := range g.DenseCandidates {
			for _, sparse := range g.SparseCandidates {
				for _, rrfK := range g.RRFK {
					result = append(result, RetrievalSearchOverride{
						DenseCandidates: dense, SparseCandidates: sparse, RRFK: rrfK,
						FusedCandidates: g.FusedCandidates, SkipRerank: true,
					})
				}
			}
		}
		return result
	}
	result := make([]RetrievalSearchOverride, 0, len(g.DenseCandidates)*len(g.SparseCandidates)*len(g.RRFK)*len(g.RerankCandidates))
	for _, dense := range g.DenseCandidates {
		for _, sparse := range g.SparseCandidates {
			for _, rrfK := range g.RRFK {
				for _, rerank := range g.RerankCandidates {
					result = append(result, RetrievalSearchOverride{
						DenseCandidates: dense, SparseCandidates: sparse,
						RRFK: rrfK, RerankCandidates: rerank,
					})
				}
			}
		}
	}
	return result
}

type RetrievalExecutor interface {
	Search(context.Context, RetrievalCase, RetrievalSearchOverride) (retrieval.SearchResult, error)
}

type RetrievalCloser interface {
	Close(context.Context) error
}

type RetrievalGridResult struct {
	DenseCandidates  int                      `json:"dense_candidates"`
	SparseCandidates int                      `json:"sparse_candidates"`
	RRFK             int                      `json:"rrf_k"`
	RerankCandidates int                      `json:"rerank_candidates"`
	CompletedCases   int                      `json:"completed_cases"`
	FailedCases      int                      `json:"failed_cases"`
	Recall           float64                  `json:"recall"`
	MRR              float64                  `json:"mrr"`
	RecallAt5        float64                  `json:"recall_at_5"`
	RecallAt10       float64                  `json:"recall_at_10"`
	RecallAt20       float64                  `json:"recall_at_20"`
	MRRAt20          float64                  `json:"mrr_at_20"`
	MacroRecallAt20  float64                  `json:"macro_recall_at_20"`
	MacroMRRAt20     float64                  `json:"macro_mrr_at_20"`
	Datasets         []RetrievalDatasetResult `json:"datasets"`

	DenseDurationMilliseconds        float64                 `json:"dense_ms"`
	BM25DurationMilliseconds         float64                 `json:"bm25_ms"`
	FuseDurationMilliseconds         float64                 `json:"fuse_ms"`
	EvidenceLoadDurationMilliseconds float64                 `json:"evidence_load_ms"`
	RerankDurationMilliseconds       float64                 `json:"rerank_ms"`
	TotalDurationMilliseconds        float64                 `json:"total_ms"`
	DenseLatencyMilliseconds         RetrievalLatencySummary `json:"dense_latency_ms"`
	BM25LatencyMilliseconds          RetrievalLatencySummary `json:"bm25_latency_ms"`
	RRFLatencyMilliseconds           RetrievalLatencySummary `json:"rrf_latency_ms"`
	EvidenceLoadLatencyMilliseconds  RetrievalLatencySummary `json:"evidence_load_latency_ms"`
}

type RetrievalLatencySummary struct {
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
}

type RetrievalDatasetResult struct {
	DatasetID      string  `json:"dataset_id"`
	CompletedCases int     `json:"completed_cases"`
	RecallAt5      float64 `json:"recall_at_5"`
	RecallAt10     float64 `json:"recall_at_10"`
	RecallAt20     float64 `json:"recall_at_20"`
	MRRAt20        float64 `json:"mrr_at_20"`
}

// RetrievalCandidateScore reports the post-rerank relevance score for one
// returned candidate, keyed by chunk ID rather than a parallel array so a
// human comparing the score distribution of expected vs. distractor
// candidates does not have to correlate two separate slices by index.
type RetrievalCandidateScore struct {
	ChunkID       string  `json:"chunk_id"`
	RerankScore   float64 `json:"rerank_score"`
	ExpectedMatch bool    `json:"expected_match"`
}

type RetrievalCaseResult struct {
	CaseID    string `json:"case_id"`
	Question  string `json:"question"`
	Language  string `json:"language"`
	SourceRef string `json:"source_ref"`
	DatasetID string `json:"dataset_id"`

	DenseCandidates  int `json:"dense_candidates"`
	SparseCandidates int `json:"sparse_candidates"`
	RRFK             int `json:"rrf_k"`
	RerankCandidates int `json:"rerank_candidates"`

	ExpectedEvidenceSetCount int                       `json:"expected_evidence_set_count"`
	Recall                   float64                   `json:"recall"`
	MRR                      float64                   `json:"mrr"`
	RecallAt5                float64                   `json:"recall_at_5"`
	RecallAt10               float64                   `json:"recall_at_10"`
	RecallAt20               float64                   `json:"recall_at_20"`
	MRRAt20                  float64                   `json:"mrr_at_20"`
	RetrievedEvidenceIDs     []string                  `json:"retrieved_evidence_ids"`
	RetrievedChunkIDs        []string                  `json:"retrieved_chunk_ids"`
	CandidateScores          []RetrievalCandidateScore `json:"candidate_scores,omitempty"`
	Degradations             []string                  `json:"degradations,omitempty"`

	DenseDurationMilliseconds        float64 `json:"dense_ms"`
	BM25DurationMilliseconds         float64 `json:"bm25_ms"`
	FuseDurationMilliseconds         float64 `json:"fuse_ms"`
	RRFDurationMilliseconds          float64 `json:"rrf_ms"`
	EvidenceLoadDurationMilliseconds float64 `json:"evidence_load_ms"`
	RerankDurationMilliseconds       float64 `json:"rerank_ms"`
	TotalDurationMilliseconds        float64 `json:"total_ms"`

	Error string `json:"error,omitempty"`
}

type RetrievalReport struct {
	SchemaVersion int           `json:"schema_version"`
	SuiteID       string        `json:"suite_id"`
	SuiteSHA256   string        `json:"suite_sha256"`
	Grid          RetrievalGrid `json:"grid"`
	Status        string        `json:"status"`

	TotalCombinations     int `json:"total_combinations"`
	CompletedCombinations int `json:"completed_combinations"`
	FailedCombinations    int `json:"failed_combinations"`
	RerankInvocationCount int `json:"rerank_invocation_count"`

	GridResults []RetrievalGridResult `json:"grid_results"`
	Cases       []RetrievalCaseResult `json:"cases"`
	Winner      *RetrievalWinner      `json:"winner,omitempty"`
}

type RetrievalWinner struct {
	DenseCandidates    int     `json:"dense_candidates"`
	SparseCandidates   int     `json:"sparse_candidates"`
	RRFK               int     `json:"rrf_k"`
	FusedCandidates    int     `json:"fused_candidates"`
	MacroRecallAt20    float64 `json:"macro_recall_at_20"`
	MacroMRRAt20       float64 `json:"macro_mrr_at_20"`
	RRFP95Milliseconds float64 `json:"rrf_p95_ms"`
}

func RunRetrievalSweep(ctx context.Context, suite RetrievalSuite, grid RetrievalGrid, executor RetrievalExecutor) (RetrievalReport, error) {
	if err := suite.Validate(); err != nil {
		return RetrievalReport{}, err
	}
	if err := grid.Validate(); err != nil {
		return RetrievalReport{}, err
	}
	if executor == nil {
		return RetrievalReport{}, errors.New("RAG retrieval Executor is required")
	}
	digest, err := suite.SHA256()
	if err != nil {
		return RetrievalReport{}, err
	}
	combinations := grid.Combinations()
	report := RetrievalReport{
		SchemaVersion: 1, SuiteID: suite.ID, SuiteSHA256: digest, Grid: grid,
		Status: "completed", TotalCombinations: len(combinations),
		GridResults: make([]RetrievalGridResult, 0, len(combinations)),
		Cases:       make([]RetrievalCaseResult, 0, len(combinations)*len(suite.Cases)),
	}
	for _, override := range combinations {
		row := RetrievalGridResult{
			DenseCandidates: override.DenseCandidates, SparseCandidates: override.SparseCandidates,
			RRFK: override.RRFK, RerankCandidates: override.RerankCandidates,
		}
		completedCases := 0
		datasetRows := make(map[string]*RetrievalDatasetResult)
		var denseLatencies, bm25Latencies, rrfLatencies, evidenceLoadLatencies []float64
		for _, evalCase := range suite.Cases {
			caseResult := RetrievalCaseResult{
				CaseID: evalCase.ID, Question: evalCase.Question, Language: evalCase.Language, DatasetID: evalCase.DatasetID,
				SourceRef: evalCase.SourceRef, DenseCandidates: override.DenseCandidates,
				SparseCandidates: override.SparseCandidates, RRFK: override.RRFK,
				RerankCandidates:         override.RerankCandidates,
				ExpectedEvidenceSetCount: len(evalCase.ExpectedEvidenceSets),
			}
			searchResult, searchErr := executor.Search(ctx, evalCase, override)
			if searchErr != nil {
				caseResult.Error = searchErr.Error()
				row.FailedCases++
				report.Status = "completed_with_errors"
				report.Cases = append(report.Cases, caseResult)
				continue
			}
			if searchResult.Diagnostics.Rerank.Completed || searchResult.Diagnostics.Rerank.DurationNanoseconds > 0 {
				report.RerankInvocationCount++
			}
			completedCases++
			row.CompletedCases++
			caseResult.RetrievedEvidenceIDs, caseResult.RetrievedChunkIDs = orderedRetrievalIDs(searchResult.Candidates)
			caseResult.RecallAt5, _ = retrievalRecallMRRAt(evalCase, caseResult.RetrievedEvidenceIDs, 5)
			caseResult.RecallAt10, _ = retrievalRecallMRRAt(evalCase, caseResult.RetrievedEvidenceIDs, 10)
			caseResult.RecallAt20, caseResult.MRRAt20 = retrievalRecallMRRAt(evalCase, caseResult.RetrievedEvidenceIDs, 20)
			caseResult.Recall, caseResult.MRR = caseResult.RecallAt20, caseResult.MRRAt20
			caseResult.CandidateScores = candidateScores(searchResult.Candidates, expectedUnitIDs(evalCase))
			caseResult.Degradations = append([]string(nil), searchResult.Degradations...)
			caseResult.DenseDurationMilliseconds = milliseconds(searchResult.Diagnostics.Dense.DurationNanoseconds)
			caseResult.BM25DurationMilliseconds = milliseconds(searchResult.Diagnostics.BM25.DurationNanoseconds)
			caseResult.FuseDurationMilliseconds = milliseconds(searchResult.Diagnostics.Fused.DurationNanoseconds)
			caseResult.RRFDurationMilliseconds = milliseconds(searchResult.Diagnostics.RRF.DurationNanoseconds)
			caseResult.EvidenceLoadDurationMilliseconds = milliseconds(searchResult.Diagnostics.EvidenceLoad.DurationNanoseconds)
			caseResult.RerankDurationMilliseconds = milliseconds(searchResult.Diagnostics.Rerank.DurationNanoseconds)
			caseResult.TotalDurationMilliseconds = caseResult.RRFDurationMilliseconds +
				caseResult.EvidenceLoadDurationMilliseconds + caseResult.RerankDurationMilliseconds

			row.Recall += caseResult.Recall
			row.MRR += caseResult.MRR
			row.RecallAt5 += caseResult.RecallAt5
			row.RecallAt10 += caseResult.RecallAt10
			row.RecallAt20 += caseResult.RecallAt20
			row.MRRAt20 += caseResult.MRRAt20
			row.DenseDurationMilliseconds += caseResult.DenseDurationMilliseconds
			row.BM25DurationMilliseconds += caseResult.BM25DurationMilliseconds
			row.FuseDurationMilliseconds += caseResult.FuseDurationMilliseconds
			row.EvidenceLoadDurationMilliseconds += caseResult.EvidenceLoadDurationMilliseconds
			row.RerankDurationMilliseconds += caseResult.RerankDurationMilliseconds
			row.TotalDurationMilliseconds += caseResult.TotalDurationMilliseconds
			denseLatencies = append(denseLatencies, caseResult.DenseDurationMilliseconds)
			bm25Latencies = append(bm25Latencies, caseResult.BM25DurationMilliseconds)
			rrfLatencies = append(rrfLatencies, caseResult.RRFDurationMilliseconds)
			evidenceLoadLatencies = append(evidenceLoadLatencies, caseResult.EvidenceLoadDurationMilliseconds)
			datasetRow := datasetRows[evalCase.DatasetID]
			if datasetRow == nil {
				datasetRow = &RetrievalDatasetResult{DatasetID: evalCase.DatasetID}
				datasetRows[evalCase.DatasetID] = datasetRow
			}
			datasetRow.CompletedCases++
			datasetRow.RecallAt5 += caseResult.RecallAt5
			datasetRow.RecallAt10 += caseResult.RecallAt10
			datasetRow.RecallAt20 += caseResult.RecallAt20
			datasetRow.MRRAt20 += caseResult.MRRAt20
			report.Cases = append(report.Cases, caseResult)
		}
		if completedCases > 0 {
			report.CompletedCombinations++
			count := float64(completedCases)
			row.Recall /= count
			row.MRR /= count
			row.RecallAt5 /= count
			row.RecallAt10 /= count
			row.RecallAt20 /= count
			row.MRRAt20 /= count
			row.DenseDurationMilliseconds /= count
			row.BM25DurationMilliseconds /= count
			row.FuseDurationMilliseconds /= count
			row.EvidenceLoadDurationMilliseconds /= count
			row.RerankDurationMilliseconds /= count
			row.TotalDurationMilliseconds /= count
			row.DenseLatencyMilliseconds = latencySummary(denseLatencies)
			row.BM25LatencyMilliseconds = latencySummary(bm25Latencies)
			row.RRFLatencyMilliseconds = latencySummary(rrfLatencies)
			row.EvidenceLoadLatencyMilliseconds = latencySummary(evidenceLoadLatencies)
			row.Datasets = datasetResults(datasetRows)
			for _, dataset := range row.Datasets {
				row.MacroRecallAt20 += dataset.RecallAt20
				row.MacroMRRAt20 += dataset.MRRAt20
			}
			row.MacroRecallAt20 /= float64(len(row.Datasets))
			row.MacroMRRAt20 /= float64(len(row.Datasets))
		} else {
			report.FailedCombinations++
		}
		report.GridResults = append(report.GridResults, row)
	}
	report.Winner = selectRetrievalWinner(report.GridResults, grid.FusedCandidates)
	if closer, ok := executor.(RetrievalCloser); ok {
		if err := closer.Close(ctx); err != nil {
			return report, fmt.Errorf("close retrieval Executor: %w", err)
		}
	}
	return report, nil
}

func selectRetrievalWinner(rows []RetrievalGridResult, fusedCandidates int) *RetrievalWinner {
	var best *RetrievalGridResult
	for index := range rows {
		row := &rows[index]
		if row.CompletedCases == 0 || row.FailedCases != 0 {
			continue
		}
		if best == nil || row.MacroRecallAt20 > best.MacroRecallAt20 ||
			(row.MacroRecallAt20 == best.MacroRecallAt20 && row.MacroMRRAt20 > best.MacroMRRAt20) ||
			(row.MacroRecallAt20 == best.MacroRecallAt20 && row.MacroMRRAt20 == best.MacroMRRAt20 &&
				row.RRFLatencyMilliseconds.P95 < best.RRFLatencyMilliseconds.P95) {
			best = row
		}
	}
	if best == nil {
		return nil
	}
	return &RetrievalWinner{
		DenseCandidates: best.DenseCandidates, SparseCandidates: best.SparseCandidates,
		RRFK: best.RRFK, FusedCandidates: fusedCandidates,
		MacroRecallAt20: best.MacroRecallAt20, MacroMRRAt20: best.MacroMRRAt20,
		RRFP95Milliseconds: best.RRFLatencyMilliseconds.P95,
	}
}

func (r RetrievalReport) CSV() ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := []string{
		"dense_candidates", "sparse_candidates", "rrf_k", "rerank_candidates",
		"completed_cases", "failed_cases", "recall_at_5", "recall_at_10", "recall_at_20", "mrr_at_20",
		"macro_recall_at_20", "macro_mrr_at_20",
		"dense_mean_ms", "dense_p50_ms", "dense_p95_ms",
		"bm25_mean_ms", "bm25_p50_ms", "bm25_p95_ms",
		"rrf_mean_ms", "rrf_p50_ms", "rrf_p95_ms",
		"evidence_load_mean_ms", "evidence_load_p50_ms", "evidence_load_p95_ms",
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, row := range r.GridResults {
		record := []string{
			strconv.Itoa(row.DenseCandidates), strconv.Itoa(row.SparseCandidates),
			strconv.Itoa(row.RRFK), strconv.Itoa(row.RerankCandidates),
			strconv.Itoa(row.CompletedCases), strconv.Itoa(row.FailedCases),
			formatMetric(row.RecallAt5), formatMetric(row.RecallAt10), formatMetric(row.RecallAt20), formatMetric(row.MRRAt20),
			formatMetric(row.MacroRecallAt20), formatMetric(row.MacroMRRAt20),
			formatMilliseconds(row.DenseLatencyMilliseconds.Mean), formatMilliseconds(row.DenseLatencyMilliseconds.P50), formatMilliseconds(row.DenseLatencyMilliseconds.P95),
			formatMilliseconds(row.BM25LatencyMilliseconds.Mean), formatMilliseconds(row.BM25LatencyMilliseconds.P50), formatMilliseconds(row.BM25LatencyMilliseconds.P95),
			formatMilliseconds(row.RRFLatencyMilliseconds.Mean), formatMilliseconds(row.RRFLatencyMilliseconds.P50), formatMilliseconds(row.RRFLatencyMilliseconds.P95),
			formatMilliseconds(row.EvidenceLoadLatencyMilliseconds.Mean), formatMilliseconds(row.EvidenceLoadLatencyMilliseconds.P50), formatMilliseconds(row.EvidenceLoadLatencyMilliseconds.P95),
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func formatMetric(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func formatMilliseconds(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func (r RetrievalReport) Markdown() []byte {
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "# Retrieval sweep: %s\n\n", r.SuiteID)
	fmt.Fprintf(&buffer, "Status: %s. Combinations: %d. Reranker invocations: %d.\n\n", r.Status, r.TotalCombinations, r.RerankInvocationCount)
	if r.Winner != nil {
		fmt.Fprintf(&buffer, "Winner: Dense %d, BM25 %d, RRF k %d, fused Top %d; macro Recall@20 %.6f, macro MRR@20 %.6f, RRF p95 %.3f ms.\n\n",
			r.Winner.DenseCandidates, r.Winner.SparseCandidates, r.Winner.RRFK, r.Winner.FusedCandidates,
			r.Winner.MacroRecallAt20, r.Winner.MacroMRRAt20, r.Winner.RRFP95Milliseconds)
	}
	buffer.WriteString("| Dense | BM25 | RRF k | Recall@5 | Recall@10 | Recall@20 | MRR@20 | RRF mean ms | RRF p50 ms | RRF p95 ms |\n")
	buffer.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range r.GridResults {
		fmt.Fprintf(&buffer, "| %d | %d | %d | %.6f | %.6f | %.6f | %.6f | %.3f | %.3f | %.3f |\n",
			row.DenseCandidates, row.SparseCandidates, row.RRFK, row.RecallAt5, row.RecallAt10,
			row.RecallAt20, row.MRRAt20, row.RRFLatencyMilliseconds.Mean,
			row.RRFLatencyMilliseconds.P50, row.RRFLatencyMilliseconds.P95)
	}
	return buffer.Bytes()
}

func validSweepValues(values []int) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func orderedRetrievalIDs(candidates []retrieval.EvidenceCandidate) ([]string, []string) {
	unitIDs := make([]string, 0)
	chunkIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		chunkIDs = append(chunkIDs, candidate.ID)
		for _, ref := range candidate.UnitRefs {
			unitIDs = append(unitIDs, ref.UnitID)
		}
	}
	return unitIDs, chunkIDs
}

func expectedUnitIDs(evalCase RetrievalCase) map[string]bool {
	allowed := make(map[string]bool)
	for _, set := range evalCase.ExpectedEvidenceSets {
		for _, id := range set {
			allowed[id] = true
		}
	}
	return allowed
}

func candidateScores(candidates []retrieval.EvidenceCandidate, allowed map[string]bool) []RetrievalCandidateScore {
	scores := make([]RetrievalCandidateScore, 0, len(candidates))
	for _, candidate := range candidates {
		expectedMatch := false
		for _, ref := range candidate.UnitRefs {
			if allowed[ref.UnitID] {
				expectedMatch = true
				break
			}
		}
		scores = append(scores, RetrievalCandidateScore{
			ChunkID: candidate.ID, RerankScore: candidate.RerankScore, ExpectedMatch: expectedMatch,
		})
	}
	return scores
}

func retrievalRecallMRR(evalCase RetrievalCase, ids []string) (float64, float64) {
	return retrievalRecallMRRAt(evalCase, ids, len(ids))
}

func retrievalRecallMRRAt(evalCase RetrievalCase, ids []string, limit int) (float64, float64) {
	if limit < len(ids) {
		ids = ids[:limit]
	}
	allowed := expectedUnitIDs(evalCase)
	found := 0
	for _, set := range evalCase.ExpectedEvidenceSets {
		matched := false
		for _, id := range ids {
			for _, expected := range set {
				if id == expected {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			found++
		}
	}
	recall := float64(found) / float64(len(evalCase.ExpectedEvidenceSets))
	mrr := 0.0
	for index, id := range ids {
		if allowed[id] {
			mrr = 1 / float64(index+1)
			break
		}
	}
	return recall, mrr
}

func latencySummary(values []float64) RetrievalLatencySummary {
	if len(values) == 0 {
		return RetrievalLatencySummary{}
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	total := 0.0
	for _, value := range ordered {
		total += value
	}
	return RetrievalLatencySummary{
		Mean: total / float64(len(ordered)),
		P50:  nearestRankPercentile(ordered, 0.50),
		P95:  nearestRankPercentile(ordered, 0.95),
	}
}

func nearestRankPercentile(ordered []float64, percentile float64) float64 {
	index := int(math.Ceil(percentile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	return ordered[index]
}

func datasetResults(rows map[string]*RetrievalDatasetResult) []RetrievalDatasetResult {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]RetrievalDatasetResult, 0, len(ids))
	for _, id := range ids {
		row := *rows[id]
		count := float64(row.CompletedCases)
		row.RecallAt5 /= count
		row.RecallAt10 /= count
		row.RecallAt20 /= count
		row.MRRAt20 /= count
		result = append(result, row)
	}
	return result
}

func milliseconds(nanoseconds int64) float64 {
	return float64(nanoseconds) / 1e6
}
