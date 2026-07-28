package studio

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseKindAcceptsOnlySprint11Outputs(t *testing.T) {
	for _, value := range []string{"report", "flashcards", "mind_map", "data_table"} {
		kind, err := ParseKind(value)
		if err != nil || kind.String() != value {
			t.Fatalf("ParseKind(%q)=%q,%v", value, kind, err)
		}
	}
	for _, value := range []string{"", "quiz", "audio", "mind-map", "Report"} {
		if _, err := ParseKind(value); err == nil {
			t.Fatalf("accepted unsupported kind %q", value)
		}
	}
}

func TestValidateArtifactAcceptsEachStrictShape(t *testing.T) {
	tests := []struct {
		kind    Kind
		payload string
		title   string
	}{
		{KindReport, `{"title":"Brief","summary":"Summary","sections":[{"id":"s1","heading":"One","markdown":"Grounded.","source_ids":["src_1"]}]}`, "Brief"},
		{KindFlashcards, validFlashcards(5), "Deck"},
		{KindMindMap, `{"title":"Map","nodes":[{"id":"root","parent_id":null,"label":"Root","detail":"","source_ids":["src_1"]},{"id":"a","parent_id":"root","label":"A","detail":"Detail","source_ids":["src_1"]},{"id":"b","parent_id":"root","label":"B","detail":"","source_ids":["src_2"]}]}`, "Map"},
		{KindDataTable, `{"title":"Table","description":"Comparison","columns":["Name","Value"],"rows":[{"id":"r1","cells":["A","1"],"source_ids":["src_2"]}]}`, "Table"},
	}
	for _, test := range tests {
		t.Run(test.kind.String(), func(t *testing.T) {
			artifact, err := ValidateArtifact(test.kind, []byte(test.payload), []string{"src_1", "src_2"})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Title != test.title || !json.Valid(artifact.JSON) {
				t.Fatalf("artifact=%+v", artifact)
			}
		})
	}
}

func TestValidateArtifactFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		kind    Kind
		payload string
		want    string
	}{
		{"unknown field", KindReport, `{"title":"Brief","summary":"Summary","sections":[{"id":"s1","heading":"One","markdown":"Text","source_ids":["src_1"]}],"extra":true}`, "unknown"},
		{"trailing json", KindReport, `{"title":"Brief","summary":"Summary","sections":[{"id":"s1","heading":"One","markdown":"Text","source_ids":["src_1"]}]} {}`, "trailing"},
		{"unpinned source", KindReport, `{"title":"Brief","summary":"Summary","sections":[{"id":"s1","heading":"One","markdown":"Text","source_ids":["src_other"]}]}`, "pinned"},
		{"duplicate section id", KindReport, `{"title":"Brief","summary":"Summary","sections":[{"id":"s1","heading":"One","markdown":"Text","source_ids":["src_1"]},{"id":"s1","heading":"Two","markdown":"Text","source_ids":["src_1"]}]}`, "duplicate"},
		{"too few cards", KindFlashcards, validFlashcards(4), "cards"},
		{"disconnected map", KindMindMap, `{"title":"Map","nodes":[{"id":"root","parent_id":null,"label":"Root","detail":"","source_ids":["src_1"]},{"id":"a","parent_id":"missing","label":"A","detail":"","source_ids":["src_1"]},{"id":"b","parent_id":"root","label":"B","detail":"","source_ids":["src_1"]}]}`, "parent"},
		{"map cycle", KindMindMap, `{"title":"Map","nodes":[{"id":"root","parent_id":null,"label":"Root","detail":"","source_ids":["src_1"]},{"id":"a","parent_id":"b","label":"A","detail":"","source_ids":["src_1"]},{"id":"b","parent_id":"a","label":"B","detail":"","source_ids":["src_1"]}]}`, "cycle"},
		{"cell mismatch", KindDataTable, `{"title":"Table","description":"","columns":["Name","Value"],"rows":[{"id":"r1","cells":["A"],"source_ids":["src_1"]}]}`, "cells"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateArtifact(test.kind, []byte(test.payload), []string{"src_1"}); err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("err=%v want substring %q", err, test.want)
			}
		})
	}
}

func validFlashcards(count int) string {
	cards := make([]map[string]any, 0, count)
	for index := 0; index < count; index++ {
		cards = append(cards, map[string]any{"id": "c" + string(rune('a'+index)), "front": "Question", "back": "Answer", "source_ids": []string{"src_1"}})
	}
	payload, _ := json.Marshal(map[string]any{"title": "Deck", "cards": cards})
	return string(payload)
}
