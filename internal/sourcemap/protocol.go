package sourcemap

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	ParserIdentity      = "pymupdf4llm"
	ParserPolicyNoOCR   = "pdf-structure-no-ocr-v1"
	MaxParserInputBytes = 100 * 1024 * 1024
	MaxParserPages      = 500
	MaxParserOutput     = 16 * 1024 * 1024
)

var (
	ErrRequestInvalid  = errors.New("Source Map parser request is invalid")
	ErrManifestInvalid = errors.New("Source Map parser manifest is invalid")
)

type ParseRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	SourceID       string `json:"source_id"`
	InputSHA256    string `json:"input_sha256"`
	InputBytes     int64  `json:"input_bytes"`
	ParserPolicyID string `json:"parser_policy_id"`
	OCR            bool   `json:"ocr"`
	MaxPages       int    `json:"max_pages"`
	MaxOutputBytes int64  `json:"max_output_bytes"`
}

func (r ParseRequest) Validate() error {
	if r.SchemaVersion != 1 || !boundedText(r.SourceID, 128) || !validSHA256(r.InputSHA256) ||
		r.InputBytes < 1 || r.InputBytes > MaxParserInputBytes || r.ParserPolicyID != ParserPolicyNoOCR || r.OCR ||
		r.MaxPages < 1 || r.MaxPages > MaxParserPages || r.MaxOutputBytes < 1 || r.MaxOutputBytes > MaxParserOutput {
		return ErrRequestInvalid
	}
	return nil
}

type Document struct {
	SchemaVersion  int            `json:"schema_version"`
	SourceID       string         `json:"source_id"`
	InputSHA256    string         `json:"input_sha256"`
	ParserIdentity string         `json:"parser_identity"`
	ParserVersion  string         `json:"parser_version"`
	ParserPolicyID string         `json:"parser_policy_id"`
	PageCount      int            `json:"page_count"`
	Outline        []OutlineEntry `json:"outline"`
	Pages          []Page         `json:"pages"`
}

type OutlineEntry struct {
	Level int    `json:"level"`
	Title string `json:"title"`
	Page  int    `json:"page"`
}

type Page struct {
	Ordinal int     `json:"ordinal"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Blocks  []Block `json:"blocks"`
}

type Block struct {
	ReadingOrder int    `json:"reading_order"`
	Kind         string `json:"kind"`
	Text         string `json:"text"`
	HeadingLevel int    `json:"heading_level,omitempty"`
	BBox         BBox   `json:"bbox"`
}

type BBox struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

func ValidateDocument(request ParseRequest, document Document) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if document.SchemaVersion != 1 || document.SourceID != request.SourceID || document.InputSHA256 != request.InputSHA256 ||
		document.ParserIdentity != ParserIdentity || !boundedText(document.ParserVersion, 64) ||
		document.ParserPolicyID != request.ParserPolicyID || document.PageCount < 1 || document.PageCount > request.MaxPages ||
		len(document.Pages) != document.PageCount || len(document.Outline) > 4096 {
		return ErrManifestInvalid
	}
	for _, item := range document.Outline {
		if item.Level < 1 || item.Level > 6 || !boundedText(item.Title, 1000) || item.Page < 1 || item.Page > document.PageCount {
			return ErrManifestInvalid
		}
	}
	for pageIndex, page := range document.Pages {
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
	return nil
}

func validBBox(value BBox, width, height float64) bool {
	for _, coordinate := range []float64{value.X0, value.Y0, value.X1, value.Y1} {
		if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) {
			return false
		}
	}
	return value.X0 >= 0 && value.Y0 >= 0 && value.X1 > value.X0 && value.Y1 > value.Y0 && value.X1 <= width && value.Y1 <= height
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validBlockKind(value string) bool {
	switch value {
	case "heading", "paragraph", "list", "table", "code":
		return true
	default:
		return false
	}
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func boundedText(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}
