package sourcemap

import (
	"bytes"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

func TestBuildArtifactPrefersEmbeddedOutlineAndPinsRevisionIdentity(t *testing.T) {
	document := parserDocument(4)
	document.Outline = []OutlineEntry{{Level: 1, Title: "Methods", Page: 2}, {Level: 1, Title: "Conclusion", Page: 4}}
	result, err := BuildArtifact(BuildInput{
		SourceID: "src_pdf", RevisionID: "rev_pdf", OriginalSHA256: strings.Repeat("a", 64),
		Parser:     &ParseResult{Document: document, CanonicalJSON: []byte(`{"parser":true}`), SHA256: strings.Repeat("b", 64)},
		Normalized: normalizedPDF(4, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Map.NavigationKind != NavigationEmbeddedOutline || result.Map.Confidence != ConfidenceHigh ||
		result.Map.SourceID != "src_pdf" || result.Map.RevisionID != "rev_pdf" || len(result.Map.Entries) != 2 ||
		result.Map.Entries[0].PageStart != 2 || result.Map.Entries[0].PageEnd != 3 || result.Map.Entries[1].PageEnd != 4 {
		t.Fatalf("map=%+v", result.Map)
	}
	if len(result.CanonicalJSON) == 0 || len(result.SHA256) != 64 || result.Map.ParserArtifactSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuildArtifactUsesInferredHeadingsBeforePageSamples(t *testing.T) {
	document := parserDocument(5)
	document.Pages[0].Blocks[0].Kind = "heading"
	document.Pages[0].Blocks[0].HeadingLevel = 1
	document.Pages[0].Blocks[0].Text = "Introduction"
	document.Pages[3].Blocks[0].Kind = "heading"
	document.Pages[3].Blocks[0].HeadingLevel = 1
	document.Pages[3].Blocks[0].Text = "Limitations"
	result, err := BuildArtifact(BuildInput{
		SourceID: "src_pdf", RevisionID: "rev_pdf", OriginalSHA256: strings.Repeat("a", 64),
		Parser:     &ParseResult{Document: document, CanonicalJSON: []byte(`{"parser":true}`), SHA256: strings.Repeat("b", 64)},
		Normalized: normalizedPDF(5, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Map.NavigationKind != NavigationInferredSections || result.Map.Confidence != ConfidenceMedium || len(result.Map.Entries) != 2 ||
		result.Map.Entries[0].Heading != "Introduction" || result.Map.Entries[0].PageEnd != 3 || result.Map.Entries[1].Heading != "Limitations" {
		t.Fatalf("map=%+v", result.Map)
	}
}

func TestBuildArtifactFallsBackToDeterministicDistributedPageSamples(t *testing.T) {
	input := BuildInput{
		SourceID: "src_scan", RevisionID: "rev_scan", OriginalSHA256: strings.Repeat("c", 64),
		Normalized: normalizedPDF(9, true), ParserFailureCode: "parser_unavailable",
	}
	first, err := BuildArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Map.NavigationKind != NavigationPageSamples || first.Map.Confidence != ConfidenceLow || len(first.Map.Entries) < 3 ||
		first.Map.Entries[0].PageStart != 1 || first.Map.Entries[len(first.Map.Entries)-1].PageEnd != 9 ||
		!containsPage(first.Map.Entries, 5) || len(first.Map.Warnings) == 0 {
		t.Fatalf("map=%+v", first.Map)
	}
	if !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) || first.SHA256 != second.SHA256 {
		t.Fatal("page-sample artifact is not deterministic")
	}
}

func TestDecodeArtifactRejectsIdentityHashAndSchemaDrift(t *testing.T) {
	artifact, err := BuildArtifact(BuildInput{
		SourceID: "src_pdf", RevisionID: "rev_pdf", OriginalSHA256: strings.Repeat("a", 64),
		Parser:     &ParseResult{Document: parserDocument(2), CanonicalJSON: []byte(`{"parser":true}`), SHA256: strings.Repeat("b", 64)},
		Normalized: normalizedPDF(2, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArtifact(artifact.CanonicalJSON, ArtifactIdentity{
		SourceID: "src_pdf", RevisionID: "rev_pdf", SHA256: artifact.SHA256, Bytes: len(artifact.CanonicalJSON),
	})
	if err != nil || decoded.MapID != artifact.Map.MapID {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	for name, payload := range map[string][]byte{
		"unknown field": append(append([]byte(nil), artifact.CanonicalJSON[:len(artifact.CanonicalJSON)-1]...), []byte(`,"secret":"drift"}`)...),
		"trailing json": append(append([]byte(nil), artifact.CanonicalJSON...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeArtifact(payload, ArtifactIdentity{
				SourceID: "src_pdf", RevisionID: "rev_pdf", SHA256: sha256Hex(payload), Bytes: len(payload),
			}); err == nil {
				t.Fatal("accepted drifted Source Map artifact")
			}
		})
	}
	if _, err := DecodeArtifact(artifact.CanonicalJSON, ArtifactIdentity{
		SourceID: "src_other", RevisionID: "rev_pdf", SHA256: artifact.SHA256, Bytes: len(artifact.CanonicalJSON),
	}); err == nil {
		t.Fatal("accepted mismatched Source identity")
	}
}

func parserDocument(pageCount int) Document {
	pages := make([]Page, pageCount)
	for index := range pages {
		pages[index] = Page{Ordinal: index + 1, Width: 612, Height: 792, Blocks: []Block{{
			ReadingOrder: 0, Kind: "paragraph", Text: "Parser page text.", BBox: BBox{X0: 72, Y0: 72, X1: 540, Y1: 100},
		}}}
	}
	return Document{
		SchemaVersion: 1, SourceID: "src_pdf", InputSHA256: strings.Repeat("a", 64), ParserIdentity: ParserIdentity,
		ParserVersion: "1.28.2", ParserPolicyID: ParserPolicyNoOCR, PageCount: pageCount, Pages: pages,
	}
}

func normalizedPDF(pageCount int, scanned bool) normalize.Artifact {
	blocks := make([]normalize.Block, pageCount)
	var text strings.Builder
	for index := range blocks {
		value := "Evidence page " + string(rune('1'+index)) + "."
		start := len([]rune(text.String()))
		text.WriteString(value)
		end := len([]rune(text.String()))
		if index+1 < pageCount {
			text.WriteByte('\n')
		}
		blocks[index] = normalize.Block{
			ID: "block_" + leftPad6(index+1), Ordinal: index, Kind: "paragraph", Text: value, StartRune: start, EndRune: end,
			Coordinate: &normalize.SourceCoordinate{Kind: "pdf_region", Page: index + 1, X: 10, Y: 20, Width: 100, Height: 20},
		}
	}
	_ = scanned
	return normalize.Artifact{SchemaVersion: "nano.normalized-source.v1", SourceID: "src_scan", ExtractionConfigID: "extract-v1", Format: "pdf", Text: text.String(), Blocks: blocks}
}

func leftPad6(value int) string {
	return strings.Repeat("0", 6-len(string(rune('0'+value)))) + string(rune('0'+value))
}

func containsPage(entries []NavigationEntry, page int) bool {
	for _, entry := range entries {
		if entry.PageStart <= page && page <= entry.PageEnd {
			return true
		}
	}
	return false
}
