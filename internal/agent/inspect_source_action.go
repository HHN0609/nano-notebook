package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcemap"
)

const (
	InspectSourceMaxEntries     = 24
	InspectSourceMaxResultBytes = 12 * 1024
	inspectSourcePreviewRunes   = 360
)

var (
	ErrSourceNotInspectable        = errors.New("Source is not inspectable in this Research Run")
	ErrSourceInspectionUnavailable = errors.New("Source inspection is unavailable")
)

type SourceInspectionBackend interface {
	InspectSource(context.Context, Attempt, string) (json.RawMessage, error)
}

type inspectSourceAction struct{ backend SourceInspectionBackend }

type inspectSourceInput struct {
	SourceID string `json:"source_id"`
}

func NewInspectSourceAction(backend SourceInspectionBackend) Action {
	return inspectSourceAction{backend: backend}
}

func (inspectSourceAction) Available(execution Execution) (bool, string) {
	if execution.SelectedSourceCount <= 0 {
		return false, "no_sources_selected"
	}
	return true, ""
}

func (inspectSourceAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "inspect_source",
		Description: "Inspect the bounded structure and representative original passages of one Ready Source before forming focused evidence queries. Navigation only; use search_evidence for report claims.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"source_id":{"type":"string","minLength":1,"maxLength":128}},"required":["source_id"],"additionalProperties":false}`),
	}
}

func (inspectSourceAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeInspectSourceInput(raw)
	return err
}

func (a inspectSourceAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	input, err := decodeInspectSourceInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	if a.backend == nil || request.Attempt.RunID == "" {
		return ActionResult{}, ErrSourceInspectionUnavailable
	}
	output, err := a.backend.InspectSource(ctx, request.Attempt, input.SourceID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSourceNotInspectable):
			return ActionResult{Status: ActionDomainError, ErrorCode: "source_not_inspectable"}, nil
		case errors.Is(err, ErrSourceInspectionUnavailable):
			return ActionResult{Status: ActionDomainError, ErrorCode: "source_inspection_unavailable"}, nil
		default:
			return ActionResult{}, err
		}
	}
	var object map[string]json.RawMessage
	if len(output) == 0 || len(output) > InspectSourceMaxResultBytes || json.Unmarshal(output, &object) != nil || object == nil {
		return ActionResult{}, ErrSourceInspectionUnavailable
	}
	return ActionResult{Status: ActionSucceeded, Output: append(json.RawMessage(nil), output...)}, nil
}

func decodeInspectSourceInput(raw json.RawMessage) (inspectSourceInput, error) {
	if len(raw) == 0 || len(raw) > 1024 {
		return inspectSourceInput{}, errors.New("invalid inspect_source input")
	}
	var input inspectSourceInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return inspectSourceInput{}, errors.New("invalid inspect_source input")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return inspectSourceInput{}, errors.New("invalid inspect_source input")
	}
	input.SourceID = strings.TrimSpace(input.SourceID)
	if input.SourceID == "" || !utf8.ValidString(input.SourceID) || utf8.RuneCountInString(input.SourceID) > 128 {
		return inspectSourceInput{}, errors.New("invalid inspect_source input")
	}
	return input, nil
}

type sourceInspectionAuthority struct {
	SourceID       string
	RevisionID     string
	Title          string
	MediaType      string
	ArtifactSHA256 string
}

type sourceInspectionUnit struct {
	ID         string
	Ordinal    int
	Kind       string
	Text       string
	Coordinate retrieval.EvidenceCoordinate
}

type sourceInspectionResult struct {
	ResultVersion       int                        `json:"result_version"`
	Source              sourceInspectionSource     `json:"source"`
	Navigation          sourceInspectionNavigation `json:"navigation"`
	Abstract            *sourceInspectionAbstract  `json:"abstract,omitempty"`
	Entries             []sourceInspectionEntry    `json:"entries"`
	Coverage            sourceInspectionCoverage   `json:"coverage"`
	Warnings            []string                   `json:"warnings"`
	EvidenceEligibility string                     `json:"evidence_eligibility"`
}

type sourceInspectionSource struct {
	SourceID           string `json:"source_id"`
	EvidenceRevisionID string `json:"evidence_revision_id"`
	Title              string `json:"title"`
	MediaType          string `json:"media_type"`
	PageCount          int    `json:"page_count"`
}

type sourceInspectionNavigation struct {
	Kind                    sourcemap.NavigationKind `json:"kind"`
	Confidence              sourcemap.Confidence     `json:"confidence"`
	StructureArtifactSHA256 string                   `json:"structure_artifact_sha256"`
}

type sourceInspectionAbstract struct {
	Text            string          `json:"text"`
	PageStart       int             `json:"page_start"`
	PageEnd         int             `json:"page_end"`
	EvidenceUnitIDs []string        `json:"evidence_unit_ids"`
	PreviewPage     int             `json:"preview_page"`
	PreviewBBox     *sourcemap.BBox `json:"preview_bbox,omitempty"`
}

type sourceInspectionEntry struct {
	EntryID         string          `json:"entry_id"`
	ParentEntryID   string          `json:"parent_entry_id,omitempty"`
	Kind            string          `json:"kind"`
	Heading         string          `json:"heading"`
	HeadingLevel    int             `json:"heading_level,omitempty"`
	PageStart       int             `json:"page_start"`
	PageEnd         int             `json:"page_end"`
	BBox            *sourcemap.BBox `json:"bbox,omitempty"`
	Preview         string          `json:"preview,omitempty"`
	EvidenceUnitIDs []string        `json:"evidence_unit_ids,omitempty"`
	PreviewPage     int             `json:"preview_page,omitempty"`
	PreviewBBox     *sourcemap.BBox `json:"preview_bbox,omitempty"`
	PreviewOmitted  bool            `json:"preview_omitted"`
}

type sourceInspectionCoverage struct {
	RepresentedPageRanges [][2]int `json:"represented_page_ranges"`
	UncoveredPageRanges   [][2]int `json:"uncovered_page_ranges"`
	OmittedEntryCount     int      `json:"omitted_entry_count"`
	OmittedPreviewCount   int      `json:"omitted_preview_count"`
	Truncated             bool     `json:"truncated"`
}

func buildSourceInspectionProjection(authority sourceInspectionAuthority, sourceMap sourcemap.SourceMap, units []sourceInspectionUnit) (json.RawMessage, error) {
	if strings.TrimSpace(authority.SourceID) == "" || strings.TrimSpace(authority.RevisionID) == "" ||
		strings.TrimSpace(authority.MediaType) == "" || len(authority.ArtifactSHA256) != 64 ||
		sourceMap.SourceID != authority.SourceID || sourceMap.RevisionID != authority.RevisionID || sourceMap.PageCount < 1 ||
		len(sourceMap.Entries) == 0 {
		return nil, ErrSourceInspectionUnavailable
	}
	selectedIndexes := selectInspectionEntries(sourceMap.Entries, InspectSourceMaxEntries)
	entries := make([]sourceInspectionEntry, 0, len(selectedIndexes))
	previewAvailable := make([]bool, 0, len(selectedIndexes))
	for _, index := range selectedIndexes {
		entry := sourceMap.Entries[index]
		projected := sourceInspectionEntry{
			EntryID: entry.EntryID, ParentEntryID: entry.ParentEntryID, Kind: entry.Kind,
			Heading: truncateRunes(strings.TrimSpace(entry.Heading), 256), HeadingLevel: entry.HeadingLevel,
			PageStart: entry.PageStart, PageEnd: entry.PageEnd, BBox: cloneSourceBBox(entry.BBox),
		}
		if unit, ok := inspectionPreviewUnit(units, entry.PageStart, entry.PageEnd); ok {
			projected.Preview = truncateRunes(strings.TrimSpace(unit.Text), inspectSourcePreviewRunes)
			projected.EvidenceUnitIDs = []string{unit.ID}
			projected.PreviewPage = unit.Coordinate.Page
			projected.PreviewBBox = evidenceCoordinateBBox(unit.Coordinate)
		}
		projected.PreviewOmitted = projected.Preview == ""
		previewAvailable = append(previewAvailable, projected.Preview != "")
		entries = append(entries, projected)
	}
	selectedEntryIDs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		selectedEntryIDs[entry.EntryID] = true
	}
	for index := range entries {
		if entries[index].ParentEntryID != "" && !selectedEntryIDs[entries[index].ParentEntryID] {
			entries[index].ParentEntryID = ""
		}
	}
	result := sourceInspectionResult{
		ResultVersion: 1,
		Source: sourceInspectionSource{
			SourceID: authority.SourceID, EvidenceRevisionID: authority.RevisionID,
			Title: truncateRunes(strings.TrimSpace(authority.Title), 512), MediaType: authority.MediaType, PageCount: sourceMap.PageCount,
		},
		Navigation: sourceInspectionNavigation{
			Kind: sourceMap.NavigationKind, Confidence: sourceMap.Confidence,
			StructureArtifactSHA256: authority.ArtifactSHA256,
		},
		Entries:             entries,
		Warnings:            boundedInspectionWarnings(sourceMap.Warnings),
		EvidenceEligibility: "navigation_only_use_search_evidence_for_claims",
	}
	if index := abstractInspectionEntry(result.Entries); index >= 0 && result.Entries[index].Preview != "" {
		entry := result.Entries[index]
		result.Abstract = &sourceInspectionAbstract{
			Text: entry.Preview, PageStart: entry.PageStart, PageEnd: entry.PageEnd,
			EvidenceUnitIDs: append([]string(nil), entry.EvidenceUnitIDs...), PreviewPage: entry.PreviewPage,
			PreviewBBox: cloneSourceBBox(entry.PreviewBBox),
		}
	}
	refreshInspectionCoverage(&result, len(sourceMap.Entries), sourceMap.PageCount, previewAvailable)
	return fitSourceInspectionResult(result, len(sourceMap.Entries), sourceMap.PageCount, previewAvailable)
}

func selectInspectionEntries(entries []sourcemap.NavigationEntry, limit int) []int {
	if len(entries) <= limit {
		result := make([]int, len(entries))
		for index := range entries {
			result[index] = index
		}
		return result
	}
	selected := make(map[int]bool, limit)
	add := func(index int) {
		if index >= 0 && index < len(entries) && len(selected) < limit {
			selected[index] = true
		}
	}
	for index, entry := range entries {
		if inspectionHeadingPriority(entry.Heading) < 3 {
			add(index)
		}
	}
	add(0)
	add(len(entries) - 1)
	for slot := 0; slot < limit && len(selected) < limit; slot++ {
		index := slot * (len(entries) - 1) / (limit - 1)
		add(index)
	}
	for index := range entries {
		add(index)
	}
	result := make([]int, 0, len(selected))
	for index := range selected {
		result = append(result, index)
	}
	sort.Ints(result)
	return result
}

func inspectionHeadingPriority(heading string) int {
	value := strings.ToLower(strings.Join(strings.Fields(heading), " "))
	switch {
	case value == "abstract" || value == "摘要" || strings.HasPrefix(value, "abstract "):
		return 0
	case strings.Contains(value, "conclusion") || strings.Contains(value, "concluding") || strings.Contains(value, "结论"):
		return 1
	case strings.Contains(value, "limitation") || strings.Contains(value, "limitations") || strings.Contains(value, "局限"):
		return 2
	default:
		return 3
	}
}

func inspectionPreviewUnit(units []sourceInspectionUnit, startPage, endPage int) (sourceInspectionUnit, bool) {
	for _, unit := range units {
		if unit.Coordinate.Kind != "pdf_region" || unit.Coordinate.Page < startPage || unit.Coordinate.Page > endPage ||
			strings.TrimSpace(unit.ID) == "" || strings.TrimSpace(unit.Text) == "" {
			continue
		}
		if unit.Kind != "heading" {
			return unit, true
		}
	}
	for _, unit := range units {
		if unit.Coordinate.Kind == "pdf_region" && unit.Coordinate.Page >= startPage && unit.Coordinate.Page <= endPage &&
			strings.TrimSpace(unit.ID) != "" && strings.TrimSpace(unit.Text) != "" {
			return unit, true
		}
	}
	return sourceInspectionUnit{}, false
}

func fitSourceInspectionResult(result sourceInspectionResult, totalEntries, pageCount int, previewAvailable []bool) (json.RawMessage, error) {
	for {
		refreshInspectionCoverage(&result, totalEntries, pageCount, previewAvailable)
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(encoded) <= InspectSourceMaxResultBytes {
			return encoded, nil
		}
		longest, longestRunes := -1, 0
		for index := range result.Entries {
			count := utf8.RuneCountInString(result.Entries[index].Preview)
			if count > longestRunes {
				longest, longestRunes = index, count
			}
		}
		if result.Abstract != nil && utf8.RuneCountInString(result.Abstract.Text) > longestRunes {
			result.Abstract.Text = truncateRunes(result.Abstract.Text, maxInt(0, utf8.RuneCountInString(result.Abstract.Text)/2))
			continue
		}
		if longest >= 0 && longestRunes > 0 {
			newLimit := longestRunes / 2
			if newLimit < 48 {
				newLimit = 0
			}
			result.Entries[longest].Preview = truncateRunes(result.Entries[longest].Preview, newLimit)
			if newLimit == 0 {
				result.Entries[longest].PreviewOmitted = true
				result.Entries[longest].EvidenceUnitIDs = nil
				result.Entries[longest].PreviewPage = 0
				result.Entries[longest].PreviewBBox = nil
			}
			continue
		}
		if result.Abstract != nil {
			result.Abstract = nil
			continue
		}
		if len(result.Entries) > 1 {
			result.Entries = result.Entries[:len(result.Entries)-1]
			previewAvailable = previewAvailable[:len(result.Entries)]
			continue
		}
		return nil, ErrSourceInspectionUnavailable
	}
}

func refreshInspectionCoverage(result *sourceInspectionResult, totalEntries, pageCount int, previewAvailable []bool) {
	ranges := make([][2]int, 0, len(result.Entries))
	omittedPreviews := 0
	for index, entry := range result.Entries {
		ranges = append(ranges, [2]int{entry.PageStart, entry.PageEnd})
		if index < len(previewAvailable) && previewAvailable[index] && entry.Preview == "" {
			omittedPreviews++
		}
	}
	represented := mergeInspectionRanges(ranges, pageCount)
	uncovered := complementInspectionRanges(represented, pageCount)
	omittedEntries := totalEntries - len(result.Entries)
	result.Coverage = sourceInspectionCoverage{
		RepresentedPageRanges: represented, UncoveredPageRanges: uncovered,
		OmittedEntryCount: omittedEntries, OmittedPreviewCount: omittedPreviews,
		Truncated: omittedEntries > 0 || omittedPreviews > 0 || len(uncovered) > 0,
	}
}

func mergeInspectionRanges(ranges [][2]int, pageCount int) [][2]int {
	valid := make([][2]int, 0, len(ranges))
	for _, item := range ranges {
		if item[0] < 1 || item[1] < item[0] || item[0] > pageCount {
			continue
		}
		if item[1] > pageCount {
			item[1] = pageCount
		}
		valid = append(valid, item)
	}
	sort.Slice(valid, func(i, j int) bool {
		return valid[i][0] < valid[j][0] || valid[i][0] == valid[j][0] && valid[i][1] < valid[j][1]
	})
	merged := make([][2]int, 0, len(valid))
	for _, item := range valid {
		if len(merged) == 0 || item[0] > merged[len(merged)-1][1]+1 {
			merged = append(merged, item)
			continue
		}
		if item[1] > merged[len(merged)-1][1] {
			merged[len(merged)-1][1] = item[1]
		}
	}
	return merged
}

func complementInspectionRanges(represented [][2]int, pageCount int) [][2]int {
	result := make([][2]int, 0)
	next := 1
	for _, item := range represented {
		if next < item[0] {
			result = append(result, [2]int{next, item[0] - 1})
		}
		if item[1]+1 > next {
			next = item[1] + 1
		}
	}
	if next <= pageCount {
		result = append(result, [2]int{next, pageCount})
	}
	return result
}

func abstractInspectionEntry(entries []sourceInspectionEntry) int {
	for index, entry := range entries {
		if inspectionHeadingPriority(entry.Heading) == 0 {
			return index
		}
	}
	return -1
}

func evidenceCoordinateBBox(coordinate retrieval.EvidenceCoordinate) *sourcemap.BBox {
	if coordinate.Width <= 0 || coordinate.Height <= 0 {
		return nil
	}
	return &sourcemap.BBox{X0: coordinate.X, Y0: coordinate.Y, X1: coordinate.X + coordinate.Width, Y1: coordinate.Y + coordinate.Height}
}

func cloneSourceBBox(source *sourcemap.BBox) *sourcemap.BBox {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func boundedInspectionWarnings(values []string) []string {
	result := make([]string, 0, minInt(len(values), 16))
	for _, value := range values {
		value = truncateRunes(strings.TrimSpace(value), 256)
		if value != "" {
			result = append(result, value)
			if len(result) == 16 {
				break
			}
		}
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
