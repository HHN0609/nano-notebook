package normalize_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

func TestHTMLAdapterExtractsStablePrimaryStructureWithoutLivePageBehavior(t *testing.T) {
	payload := []byte(`<!doctype html><html><head><title>Ignored title</title><style>.x{}</style></head><body>
		<nav>Navigation noise</nav><main>
			<h1>Research &amp; Results</h1>
			<p>Primary <strong>paragraph</strong>.</p>
			<ul><li>First item</li><li>Second item</li></ul>
			<table><tr><th>Metric</th><th>Value</th></tr><tr><td>Recall</td><td>0.91</td></tr></table>
			<script>steal()</script><aside>Related noise</aside>
		</main><footer>Footer noise</footer></body></html>`)
	input := normalize.Input{SourceID: "src_html", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: payload}
	first, err := normalize.HTML(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalize.HTML(input)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"heading", "paragraph", "list", "list", "table"}
	wantText := []string{"Research & Results", "Primary paragraph.", "First item", "Second item", "Metric | Value\nRecall | 0.91"}
	if len(first.Blocks) != len(wantKinds) || first.SHA256 != second.SHA256 || !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) {
		t.Fatalf("HTML artifact=%+v", first)
	}
	for index, block := range first.Blocks {
		if block.Kind != wantKinds[index] || block.Text != wantText[index] || block.Coordinate == nil ||
			block.Coordinate.Kind != "html_block" || block.Coordinate.Block != index+1 {
			t.Fatalf("block %d=%+v", index, block)
		}
	}
	for _, forbidden := range []string{"Navigation noise", "steal", "Related noise", "Footer noise", "Ignored title"} {
		if bytes.Contains(first.CanonicalJSON, []byte(forbidden)) {
			t.Fatalf("HTML artifact retained excluded content %q", forbidden)
		}
	}
}

func TestHTMLAdapterUsesArticleBeforeBodyFallback(t *testing.T) {
	artifact, err := normalize.HTML(normalize.Input{
		SourceID: "src_article", ExtractionConfigID: "extract-html-primary-v1", Format: "html",
		Payload: []byte(`<html><body><p>Body noise.</p><article><h2>Article</h2><p>Kept.</p></article></body></html>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Text != "Article\n\nKept." || artifact.Blocks[0].HeadingLevel != 2 {
		t.Fatalf("article artifact=%+v", artifact)
	}
}

func TestHTMLAdapterRejectsInvalidIdentityEncodingEmptyAndExcessiveDOM(t *testing.T) {
	tooManyNodes := bytes.Repeat([]byte("<span>x</span>"), 100_001)
	tooDeep := strings.Repeat("<div>", 300) + "<p>x</p>" + strings.Repeat("</div>", 300)
	tests := []normalize.Input{
		{SourceID: "", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: []byte("<p>x</p>")},
		{SourceID: "bad_utf8", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: []byte{'<', 'p', '>', 0xff}},
		{SourceID: "empty", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: []byte("<html><body><nav>Only navigation.</nav></body></html>")},
		{SourceID: "large_dom", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: append([]byte("<html><body><main><p>"), tooManyNodes...)},
		{SourceID: "deep_dom", ExtractionConfigID: "extract-html-primary-v1", Format: "html", Payload: []byte(tooDeep)},
	}
	for _, input := range tests {
		if _, err := normalize.HTML(input); err == nil {
			t.Fatalf("HTML accepted invalid %s input", input.SourceID)
		}
	}
}

func TestHTMLAdapterClassifiesDOMBudgetSeparatelyFromMalformedContent(t *testing.T) {
	payload := append([]byte("<html><body><main>"), bytes.Repeat([]byte("<span>x</span>"), 100_001)...)
	_, err := normalize.HTML(normalize.Input{SourceID: "budget", ExtractionConfigID: "html-v1", Format: "html", Payload: payload})
	if !errors.Is(err, normalize.ErrProcessingBudget) {
		t.Fatalf("HTML budget error=%v", err)
	}
}

func TestHTMLPrimaryV2SelectsDenseContentAndRemovesRepeatedBoilerplate(t *testing.T) {
	payload := []byte(`<!doctype html><html><body>
		<header><a href="/">Home</a><a href="/pricing">Pricing</a></header>
		<div class="layout"><aside>Related links and newsletter</aside><div class="post-content">
			<h1>Deterministic web evidence</h1>
			<p>This paragraph contains the durable primary material that a notebook user selected for later grounded retrieval.</p>
			<p>Repeated subscription template should disappear from the normalized evidence.</p>
			<p>Repeated subscription template should disappear from the normalized evidence.</p>
			<pre><code>result := search(query)</code></pre>
			<table><tr><th>Stage</th><th>State</th></tr><tr><td>Import</td><td>Ready</td></tr></table>
		</div></div><footer>Privacy Terms Contact</footer></body></html>`)
	artifact, err := normalize.HTML(normalize.Input{
		SourceID: "src_html_v2", ExtractionConfigID: "html-primary-v2", Format: "html", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ExtractionConfigID != "html-primary-v2" || !strings.Contains(artifact.Text, "durable primary material") ||
		strings.Contains(artifact.Text, "Home Pricing") || strings.Contains(artifact.Text, "Privacy Terms") ||
		strings.Count(artifact.Text, "Repeated subscription template") != 1 {
		t.Fatalf("v2 artifact text=%q", artifact.Text)
	}
	wantKinds := []string{"heading", "paragraph", "paragraph", "code", "table"}
	if len(artifact.Blocks) != len(wantKinds) {
		t.Fatalf("v2 blocks=%+v", artifact.Blocks)
	}
	for index, kind := range wantKinds {
		if artifact.Blocks[index].Kind != kind || artifact.Blocks[index].Coordinate == nil || artifact.Blocks[index].Coordinate.Block != index+1 {
			t.Fatalf("v2 block %d=%+v", index, artifact.Blocks[index])
		}
	}
}

func TestHTMLPrimaryV2RejectsLoginLinkFarmAndAbnormalDuplication(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{name: "login wall", html: `<html><body><main><h1>Sign in</h1><p>Please log in to continue and access this content.</p></main></body></html>`},
		{name: "link farm", html: `<html><body><main><h1>Directory</h1><p><a href="/1">one linked destination</a> <a href="/2">two linked destination</a> <a href="/3">three linked destination</a> <a href="/4">four linked destination</a></p></main></body></html>`},
		{name: "duplicates", html: `<html><body><main><h1>Article</h1><p>Identical repeated boilerplate material with enough words to pass the minimum useful content boundary.</p><p>Identical repeated boilerplate material with enough words to pass the minimum useful content boundary.</p><p>Identical repeated boilerplate material with enough words to pass the minimum useful content boundary.</p></main></body></html>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalize.HTML(normalize.Input{SourceID: "src_bad_v2", ExtractionConfigID: "html-primary-v2", Format: "html", Payload: []byte(test.html)})
			if !errors.Is(err, normalize.ErrHTMLQuality) {
				t.Fatalf("quality error=%v", err)
			}
		})
	}
}

func TestHTMLPrimaryV2PublishesThinContentAsPartialCoverageWarning(t *testing.T) {
	artifact, err := normalize.HTML(normalize.Input{
		SourceID: "src_thin_v2", ExtractionConfigID: "html-primary-v2", Format: "html",
		Payload: []byte(`<html><body><article><h1>Release note</h1><p>The stable release fixes notebook search and source import behavior for editors.</p></article></body></html>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Coverage.Status != "partial" || len(artifact.Coverage.Gaps) != 1 || artifact.Coverage.Gaps[0].Reason != "thin_primary_content" {
		t.Fatalf("coverage=%+v", artifact.Coverage)
	}
}
