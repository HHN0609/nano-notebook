package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

func TestBuildSearchEvidenceModelOutputIsBudgetedAndOmitsRecoveryMetadata(t *testing.T) {
	manifest := searchEvidenceResult{
		ResultVersion: SearchEvidenceResultVersion,
		Evidence:      make([]searchEvidenceReference, 0, maxSearchEvidenceCandidates),
	}
	candidates := make([]retrieval.EvidenceCandidate, 0, maxSearchEvidenceCandidates)
	for index := 0; index < maxSearchEvidenceCandidates; index++ {
		chunkID := fmt.Sprintf("chunk_%d", index)
		manifest.Evidence = append(manifest.Evidence, searchEvidenceReference{
			ChunkID: chunkID, SourceID: fmt.Sprintf("src_%d", index), EvidenceRevisionID: fmt.Sprintf("evr_%d", index),
		})
		candidates = append(candidates, retrieval.EvidenceCandidate{
			ID: chunkID, SourceID: fmt.Sprintf("src_%d", index), RevisionID: fmt.Sprintf("evr_%d", index),
			SourceTitle: fmt.Sprintf("Source %d", index), Preview: fmt.Sprintf("passage-%d %s", index, strings.Repeat("文", 900)),
			UnitRefs: []retrieval.UnitRef{{UnitID: fmt.Sprintf("unit_secret_%d", index), StartRune: 0, EndRune: 900}},
		})
	}

	const limit = 4 * 1024
	encoded, err := buildSearchEvidenceModelOutput(manifest, candidates, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > limit {
		t.Fatalf("projection bytes=%d, limit=%d", len(encoded), limit)
	}
	for _, forbidden := range [][]byte{[]byte(`"chunk_id"`), []byte(`"evidence_ranges"`), []byte("unit_secret_")} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("projection leaked recovery metadata %q: %s", forbidden, encoded)
		}
	}
	var output struct {
		Evidence []struct {
			SourceID    string `json:"source_id"`
			SourceTitle string `json:"source_title"`
			Preview     string `json:"preview"`
		} `json:"evidence"`
		Truncated    bool `json:"truncated"`
		OmittedCount int  `json:"omitted_count"`
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Evidence) == 0 || output.Evidence[0].SourceID != "src_0" || output.Evidence[0].SourceTitle != "Source 0" ||
		!strings.HasPrefix(output.Evidence[0].Preview, "passage-0 ") || !output.Truncated || output.OmittedCount == 0 {
		t.Fatalf("projection=%s", encoded)
	}
}

func TestBuildSearchEvidenceModelOutputTruncatesTheTopPreviewBeforeDroppingAllEvidence(t *testing.T) {
	manifest := searchEvidenceResult{
		ResultVersion: SearchEvidenceResultVersion,
		Evidence:      []searchEvidenceReference{{ChunkID: "chunk_a", SourceID: "src_a", EvidenceRevisionID: "evr_a"}},
	}
	encoded, err := buildSearchEvidenceModelOutput(manifest, []retrieval.EvidenceCandidate{{
		ID: "chunk_a", SourceID: "src_a", RevisionID: "evr_a", SourceTitle: "Title", Preview: strings.Repeat("evidence ", 2000),
	}}, 512)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Evidence []struct {
			Preview          string `json:"preview"`
			PreviewTruncated bool   `json:"preview_truncated"`
		} `json:"evidence"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 512 || len(output.Evidence) != 1 || output.Evidence[0].Preview == "" || !output.Evidence[0].PreviewTruncated || !output.Truncated {
		t.Fatalf("projection=%s", encoded)
	}
}

func TestDecodeSearchEvidenceResultAcceptsLegacyExpandedCheckpoint(t *testing.T) {
	decoded, err := decodeSearchEvidenceResult(json.RawMessage(`{
		"complete_empty":false,"degraded":false,"degradations":[],
		"evidence":[{"source_id":"src_old","evidence_revision_id":"evr_old","source_title":"Old title","preview":"Old passage",
		"evidence_ranges":[{"unit_id":"unit_old","start_rune":1,"end_rune":8}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Legacy || len(decoded.Evidence) != 1 || decoded.Evidence[0].SourceID != "src_old" || decoded.Evidence[0].Preview != "Old passage" {
		t.Fatalf("decoded=%+v", decoded)
	}
}
