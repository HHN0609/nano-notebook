package documentreading_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/documentreading"
	"github.com/huangxinxinyu/nano-notebook/internal/documentrender"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type visionStub struct{ calls int }

func (v *visionStub) DescribeImage(_ context.Context, request models.VisionRequest) (models.VisionOutcome, error) {
	v.calls++
	return models.VisionOutcome{Regions: []models.VisionRegion{{
		Text: "Scanned page evidence.", X: 10, Y: 20, Width: float64(request.Width - 20), Height: 40,
	}}}, nil
}

func TestPDFExtractorKeepsNativePagesAndUsesVisionOnlyForTextlessPages(t *testing.T) {
	payload := minimalTextPDF("Native page evidence.", "")
	image := []byte("rendered-png")
	imageDigest := sha256.Sum256(image)
	inputDigest := sha256.Sum256(payload)
	rendered := documentrender.Result{
		Manifest: documentrender.Manifest{
			SchemaVersion: 1, SourceID: "research_document", Format: documentrender.FormatPDF,
			InputSHA256: hex.EncodeToString(inputDigest[:]), RenderConfigID: "render-v1",
		},
		Assets: []documentrender.Asset{{Page: documentrender.Page{
			Ordinal: 2, Width: 612, Height: 792, MediaType: "image/png", Bytes: int64(len(image)),
			SHA256: hex.EncodeToString(imageDigest[:]), Filename: "page-000002.png",
		}, Payload: image}},
	}
	vision := &visionStub{}
	extractor := documentreading.NewPDFExtractor(vision, documentreading.PDFExtractorConfig{
		VisionModel: "vision-model", VisionPromptVersion: "vision-v1", MaxVisionPages: 2,
	})

	artifact, err := extractor.Extract(context.Background(), documentreading.PDFDocument{
		ID: "research_document", Payload: payload, ExtractionConfigID: "research-pdf-v1",
	}, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if vision.calls != 1 || len(artifact.Blocks) != 2 || artifact.Blocks[0].Text != "Native page evidence." ||
		artifact.Blocks[0].Coordinate.Page != 1 || artifact.Blocks[1].Text != "Scanned page evidence." || artifact.Blocks[1].Coordinate.Page != 2 {
		t.Fatalf("calls=%d artifact=%+v", vision.calls, artifact)
	}
}

func TestPDFExtractorSkipsVisionAndRenderingForFullyNativePDF(t *testing.T) {
	payload := minimalTextPDF("First page.", "Second page.")
	vision := &visionStub{}
	extractor := documentreading.NewPDFExtractor(vision, documentreading.PDFExtractorConfig{
		VisionModel: "vision-model", VisionPromptVersion: "vision-v1", MaxVisionPages: 2,
	})

	artifact, err := extractor.Extract(context.Background(), documentreading.PDFDocument{
		ID: "research_document", Payload: payload, ExtractionConfigID: "research-pdf-v1",
	}, documentrender.Result{})
	if err != nil {
		t.Fatal(err)
	}
	if vision.calls != 0 || len(artifact.Blocks) != 2 {
		t.Fatalf("calls=%d artifact=%+v", vision.calls, artifact)
	}
}

func TestPDFExtractorEnforcesVisionBudgetAndRenderedPageIdentity(t *testing.T) {
	payload := minimalTextPDF("")
	for name, extractor := range map[string]*documentreading.PDFExtractor{
		"missing vision config": documentreading.NewPDFExtractor(nil, documentreading.PDFExtractorConfig{}),
		"zero page budget": documentreading.NewPDFExtractor(&visionStub{}, documentreading.PDFExtractorConfig{
			VisionModel: "vision-model", VisionPromptVersion: "vision-v1", MaxVisionPages: -1,
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := extractor.Extract(context.Background(), documentreading.PDFDocument{
				ID: "research_document", Payload: payload, ExtractionConfigID: "research-pdf-v1",
			}, documentrender.Result{}); err == nil {
				t.Fatal("accepted textless PDF without a usable bounded vision path")
			}
		})
	}
}

func minimalTextPDF(pageTexts ...string) []byte {
	objects := make([]string, 3+2*len(pageTexts))
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	kids := make([]string, 0, len(pageTexts))
	for index := range pageTexts {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+index*2))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	objects[2] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	for index, text := range pageTexts {
		pageObject := 4 + index*2
		contentObject := pageObject + 1
		objects[pageObject-1] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentObject)
		content := ""
		if text != "" {
			content = "BT /F1 12 Tf 72 720 Td (" + strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text) + ") Tj ET"
		}
		objects[contentObject-1] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	}
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index < len(offsets); index++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return document.Bytes()
}
