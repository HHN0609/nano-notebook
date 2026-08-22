package sourceprocessing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourceprocessing"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

// jsShellHTML fails the html-primary-v2 quality gate: no primary content.
const jsShellHTML = `<!doctype html><html><head><title>App</title></head><body><div id="root"></div><script>bootstrap()</script></body></html>`

const articleHTML = `<!doctype html><html><body><main><p>` +
	`This article carries enough primary text to pass the deterministic quality gate without any fallback.` +
	`</p></main></body></html>`

const readerMarkdown = "# Rescued Heading\n\n" +
	"The web reader returned enough markdown body text to clear the rescue floor, so the source publishes."

type fakeWebReader struct {
	calls int
	page  webreader.Page
	err   error
}

func (f *fakeWebReader) Parse(_ context.Context, _ webreader.Request) (webreader.Page, error) {
	f.calls++
	return f.page, f.err
}

func htmlSource(finalURL string) source.Source {
	return source.Source{ID: "src_html", Format: source.FormatHTML, FinalURL: finalURL}
}

func TestNativeExtractorHTMLFallsBackToWebReaderOnQualityGateRejection(t *testing.T) {
	reader := &fakeWebReader{page: webreader.Page{Content: readerMarkdown, Engine: "browser"}}
	extractor := sourceprocessing.NewNativeExtractorWithWebReader(nil, reader, sourceprocessing.NativeExtractorConfig{})

	artifact, err := extractor.Extract(context.Background(), htmlSource("https://example.com/app"), []byte(jsShellHTML), "extract-v1")
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 {
		t.Fatalf("web reader calls=%d", reader.calls)
	}
	if artifact.ExtractionConfigID != "html-reader-v1" || artifact.Format != "markdown" {
		t.Fatalf("artifact config=%s format=%s", artifact.ExtractionConfigID, artifact.Format)
	}
	if err := normalize.Validate(artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Blocks[0].Kind != "heading" || artifact.Blocks[0].HeadingLevel != 1 {
		t.Fatalf("blocks[0]=%+v", artifact.Blocks[0])
	}
}

func TestNativeExtractorHTMLKeepsPrimaryExtractionWhenQualityGatePasses(t *testing.T) {
	reader := &fakeWebReader{page: webreader.Page{Content: readerMarkdown}}
	extractor := sourceprocessing.NewNativeExtractorWithWebReader(nil, reader, sourceprocessing.NativeExtractorConfig{})

	artifact, err := extractor.Extract(context.Background(), htmlSource("https://example.com/post"), []byte(articleHTML), "extract-v1")
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 0 {
		t.Fatalf("web reader calls=%d", reader.calls)
	}
	if artifact.ExtractionConfigID != "html-primary-v2" || artifact.Format != "html" {
		t.Fatalf("artifact config=%s format=%s", artifact.ExtractionConfigID, artifact.Format)
	}
}

func TestNativeExtractorHTMLFallbackFailuresSurfaceQualityGateCause(t *testing.T) {
	base := sourceprocessing.NewNativeExtractorWithWebReader(nil, nil, sourceprocessing.NativeExtractorConfig{})
	_, legacyErr := base.Extract(context.Background(), htmlSource("https://example.com/app"), []byte(jsShellHTML), "extract-v1")
	if !errors.Is(legacyErr, normalize.ErrHTMLQuality) {
		t.Fatalf("legacy err=%v", legacyErr)
	}

	for name, reader := range map[string]*fakeWebReader{
		"sidecar error": {err: errors.New("upstream_failed: timed out")},
		"thin content":  {page: webreader.Page{Content: "too short"}},
		"empty content": {page: webreader.Page{Content: "   "}},
	} {
		extractor := sourceprocessing.NewNativeExtractorWithWebReader(nil, reader, sourceprocessing.NativeExtractorConfig{})
		_, err := extractor.Extract(context.Background(), htmlSource("https://example.com/app"), []byte(jsShellHTML), "extract-v1")
		if !errors.Is(err, normalize.ErrHTMLQuality) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestNativeExtractorHTMLFallbackRequiresFinalURL(t *testing.T) {
	reader := &fakeWebReader{page: webreader.Page{Content: readerMarkdown}}
	extractor := sourceprocessing.NewNativeExtractorWithWebReader(nil, reader, sourceprocessing.NativeExtractorConfig{})

	_, err := extractor.Extract(context.Background(), htmlSource(""), []byte(jsShellHTML), "extract-v1")
	if !errors.Is(err, normalize.ErrHTMLQuality) {
		t.Fatalf("err=%v", err)
	}
	if reader.calls != 0 {
		t.Fatalf("web reader calls=%d", reader.calls)
	}
}

func TestNativeExtractorHTMLFallbackDoesNotRetryStructuralFailures(t *testing.T) {
	reader := &fakeWebReader{page: webreader.Page{Content: readerMarkdown}}
	extractor := sourceprocessing.NewNativeExtractorWithWebReader(nil, reader, sourceprocessing.NativeExtractorConfig{})

	// Non-UTF8 payload fails input validation (not the quality gate), so the
	// web reader must never be consulted.
	_, err := extractor.Extract(context.Background(), htmlSource("https://example.com/app"), []byte{0xff, 0xfe, 0x00}, "extract-v1")
	if err == nil {
		t.Fatal("expected error")
	}
	if reader.calls != 0 {
		t.Fatalf("web reader calls=%d", reader.calls)
	}
}
