package sourcemap

import (
	"strings"
	"testing"
)

func TestParseRequestRequiresPinnedOCRFreeBoundedPolicy(t *testing.T) {
	valid := ParseRequest{
		SchemaVersion: 1, SourceID: "src_pdf", InputSHA256: strings.Repeat("a", 64), InputBytes: 1024,
		ParserPolicyID: "pdf-structure-no-ocr-v1", OCR: false, MaxPages: 500, MaxOutputBytes: 16 << 20,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for name, mutate := range map[string]func(*ParseRequest){
		"ocr":        func(value *ParseRequest) { value.OCR = true },
		"policy":     func(value *ParseRequest) { value.ParserPolicyID = "model-selected" },
		"pages":      func(value *ParseRequest) { value.MaxPages = 501 },
		"output":     func(value *ParseRequest) { value.MaxOutputBytes = 0 },
		"input hash": func(value *ParseRequest) { value.InputSHA256 = "not-a-hash" },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatalf("accepted invalid request: %#v", request)
			}
		})
	}
}

func TestValidateDocumentRequiresOrderedPagesBlocksAndBoundedBBoxes(t *testing.T) {
	request := ParseRequest{
		SchemaVersion: 1, SourceID: "src_pdf", InputSHA256: strings.Repeat("a", 64), InputBytes: 1024,
		ParserPolicyID: "pdf-structure-no-ocr-v1", MaxPages: 10, MaxOutputBytes: 1 << 20,
	}
	document := Document{
		SchemaVersion: 1, SourceID: request.SourceID, InputSHA256: request.InputSHA256,
		ParserIdentity: "pymupdf4llm", ParserVersion: "1.28.2", ParserPolicyID: request.ParserPolicyID,
		PageCount: 2,
		Outline:   []OutlineEntry{{Level: 1, Title: "Methods", Page: 2}},
		Pages: []Page{
			{Ordinal: 1, Width: 612, Height: 792, Blocks: []Block{{ReadingOrder: 0, Kind: "paragraph", Text: "Abstract text.", BBox: BBox{X0: 72, Y0: 80, X1: 540, Y1: 120}}}},
			{Ordinal: 2, Width: 612, Height: 792, Blocks: []Block{{ReadingOrder: 0, Kind: "heading", Text: "Methods", HeadingLevel: 1, BBox: BBox{X0: 72, Y0: 80, X1: 300, Y1: 110}}}},
		},
	}
	if err := ValidateDocument(request, document); err != nil {
		t.Fatalf("ValidateDocument: %v", err)
	}
	for name, mutate := range map[string]func(*Document){
		"identity": func(value *Document) { value.InputSHA256 = strings.Repeat("b", 64) },
		"page gap": func(value *Document) { value.Pages[1].Ordinal = 3 },
		"bbox":     func(value *Document) { value.Pages[0].Blocks[0].BBox.X1 = 700 },
		"order":    func(value *Document) { value.Pages[0].Blocks[0].ReadingOrder = 1 },
		"heading":  func(value *Document) { value.Pages[1].Blocks[0].HeadingLevel = 7 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := document
			candidate.Pages = append([]Page(nil), document.Pages...)
			candidate.Pages[0].Blocks = append([]Block(nil), document.Pages[0].Blocks...)
			candidate.Pages[1].Blocks = append([]Block(nil), document.Pages[1].Blocks...)
			mutate(&candidate)
			if err := ValidateDocument(request, candidate); err == nil {
				t.Fatalf("accepted invalid document: %#v", candidate)
			}
		})
	}
}
