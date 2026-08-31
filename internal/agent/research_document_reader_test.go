package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/documentreading"
	"github.com/huangxinxinyu/nano-notebook/internal/documentrender"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

type acquiringStub struct {
	content webreader.Content
	err     error
	calls   int
}

func (s *acquiringStub) Acquire(context.Context, webreader.Request) (webreader.Content, error) {
	s.calls++
	return s.content, s.err
}

type rendererStub struct{ calls int }

func (s *rendererStub) Render(context.Context, documentrender.Request, []byte) (documentrender.Result, error) {
	s.calls++
	return documentrender.Result{}, errors.New("renderer must not be called for fully native PDFs")
}

func TestResearchURLContentReaderStoresPDFAndReusesItOnCrashReplay(t *testing.T) {
	pdf := researchTextPDF("First page evidence.", "Second page evidence.")
	acquirer := &acquiringStub{content: webreader.Content{
		MediaType: webreader.MediaTypePDF, FinalURL: "https://arxiv.org/pdf/1234.5678", PDF: pdf,
	}}
	renderer := &rendererStub{}
	reader := NewResearchURLContentReader(acquirer, renderer, documentreading.NewPDFExtractor(nil, documentreading.PDFExtractorConfig{}), objectstore.NewMemoryStore(), ResearchURLReaderConfig{
		ExtractionConfigID: "research-pdf-v1", RenderConfigID: "render-v1", RenderMaxPages: 500,
		RenderDPI: 144, RenderMaxPixelsPerPage: 20_000_000, RenderMaxOutputBytes: 256 << 20,
		MaxNormalizedRunes: 20_000_000, MaxModelChars: ResearchReadURLMaxChars, MaxPageRead: 20,
	})
	request := ResearchURLReadRequest{URL: "https://arxiv.org/abs/1234.5678", RunID: "run_a", ActionID: "decision:1/action:0"}

	first, err := reader.Read(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaType != webreader.MediaTypePDF || first.Engine != "pdf-native" || first.PageCount != 2 || first.DocumentHandle == "" || first.FinalURL != "https://arxiv.org/pdf/1234.5678" ||
		!strings.Contains(first.Markdown, "<!-- nano-pdf-page:1 -->\n\nFirst page evidence.") ||
		!strings.Contains(first.Markdown, "<!-- nano-pdf-page:2 -->\n\nSecond page evidence.") || renderer.calls != 0 {
		t.Fatalf("result=%+v render_calls=%d", first, renderer.calls)
	}

	acquirer.err = errors.New("network must not be used during replay")
	second, err := reader.Read(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if acquirer.calls != 1 || second.DocumentHandle != first.DocumentHandle || second.Markdown != first.Markdown {
		t.Fatalf("calls=%d first=%+v second=%+v", acquirer.calls, first, second)
	}
}

func TestResearchURLContentReaderReadsBoundedPagesOnlyWithinOwningRun(t *testing.T) {
	pdf := researchTextPDF("Page one.", "Page two.", "Page three.")
	store := objectstore.NewMemoryStore()
	reader := NewResearchURLContentReader(&acquiringStub{content: webreader.Content{
		MediaType: webreader.MediaTypePDF, FinalURL: "https://example.com/paper.pdf", PDF: pdf,
	}}, &rendererStub{}, documentreading.NewPDFExtractor(nil, documentreading.PDFExtractorConfig{}), store, ResearchURLReaderConfig{
		ExtractionConfigID: "research-pdf-v1", RenderConfigID: "render-v1", RenderMaxPages: 500,
		RenderDPI: 144, RenderMaxPixelsPerPage: 20_000_000, RenderMaxOutputBytes: 256 << 20,
		MaxNormalizedRunes: 20_000_000, MaxModelChars: ResearchReadURLMaxChars, MaxPageRead: 2,
	})
	read, err := reader.Read(context.Background(), ResearchURLReadRequest{
		URL: "https://example.com/paper.pdf", RunID: "run_owner", ActionID: "decision:1/action:0",
	})
	if err != nil {
		t.Fatal(err)
	}

	pages, err := reader.ReadPages(context.Background(), ResearchPageReadRequest{
		RunID: "run_owner", DocumentHandle: read.DocumentHandle, StartPage: 2, EndPage: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages.StartPage != 2 || pages.EndPage != 3 || strings.Contains(pages.Markdown, "Page one.") ||
		!strings.Contains(pages.Markdown, "<!-- nano-pdf-page:2 -->") || !strings.Contains(pages.Markdown, "Page three.") {
		t.Fatalf("pages=%+v", pages)
	}
	if _, err := reader.ReadPages(context.Background(), ResearchPageReadRequest{
		RunID: "run_other", DocumentHandle: read.DocumentHandle, StartPage: 1, EndPage: 1,
	}); !errors.Is(err, ErrResearchDocumentNotFound) {
		t.Fatalf("cross-run err=%v", err)
	}
	if _, err := reader.ReadPages(context.Background(), ResearchPageReadRequest{
		RunID: "run_owner", DocumentHandle: read.DocumentHandle, StartPage: 1, EndPage: 3,
	}); !errors.Is(err, ErrResearchPageRangeInvalid) {
		t.Fatalf("oversized range err=%v", err)
	}
}

func TestResearchURLContentReaderBoundsPageReadMarkdown(t *testing.T) {
	pdf := researchTextPDF(strings.Repeat("evidence ", 100))
	reader := NewResearchURLContentReader(&acquiringStub{content: webreader.Content{
		MediaType: webreader.MediaTypePDF, FinalURL: "https://example.com/long.pdf", PDF: pdf,
	}}, &rendererStub{}, documentreading.NewPDFExtractor(nil, documentreading.PDFExtractorConfig{}), objectstore.NewMemoryStore(), ResearchURLReaderConfig{
		ExtractionConfigID: "research-pdf-v1", RenderConfigID: "render-v1", RenderMaxPages: 500,
		RenderDPI: 144, RenderMaxPixelsPerPage: 20_000_000, RenderMaxOutputBytes: 256 << 20,
		MaxNormalizedRunes: 20_000_000, MaxModelChars: 80, MaxPageRead: 20,
	})
	read, err := reader.Read(context.Background(), ResearchURLReadRequest{
		URL: "https://example.com/long.pdf", RunID: "run_bounded", ActionID: "decision:1/action:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	pages, err := reader.ReadPages(context.Background(), ResearchPageReadRequest{
		RunID: "run_bounded", DocumentHandle: read.DocumentHandle, StartPage: 1, EndPage: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !pages.Truncated || !strings.Contains(pages.Markdown, "[content truncated; use read_document_pages]") {
		t.Fatalf("pages=%+v", pages)
	}
}

func TestResearchURLContentReaderAppliesOneReadTimeout(t *testing.T) {
	acquirer := blockingAcquirer{}
	reader := NewResearchURLContentReader(acquirer, nil, documentreading.NewPDFExtractor(nil, documentreading.PDFExtractorConfig{}), objectstore.NewMemoryStore(), ResearchURLReaderConfig{
		ExtractionConfigID: "research-pdf-v1", RenderConfigID: "render-v1", RenderMaxPages: 500,
		RenderDPI: 144, RenderMaxPixelsPerPage: 20_000_000, RenderMaxOutputBytes: 256 << 20,
		MaxNormalizedRunes: 20_000_000, MaxModelChars: ResearchReadURLMaxChars, MaxPageRead: 20,
		ReadTimeout: 20 * time.Millisecond,
	})
	_, err := reader.Read(context.Background(), ResearchURLReadRequest{
		URL: "https://example.com/paper.pdf", RunID: "run_timeout", ActionID: "decision:1/action:0",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

type blockingAcquirer struct{}

func (blockingAcquirer) Acquire(ctx context.Context, _ webreader.Request) (webreader.Content, error) {
	<-ctx.Done()
	return webreader.Content{}, ctx.Err()
}

func researchTextPDF(pageTexts ...string) []byte {
	objects := make([]string, 3+2*len(pageTexts))
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	kids := make([]string, 0, len(pageTexts))
	for index := range pageTexts {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+index*2))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))
	objects[2] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	for index, text := range pageTexts {
		pageObject, contentObject := 4+index*2, 5+index*2
		objects[pageObject-1] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentObject)
		content := "BT /F1 12 Tf 72 720 Td (" + strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text) + ") Tj ET"
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
