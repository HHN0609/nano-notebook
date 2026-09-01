package sourcemap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

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

type ParseResult struct {
	Document      Document
	CanonicalJSON []byte
	SHA256        string
}

type Adapter interface {
	ParsePDF(context.Context, ParseRequest, []byte) (ParseResult, error)
}

func NewHTTPAdapter(config HTTPConfig) (*HTTPAdapter, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.Endpoint), "/"))
	config.ServiceToken = strings.TrimSpace(config.ServiceToken)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || config.ServiceToken == "" || config.HTTPClient == nil {
		return nil, errors.New("Source Map parser HTTP Adapter configuration is invalid")
	}
	return &HTTPAdapter{endpoint: parsed.String(), serviceToken: config.ServiceToken, client: config.HTTPClient}, nil
}

func (a *HTTPAdapter) ParsePDF(ctx context.Context, request ParseRequest, input []byte) (ParseResult, error) {
	if a == nil || a.client == nil {
		return ParseResult{}, errors.New("nil Source Map parser HTTP Adapter")
	}
	if err := request.Validate(); err != nil {
		return ParseResult{}, err
	}
	digest := sha256.Sum256(input)
	if int64(len(input)) != request.InputBytes || hex.EncodeToString(digest[:]) != request.InputSHA256 {
		return ParseResult{}, ErrRequestInvalid
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	manifestHeader := make(textproto.MIMEHeader)
	manifestHeader.Set("Content-Disposition", `form-data; name="manifest"`)
	manifestHeader.Set("Content-Type", "application/json")
	manifestPart, err := writer.CreatePart(manifestHeader)
	if err != nil {
		return ParseResult{}, err
	}
	if err := json.NewEncoder(manifestPart).Encode(request); err != nil {
		return ParseResult{}, err
	}
	documentHeader := make(textproto.MIMEHeader)
	documentHeader.Set("Content-Disposition", `form-data; name="document"; filename="input.pdf"`)
	documentHeader.Set("Content-Type", "application/pdf")
	documentPart, err := writer.CreatePart(documentHeader)
	if err != nil {
		return ParseResult{}, err
	}
	if _, err := documentPart.Write(input); err != nil {
		return ParseResult{}, err
	}
	if err := writer.Close(); err != nil {
		return ParseResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/v1/parse-pdf", &body)
	if err != nil {
		return ParseResult{}, err
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	httpRequest.Header.Set("Authorization", "Bearer "+a.serviceToken)
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return ParseResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return ParseResult{}, fmt.Errorf("Source Map parser returned status %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, request.MaxOutputBytes+1))
	if err != nil || int64(len(encoded)) > request.MaxOutputBytes {
		return ParseResult{}, ErrManifestInvalid
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return ParseResult{}, fmt.Errorf("Source Map parser manifest: %w", ErrManifestInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ParseResult{}, fmt.Errorf("Source Map parser manifest trailing JSON: %w", ErrManifestInvalid)
	}
	if err := ValidateDocument(request, document); err != nil {
		return ParseResult{}, fmt.Errorf("Source Map parser manifest: %w", err)
	}
	canonical, err := json.Marshal(document)
	if err != nil || int64(len(canonical)) > request.MaxOutputBytes {
		return ParseResult{}, ErrManifestInvalid
	}
	canonicalDigest := sha256.Sum256(canonical)
	return ParseResult{Document: document, CanonicalJSON: canonical, SHA256: hex.EncodeToString(canonicalDigest[:])}, nil
}
