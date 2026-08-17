package main

import (
	"testing"
	"time"
)

func TestParseConfigRequiresFrozenDatasetAndBoundedQueryLoad(t *testing.T) {
	parsed, err := parseConfig([]string{
		"-base-url", "http://10.42.0.10:8082", "-token", "query-token",
		"-seed", "primary-1m-v1", "-roots", "1000000",
		"-requests", "10000", "-concurrency", "16", "-timeout", "2m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.BaseURL != "http://10.42.0.10:8082" || parsed.Roots != 1_000_000 || parsed.Requests != 10_000 ||
		parsed.Concurrency != 16 || parsed.Timeout != 2*time.Minute {
		t.Fatalf("config=%#v", parsed)
	}
	if _, err := parseConfig([]string{"-base-url", "http://127.0.0.1:8082"}); err == nil {
		t.Fatal("incomplete query benchmark configuration was accepted")
	}
}
