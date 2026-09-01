package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/retrieval"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcemap"
)

func TestInspectSourceActionIsQueryFreeAndMapsAuthorizationFailure(t *testing.T) {
	backend := &inspectSourceBackendStub{result: json.RawMessage(`{"result_version":1}`)}
	action := NewInspectSourceAction(backend)
	if err := action.ValidateInput(json.RawMessage(`{"source_id":"src_ready"}`)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{}`,
		`{"source_id":"src_ready","query":"tell me everything"}`,
		`{"source_id":" "}`,
	} {
		if err := action.ValidateInput(json.RawMessage(invalid)); err == nil {
			t.Fatalf("accepted invalid input %s", invalid)
		}
	}
	result, err := action.Execute(context.Background(), ActionRequest{
		Attempt: Attempt{RunID: "run_research"}, Input: json.RawMessage(`{"source_id":"src_ready"}`),
	})
	if err != nil || result.Status != ActionSucceeded || backend.sourceID != "src_ready" {
		t.Fatalf("result=%+v err=%v source=%q", result, err, backend.sourceID)
	}
	backend.err = ErrSourceNotInspectable
	result, err = action.Execute(context.Background(), ActionRequest{
		Attempt: Attempt{RunID: "run_research"}, Input: json.RawMessage(`{"source_id":"src_other"}`),
	})
	if err != nil || result.Status != ActionDomainError || result.ErrorCode != "source_not_inspectable" {
		t.Fatalf("authorization result=%+v err=%v", result, err)
	}
}

func TestSourceInspectionProjectionIsDeterministicCoverageOrientedAndBounded(t *testing.T) {
	entries := make([]sourcemap.NavigationEntry, 0, 40)
	units := make([]sourceInspectionUnit, 0, 40)
	for page := 1; page <= 40; page++ {
		heading := fmt.Sprintf("Section %02d", page)
		switch page {
		case 1:
			heading = "Abstract"
		case 33:
			heading = "Limitations"
		case 39:
			heading = "Conclusion"
		}
		entries = append(entries, sourcemap.NavigationEntry{
			EntryID: fmt.Sprintf("entry_%02d", page), Kind: "section", Heading: heading,
			HeadingLevel: 1, PageStart: page, PageEnd: page,
		})
		units = append(units, sourceInspectionUnit{
			ID: fmt.Sprintf("ev_%02d", page), Ordinal: page - 1, Kind: "paragraph",
			Text:       strings.Repeat(fmt.Sprintf("original page %d passage ", page), 80),
			Coordinate: retrieval.EvidenceCoordinate{Kind: "pdf_region", Page: page, X: 10, Y: 20, Width: 30, Height: 40},
		})
	}
	input := sourceInspectionAuthority{
		SourceID: "src_ready", RevisionID: "evr_ready", Title: strings.Repeat("Paper ", 100),
		MediaType: "application/pdf", ArtifactSHA256: strings.Repeat("a", 64),
	}
	value := sourcemap.SourceMap{
		SchemaVersion: "nano.source-map.v1", SourceID: input.SourceID, RevisionID: input.RevisionID,
		NavigationKind: sourcemap.NavigationInferredSections, Confidence: sourcemap.ConfidenceMedium,
		PageCount: 40, Entries: entries, Warnings: []string{"section hierarchy was inferred from PDF layout"},
	}
	first, err := buildSourceInspectionProjection(input, value, units)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildSourceInspectionProjection(input, value, units)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("projection is not deterministic")
	}
	if len(first) > InspectSourceMaxResultBytes {
		t.Fatalf("projection bytes=%d", len(first))
	}
	var output struct {
		Abstract *struct {
			Text            string   `json:"text"`
			EvidenceUnitIDs []string `json:"evidence_unit_ids"`
		} `json:"abstract"`
		Entries []struct {
			Heading         string   `json:"heading"`
			Preview         string   `json:"preview"`
			EvidenceUnitIDs []string `json:"evidence_unit_ids"`
			PreviewPage     int      `json:"preview_page"`
			PreviewBBox     *struct {
				X0 float64 `json:"x0"`
			} `json:"preview_bbox"`
		} `json:"entries"`
		Coverage struct {
			OmittedEntryCount   int      `json:"omitted_entry_count"`
			OmittedPreviewCount int      `json:"omitted_preview_count"`
			UncoveredPageRanges [][2]int `json:"uncovered_page_ranges"`
			Truncated           bool     `json:"truncated"`
		} `json:"coverage"`
		EvidenceEligibility string `json:"evidence_eligibility"`
	}
	if err := json.Unmarshal(first, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Entries) > InspectSourceMaxEntries || output.Abstract == nil || len(output.Abstract.EvidenceUnitIDs) != 1 ||
		output.Coverage.OmittedEntryCount == 0 || !output.Coverage.Truncated || len(output.Coverage.UncoveredPageRanges) == 0 ||
		output.EvidenceEligibility != "navigation_only_use_search_evidence_for_claims" {
		t.Fatalf("unexpected bounded projection: %+v", output)
	}
	seen := map[string]bool{}
	for _, entry := range output.Entries {
		seen[entry.Heading] = true
		if entry.Preview != "" && (len(entry.EvidenceUnitIDs) == 0 || entry.PreviewPage == 0 || entry.PreviewBBox == nil) {
			t.Fatalf("preview lost provenance: %+v", entry)
		}
	}
	for _, priority := range []string{"Abstract", "Limitations", "Conclusion"} {
		if !seen[priority] {
			t.Fatalf("priority entry %q was omitted", priority)
		}
	}
}

type inspectSourceBackendStub struct {
	result   json.RawMessage
	err      error
	sourceID string
}

func (b *inspectSourceBackendStub) InspectSource(_ context.Context, _ Attempt, sourceID string) (json.RawMessage, error) {
	b.sourceID = sourceID
	if b.err != nil {
		return nil, b.err
	}
	if len(b.result) == 0 {
		return nil, errors.New("missing stub result")
	}
	return append(json.RawMessage(nil), b.result...), nil
}
