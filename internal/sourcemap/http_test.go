package sourcemap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAdapterSendsOnlyManifestAndVerifiedPDFAndCanonicalizesResponse(t *testing.T) {
	payload := []byte("%PDF-1.4 fixture")
	digest := sha256.Sum256(payload)
	request := ParseRequest{
		SchemaVersion: 1, SourceID: "src_http_pdf", InputSHA256: hex.EncodeToString(digest[:]), InputBytes: int64(len(payload)),
		ParserPolicyID: "pdf-structure-no-ocr-v1", MaxPages: 10, MaxOutputBytes: 1 << 20,
	}
	document := Document{
		SchemaVersion: 1, SourceID: request.SourceID, InputSHA256: request.InputSHA256,
		ParserIdentity: "pymupdf4llm", ParserVersion: "1.28.2", ParserPolicyID: request.ParserPolicyID,
		PageCount: 1, Pages: []Page{{Ordinal: 1, Width: 612, Height: 792, Blocks: []Block{{
			ReadingOrder: 0, Kind: "paragraph", Text: "Representative text.", BBox: BBox{X0: 72, Y0: 72, X1: 540, Y1: 100},
		}}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/parse-pdf" || r.Header.Get("Authorization") != "Bearer parser-token" {
			t.Errorf("request=%s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string][]byte{}
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			var body bytes.Buffer
			_, _ = body.ReadFrom(part)
			parts[part.FormName()] = body.Bytes()
		}
		var got ParseRequest
		if err := json.Unmarshal(parts["manifest"], &got); err != nil || got != request || got.OCR {
			t.Errorf("manifest=%s err=%v", parts["manifest"], err)
		}
		if !bytes.Equal(parts["document"], payload) || len(parts) != 2 {
			t.Errorf("parts=%v document=%q", parts, parts["document"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	defer server.Close()

	adapter, err := NewHTTPAdapter(HTTPConfig{Endpoint: server.URL, ServiceToken: "parser-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ParsePDF(context.Background(), request, payload)
	if err != nil {
		t.Fatalf("ParsePDF: %v", err)
	}
	if result.Document.PageCount != 1 || result.Document.Pages[0].Blocks[0].Text != "Representative text." ||
		len(result.CanonicalJSON) == 0 || !json.Valid(result.CanonicalJSON) || len(result.SHA256) != 64 {
		t.Fatalf("result=%+v", result)
	}
	canonicalAgain, _ := json.Marshal(result.Document)
	if !bytes.Equal(result.CanonicalJSON, canonicalAgain) {
		t.Fatalf("canonical=%s want=%s", result.CanonicalJSON, canonicalAgain)
	}
}

func TestHTTPAdapterRejectsInputIdentityDriftAndUnknownResponseFields(t *testing.T) {
	payload := []byte("%PDF fixture")
	digest := sha256.Sum256(payload)
	request := ParseRequest{SchemaVersion: 1, SourceID: "src", InputSHA256: hex.EncodeToString(digest[:]), InputBytes: int64(len(payload)), ParserPolicyID: "pdf-structure-no-ocr-v1", MaxPages: 1, MaxOutputBytes: 1024}
	adapter, _ := NewHTTPAdapter(HTTPConfig{Endpoint: "http://127.0.0.1:1", ServiceToken: "token", HTTPClient: http.DefaultClient})
	if _, err := adapter.ParsePDF(context.Background(), request, append(payload, 'x')); err == nil {
		t.Fatal("accepted input identity drift")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"unknown":true}`))
	}))
	defer server.Close()
	adapter, _ = NewHTTPAdapter(HTTPConfig{Endpoint: server.URL, ServiceToken: "token", HTTPClient: server.Client()})
	if _, err := adapter.ParsePDF(context.Background(), request, payload); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("err=%v", err)
	}
}

var _ = multipart.ErrMessageTooLarge
