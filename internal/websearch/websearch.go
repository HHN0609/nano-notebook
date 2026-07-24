package websearch

import (
	"context"
	"errors"
)

var (
	ErrNotConfigured   = errors.New("Web Search Provider is not configured")
	ErrInvalidQuery    = errors.New("Web Search request is invalid")
	ErrTimeout         = errors.New("Web Search Provider request timed out")
	ErrRateLimited     = errors.New("Web Search Provider rate limited the request")
	ErrUnavailable     = errors.New("Web Search Provider is unavailable")
	ErrInvalidResponse = errors.New("Web Search Provider returned an invalid response")
)

// Provider returns bounded, provider-neutral discovery candidates.
type Provider interface {
	Search(context.Context, Request) ([]Candidate, error)
}

type Request struct {
	Query      string
	Count      int
	Country    string
	SearchLang string
}

type Candidate struct {
	Title       string
	URL         string
	DisplayURL  string
	Description string
	Rank        int
}
