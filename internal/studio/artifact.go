package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const maxArtifactBytes = 65536

type Kind string

const (
	KindReport     Kind = "report"
	KindFlashcards Kind = "flashcards"
	KindMindMap    Kind = "mind_map"
	KindDataTable  Kind = "data_table"
)

func ParseKind(value string) (Kind, error) {
	kind := Kind(value)
	switch kind {
	case KindReport, KindFlashcards, KindMindMap, KindDataTable:
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported Studio Output kind %q", value)
	}
}

func (k Kind) String() string { return string(k) }

type Artifact struct {
	Kind  Kind
	Title string
	JSON  json.RawMessage
}

type Report struct {
	Title    string          `json:"title"`
	Summary  string          `json:"summary"`
	Sections []ReportSection `json:"sections"`
}

type ReportSection struct {
	ID        string   `json:"id"`
	Heading   string   `json:"heading"`
	Markdown  string   `json:"markdown"`
	SourceIDs []string `json:"source_ids"`
}

type Flashcards struct {
	Title string      `json:"title"`
	Cards []Flashcard `json:"cards"`
}

type Flashcard struct {
	ID        string   `json:"id"`
	Front     string   `json:"front"`
	Back      string   `json:"back"`
	SourceIDs []string `json:"source_ids"`
}

type MindMap struct {
	Title string        `json:"title"`
	Nodes []MindMapNode `json:"nodes"`
}

type MindMapNode struct {
	ID        string   `json:"id"`
	ParentID  *string  `json:"parent_id"`
	Label     string   `json:"label"`
	Detail    string   `json:"detail"`
	SourceIDs []string `json:"source_ids"`
}

type DataTable struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Columns     []string       `json:"columns"`
	Rows        []DataTableRow `json:"rows"`
}

type DataTableRow struct {
	ID        string   `json:"id"`
	Cells     []string `json:"cells"`
	SourceIDs []string `json:"source_ids"`
}

func ValidateArtifact(kind Kind, payload []byte, pinnedSourceIDs []string) (Artifact, error) {
	if _, err := ParseKind(kind.String()); err != nil {
		return Artifact{}, err
	}
	if len(payload) == 0 || len(payload) > maxArtifactBytes {
		return Artifact{}, errors.New("Studio artifact size is invalid")
	}
	pinned, err := sourceSet(pinnedSourceIDs, false)
	if err != nil || len(pinned) == 0 {
		return Artifact{}, errors.New("pinned Source set is invalid")
	}
	var value any
	switch kind {
	case KindReport:
		var report Report
		if err := decodeStrict(payload, &report); err != nil {
			return Artifact{}, err
		}
		if err := validateReport(report, pinned); err != nil {
			return Artifact{}, err
		}
		value = report
	case KindFlashcards:
		var cards Flashcards
		if err := decodeStrict(payload, &cards); err != nil {
			return Artifact{}, err
		}
		if err := validateFlashcards(cards, pinned); err != nil {
			return Artifact{}, err
		}
		value = cards
	case KindMindMap:
		var mindMap MindMap
		if err := decodeStrict(payload, &mindMap); err != nil {
			return Artifact{}, err
		}
		if err := validateMindMap(mindMap, pinned); err != nil {
			return Artifact{}, err
		}
		value = mindMap
	case KindDataTable:
		var table DataTable
		if err := decodeStrict(payload, &table); err != nil {
			return Artifact{}, err
		}
		if err := validateDataTable(table, pinned); err != nil {
			return Artifact{}, err
		}
		value = table
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return Artifact{}, fmt.Errorf("encode Studio artifact: %w", err)
	}
	return Artifact{Kind: kind, Title: artifactTitle(value), JSON: canonical}, nil
}

func decodeStrict(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Studio artifact: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("Studio artifact has trailing JSON")
	}
	return nil
}

func validateReport(value Report, pinned map[string]bool) error {
	if err := boundedText("report title", value.Title, 1, 200); err != nil {
		return err
	}
	if err := boundedText("report summary", value.Summary, 1, 2000); err != nil {
		return err
	}
	if len(value.Sections) < 1 || len(value.Sections) > 16 {
		return errors.New("report sections count is invalid")
	}
	seen := make(map[string]bool, len(value.Sections))
	for _, section := range value.Sections {
		if err := uniqueID("report section", section.ID, seen); err != nil {
			return err
		}
		if err := boundedText("report heading", section.Heading, 1, 200); err != nil {
			return err
		}
		if err := boundedText("report markdown", section.Markdown, 1, 12000); err != nil {
			return err
		}
		if err := validateReferences(section.SourceIDs, pinned); err != nil {
			return err
		}
	}
	return nil
}

func validateFlashcards(value Flashcards, pinned map[string]bool) error {
	if err := boundedText("flashcards title", value.Title, 1, 200); err != nil {
		return err
	}
	if len(value.Cards) < 5 || len(value.Cards) > 24 {
		return errors.New("flashcards cards count is invalid")
	}
	seen := make(map[string]bool, len(value.Cards))
	for _, card := range value.Cards {
		if err := uniqueID("flashcard", card.ID, seen); err != nil {
			return err
		}
		if err := boundedText("flashcard front", card.Front, 1, 500); err != nil {
			return err
		}
		if err := boundedText("flashcard back", card.Back, 1, 2000); err != nil {
			return err
		}
		if err := validateReferences(card.SourceIDs, pinned); err != nil {
			return err
		}
	}
	return nil
}

func validateMindMap(value MindMap, pinned map[string]bool) error {
	if err := boundedText("mind map title", value.Title, 1, 200); err != nil {
		return err
	}
	if len(value.Nodes) < 3 || len(value.Nodes) > 64 {
		return errors.New("mind map nodes count is invalid")
	}
	byID := make(map[string]MindMapNode, len(value.Nodes))
	rootCount := 0
	for _, node := range value.Nodes {
		if err := uniqueID("mind map node", node.ID, mapKeys(byID)); err != nil {
			return err
		}
		if err := boundedText("mind map label", node.Label, 1, 200); err != nil {
			return err
		}
		if err := boundedText("mind map detail", node.Detail, 0, 1000); err != nil {
			return err
		}
		if err := validateReferences(node.SourceIDs, pinned); err != nil {
			return err
		}
		if node.ParentID == nil {
			rootCount++
		}
		byID[node.ID] = node
	}
	if rootCount != 1 {
		return errors.New("mind map must contain exactly one root")
	}
	for _, node := range value.Nodes {
		depth := 0
		seen := map[string]bool{node.ID: true}
		current := node
		for current.ParentID != nil {
			parent, ok := byID[*current.ParentID]
			if !ok {
				return fmt.Errorf("mind map parent %q does not exist", *current.ParentID)
			}
			if seen[parent.ID] {
				return errors.New("mind map contains a cycle")
			}
			seen[parent.ID] = true
			depth++
			if depth > 4 {
				return errors.New("mind map depth exceeds four")
			}
			current = parent
		}
	}
	return nil
}

func validateDataTable(value DataTable, pinned map[string]bool) error {
	if err := boundedText("data table title", value.Title, 1, 200); err != nil {
		return err
	}
	if err := boundedText("data table description", value.Description, 0, 2000); err != nil {
		return err
	}
	if len(value.Columns) < 2 || len(value.Columns) > 12 {
		return errors.New("data table columns count is invalid")
	}
	columns := make(map[string]bool, len(value.Columns))
	for _, column := range value.Columns {
		if err := boundedText("data table column", column, 1, 120); err != nil {
			return err
		}
		key := strings.TrimSpace(column)
		if columns[key] {
			return errors.New("data table contains duplicate columns")
		}
		columns[key] = true
	}
	if len(value.Rows) < 1 || len(value.Rows) > 50 {
		return errors.New("data table rows count is invalid")
	}
	seen := make(map[string]bool, len(value.Rows))
	for _, row := range value.Rows {
		if err := uniqueID("data table row", row.ID, seen); err != nil {
			return err
		}
		if len(row.Cells) != len(value.Columns) {
			return errors.New("data table row cells do not match columns")
		}
		for _, cell := range row.Cells {
			if err := boundedText("data table cell", cell, 0, 2000); err != nil {
				return err
			}
		}
		if err := validateReferences(row.SourceIDs, pinned); err != nil {
			return err
		}
	}
	return nil
}

func boundedText(field, value string, minimum, maximum int) error {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	if length < minimum || length > maximum {
		return fmt.Errorf("%s length is invalid", field)
	}
	return nil
}

func uniqueID(field, id string, seen map[string]bool) error {
	if err := boundedText(field+" id", id, 1, 64); err != nil {
		return err
	}
	if seen[id] {
		return fmt.Errorf("%s has duplicate id %q", field, id)
	}
	seen[id] = true
	return nil
}

func validateReferences(sourceIDs []string, pinned map[string]bool) error {
	if len(sourceIDs) < 1 || len(sourceIDs) > 8 {
		return errors.New("Source references count is invalid")
	}
	seen, err := sourceSet(sourceIDs, true)
	if err != nil {
		return err
	}
	for sourceID := range seen {
		if !pinned[sourceID] {
			return fmt.Errorf("Source reference %q is not pinned", sourceID)
		}
	}
	return nil
}

func sourceSet(sourceIDs []string, rejectDuplicates bool) (map[string]bool, error) {
	set := make(map[string]bool, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if strings.TrimSpace(sourceID) == "" || utf8.RuneCountInString(sourceID) > 128 {
			return nil, errors.New("Source reference is invalid")
		}
		if rejectDuplicates && set[sourceID] {
			return nil, errors.New("Source references contain a duplicate")
		}
		set[sourceID] = true
	}
	return set, nil
}

func mapKeys(values map[string]MindMapNode) map[string]bool {
	keys := make(map[string]bool, len(values))
	for key := range values {
		keys[key] = true
	}
	return keys
}

func artifactTitle(value any) string {
	switch typed := value.(type) {
	case Report:
		return typed.Title
	case Flashcards:
		return typed.Title
	case MindMap:
		return typed.Title
	case DataTable:
		return typed.Title
	default:
		return ""
	}
}
