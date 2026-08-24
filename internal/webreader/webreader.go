// Package webreader adapts the web-reader sidecar (/v1/parse, services/web-reader)
// for HTML Sources whose deterministic extraction failed the quality gate. The
// sidecar re-fetches the URL (with an optional browser render for JS shells)
// and returns Readability-cleaned Markdown; this Adapter follows the same
// HTTP-sidecar contract style as internal/documentrender.
package webreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrRequestInvalid    = errors.New("web reader Request is invalid")
	ErrResponseInvalid   = errors.New("web reader Response is invalid")
	ErrUnsafeDestination = errors.New("web reader destination is unsafe")
	ErrResponseTooLarge  = errors.New("web reader response is too large")
	ErrUnsupportedType   = errors.New("web reader content type is unsupported")
)

const (
	FormatMarkdown  = "markdown"
	MaxContentChars = 250_000
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
	default:
		return fmt.Errorf("web reader returned %s: %s", code, message)
	}
	return fmt.Errorf("%w: %s", kind, message)
}
