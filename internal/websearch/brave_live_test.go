//go:build live

package websearch

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBraveProviderLive(t *testing.T) {
	if os.Getenv("NANO_BRAVE_SEARCH_LIVE") != "1" {
		t.Skip("set NANO_BRAVE_SEARCH_LIVE=1 to run the credentialed smoke test")
	}
	apiKey := os.Getenv("NANO_BRAVE_SEARCH_API_KEY")
	if apiKey == "" {
		t.Skip("NANO_BRAVE_SEARCH_API_KEY is unavailable")
	}

	provider, err := NewBraveProvider(BraveConfig{APIKey: apiKey})
	if err != nil {
		t.Fatalf("configure Brave provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	candidates, err := provider.Search(ctx, Request{Query: "Nano Notebook research", Count: 1})
	if err != nil {
		t.Fatalf("bounded Brave search failed: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("bounded Brave search returned no candidates")
	}
	if candidates[0].URL == "" || candidates[0].Title == "" {
		t.Fatal("Brave candidate was not normalized")
	}
}
