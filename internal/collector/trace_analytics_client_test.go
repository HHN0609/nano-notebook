package collector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

func TestHTTPQueryClientForwardsBoundedTraceAnalyticsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/agent-observability/v1/trace-analytics/overview" ||
			r.Header.Get("Authorization") != "Bearer query-secret" || r.URL.Query().Get("started_after_unix_nano") != "100" ||
			r.URL.Query().Get("started_before_unix_nano") != "200" || r.URL.Query().Get("bucket") != "5m" ||
			r.URL.Query().Get("agent") != "agent-a" || r.URL.Query().Has("notebook_id") {
			t.Errorf("analytics request path=%s headers=%v query=%v", r.URL.Path, r.Header, r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"generated_at":"2026-08-17T00:00:00Z","fresh_through":null,"filters":{"started_after":"2026-08-16T00:00:00Z","started_before":"2026-08-17T00:00:00Z","workload_kind":"agent_run"},"bucket":"5m","coverage":{"total_samples":7,"token_samples":6,"cost_samples":5},"data":{"run_count":7,"completed_count":6,"success_rate":0.5,"error_rate":0.5,"retry_rate":0.25,"p95_duration_nanoseconds":1000,"input_tokens":10,"output_tokens":5,"total_tokens":15,"costs":[]}}`))
	}))
	defer server.Close()
	client, err := collector.NewHTTPQueryClient(collector.HTTPQueryClientConfig{Endpoint: server.URL, ServiceToken: "query-secret"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Overview(context.Background(), collector.TraceAnalyticsQuery{
		StartedAfterUnixNano: 100, StartedBeforeUnixNano: 200, Bucket: collector.AnalyticsBucketFiveMinutes,
		AgentName: "agent-a", NotebookIDs: []string{"must-not-leave-control-plane"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.RunCount != 7 || result.Coverage.TokenSamples != 6 {
		t.Fatalf("analytics result=%#v", result)
	}
}

func TestHTTPQueryClientForwardsTraceAnalyticsBreakdownsAndTools(t *testing.T) {
	paths := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/internal/agent-observability/v1/trace-analytics/breakdowns" {
			_, _ = w.Write([]byte(`{"schema_version":1,"data":[{"value":"provider-a","run_count":2}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"schema_version":1,"data":[{"value":"search","call_count":3}]}`))
	}))
	defer server.Close()
	client, err := collector.NewHTTPQueryClient(collector.HTTPQueryClientConfig{Endpoint: server.URL, ServiceToken: "query-secret"})
	if err != nil {
		t.Fatal(err)
	}
	breakdowns, err := client.Breakdowns(context.Background(), collector.TraceAnalyticsQuery{GroupBy: collector.AnalyticsDimensionProvider, Limit: 7})
	if err != nil || len(breakdowns.Data) != 1 || breakdowns.Data[0].Value != "provider-a" {
		t.Fatalf("breakdowns=%#v err=%v", breakdowns, err)
	}
	toolRows, err := client.Tools(context.Background(), collector.TraceAnalyticsQuery{Limit: 9})
	if err != nil || len(toolRows.Data) != 1 || toolRows.Data[0].CallCount != 3 {
		t.Fatalf("tools=%#v err=%v", toolRows, err)
	}
	if got := <-paths; got != "/internal/agent-observability/v1/trace-analytics/breakdowns?group_by=provider&limit=7" {
		t.Fatalf("breakdowns path=%q", got)
	}
	if got := <-paths; got != "/internal/agent-observability/v1/trace-analytics/tools?limit=9" {
		t.Fatalf("tools path=%q", got)
	}
}
