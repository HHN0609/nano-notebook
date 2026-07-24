package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNewBraveProviderRejectsMissingCredential(t *testing.T) {
	t.Parallel()

	_, err := NewBraveProvider(BraveConfig{
		Endpoint:   "https://api.search.brave.com/res/v1/web/search",
		HTTPClient: http.DefaultClient,
	})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestNewBraveProviderAppliesSafeDefaults(t *testing.T) {
	t.Parallel()

	provider, err := NewBraveProvider(BraveConfig{APIKey: "brave-secret"})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}
	if got, want := provider.endpoint, "https://api.search.brave.com/res/v1/web/search"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if provider.httpClient == nil {
		t.Fatal("HTTP client is nil")
	}
	if got, want := provider.httpClient.Timeout, 10*time.Second; got != want {
		t.Fatalf("HTTP timeout = %s, want %s", got, want)
	}
}

func TestBraveProviderSearchReturnsProviderNeutralCandidates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "brave-secret" {
			t.Fatalf("X-Subscription-Token = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "how to make a film" {
			t.Fatalf("q = %q", got)
		}
		if got := r.URL.Query().Get("count"); got != "7" {
			t.Fatalf("count = %q", got)
		}
		if got := r.URL.Query().Get("country"); got != "US" {
			t.Fatalf("country = %q", got)
		}
		if got := r.URL.Query().Get("search_lang"); got != "en" {
			t.Fatalf("search_lang = %q", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{
						"title":       "Film production guide",
						"url":         "https://example.com/guides/film",
						"description": "A practical production guide.",
					},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	candidates, err := provider.Search(context.Background(), Request{
		Query:      "how to make a film",
		Count:      7,
		Country:    "US",
		SearchLang: "en",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d", len(candidates))
	}
	want := Candidate{
		Title:       "Film production guide",
		URL:         "https://example.com/guides/film",
		DisplayURL:  "example.com/guides/film",
		Description: "A practical production guide.",
		Rank:        1,
	}
	if candidates[0] != want {
		t.Fatalf("candidate = %#v, want %#v", candidates[0], want)
	}
}

func TestBraveProviderMapsProviderFailuresToSafeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		want       error
	}{
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: `{"message":"provider-secret-body"}`, want: ErrRateLimited},
		{name: "unavailable", statusCode: http.StatusServiceUnavailable, body: `{"message":"provider-secret-body"}`, want: ErrUnavailable},
		{name: "invalid success envelope", statusCode: http.StatusOK, body: `{`, want: ErrInvalidResponse},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			provider, err := NewBraveProvider(BraveConfig{
				Endpoint:   server.URL,
				APIKey:     "brave-secret",
				HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatalf("NewBraveProvider: %v", err)
			}
			_, err = provider.Search(context.Background(), Request{Query: "film", Count: 10})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), "provider-secret-body") {
				t.Fatalf("error exposed provider response body: %v", err)
			}
		})
	}
}

func TestBraveProviderRejectsUnboundedRequestsBeforeCallingProvider(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	tests := []Request{
		{Query: "   ", Count: 10},
		{Query: strings.Repeat("界", 501), Count: 10},
		{Query: "film", Count: 0},
		{Query: "film", Count: 21},
	}
	for _, input := range tests {
		if _, err := provider.Search(context.Background(), input); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("Search(%#v) error = %v, want ErrInvalidQuery", input, err)
		}
	}
	if called {
		t.Fatal("provider was called for an invalid request")
	}
}

func TestBraveProviderSanitizesAndBoundsCandidates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{
					{
						"title":       "  <b>Film&nbsp;Guide</b>\x00 ",
						"url":         "https://Example.COM/guides/film?ref=search#part",
						"description": " Make <em>films</em> &amp; learn.\x00 ",
					},
					{
						"title":       "Unsafe",
						"url":         "javascript:alert(1)",
						"description": "Must be discarded.",
					},
					{
						"title":       "Lighting",
						"url":         "https://lighting.example/tutorial",
						"description": "Lighting tutorial.",
					},
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	candidates, err := provider.Search(context.Background(), Request{Query: "film", Count: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(candidates))
	}
	if got, want := candidates[0], (Candidate{
		Title:       "Film Guide",
		URL:         "https://Example.COM/guides/film?ref=search#part",
		DisplayURL:  "example.com/guides/film",
		Description: "Make films & learn.",
		Rank:        1,
	}); got != want {
		t.Fatalf("first candidate = %#v, want %#v", got, want)
	}
	if got, want := candidates[1].Rank, 3; got != want {
		t.Fatalf("second candidate rank = %d, want %d", got, want)
	}
}

func TestBraveProviderClassifiesDeadlineWithoutExposingTransportDetails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(250 * time.Millisecond)
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err = provider.Search(ctx, Request{Query: "film", Count: 10})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "brave-secret") {
		t.Fatalf("error exposed request details: %v", err)
	}
}

func TestBraveProviderRejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 33)))
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:         server.URL,
		APIKey:           "brave-secret",
		HTTPClient:       server.Client(),
		MaxResponseBytes: 32,
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	_, err = provider.Search(context.Background(), Request{Query: "film", Count: 10})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error = %v, want ErrInvalidResponse", err)
	}
}

func TestBraveProviderOmitsUnsetLocaleHints(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["country"]; ok {
			t.Fatal("country query parameter must be omitted when unset")
		}
		if _, ok := r.URL.Query()["search_lang"]; ok {
			t.Fatal("search_lang query parameter must be omitted when unset")
		}
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	if _, err := provider.Search(context.Background(), Request{Query: "film", Count: 10}); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestBraveProviderBoundsCandidateText(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]any{{
					"title":       strings.Repeat("界", 301),
					"url":         "https://example.com/film",
					"description": strings.Repeat("光", 1001),
				}},
			},
		})
	}))
	t.Cleanup(server.Close)
	provider, err := NewBraveProvider(BraveConfig{
		Endpoint:   server.URL,
		APIKey:     "brave-secret",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewBraveProvider: %v", err)
	}

	candidates, err := provider.Search(context.Background(), Request{Query: "film", Count: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := utf8.RuneCountInString(candidates[0].Title); got != 300 {
		t.Fatalf("title runes = %d, want 300", got)
	}
	if got := utf8.RuneCountInString(candidates[0].Description); got != 1000 {
		t.Fatalf("description runes = %d, want 1000", got)
	}
}
