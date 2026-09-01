package sourcemap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
)

type NavigationKind string
type Confidence string

const (
	NavigationEmbeddedOutline  NavigationKind = "embedded_outline"
	NavigationInferredSections NavigationKind = "inferred_sections"
	NavigationPageSamples      NavigationKind = "page_samples"

	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

type BuildInput struct {
	SourceID          string
	RevisionID        string
	OriginalSHA256    string
	Parser            *ParseResult
	ParserFailureCode string
	Normalized        normalize.Artifact
}

type Artifact struct {
	Map           SourceMap
	CanonicalJSON []byte
	SHA256        string
}

type ArtifactIdentity struct {
	SourceID   string
	RevisionID string
	SHA256     string
	Bytes      int
}

type SourceMap struct {
	SchemaVersion        string            `json:"schema_version"`
	MapID                string            `json:"map_id"`
	SourceID             string            `json:"source_id"`
	RevisionID           string            `json:"revision_id"`
	OriginalSHA256       string            `json:"original_sha256"`
	ParserIdentity       string            `json:"parser_identity"`
	ParserVersion        string            `json:"parser_version"`
	ParserPolicyID       string            `json:"parser_policy_id"`
	ParserArtifactSHA256 string            `json:"parser_artifact_sha256,omitempty"`
	NavigationKind       NavigationKind    `json:"navigation_kind"`
	Confidence           Confidence        `json:"confidence"`
	PageCount            int               `json:"page_count"`
	Entries              []NavigationEntry `json:"entries"`
	Pages                []Page            `json:"pages"`
	Warnings             []string          `json:"warnings"`
}

type NavigationEntry struct {
	EntryID       string `json:"entry_id"`
	ParentEntryID string `json:"parent_entry_id,omitempty"`
	Kind          string `json:"kind"`
	Heading       string `json:"heading"`
	HeadingLevel  int    `json:"heading_level,omitempty"`
	PageStart     int    `json:"page_start"`
	PageEnd       int    `json:"page_end"`
	BBox          *BBox  `json:"bbox,omitempty"`
}

func BuildArtifact(input BuildInput) (Artifact, error) {
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	if input.SourceID == "" || input.RevisionID == "" || !validSHA256(input.OriginalSHA256) || input.Normalized.Format != "pdf" {
		return Artifact{}, errors.New("invalid Source Map build input")
	}
	value := SourceMap{
		SchemaVersion: "nano.source-map.v1", SourceID: input.SourceID, RevisionID: input.RevisionID,
		OriginalSHA256: input.OriginalSHA256, ParserPolicyID: ParserPolicyNoOCR,
		Entries: make([]NavigationEntry, 0), Pages: make([]Page, 0), Warnings: make([]string, 0),
	}
	if input.Parser != nil && ValidateDocument(ParseRequest{
		SchemaVersion: 1, SourceID: input.SourceID, InputSHA256: input.OriginalSHA256, InputBytes: 1,
		ParserPolicyID: ParserPolicyNoOCR, MaxPages: MaxParserPages, MaxOutputBytes: MaxParserOutput,
	}, input.Parser.Document) == nil && validSHA256(input.Parser.SHA256) {
		value.ParserIdentity = input.Parser.Document.ParserIdentity
		value.ParserVersion = input.Parser.Document.ParserVersion
		value.ParserArtifactSHA256 = input.Parser.SHA256
		value.PageCount = input.Parser.Document.PageCount
		value.Pages = clonePages(input.Parser.Document.Pages)
		if entries := outlineEntries(input.Parser.Document.Outline, value.PageCount); len(entries) > 0 {
			value.NavigationKind, value.Confidence, value.Entries = NavigationEmbeddedOutline, ConfidenceHigh, entries
		} else if entries := inferredEntries(value.Pages, value.PageCount); len(entries) > 0 {
			value.NavigationKind, value.Confidence, value.Entries = NavigationInferredSections, ConfidenceMedium, entries
			value.Warnings = append(value.Warnings, "section hierarchy was inferred from PDF layout")
		}
	}
	if len(value.Entries) == 0 {
		pages, pageCount, err := pagesFromNormalized(input.Normalized)
		if err != nil {
			return Artifact{}, err
		}
		value.ParserIdentity = ParserIdentity
		value.ParserVersion = "unavailable"
		value.PageCount = pageCount
		value.Pages = pages
		value.NavigationKind = NavigationPageSamples
		value.Confidence = ConfidenceLow
		value.Entries = pageSampleEntries(pageCount)
		warning := "reliable PDF structure was unavailable; navigation uses distributed page samples"
		if code := strings.TrimSpace(input.ParserFailureCode); code != "" {
			warning += " (" + code + ")"
		}
		value.Warnings = append(value.Warnings, warning)
	}
	value.MapID = stableMapID(value)
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) == 0 || len(canonical) > MaxParserOutput {
		return Artifact{}, errors.New("Source Map artifact exceeds canonical budget")
	}
	digest := sha256.Sum256(canonical)
	return Artifact{Map: value, CanonicalJSON: canonical, SHA256: hex.EncodeToString(digest[:])}, nil
}

func DecodeArtifact(payload []byte, identity ArtifactIdentity) (SourceMap, error) {
	if len(payload) < 1 || len(payload) > MaxParserOutput || identity.Bytes != len(payload) ||
		!validSHA256(identity.SHA256) || sha256Hex(payload) != identity.SHA256 ||
		strings.TrimSpace(identity.SourceID) == "" || strings.TrimSpace(identity.RevisionID) == "" {
		return SourceMap{}, errors.New("Source Map artifact identity is invalid")
	}
	var value SourceMap
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return SourceMap{}, errors.New("Source Map artifact is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SourceMap{}, errors.New("Source Map artifact has trailing data")
	}
	if value.SourceID != identity.SourceID || value.RevisionID != identity.RevisionID || validateSourceMap(value) != nil {
		return SourceMap{}, errors.New("Source Map artifact authority changed")
	}
	return value, nil
}

func validateSourceMap(value SourceMap) error {
	if value.SchemaVersion != "nano.source-map.v1" || !boundedText(value.SourceID, 128) || !boundedText(value.RevisionID, 128) ||
		!validSHA256(value.OriginalSHA256) || value.ParserIdentity != ParserIdentity || !boundedText(value.ParserVersion, 64) ||
		value.ParserPolicyID != ParserPolicyNoOCR || value.PageCount < 1 || value.PageCount > MaxParserPages ||
		len(value.Pages) != value.PageCount || len(value.Entries) < 1 || len(value.Entries) > 4096 || len(value.Warnings) > 128 ||
		(value.ParserArtifactSHA256 != "" && !validSHA256(value.ParserArtifactSHA256)) {
		return ErrManifestInvalid
	}
	switch value.NavigationKind {
	case NavigationEmbeddedOutline:
		if value.Confidence != ConfidenceHigh || value.ParserArtifactSHA256 == "" {
			return ErrManifestInvalid
		}
	case NavigationInferredSections:
		if value.Confidence != ConfidenceMedium || value.ParserArtifactSHA256 == "" {
			return ErrManifestInvalid
		}
	case NavigationPageSamples:
		if value.Confidence != ConfidenceLow {
			return ErrManifestInvalid
		}
	default:
		return ErrManifestInvalid
	}
	for _, warning := range value.Warnings {
		if !boundedText(warning, 512) {
			return ErrManifestInvalid
		}
	}
	for pageIndex, page := range value.Pages {
		if page.Ordinal != pageIndex+1 || !positiveFinite(page.Width) || !positiveFinite(page.Height) || len(page.Blocks) > 100_000 {
			return ErrManifestInvalid
		}
		for blockIndex, block := range page.Blocks {
			if block.ReadingOrder != blockIndex || !validBlockKind(block.Kind) || !boundedText(block.Text, 1_000_000) ||
				(block.Kind == "heading" && (block.HeadingLevel < 1 || block.HeadingLevel > 6)) ||
				(block.Kind != "heading" && block.HeadingLevel != 0) || !validBBox(block.BBox, page.Width, page.Height) {
				return ErrManifestInvalid
			}
		}
	}
	seenEntries := make(map[string]bool, len(value.Entries))
	for _, entry := range value.Entries {
		if !boundedText(entry.EntryID, 128) || seenEntries[entry.EntryID] || !boundedText(entry.Heading, 1000) ||
			entry.PageStart < 1 || entry.PageEnd < entry.PageStart || entry.PageEnd > value.PageCount ||
			(entry.Kind != "section" && entry.Kind != "page_sample") ||
			(entry.Kind == "section" && (entry.HeadingLevel < 1 || entry.HeadingLevel > 6)) ||
			(entry.Kind == "page_sample" && entry.HeadingLevel != 0) ||
			(entry.ParentEntryID != "" && !seenEntries[entry.ParentEntryID]) {
			return ErrManifestInvalid
		}
		if entry.BBox != nil {
			page := value.Pages[entry.PageStart-1]
			if !validBBox(*entry.BBox, page.Width, page.Height) {
				return ErrManifestInvalid
			}
		}
		seenEntries[entry.EntryID] = true
	}
	if value.MapID != stableMapID(value) {
		return ErrManifestInvalid
	}
	return nil
}

func outlineEntries(outline []OutlineEntry, pageCount int) []NavigationEntry {
	usable := make([]OutlineEntry, 0, len(outline))
	for _, item := range outline {
		item.Title = strings.Join(strings.Fields(item.Title), " ")
		if item.Level >= 1 && item.Level <= 6 && item.Title != "" && item.Page >= 1 && item.Page <= pageCount {
			usable = append(usable, item)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	entries := make([]NavigationEntry, 0, len(usable))
	parents := make(map[int]string)
	for index, item := range usable {
		end := pageCount
		if index+1 < len(usable) && usable[index+1].Page > item.Page {
			end = usable[index+1].Page - 1
		}
		entry := NavigationEntry{Kind: "section", Heading: item.Title, HeadingLevel: item.Level, PageStart: item.Page, PageEnd: end}
		entry.EntryID = stableEntryID(entry, index)
		if item.Level > 1 {
			entry.ParentEntryID = parents[item.Level-1]
		}
		parents[item.Level] = entry.EntryID
		for level := item.Level + 1; level <= 6; level++ {
			delete(parents, level)
		}
		entries = append(entries, entry)
	}
	return entries
}

func inferredEntries(pages []Page, pageCount int) []NavigationEntry {
	type heading struct {
		page  int
		block Block
	}
	headings := make([]heading, 0)
	for _, page := range pages {
		for _, block := range page.Blocks {
			if block.Kind == "heading" && block.HeadingLevel >= 1 && block.HeadingLevel <= 6 {
				headings = append(headings, heading{page: page.Ordinal, block: block})
			}
		}
	}
	entries := make([]NavigationEntry, 0, len(headings))
	parents := make(map[int]string)
	for index, item := range headings {
		end := pageCount
		if index+1 < len(headings) && headings[index+1].page > item.page {
			end = headings[index+1].page - 1
		}
		bbox := item.block.BBox
		entry := NavigationEntry{Kind: "section", Heading: item.block.Text, HeadingLevel: item.block.HeadingLevel, PageStart: item.page, PageEnd: end, BBox: &bbox}
		entry.EntryID = stableEntryID(entry, index)
		if entry.HeadingLevel > 1 {
			entry.ParentEntryID = parents[entry.HeadingLevel-1]
		}
		parents[entry.HeadingLevel] = entry.EntryID
		entries = append(entries, entry)
	}
	return entries
}

func pagesFromNormalized(artifact normalize.Artifact) ([]Page, int, error) {
	byPage := make(map[int][]Block)
	maxPage := 0
	for _, block := range artifact.Blocks {
		if block.Coordinate == nil || block.Coordinate.Kind != "pdf_region" || block.Coordinate.Page < 1 {
			continue
		}
		page := block.Coordinate.Page
		if page > maxPage {
			maxPage = page
		}
		coordinate := block.Coordinate
		kind := block.Kind
		if !validBlockKind(kind) {
			kind = "paragraph"
		}
		byPage[page] = append(byPage[page], Block{
			ReadingOrder: len(byPage[page]), Kind: kind, Text: block.Text, HeadingLevel: block.HeadingLevel,
			BBox: BBox{X0: coordinate.X, Y0: coordinate.Y, X1: coordinate.X + coordinate.Width, Y1: coordinate.Y + coordinate.Height},
		})
	}
	if maxPage < 1 {
		return nil, 0, errors.New("page-aware Evidence is unavailable for Source Map fallback")
	}
	pages := make([]Page, maxPage)
	for index := range pages {
		blocks := byPage[index+1]
		width, height := 1.0, 1.0
		for _, block := range blocks {
			if block.BBox.X1 > width {
				width = block.BBox.X1
			}
			if block.BBox.Y1 > height {
				height = block.BBox.Y1
			}
		}
		pages[index] = Page{Ordinal: index + 1, Width: width, Height: height, Blocks: blocks}
	}
	return pages, maxPage, nil
}

func pageSampleEntries(pageCount int) []NavigationEntry {
	count := pageCount
	if count > 9 {
		count = 9
	}
	selected := make(map[int]bool, count)
	for index := 0; index < count; index++ {
		page := 1
		if count > 1 {
			page = 1 + (index*(pageCount-1)+(count-1)/2)/(count-1)
		}
		selected[page] = true
	}
	pages := make([]int, 0, len(selected))
	for page := range selected {
		pages = append(pages, page)
	}
	sort.Ints(pages)
	entries := make([]NavigationEntry, 0, len(pages))
	for index, page := range pages {
		entry := NavigationEntry{Kind: "page_sample", Heading: fmt.Sprintf("Page %d", page), PageStart: page, PageEnd: page}
		entry.EntryID = stableEntryID(entry, index)
		entries = append(entries, entry)
	}
	return entries
}

func stableMapID(value SourceMap) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		value.SourceID, value.RevisionID, value.OriginalSHA256, value.ParserIdentity, value.ParserVersion,
		value.ParserPolicyID, string(value.NavigationKind),
	}, "\x00")))
	return "smap_" + hex.EncodeToString(digest[:16])
}

func stableEntryID(value NavigationEntry, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d\x00%d\x00%d", index, value.Heading, value.HeadingLevel, value.PageStart, value.PageEnd)))
	return "entry_" + hex.EncodeToString(digest[:8])
}

func clonePages(source []Page) []Page {
	cloned := make([]Page, len(source))
	for index, page := range source {
		cloned[index] = page
		cloned[index].Blocks = append([]Block(nil), page.Blocks...)
	}
	return cloned
}
