package obsbench_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/obsbench"
)

func TestProductQueryPlanV1FreezesAcceptedWeights(t *testing.T) {
	plan, err := obsbench.NewProductQueryPlanV1(obsbench.ReferenceWorkloadV1(), "query-seed-v1", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 100 {
		t.Fatalf("plan length=%d", len(plan))
	}
	counts := map[obsbench.ProductQueryKind]int{}
	for _, query := range plan {
		counts[query.Kind]++
		if !strings.HasPrefix(query.Path, "/internal/agent-observability/v1/traces") {
			t.Fatalf("query path=%q", query.Path)
		}
		if _, err := url.ParseRequestURI(query.Path); err != nil {
			t.Fatalf("query path %q is invalid: %v", query.Path, err)
		}
	}
	want := map[obsbench.ProductQueryKind]int{
		obsbench.QueryRecentList: 25, obsbench.QueryCombinedFilter: 20,
		obsbench.QueryDeepCursor: 10, obsbench.QueryExactIdentity: 15,
		obsbench.QueryOrdinaryDetail: 20, obsbench.QueryComplexDetail: 10,
	}
	for kind, expected := range want {
		if counts[kind] != expected {
			t.Errorf("%s count=%d, want %d", kind, counts[kind], expected)
		}
	}
}

func TestSummarizeQueryLatenciesUsesNearestRankPercentiles(t *testing.T) {
	samples := make([]time.Duration, 100)
	for index := range samples {
		samples[index] = time.Duration(index+1) * time.Millisecond
	}
	latency, err := obsbench.SummarizeQueryLatencies(samples)
	if err != nil {
		t.Fatal(err)
	}
	if latency.P50Milliseconds != 50 || latency.P95Milliseconds != 95 || latency.P99Milliseconds != 99 {
		t.Fatalf("latency=%#v", latency)
	}
}

func TestRunProductQueriesExecutesRepeatedPlanWithBoundedConcurrency(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer query-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.RawQuery, "__CURSOR__") {
			t.Errorf("deep cursor placeholder was not replaced: %s", r.URL.String())
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/internal/agent-observability/v1/traces" {
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "data": map[string]any{
				"items": []any{}, "next_cursor": "cursor-v1",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "data": map[string]any{}})
	}))
	defer server.Close()

	plan, err := obsbench.NewProductQueryPlanV1(obsbench.ReferenceWorkloadV1(), "query-seed-v1", 100)
	if err != nil {
		t.Fatal(err)
	}
	result, err := obsbench.RunProductQueries(context.Background(), obsbench.ProductQueryRunnerConfig{
		BaseURL: server.URL, Token: "query-token", Plan: plan,
		Requests: 200, Concurrency: 8, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 200 || result.Errors != 0 || requests.Load() != 201 {
		t.Fatalf("result=%#v server_requests=%d", result, requests.Load())
	}
	if result.ByKind[obsbench.QueryRecentList].Completed != 50 || result.ByKind[obsbench.QueryComplexDetail].Completed != 20 {
		t.Fatalf("by_kind=%#v", result.ByKind)
	}
}

func TestProductQueryPlanV1UsesMaterializedDeterministicIdentities(t *testing.T) {
	plan, err := obsbench.NewProductQueryPlanV1(obsbench.ReferenceWorkloadV1(), "query-seed-v1", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range plan {
		if query.Kind == obsbench.QueryExactIdentity || query.Kind == obsbench.QueryOrdinaryDetail || query.Kind == obsbench.QueryComplexDetail {
			if !strings.Contains(query.Path, "trace-") {
				t.Fatalf("identity query has no deterministic trace ID: %#v", query)
			}
		}
	}
}
