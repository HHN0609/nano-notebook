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
	DenseCandidates  int     `json:"dense_candidates"`
	SparseCandidates int     `json:"sparse_candidates"`
	RRFK             int     `json:"rrf_k"`
	RerankCandidates int     `json:"rerank_candidates"`
	CompletedCases   int     `json:"completed_cases"`
	FailedCases      int     `json:"failed_cases"`
	Recall           float64 `json:"recall"`
	MRR              float64 `json:"mrr"`

	DenseDurationMilliseconds        float64 `json:"dense_ms"`
	BM25DurationMilliseconds         float64 `json:"bm25_ms"`
	FuseDurationMilliseconds         float64 `json:"fuse_ms"`
	EvidenceLoadDurationMilliseconds float64 `json:"evidence_load_ms"`
	RerankDurationMilliseconds       float64 `json:"rerank_ms"`
	TotalDurationMilliseconds        float64 `json:"total_ms"`
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

	DenseCandidates  int `json:"dense_candidates"`
	SparseCandidates int `json:"sparse_candidates"`
	RRFK             int `json:"rrf_k"`
	RerankCandidates int `json:"rerank_candidates"`

	ExpectedEvidenceSetCount int                       `json:"expected_evidence_set_count"`
	Recall                   float64                   `json:"recall"`
	MRR                      float64                   `json:"mrr"`
	RetrievedEvidenceIDs     []string                  `json:"retrieved_evidence_ids"`
	RetrievedChunkIDs        []string                  `json:"retrieved_chunk_ids"`
	CandidateScores          []RetrievalCandidateScore `json:"candidate_scores,omitempty"`
	Degradations             []string                  `json:"degradations,omitempty"`

	DenseDurationMilliseconds        float64 `json:"dense_ms"`
	BM25DurationMilliseconds         float64 `json:"bm25_ms"`
	FuseDurationMilliseconds         float64 `json:"fuse_ms"`
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

	GridResults []RetrievalGridResult `json:"grid_results"`
	Cases       []RetrievalCaseResult `json:"cases"`
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
		for _, evalCase := range suite.Cases {
			caseResult := RetrievalCaseResult{
				CaseID: evalCase.ID, Question: evalCase.Question, Language: evalCase.Language,
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
			completedCases++
			row.CompletedCases++
			caseResult.RetrievedEvidenceIDs, caseResult.RetrievedChunkIDs = orderedRetrievalIDs(searchResult.Candidates)
			caseResult.Recall, caseResult.MRR = retrievalRecallMRR(evalCase, caseResult.RetrievedEvidenceIDs)
			caseResult.CandidateScores = candidateScores(searchResult.Candidates, expectedUnitIDs(evalCase))
			caseResult.Degradations = append([]string(nil), searchResult.Degradations...)
			caseResult.DenseDurationMilliseconds = milliseconds(searchResult.Diagnostics.Dense.DurationNanoseconds)
			caseResult.BM25DurationMilliseconds = milliseconds(searchResult.Diagnostics.BM25.DurationNanoseconds)
			caseResult.FuseDurationMilliseconds = milliseconds(searchResult.Diagnostics.Fused.DurationNanoseconds)
			caseResult.EvidenceLoadDurationMilliseconds = milliseconds(searchResult.Diagnostics.EvidenceLoad.DurationNanoseconds)
			caseResult.RerankDurationMilliseconds = milliseconds(searchResult.Diagnostics.Rerank.DurationNanoseconds)
			caseResult.TotalDurationMilliseconds = caseResult.DenseDurationMilliseconds + caseResult.BM25DurationMilliseconds +
				caseResult.FuseDurationMilliseconds + caseResult.EvidenceLoadDurationMilliseconds + caseResult.RerankDurationMilliseconds

			row.Recall += caseResult.Recall
			row.MRR += caseResult.MRR
			row.DenseDurationMilliseconds += caseResult.DenseDurationMilliseconds
			row.BM25DurationMilliseconds += caseResult.BM25DurationMilliseconds
			row.FuseDurationMilliseconds += caseResult.FuseDurationMilliseconds
			row.EvidenceLoadDurationMilliseconds += caseResult.EvidenceLoadDurationMilliseconds
			row.RerankDurationMilliseconds += caseResult.RerankDurationMilliseconds
			row.TotalDurationMilliseconds += caseResult.TotalDurationMilliseconds
			report.Cases = append(report.Cases, caseResult)
		}
		if completedCases > 0 {
			report.CompletedCombinations++
			count := float64(completedCases)
			row.Recall /= count
			row.MRR /= count
			row.DenseDurationMilliseconds /= count
			row.BM25DurationMilliseconds /= count
			row.FuseDurationMilliseconds /= count
			row.EvidenceLoadDurationMilliseconds /= count
			row.RerankDurationMilliseconds /= count
			row.TotalDurationMilliseconds /= count
		} else {
			report.FailedCombinations++
		}
		report.GridResults = append(report.GridResults, row)
	}
	if closer, ok := executor.(RetrievalCloser); ok {
		if err := closer.Close(ctx); err != nil {
			return report, fmt.Errorf("close retrieval Executor: %w", err)
		}
	}
	return report, nil
}

func (r RetrievalReport) CSV() ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := []string{
		"dense_candidates", "sparse_candidates", "rrf_k", "rerank_candidates",
		"completed_cases", "failed_cases", "recall", "mrr",
		"dense_ms", "bm25_ms", "fuse_ms", "evidence_load_ms", "rerank_ms", "total_ms",
	}
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	for _, row := range r.GridResults {
		record := []string{
			strconv.Itoa(row.DenseCandidates), strconv.Itoa(row.SparseCandidates),
			strconv.Itoa(row.RRFK), strconv.Itoa(row.RerankCandidates),
			strconv.Itoa(row.CompletedCases), strconv.Itoa(row.FailedCases),
			strconv.FormatFloat(row.Recall, 'f', 6, 64), strconv.FormatFloat(row.MRR, 'f', 6, 64),
			strconv.FormatFloat(row.DenseDurationMilliseconds, 'f', 3, 64),
			strconv.FormatFloat(row.BM25DurationMilliseconds, 'f', 3, 64),
			strconv.FormatFloat(row.FuseDurationMilliseconds, 'f', 3, 64),
			strconv.FormatFloat(row.EvidenceLoadDurationMilliseconds, 'f', 3, 64),
			strconv.FormatFloat(row.RerankDurationMilliseconds, 'f', 3, 64),
			strconv.FormatFloat(row.TotalDurationMilliseconds, 'f', 3, 64),
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

func milliseconds(nanoseconds int64) float64 {
	return float64(nanoseconds) / 1e6
}
