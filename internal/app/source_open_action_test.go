package app

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
)

func TestSourceOpenActionFormatPolicy(t *testing.T) {
	tests := []struct {
		name  string
		item  source.Source
		state string
		want  sourceOpenAction
	}{
		{name: "web", item: source.Source{ID: "web", InputKind: "url", Format: source.FormatHTML, FinalURL: "https://example.com/final"}, state: "ready", want: sourceOpenAction{Kind: "external", Href: "https://example.com/final"}},
		{name: "youtube", item: source.Source{ID: "youtube", InputKind: "url", Format: source.FormatYouTube, FinalURL: "https://www.youtube.com/watch?v=abc"}, state: "ready", want: sourceOpenAction{Kind: "external", Href: "https://www.youtube.com/watch?v=abc"}},
		{name: "txt", item: source.Source{ID: "txt", InputKind: "file", Format: source.FormatTXT, MediaType: "text/plain"}, state: "ready", want: inlineAction("txt", "text/plain")},
		{name: "markdown", item: source.Source{ID: "markdown", InputKind: "file", Format: source.FormatMarkdown, MediaType: "text/markdown"}, state: "ready", want: inlineAction("markdown", "text/markdown")},
		{name: "pdf", item: source.Source{ID: "pdf", InputKind: "file", Format: source.FormatPDF, MediaType: "application/pdf"}, state: "ready", want: inlineAction("pdf", "application/pdf")},
		{name: "mp3", item: source.Source{ID: "mp3", InputKind: "file", Format: source.FormatMP3, MediaType: "audio/mpeg"}, state: "ready", want: inlineAction("mp3", "audio/mpeg")},
		{name: "wav", item: source.Source{ID: "wav", InputKind: "file", Format: source.FormatWAV, MediaType: "audio/x-wav"}, state: "ready", want: inlineAction("wav", "audio/x-wav")},
		{name: "m4a", item: source.Source{ID: "m4a", InputKind: "file", Format: source.FormatM4A, MediaType: "audio/mp4"}, state: "ready", want: inlineAction("m4a", "audio/mp4")},
		{name: "png", item: source.Source{ID: "png", InputKind: "file", Format: source.FormatPNG, MediaType: "image/png"}, state: "ready", want: inlineAction("png", "image/png")},
		{name: "jpeg", item: source.Source{ID: "jpeg", InputKind: "file", Format: source.FormatJPEG, MediaType: "image/jpeg"}, state: "ready", want: inlineAction("jpeg", "image/jpeg")},
		{name: "webp", item: source.Source{ID: "webp", InputKind: "file", Format: source.FormatWebP, MediaType: "image/webp"}, state: "ready", want: inlineAction("webp", "image/webp")},
		{name: "docx", item: source.Source{ID: "docx", InputKind: "file", Format: source.FormatDOCX, MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, state: "ready", want: sourceOpenAction{Kind: "none"}},
		{name: "pptx", item: source.Source{ID: "pptx", InputKind: "file", Format: source.FormatPPTX, MediaType: "application/vnd.openxmlformats-officedocument.presentationml.presentation"}, state: "ready", want: sourceOpenAction{Kind: "none"}},
		{name: "not ready", item: source.Source{ID: "pending", InputKind: "file", Format: source.FormatPDF, MediaType: "application/pdf"}, state: "processing", want: sourceOpenAction{Kind: "none"}},
		{name: "mismatched media", item: source.Source{ID: "mismatch", InputKind: "file", Format: source.FormatPDF, MediaType: "text/html"}, state: "ready", want: sourceOpenAction{Kind: "none"}},
		{name: "active file", item: source.Source{ID: "active", InputKind: "file", Format: source.FormatHTML, MediaType: "text/html"}, state: "ready", want: sourceOpenAction{Kind: "none"}},
		{name: "invalid external", item: source.Source{ID: "invalid", InputKind: "url", Format: source.FormatHTML, FinalURL: "javascript:alert(1)"}, state: "ready", want: sourceOpenAction{Kind: "none"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sourceOpenActionFor(test.item, test.state); got != test.want {
				t.Fatalf("sourceOpenActionFor()=%+v want=%+v", got, test.want)
			}
		})
	}
}

func inlineAction(sourceID, mediaType string) sourceOpenAction {
	return sourceOpenAction{Kind: "inline_original", Href: "/api/v1/sources/" + sourceID + "/original-asset", MediaType: mediaType}
}
