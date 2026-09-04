package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProjectPublicActivitySanitizesURLAndHidesUnknownArguments(t *testing.T) {
	started := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	activity := projectPublicActivity(AcceptedAction{
		Name: "read_url", Input: json.RawMessage(`{"url":"https://user:secret@example.com/report?q=private#fragment"}`),
	}, started, publicActivityContext{})
	if activity.Kind != "reading_webpage" || activity.Detail != "example.com/report" || activity.StartedAt != started {
		t.Fatalf("activity=%+v", activity)
	}
	encoded, err := json.Marshal(activity)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"read_url", "user", "secret", "private", "fragment", "url"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public activity leaked %q: %s", forbidden, encoded)
		}
	}

	unknown := projectPublicActivity(AcceptedAction{
		Name: "internal_secret_tool", Input: json.RawMessage(`{"token":"do-not-show"}`),
	}, started, publicActivityContext{})
	if unknown.Kind != "working" || unknown.Detail != "" {
		t.Fatalf("unknown=%+v", unknown)
	}

	malformed := projectPublicActivity(AcceptedAction{
		Name: "read_url", Input: json.RawMessage(`{"url":`),
	}, started, publicActivityContext{})
	if malformed.Kind != "working" || malformed.Detail != "" {
		t.Fatalf("malformed=%+v", malformed)
	}
}

func TestProjectPublicActivityShowsSafeSourceAndPDFPageDetails(t *testing.T) {
	started := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	context := publicActivityContext{
		sourceTitles:   map[string]string{"src_1": "Transformer Paper"},
		documentTitles: map[string]string{"rdoc_0123456789abcdef0123456789abcdef": "Attention Paper"},
		selectedTitles: []string{"Course Guide", "Policy FAQ"},
	}
	search := projectPublicActivity(AcceptedAction{
		Name: "search_evidence", Input: json.RawMessage(`{"query":"private full query","purpose":"hidden purpose"}`),
	}, started, context)
	if search.Kind != "searching_sources" || search.Detail != "Course Guide、Policy FAQ" {
		t.Fatalf("search=%+v", search)
	}
	inspect := projectPublicActivity(AcceptedAction{
		Name: "inspect_source", Input: json.RawMessage(`{"source_id":"src_1"}`),
	}, started, context)
	if inspect.Kind != "inspecting_source" || inspect.Detail != "Transformer Paper" {
		t.Fatalf("inspect=%+v", inspect)
	}
	pages := projectPublicActivity(AcceptedAction{
		Name: "read_document_pages", Input: json.RawMessage(`{"document_handle":"rdoc_0123456789abcdef0123456789abcdef","start_page":3,"end_page":5}`),
	}, started, context)
	if pages.Kind != "reading_pdf" || pages.Detail != "Attention Paper · 3–5" {
		t.Fatalf("pages=%+v", pages)
	}
}
