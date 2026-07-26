package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
)

const SearchEvidenceResultVersion = 2

type searchEvidenceReference struct {
	ChunkID            string              `json:"chunk_id,omitempty"`
	SourceID           string              `json:"source_id"`
	EvidenceRevisionID string              `json:"evidence_revision_id"`
	SourceTitle        string              `json:"source_title,omitempty"`
	Preview            string              `json:"preview,omitempty"`
	EvidenceRanges     []retrieval.UnitRef `json:"evidence_ranges,omitempty"`
}

type searchEvidenceResult struct {
	ResultVersion int                       `json:"result_version,omitempty"`
	CompleteEmpty bool                      `json:"complete_empty"`
	Degraded      bool                      `json:"degraded"`
	Degradations  []string                  `json:"degradations"`
	Evidence      []searchEvidenceReference `json:"evidence"`
	Legacy        bool                      `json:"-"`
}

func newSearchEvidenceResult(result retrieval.SearchResult) (searchEvidenceResult, error) {
	output := searchEvidenceResult{
		ResultVersion: SearchEvidenceResultVersion,
		CompleteEmpty: result.CompleteEmpty,
		Degraded:      result.Degraded,
		Degradations:  append([]string(nil), result.Degradations...),
		Evidence:      make([]searchEvidenceReference, 0, len(result.Candidates)),
	}
	for _, candidate := range result.Candidates {
		output.Evidence = append(output.Evidence, searchEvidenceReference{
			ChunkID: candidate.ID, SourceID: candidate.SourceID, EvidenceRevisionID: candidate.RevisionID,
		})
	}
	if err := validateSearchEvidenceResult(output); err != nil {
		return searchEvidenceResult{}, err
	}
	return output, nil
}

func decodeSearchEvidenceResult(raw json.RawMessage) (searchEvidenceResult, error) {
	if len(raw) == 0 {
		return searchEvidenceResult{}, ErrGroundingInvalid
	}
	var output searchEvidenceResult
	if err := json.Unmarshal(raw, &output); err != nil {
		return searchEvidenceResult{}, ErrGroundingInvalid
	}
	switch output.ResultVersion {
	case 0:
		output.Legacy = true
	case SearchEvidenceResultVersion:
	default:
		return searchEvidenceResult{}, ErrGroundingInvalid
	}
	if err := validateSearchEvidenceResult(output); err != nil {
		return searchEvidenceResult{}, err
	}
	return output, nil
}

func validateSearchEvidenceResult(output searchEvidenceResult) error {
	if len(output.Evidence) > maxSearchEvidenceCandidates {
		return ErrGroundingInvalid
	}
	if output.CompleteEmpty && len(output.Evidence) != 0 {
		return ErrGroundingInvalid
	}
	for _, degradation := range output.Degradations {
		if strings.TrimSpace(degradation) == "" {
			return ErrGroundingInvalid
		}
	}
	seenChunks := make(map[string]struct{}, len(output.Evidence))
	for _, evidence := range output.Evidence {
		if strings.TrimSpace(evidence.SourceID) == "" || strings.TrimSpace(evidence.EvidenceRevisionID) == "" {
			return ErrGroundingInvalid
		}
		if !output.Legacy {
			if !validSearchEvidenceIdentity(evidence.SourceID) || !validSearchEvidenceIdentity(evidence.EvidenceRevisionID) ||
				!validSearchEvidenceIdentity(evidence.ChunkID) || evidence.SourceTitle != "" || evidence.Preview != "" || len(evidence.EvidenceRanges) != 0 {
				return ErrGroundingInvalid
			}
			if _, duplicate := seenChunks[evidence.ChunkID]; duplicate {
				return ErrGroundingInvalid
			}
			seenChunks[evidence.ChunkID] = struct{}{}
			continue
		}
		for _, item := range evidence.EvidenceRanges {
			if strings.TrimSpace(item.UnitID) == "" || item.StartRune < 0 || item.EndRune <= item.StartRune {
				return ErrGroundingInvalid
			}
		}
	}
	return nil
}

func validSearchEvidenceIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 256
}

type searchEvidenceModelItem struct {
	SourceID         string `json:"source_id"`
	SourceTitle      string `json:"source_title"`
	Preview          string `json:"preview"`
	PreviewTruncated bool   `json:"preview_truncated,omitempty"`
}

type searchEvidenceModelOutput struct {
	CompleteEmpty bool                      `json:"complete_empty"`
	Degraded      bool                      `json:"degraded"`
	Degradations  []string                  `json:"degradations"`
	Evidence      []searchEvidenceModelItem `json:"evidence"`
	Truncated     bool                      `json:"truncated"`
	OmittedCount  int                       `json:"omitted_count"`
}

func buildSearchEvidenceModelOutput(manifest searchEvidenceResult, candidates []retrieval.EvidenceCandidate, byteLimit int) (json.RawMessage, error) {
	if manifest.Legacy || manifest.ResultVersion != SearchEvidenceResultVersion || byteLimit < 1 {
		return nil, errors.New("invalid search evidence model projection")
	}
	byID := make(map[string]retrieval.EvidenceCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	items := make([]searchEvidenceModelItem, 0, len(manifest.Evidence))
	for _, reference := range manifest.Evidence {
		candidate, ok := byID[reference.ChunkID]
		if !ok || candidate.SourceID != reference.SourceID || candidate.RevisionID != reference.EvidenceRevisionID {
			return nil, fmt.Errorf("%w: search evidence manifest no longer resolves", ErrGroundingInvalid)
		}
		items = append(items, searchEvidenceModelItem{
			SourceID: candidate.SourceID, SourceTitle: candidate.SourceTitle, Preview: candidate.Preview,
		})
	}
	return encodeSearchEvidenceModelOutput(manifest.CompleteEmpty, manifest.Degraded, manifest.Degradations, items, byteLimit)
}

func buildLegacySearchEvidenceModelOutput(result searchEvidenceResult, byteLimit int) (json.RawMessage, error) {
	items := make([]searchEvidenceModelItem, 0, len(result.Evidence))
	for _, evidence := range result.Evidence {
		items = append(items, searchEvidenceModelItem{
			SourceID: evidence.SourceID, SourceTitle: evidence.SourceTitle, Preview: evidence.Preview,
		})
	}
	return encodeSearchEvidenceModelOutput(result.CompleteEmpty, result.Degraded, result.Degradations, items, byteLimit)
}

func encodeSearchEvidenceModelOutput(completeEmpty, degraded bool, degradations []string, items []searchEvidenceModelItem, byteLimit int) (json.RawMessage, error) {
	for keep := len(items); keep >= 1; keep-- {
		output := searchEvidenceModelOutput{
			CompleteEmpty: completeEmpty, Degraded: degraded,
			Degradations: append([]string(nil), degradations...), Evidence: append([]searchEvidenceModelItem(nil), items[:keep]...),
			Truncated: keep != len(items), OmittedCount: len(items) - keep,
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= byteLimit {
			return encoded, nil
		}
	}
	if len(items) > 0 {
		previewRunes := []rune(items[0].Preview)
		low, high := 0, len(previewRunes)-1
		var best json.RawMessage
		for low <= high {
			middle := low + (high-low)/2
			item := items[0]
			item.Preview = string(previewRunes[:middle+1])
			item.PreviewTruncated = true
			encoded, err := json.Marshal(searchEvidenceModelOutput{
				CompleteEmpty: completeEmpty, Degraded: degraded, Degradations: append([]string(nil), degradations...),
				Evidence: []searchEvidenceModelItem{item}, Truncated: true, OmittedCount: len(items) - 1,
			})
			if err != nil {
				return nil, err
			}
			if len(encoded) <= byteLimit {
				best = encoded
				low = middle + 1
			} else {
				high = middle - 1
			}
		}
		if len(best) > 0 {
			return best, nil
		}
	}
	encoded, err := json.Marshal(searchEvidenceModelOutput{
		CompleteEmpty: completeEmpty, Degraded: degraded, Degradations: append([]string(nil), degradations...),
		Evidence: []searchEvidenceModelItem{}, Truncated: len(items) > 0, OmittedCount: len(items),
	})
	if err != nil {
		return nil, err
	}
	if len(encoded) <= byteLimit {
		return encoded, nil
	}
	return nil, errors.New("search evidence model projection byte limit is too small")
}
