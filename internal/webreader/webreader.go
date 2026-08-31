// Package webreader adapts the web-reader sidecar (/v1/parse, services/web-reader)
// for HTML Sources whose deterministic extraction failed the quality gate. The
// sidecar re-fetches the URL (with an optional browser render for JS shells)
// and returns Readability-cleaned Markdown; this Adapter follows the same
// HTTP-sidecar contract style as internal/documentrender.
package webreader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrRequestInvalid       = errors.New("web reader Request is invalid")
	ErrResponseInvalid      = errors.New("web reader Response is invalid")
	ErrUnsafeDestination    = errors.New("web reader destination is unsafe")
	ErrResponseTooLarge     = errors.New("web reader response is too large")
	ErrUnsupportedType      = errors.New("web reader content type is unsupported")
	ErrDocumentTypeMismatch = errors.New("web reader document type does not match its signature")
)

const (
	FormatMarkdown  = "markdown"
	MaxContentChars = 250_000
	MaxPDFBytes     = 20 << 20
	MediaTypeHTML   = "text/html"
	MediaTypePDF    = "application/pdf"
)

type Request struct {
	URL      string
	Format   string
	MaxChars int
}

func (r Request) Validate() error {
	parsed, err := url.Parse(strings.TrimSpace(r.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return ErrRequestInvalid
	}
	if r.Format != FormatMarkdown {
		return ErrRequestInvalid
	}
	if r.MaxChars < 1 || r.MaxChars > MaxContentChars {
		return ErrRequestInvalid
	}
	return nil
}

type Page struct {
	Title     string
	Content   string
	FinalURL  string
	Engine    string
	WordCount int
	Truncated bool
}

type Adapter interface {
	Parse(context.Context, Request) (Page, error)
}

// Acquirer is the media-aware Research boundary. Adapter remains HTML-only so
// existing Source and importability callers do not acquire binary documents.
type Acquirer interface {
	Acquire(context.Context, Request) (Content, error)
}

type Content struct {
	MediaType string
	FinalURL  string
	Page      Page
	PDF       []byte
}

type HTTPConfig struct {
	Endpoint     string
	ServiceToken string
	HTTPClient   *http.Client
}

type HTTPAdapter struct {
	endpoint     string
	serviceToken string
	client       *http.Client
}

func NewHTTPAdapter(config HTTPConfig) (*HTTPAdapter, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || config.HTTPClient == nil {
		return nil, errors.New("web reader HTTP Adapter configuration is invalid")
	}
	return &HTTPAdapter{endpoint: endpoint, serviceToken: strings.TrimSpace(config.ServiceToken), client: config.HTTPClient}, nil
}

// parseResponse mirrors the /v1/parse schema v1 contract (services/web-reader
// src/server.ts); decoding is strict so sidecar contract drift fails loudly.
type parseResponse struct {
	SchemaVersion string `json:"schema_version"`
	URL           string `json:"url"`
	FinalURL      string `json:"final_url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	SiteName      string `json:"site_name"`
	PublishedTime string `json:"published_time"`
	Lang          string `json:"lang"`
	Extraction    string `json:"extraction"`
	Engine        string `json:"engine"`
	Upgraded      bool   `json:"upgraded"`
	Format        string `json:"format"`
	Content       string `json:"content"`
	CharCount     int    `json:"char_count"`
	WordCount     int    `json:"word_count"`
	Truncated     bool   `json:"truncated"`
	Fetch         struct {
		Status      int    `json:"status"`
		ContentType string `json:"content_type"`
		Charset     string `json:"charset"`
		Bytes       int64  `json:"bytes"`
		Redirects   int    `json:"redirects"`
	} `json:"fetch"`
}

type acquireResponse struct {
	SchemaVersion string `json:"schema_version"`
	MediaType     string `json:"media_type"`
	SHA256        string `json:"sha256"`
	URL           string `json:"url"`
	FinalURL      string `json:"final_url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	SiteName      string `json:"site_name"`
	PublishedTime string `json:"published_time"`
	Lang          string `json:"lang"`
	Extraction    string `json:"extraction"`
	Engine        string `json:"engine"`
	Upgraded      bool   `json:"upgraded"`
	Format        string `json:"format"`
	Content       string `json:"content"`
	CharCount     int    `json:"char_count"`
	WordCount     int    `json:"word_count"`
	Truncated     bool   `json:"truncated"`
	Fetch         struct {
		Status      int    `json:"status"`
		ContentType string `json:"content_type"`
		Charset     string `json:"charset"`
		Bytes       int64  `json:"bytes"`
		Redirects   int    `json:"redirects"`
	} `json:"fetch"`
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *HTTPAdapter) Parse(ctx context.Context, request Request) (Page, error) {
	if a == nil || a.client == nil {
		return Page{}, errors.New("nil web reader HTTP Adapter")
	}
	if err := request.Validate(); err != nil {
		return Page{}, err
	}
	body, err := json.Marshal(struct {
		URL      string `json:"url"`
		Format   string `json:"format"`
		MaxChars int    `json:"max_chars"`
	}{URL: request.URL, Format: request.Format, MaxChars: request.MaxChars})
	if err != nil {
		return Page{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/v1/parse", bytes.NewReader(body))
	if err != nil {
		return Page{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if a.serviceToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+a.serviceToken)
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return Page{}, fmt.Errorf("web reader request failed: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, int64(request.MaxChars)*4+(1<<20)+1))
	if err != nil || len(payload) > int(request.MaxChars)*4+(1<<20) {
		return Page{}, ErrResponseInvalid
	}
	if response.StatusCode != http.StatusOK {
		var failure errorEnvelope
		if json.Unmarshal(payload, &failure) == nil && failure.Error.Code != "" {
			return Page{}, typedSidecarError(failure.Error.Code, failure.Error.Message)
		}
		return Page{}, fmt.Errorf("web reader returned status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded parseResponse
	if err := decoder.Decode(&decoded); err != nil {
		return Page{}, ErrResponseInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Page{}, ErrResponseInvalid
	}
	if decoded.SchemaVersion != "1" || decoded.Format != request.Format ||
		decoded.Engine == "" || strings.TrimSpace(decoded.Content) == "" ||
		decoded.CharCount < 0 || decoded.WordCount < 0 {
		return Page{}, ErrResponseInvalid
	}
	return Page{
		Title: decoded.Title, Content: decoded.Content, FinalURL: decoded.FinalURL,
		Engine: decoded.Engine, WordCount: decoded.WordCount, Truncated: decoded.Truncated,
	}, nil
}

func (a *HTTPAdapter) Acquire(ctx context.Context, request Request) (Content, error) {
	if a == nil || a.client == nil {
		return Content{}, errors.New("nil web reader HTTP Adapter")
	}
	if err := request.Validate(); err != nil {
		return Content{}, err
	}
	body, err := json.Marshal(struct {
		URL      string `json:"url"`
		Format   string `json:"format"`
		MaxChars int    `json:"max_chars"`
	}{URL: request.URL, Format: request.Format, MaxChars: request.MaxChars})
	if err != nil {
		return Content{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/v1/acquire", bytes.NewReader(body))
	if err != nil {
		return Content{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if a.serviceToken != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+a.serviceToken)
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return Content{}, fmt.Errorf("web reader request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Content{}, decodeSidecarFailure(response)
	}

	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" || strings.TrimSpace(params["boundary"]) == "" {
		return Content{}, ErrResponseInvalid
	}
	reader := multipart.NewReader(response.Body, params["boundary"])
	metadataPart, err := reader.NextPart()
	if err != nil || !validPart(metadataPart, "application/json", "metadata", false) {
		return Content{}, ErrResponseInvalid
	}
	metadataBytes, err := readBounded(metadataPart, int64(request.MaxChars)*4+(1<<20))
	if err != nil {
		return Content{}, ErrResponseInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(metadataBytes))
	decoder.DisallowUnknownFields()
	var metadata acquireResponse
	if err := decoder.Decode(&metadata); err != nil {
		return Content{}, ErrResponseInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Content{}, ErrResponseInvalid
	}
	if metadata.SchemaVersion != "1" || metadata.FinalURL == "" || metadata.Fetch.Status < 100 ||
		metadata.Fetch.Bytes < 0 || metadata.Fetch.Redirects < 0 {
		return Content{}, ErrResponseInvalid
	}

	switch metadata.MediaType {
	case MediaTypeHTML:
		if metadata.Format != request.Format || metadata.Engine == "" || strings.TrimSpace(metadata.Content) == "" ||
			metadata.CharCount < 0 || metadata.WordCount < 0 || metadata.Fetch.ContentType == "" {
			return Content{}, ErrResponseInvalid
		}
		if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
			return Content{}, ErrResponseInvalid
		}
		page := Page{
			Title: metadata.Title, Content: metadata.Content, FinalURL: metadata.FinalURL,
			Engine: metadata.Engine, WordCount: metadata.WordCount, Truncated: metadata.Truncated,
		}
		return Content{MediaType: MediaTypeHTML, FinalURL: metadata.FinalURL, Page: page}, nil

	case MediaTypePDF:
		if normalizedMediaType(metadata.Fetch.ContentType) != MediaTypePDF || metadata.Fetch.Bytes > MaxPDFBytes {
			return Content{}, ErrResponseInvalid
		}
		documentPart, err := reader.NextPart()
		if err != nil || !validPart(documentPart, MediaTypePDF, "document", true) {
			return Content{}, ErrResponseInvalid
		}
		declared, err := strconv.ParseInt(documentPart.Header.Get("Content-Length"), 10, 64)
		if err != nil || declared < 1 || declared > MaxPDFBytes || declared != metadata.Fetch.Bytes {
			return Content{}, ErrResponseInvalid
		}
		pdf, err := readBounded(documentPart, MaxPDFBytes)
		digest := sha256.Sum256(pdf)
		if err != nil || int64(len(pdf)) != declared || !bytes.HasPrefix(pdf, []byte("%PDF-")) ||
			metadata.SHA256 != hex.EncodeToString(digest[:]) {
			return Content{}, ErrResponseInvalid
		}
		if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
			return Content{}, ErrResponseInvalid
		}
		return Content{MediaType: MediaTypePDF, FinalURL: metadata.FinalURL, PDF: pdf}, nil
	default:
		return Content{}, ErrResponseInvalid
	}
}

func validPart(part *multipart.Part, wantType, wantID string, requireLength bool) bool {
	if part == nil || part.Header.Get("Content-ID") != wantID || normalizedMediaType(part.Header.Get("Content-Type")) != wantType {
		return false
	}
	allowed := map[string]bool{"Content-Type": true, "Content-Id": true, "Content-Length": true}
	for key := range part.Header {
		if !allowed[http.CanonicalHeaderKey(key)] {
			return false
		}
	}
	return !requireLength || part.Header.Get("Content-Length") != ""
}

func normalizedMediaType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return ""
	}
	return mediaType
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(payload)) > limit {
		return nil, ErrResponseInvalid
	}
	return payload, nil
}

func decodeSidecarFailure(response *http.Response) error {
	payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(payload) > 1<<20 {
		return ErrResponseInvalid
	}
	var failure errorEnvelope
	if json.Unmarshal(payload, &failure) == nil && failure.Error.Code != "" {
		return typedSidecarError(failure.Error.Code, failure.Error.Message)
	}
	return fmt.Errorf("web reader returned status %d", response.StatusCode)
}

func typedSidecarError(code, message string) error {
	var kind error
	switch code {
	case "invalid_request":
		kind = ErrRequestInvalid
	case "unsafe_destination":
		kind = ErrUnsafeDestination
	case "response_too_large":
		kind = ErrResponseTooLarge
	case "unsupported_type":
		kind = ErrUnsupportedType
	case "document_type_mismatch":
		kind = ErrDocumentTypeMismatch
	default:
		return fmt.Errorf("web reader returned %s: %s", code, message)
	}
	return fmt.Errorf("%w: %s", kind, message)
}
