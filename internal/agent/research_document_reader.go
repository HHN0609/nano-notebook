package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/documentreading"
	"github.com/huangxinxinyu/nano-notebook/internal/documentrender"
	"github.com/huangxinxinyu/nano-notebook/internal/normalize"
	"github.com/huangxinxinyu/nano-notebook/internal/objectstore"
	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

var (
	ErrResearchDocumentNotFound = errors.New("Research document is not found")
	ErrResearchPageRangeInvalid = errors.New("Research document page range is invalid")
)

const researchDocumentManifestMaxBytes int64 = 32 * 1024

var (
	researchDocumentRunPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
	researchDocumentHandlePattern = regexp.MustCompile(`^rdoc_[a-f0-9]{32}$`)
)

type ResearchURLReaderConfig struct {
	ExtractionConfigID     string
	RenderConfigID         string
	RenderMaxPages         int
	RenderDPI              int
	RenderMaxPixelsPerPage int64
	RenderMaxOutputBytes   int64
	MaxNormalizedRunes     int
	MaxModelChars          int
	MaxPageRead            int
	MaxPDFConcurrent       int
	ReadTimeout            time.Duration
}

type ResearchURLReadRequest struct {
	URL      string
	RunID    string
	ActionID string
}

type ResearchURLReadResult struct {
	Title          string
	FinalURL       string
	Markdown       string
	Engine         string
	WordCount      int
	Truncated      bool
	MediaType      string
	PageCount      int
	DocumentHandle string
}

type ResearchPageReadRequest struct {
	RunID          string
	DocumentHandle string
	StartPage      int
	EndPage        int
}

type ResearchPageReadResult struct {
	DocumentHandle string
	StartPage      int
	EndPage        int
	PageCount      int
	Markdown       string
	Truncated      bool
}

type ResearchURLContentReader struct {
	acquirer webreader.Acquirer
	renderer documentrender.Adapter
	pdf      *documentreading.PDFExtractor
	store    objectstore.Store
	config   ResearchURLReaderConfig
	pdfGate  chan struct{}
}

type researchDocumentAcquisition struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	URL           string `json:"url"`
	FinalURL      string `json:"final_url"`
	PDFSHA256     string `json:"pdf_sha256"`
	PDFBytes      int64  `json:"pdf_bytes"`
	PDFObjectKey  string `json:"pdf_object_key"`
}

type researchDocumentManifest struct {
	SchemaVersion     string `json:"schema_version"`
	RunID             string `json:"run_id"`
	DocumentHandle    string `json:"document_handle"`
	URL               string `json:"url"`
	FinalURL          string `json:"final_url"`
	PDFSHA256         string `json:"pdf_sha256"`
	PDFBytes          int64  `json:"pdf_bytes"`
	PDFObjectKey      string `json:"pdf_object_key"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ArtifactBytes     int64  `json:"artifact_bytes"`
	ArtifactObjectKey string `json:"artifact_object_key"`
	PageCount         int    `json:"page_count"`
	WordCount         int    `json:"word_count"`
	Engine            string `json:"engine"`
}

func NewResearchURLContentReader(
	acquirer webreader.Acquirer,
	renderer documentrender.Adapter,
	pdf *documentreading.PDFExtractor,
	store objectstore.Store,
	config ResearchURLReaderConfig,
) *ResearchURLContentReader {
	if config.MaxPDFConcurrent == 0 {
		config.MaxPDFConcurrent = 2
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 120 * time.Second
	}
	var gate chan struct{}
	if config.MaxPDFConcurrent > 0 {
		gate = make(chan struct{}, config.MaxPDFConcurrent)
	}
	return &ResearchURLContentReader{acquirer: acquirer, renderer: renderer, pdf: pdf, store: store, config: config, pdfGate: gate}
}

func (r *ResearchURLContentReader) Read(ctx context.Context, request ResearchURLReadRequest) (ResearchURLReadResult, error) {
	if err := r.validateReadRequest(request); err != nil {
		return ResearchURLReadResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.ReadTimeout)
	defer cancel()
	actionPrefix := researchDocumentActionPrefix(request.RunID, request.ActionID)
	if manifest, err := r.loadManifest(ctx, actionPrefix+"/result.json", request.RunID); err == nil {
		return r.resultFromManifest(ctx, manifest)
	} else if !errors.Is(err, objectstore.ErrNotFound) {
		return ResearchURLReadResult{}, err
	}

	acquisition, err := r.loadAcquisition(ctx, actionPrefix+"/acquisition.json", request.RunID)
	if errors.Is(err, objectstore.ErrNotFound) {
		content, acquireErr := r.acquirer.Acquire(ctx, webreader.Request{
			URL: request.URL, Format: webreader.FormatMarkdown, MaxChars: r.config.MaxModelChars,
		})
		if acquireErr != nil {
			return ResearchURLReadResult{}, acquireErr
		}
		if content.MediaType == webreader.MediaTypeHTML {
			return ResearchURLReadResult{
				Title: content.Page.Title, FinalURL: content.Page.FinalURL, Markdown: content.Page.Content,
				Engine: content.Page.Engine, WordCount: content.Page.WordCount, Truncated: content.Page.Truncated,
				MediaType: webreader.MediaTypeHTML,
			}, nil
		}
		if content.MediaType != webreader.MediaTypePDF || len(content.PDF) < 5 || len(content.PDF) > webreader.MaxPDFBytes {
			return ResearchURLReadResult{}, webreader.ErrResponseInvalid
		}
		pdfDigest := sha256.Sum256(content.PDF)
		pdfSHA := hex.EncodeToString(pdfDigest[:])
		pdfKey := fmt.Sprintf("research-documents/%s/content/%s/document.pdf", request.RunID, pdfSHA)
		if err := r.store.Put(ctx, pdfKey, content.PDF); err != nil {
			return ResearchURLReadResult{}, err
		}
		acquisition = researchDocumentAcquisition{
			SchemaVersion: "1", RunID: request.RunID, URL: request.URL, FinalURL: content.FinalURL,
			PDFSHA256: pdfSHA, PDFBytes: int64(len(content.PDF)), PDFObjectKey: pdfKey,
		}
		if err := putJSON(ctx, r.store, actionPrefix+"/acquisition.json", acquisition); err != nil {
			return ResearchURLReadResult{}, err
		}
	} else if err != nil {
		return ResearchURLReadResult{}, err
	}
	if err := r.acquirePDFSlot(ctx); err != nil {
		return ResearchURLReadResult{}, err
	}
	defer r.releasePDFSlot()

	pdfPayload, err := r.loadPDF(ctx, acquisition)
	if err != nil {
		return ResearchURLReadResult{}, err
	}
	documentID := "research_pdf_" + acquisition.PDFSHA256[:32]
	missing, err := normalize.PDFPagesRequiringVision(pdfPayload)
	if err != nil {
		return ResearchURLReadResult{}, err
	}
	var rendered documentrender.Result
	engine := "pdf-native"
	if len(missing) > 0 {
		engine = "pdf-native+vision"
		if r.renderer == nil {
			return ResearchURLReadResult{}, errors.New("Research PDF renderer is unavailable")
		}
		rendered, err = r.renderer.Render(ctx, documentrender.Request{
			SchemaVersion: 1, SourceID: documentID, Format: documentrender.FormatPDF,
			InputSHA256: acquisition.PDFSHA256, InputBytes: acquisition.PDFBytes,
			RenderConfigID: r.config.RenderConfigID, MaxPages: r.config.RenderMaxPages, DPI: r.config.RenderDPI,
			MaxPixelsPerPage: r.config.RenderMaxPixelsPerPage, MaxOutputBytes: r.config.RenderMaxOutputBytes,
		}, pdfPayload)
		if err != nil {
			return ResearchURLReadResult{}, err
		}
	}
	artifact, err := r.pdf.Extract(ctx, documentreading.PDFDocument{
		ID: documentID, Payload: pdfPayload, ExtractionConfigID: r.config.ExtractionConfigID,
	}, rendered)
	if err != nil {
		return ResearchURLReadResult{}, err
	}
	if artifact.Coverage.TotalRunes > r.config.MaxNormalizedRunes {
		return ResearchURLReadResult{}, normalize.ErrProcessingBudget
	}
	pageCount := artifactPageCount(artifact)
	if pageCount < 1 || pageCount > r.config.RenderMaxPages {
		return ResearchURLReadResult{}, normalize.ErrProcessingBudget
	}
	artifactPayload, err := json.Marshal(artifact)
	if err != nil {
		return ResearchURLReadResult{}, err
	}
	artifactDigest := sha256.Sum256(artifactPayload)
	artifactObjectSHA := hex.EncodeToString(artifactDigest[:])
	artifactKey := fmt.Sprintf("research-documents/%s/content/%s/%s.json", request.RunID, acquisition.PDFSHA256, artifactObjectSHA)
	if err := r.store.Put(ctx, artifactKey, artifactPayload); err != nil {
		return ResearchURLReadResult{}, err
	}
	handleDigest := sha256.Sum256([]byte(request.RunID + "\x00" + acquisition.PDFSHA256))
	handle := "rdoc_" + hex.EncodeToString(handleDigest[:16])
	manifest := researchDocumentManifest{
		SchemaVersion: "1", RunID: request.RunID, DocumentHandle: handle, URL: acquisition.URL, FinalURL: acquisition.FinalURL,
		PDFSHA256: acquisition.PDFSHA256, PDFBytes: acquisition.PDFBytes, PDFObjectKey: acquisition.PDFObjectKey,
		ArtifactSHA256: artifactObjectSHA, ArtifactBytes: int64(len(artifactPayload)), ArtifactObjectKey: artifactKey,
		PageCount: pageCount, WordCount: len(strings.Fields(artifact.Text)), Engine: engine,
	}
	handleKey := researchDocumentHandleKey(request.RunID, handle)
	if err := putJSON(ctx, r.store, handleKey, manifest); err != nil {
		return ResearchURLReadResult{}, err
	}
	if err := putJSON(ctx, r.store, actionPrefix+"/result.json", manifest); err != nil {
		return ResearchURLReadResult{}, err
	}
	return r.resultFromArtifact(manifest, artifact), nil
}

func (r *ResearchURLContentReader) ReadPages(ctx context.Context, request ResearchPageReadRequest) (ResearchPageReadResult, error) {
	if r == nil || r.store == nil || !researchDocumentRunPattern.MatchString(request.RunID) ||
		!researchDocumentHandlePattern.MatchString(request.DocumentHandle) || request.StartPage < 1 || request.EndPage < request.StartPage ||
		request.EndPage-request.StartPage+1 > r.config.MaxPageRead {
		return ResearchPageReadResult{}, ErrResearchPageRangeInvalid
	}
	manifest, err := r.loadManifest(ctx, researchDocumentHandleKey(request.RunID, request.DocumentHandle), request.RunID)
	if errors.Is(err, objectstore.ErrNotFound) {
		return ResearchPageReadResult{}, ErrResearchDocumentNotFound
	}
	if err != nil {
		return ResearchPageReadResult{}, err
	}
	if request.EndPage > manifest.PageCount {
		return ResearchPageReadResult{}, ErrResearchPageRangeInvalid
	}
	artifact, err := r.loadArtifact(ctx, manifest)
	if err != nil {
		return ResearchPageReadResult{}, err
	}
	markdown, truncated := truncateResearchMarkdown(artifactPagesMarkdown(artifact, request.StartPage, request.EndPage), r.config.MaxModelChars)
	return ResearchPageReadResult{
		DocumentHandle: request.DocumentHandle, StartPage: request.StartPage, EndPage: request.EndPage,
		PageCount: manifest.PageCount, Markdown: markdown, Truncated: truncated,
	}, nil
}

func (r *ResearchURLContentReader) validateReadRequest(request ResearchURLReadRequest) error {
	if r == nil || r.acquirer == nil || r.pdf == nil || r.store == nil || !researchDocumentRunPattern.MatchString(request.RunID) ||
		strings.TrimSpace(request.ActionID) == "" || strings.TrimSpace(r.config.ExtractionConfigID) == "" ||
		strings.TrimSpace(r.config.RenderConfigID) == "" || r.config.RenderMaxPages < 1 || r.config.RenderMaxPages > 500 ||
		r.config.RenderDPI < 72 || r.config.RenderDPI > 300 || r.config.RenderMaxPixelsPerPage < 1 ||
		r.config.RenderMaxOutputBytes < 1 || r.config.MaxNormalizedRunes < 1 || r.config.MaxModelChars < 1 ||
		r.config.MaxModelChars > webreader.MaxContentChars || r.config.MaxPageRead < 1 || r.config.MaxPageRead > 20 ||
		r.config.MaxPDFConcurrent < 1 || r.config.MaxPDFConcurrent > 16 || r.config.ReadTimeout < 1 || r.pdfGate == nil {
		return errors.New("Research URL content reader is invalid")
	}
	return webreader.Request{URL: request.URL, Format: webreader.FormatMarkdown, MaxChars: r.config.MaxModelChars}.Validate()
}

func (r *ResearchURLContentReader) acquirePDFSlot(ctx context.Context) error {
	select {
	case r.pdfGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ResearchURLContentReader) releasePDFSlot() {
	<-r.pdfGate
}

func (r *ResearchURLContentReader) loadPDF(ctx context.Context, acquisition researchDocumentAcquisition) ([]byte, error) {
	payload, err := r.store.Get(ctx, acquisition.PDFObjectKey, webreader.MaxPDFBytes)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	if int64(len(payload)) != acquisition.PDFBytes || hex.EncodeToString(digest[:]) != acquisition.PDFSHA256 {
		return nil, errors.New("Research PDF object integrity mismatch")
	}
	return payload, nil
}

func (r *ResearchURLContentReader) loadAcquisition(ctx context.Context, key, runID string) (researchDocumentAcquisition, error) {
	var acquisition researchDocumentAcquisition
	if err := getJSON(ctx, r.store, key, researchDocumentManifestMaxBytes, &acquisition); err != nil {
		return researchDocumentAcquisition{}, err
	}
	if acquisition.SchemaVersion != "1" || acquisition.RunID != runID || acquisition.FinalURL == "" ||
		len(acquisition.PDFSHA256) != 64 || acquisition.PDFBytes < 1 || acquisition.PDFBytes > webreader.MaxPDFBytes ||
		acquisition.PDFObjectKey != fmt.Sprintf("research-documents/%s/content/%s/document.pdf", runID, acquisition.PDFSHA256) {
		return researchDocumentAcquisition{}, errors.New("Research PDF acquisition record is invalid")
	}
	return acquisition, nil
}

func (r *ResearchURLContentReader) loadManifest(ctx context.Context, key, runID string) (researchDocumentManifest, error) {
	var manifest researchDocumentManifest
	if err := getJSON(ctx, r.store, key, researchDocumentManifestMaxBytes, &manifest); err != nil {
		return researchDocumentManifest{}, err
	}
	if manifest.SchemaVersion != "1" || manifest.RunID != runID || !researchDocumentHandlePattern.MatchString(manifest.DocumentHandle) ||
		manifest.PageCount < 1 || manifest.PageCount > r.config.RenderMaxPages || manifest.WordCount < 0 || strings.TrimSpace(manifest.Engine) == "" ||
		manifest.ArtifactBytes < 1 || manifest.ArtifactBytes > int64(r.config.MaxNormalizedRunes)*4+(1<<20) ||
		len(manifest.ArtifactSHA256) != 64 || !strings.HasPrefix(manifest.ArtifactObjectKey, "research-documents/"+runID+"/content/") {
		return researchDocumentManifest{}, errors.New("Research document manifest is invalid")
	}
	return manifest, nil
}

func (r *ResearchURLContentReader) resultFromManifest(ctx context.Context, manifest researchDocumentManifest) (ResearchURLReadResult, error) {
	artifact, err := r.loadArtifact(ctx, manifest)
	if err != nil {
		return ResearchURLReadResult{}, err
	}
	return r.resultFromArtifact(manifest, artifact), nil
}

func (r *ResearchURLContentReader) resultFromArtifact(manifest researchDocumentManifest, artifact normalize.Artifact) ResearchURLReadResult {
	markdown := artifactPagesMarkdown(artifact, 1, manifest.PageCount)
	markdown, truncated := truncateResearchMarkdown(markdown, r.config.MaxModelChars)
	return ResearchURLReadResult{
		FinalURL: manifest.FinalURL, Markdown: markdown, Engine: manifest.Engine, WordCount: manifest.WordCount,
		Truncated: truncated, MediaType: webreader.MediaTypePDF, PageCount: manifest.PageCount, DocumentHandle: manifest.DocumentHandle,
	}
}

func (r *ResearchURLContentReader) loadArtifact(ctx context.Context, manifest researchDocumentManifest) (normalize.Artifact, error) {
	payload, err := r.store.Get(ctx, manifest.ArtifactObjectKey, int64(r.config.MaxNormalizedRunes)*4+(1<<20))
	if err != nil {
		return normalize.Artifact{}, err
	}
	digest := sha256.Sum256(payload)
	if int64(len(payload)) != manifest.ArtifactBytes || hex.EncodeToString(digest[:]) != manifest.ArtifactSHA256 {
		return normalize.Artifact{}, errors.New("Research normalized document integrity mismatch")
	}
	var artifact normalize.Artifact
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&artifact) != nil || normalize.Validate(artifact) != nil {
		return normalize.Artifact{}, errors.New("Research normalized document is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return normalize.Artifact{}, errors.New("Research normalized document is invalid")
	}
	return artifact, nil
}

func artifactPageCount(artifact normalize.Artifact) int {
	count := 0
	for _, block := range artifact.Blocks {
		if block.Coordinate != nil && block.Coordinate.Page > count {
			count = block.Coordinate.Page
		}
	}
	return count
}

func artifactPagesMarkdown(artifact normalize.Artifact, startPage, endPage int) string {
	var builder strings.Builder
	for page := startPage; page <= endPage; page++ {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		fmt.Fprintf(&builder, "<!-- nano-pdf-page:%d -->", page)
		for _, block := range artifact.Blocks {
			if block.Coordinate != nil && block.Coordinate.Page == page {
				builder.WriteString("\n\n")
				builder.WriteString(strings.TrimSpace(block.Text))
			}
		}
	}
	return builder.String()
}

func truncateResearchMarkdown(markdown string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(markdown) <= maxChars {
		return markdown, false
	}
	runes := []rune(markdown)
	return string(runes[:maxChars]) + "\n\n[content truncated; use read_document_pages]", true
}

func researchDocumentActionPrefix(runID, actionID string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + actionID))
	return fmt.Sprintf("research-documents/%s/actions/%s", runID, hex.EncodeToString(digest[:]))
}

func researchDocumentHandleKey(runID, handle string) string {
	return fmt.Sprintf("research-documents/%s/handles/%s.json", runID, handle)
}

func putJSON(ctx context.Context, store objectstore.Store, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return store.Put(ctx, key, payload)
}

func getJSON(ctx context.Context, store objectstore.Store, key string, maxBytes int64, target any) error {
	payload, err := store.Get(ctx, key, maxBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON object has trailing content")
	}
	return nil
}
