package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type BraveConfig struct {
	Endpoint         string
	APIKey           string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

type BraveProvider struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	maxBytes   int64
}

const braveWebSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

func NewBraveProvider(config BraveConfig) (*BraveProvider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(config.Endpoint) == "" {
		config.Endpoint = braveWebSearchEndpoint
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 1024 * 1024
	}
	return &BraveProvider{
		endpoint:   config.Endpoint,
		apiKey:     config.APIKey,
		httpClient: config.HTTPClient,
		maxBytes:   config.MaxResponseBytes,
	}, nil
}

func (p *BraveProvider) Search(ctx context.Context, input Request) ([]Candidate, error) {
	queryText := strings.TrimSpace(input.Query)
	if queryText == "" || utf8.RuneCountInString(queryText) > 500 || input.Count < 1 || input.Count > 20 {
		return nil, ErrInvalidQuery
	}
	endpoint, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("q", queryText)
	query.Set("count", strconv.Itoa(input.Count))
	if country := strings.TrimSpace(input.Country); country != "" {
		query.Set("country", country)
	}
	if language := strings.TrimSpace(input.SearchLang); language != "" {
		query.Set("search_lang", language)
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", p.apiKey)

	response, err := p.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrUnavailable
	}

	var envelope struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, p.maxBytes+1))
	if err != nil || int64(len(body)) > p.maxBytes {
		return nil, ErrInvalidResponse
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, ErrInvalidResponse
	}

	candidates := make([]Candidate, 0, len(envelope.Web.Results))
	for index, result := range envelope.Web.Results {
		if len(candidates) == input.Count {
			break
		}
		parsedURL, err := url.Parse(result.URL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Hostname() == "" || parsedURL.User != nil {
			continue
		}
		candidates = append(candidates, Candidate{
			Title:       boundRunes(plainText(result.Title), 300),
			URL:         result.URL,
			DisplayURL:  strings.ToLower(parsedURL.Hostname()) + parsedURL.EscapedPath(),
			Description: boundRunes(plainText(result.Description), 1000),
			Rank:        index + 1,
		})
	}
	return candidates, nil
}

func boundRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}

func plainText(value string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(strings.ReplaceAll(value, "\x00", "")))
	var text strings.Builder
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if tokenizer.Err() == io.EOF {
				return strings.Join(strings.Fields(text.String()), " ")
			}
			return ""
		case html.TextToken:
			text.Write(tokenizer.Text())
		}
	}
}
