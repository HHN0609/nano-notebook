package webreader_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/webreader"
)

const validBody = `{
	"schema_version": "1",
	"url": "https://example.com/post",
	"final_url": "https://example.com/post",
	"title": "Example Post",
	"description": "",
	"site_name": "example.com",
	"published_time": "",
	"lang": "en",
	"extraction": "readability",
	"engine": "lightweight",
	"upgraded": false,
	"format": "markdown",
	"content": "# Example Post\n\nBody text with enough runes to matter.",
	"char_count": 48,
	"word_count": 9,
	"truncated": false,
	"fetch": {"status": 200, "content_type": "text/html", "charset": "utf-8", "bytes": 4096, "redirects": 0}
}`

func newAdapter(t *testing.T, handler http.Handler, token string) *webreader.HTTPAdapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adapter, err := webreader.NewHTTPAdapter(webreader.HTTPConfig{
		Endpoint: server.URL, ServiceToken: token, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestHTTPAdapterParsesMarkdownPage(t *testing.T) {
	adapter := newAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/parse" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validBody))
	}), "token")

	page, err := adapter.Parse(context.Background(), webreader.Request{
		URL: "https://example.com/post", Format: webreader.FormatMarkdown, MaxChars: 250_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Example Post" || page.Engine != "lightweight" ||
		!strings.HasPrefix(page.Content, "# Example Post") || page.WordCount != 9 || page.Truncated {
		t.Fatalf("page=%+v", page)
	}
}

func TestHTTPAdapterSendsBearerTokenAndRequestContract(t *testing.T) {
	adapter := newAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"url":"https://example.com/post"`) ||
			!strings.Contains(string(body), `"format":"markdown"`) ||
			!strings.Contains(string(body), `"max_chars":250000`) {
			t.Errorf("request body=%s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validBody))
	}), "token")

	if _, err := adapter.Parse(context.Background(), webreader.Request{
		URL: "https://example.com/post", Format: webreader.FormatMarkdown, MaxChars: 250_000,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAdapterSurfacesSidecarErrorEnvelope(t *testing.T) {
	for _, test := range []struct {
		code   string
		status int
		want   error
	}{
		{code: "unsafe_destination", status: http.StatusUnprocessableEntity, want: webreader.ErrUnsafeDestination},
		{code: "response_too_large", status: http.StatusRequestEntityTooLarge, want: webreader.ErrResponseTooLarge},
		{code: "unsupported_type", status: http.StatusUnsupportedMediaType, want: webreader.ErrUnsupportedType},
	} {
		t.Run(test.code, func(t *testing.T) {
			adapter := newAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"error":{"code":"` + test.code + `","message":"reader rejected input"}}`))
			}), "")
			_, err := adapter.Parse(context.Background(), webreader.Request{
				URL: "https://example.com/post", Format: webreader.FormatMarkdown, MaxChars: webreader.MaxContentChars,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}

func TestHTTPAdapterRejectsContractViolations(t *testing.T) {
	adapter := newAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validBody))
	}), "")

	for name, request := range map[string]webreader.Request{
		"non-http url":   {URL: "ftp://example.com/", Format: webreader.FormatMarkdown, MaxChars: 250_000},
		"empty url":      {URL: "  ", Format: webreader.FormatMarkdown, MaxChars: 250_000},
		"wrong format":   {URL: "https://example.com/", Format: "html", MaxChars: 250_000},
		"zero max chars": {URL: "https://example.com/", Format: webreader.FormatMarkdown, MaxChars: 0},
	} {
		if _, err := adapter.Parse(context.Background(), request); !errors.Is(err, webreader.ErrRequestInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestHTTPAdapterRejectsInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":       validBody[:len(validBody)-1] + `,"unexpected":1}`,
		"wrong schema":        strings.Replace(validBody, `"schema_version": "1"`, `"schema_version": "2"`, 1),
		"empty content":       strings.Replace(validBody, `"content": "# Example Post\n\nBody text with enough runes to matter."`, `"content": ""`, 1),
		"missing engine":      strings.Replace(validBody, `"engine": "lightweight"`, `"engine": ""`, 1),
		"trailing json":       validBody + `{"extra":true}`,
		"not json":            `oops`,
		"wrong format echoed": strings.Replace(validBody, `"format": "markdown"`, `"format": "text"`, 1),
	} {
		adapter := newAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}), "")
		_, err := adapter.Parse(context.Background(), webreader.Request{
			URL: "https://example.com/post", Format: webreader.FormatMarkdown, MaxChars: 250_000,
		})
		if !errors.Is(err, webreader.ErrResponseInvalid) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestNewHTTPAdapterRejectsInvalidConfiguration(t *testing.T) {
	if _, err := webreader.NewHTTPAdapter(webreader.HTTPConfig{Endpoint: "http://127.0.0.1:8085"}); err == nil {
		t.Error("missing http client must fail")
	}
	if _, err := webreader.NewHTTPAdapter(webreader.HTTPConfig{Endpoint: ":::", HTTPClient: &http.Client{}}); err == nil {
		t.Error("invalid endpoint must fail")
	}
	if _, err := webreader.NewHTTPAdapter(webreader.HTTPConfig{Endpoint: "", HTTPClient: &http.Client{}}); err == nil {
		t.Error("empty endpoint must fail")
	}
}
