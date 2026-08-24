package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sprint5CapacitySummaryCount = 100_000

func TestSprint5QueryCapacityAtControlPlaneBoundary(t *testing.T) {
	if os.Getenv("NANO_RUN_SPRINT5_CAPACITY") != "1" {
		t.Skip("set NANO_RUN_SPRINT5_CAPACITY=1 to run the production-shaped capacity gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	observability := openCapacityObservabilityPool(t, ctx)
	defer observability.Close()
	resetCapacityObservability(t, ctx, observability)
	seedStarted := time.Now()
	seedCapacitySummaries(t, ctx, observability, sprint5CapacitySummaryCount)
	maximumTraceID := capacityTraceID(sprint5CapacitySummaryCount - 1)
	seedMaximumCapacityDetail(t, ctx, observability, maximumTraceID, 256)
	t.Logf("seeded %d Trace summaries and 256-Span detail in %s", sprint5CapacitySummaryCount, time.Since(seedStarted))

	queryStore, err := collector.NewTraceQueryStore(observability, nil)
	if err != nil {
		t.Fatal(err)
	}
	ingestor, _ := collector.NewIngestor(collector.IngestorConfig{ProducerID: "capacity-producer", Store: collector.NewMemoryStore()})
	collectorHandler, err := collector.NewHTTPHandler(collector.HTTPConfig{
		Ingestor: ingestor, ServiceToken: "capacity-ingest-token", QueryStore: queryStore,
		QueryToken: "capacity-query-token", MaxBodyBytes: 2 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	collectorServer := httptest.NewServer(collectorHandler)
	defer collectorServer.Close()
	queryClient, err := collector.NewHTTPQueryClient(collector.HTTPQueryClientConfig{
		Endpoint: collectorServer.URL, ServiceToken: "capacity-query-token",
		Client: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}

	api := newTestAPI(t)
	session, _ := api.registerWithCSRF(t, "sprint5-capacity-operator@example.com")
	var userID string
	if err := api.db.Pool().QueryRow(ctx, `select id from identity_users where canonical_email = 'sprint5-capacity-operator@example.com'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	grantCapability(t, api, userID, "platform.trace.read")
	server := app.NewServer(newConfiguredServerConfig(app.Config{CookieSecure: false, AdminTraces: queryClient}), api.db)
	controlPlane := httptest.NewServer(server.Handler())
	defer controlPlane.Close()

	listPath := "/api/admin/traces?page_size=50"
	for range 3 {
		assertCapacityGET(t, controlPlane.URL+listPath, session)
	}
	listP95 := measureCapacityP95(t, 20, func() {
		assertCapacityGET(t, controlPlane.URL+listPath, session)
	})
	if listP95 >= 500*time.Millisecond {
		t.Fatalf("Trace list p95 = %s, want < 500ms", listP95)
	}

	detailPath := "/api/admin/traces/" + maximumTraceID
	for range 3 {
		assertCapacityGET(t, controlPlane.URL+detailPath, session)
	}
	detailP95 := measureCapacityP95(t, 20, func() {
		assertCapacityGET(t, controlPlane.URL+detailPath, session)
	})
	if detailP95 >= time.Second {
		t.Fatalf("maximum Trace detail p95 = %s, want < 1s", detailP95)
	}

	first := capacityListPage(t, controlPlane.URL+"/api/admin/traces?page_size=50", session)
	seedCapacitySummary(t, ctx, observability, sprint5CapacitySummaryCount+1, capacityStartedAt(sprint5CapacitySummaryCount+1), 1)
	second := capacityListPage(t, controlPlane.URL+"/api/admin/traces?page_size=50&cursor="+url.QueryEscape(first.Data.NextCursor), session)
	seen := make(map[string]bool, len(first.Data.Items))
	for _, item := range first.Data.Items {
		seen[string(item.Summary.TraceID)] = true
	}
	for _, item := range second.Data.Items {
		if seen[string(item.Summary.TraceID)] {
			t.Fatalf("cursor page repeated Trace %q after concurrent ingestion", item.Summary.TraceID)
		}
	}
	t.Logf("Sprint 5 capacity: list_p95=%s detail_256_span_p95=%s", listP95, detailP95)
}

func openCapacityObservabilityPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NANO_TEST_OBSERVABILITY_CAPACITY_DATABASE_URL")
	if dsn == "" {
		t.Fatal("NANO_TEST_OBSERVABILITY_CAPACITY_DATABASE_URL is required for the capacity gate")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_capacity") {
		t.Fatalf("capacity gate refuses non-capacity database %q", config.ConnConfig.Database)
	}
	config.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool
}

// requireTestDatabase refuses to let a destructive schema reset run against
// anything but a dedicated "_test"-suffixed database (repo convention, see
// scripts/test-go) — the guard that was missing when a hand-typed test DSN
// once pointed at the dev database and wiped it.
func requireTestDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var databaseName string
	if err := pool.QueryRow(ctx, `select current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("refusing to reset database %q: only databases whose name ends in \"_test\" may be reset", databaseName)
	}
}

func resetCapacityObservability(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	requireTestDatabase(t, ctx, pool)
	if _, err := pool.Exec(ctx, `drop schema if exists public cascade`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create schema public`); err != nil {
		t.Fatal(err)
	}
	if err := collector.RunMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
}

func seedCapacitySummaries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, count int) {
	t.Helper()
	traceColumns := []string{"trace_id", "run_id", "chat_id", "notebook_id", "root_span_id", "agent_name", "schema_version", "semantic_convention_version", "committed_sequence", "projected_sequence"}
	inserted, err := pool.CopyFrom(ctx, pgx.Identifier{"obs_traces"}, traceColumns, pgx.CopyFromSlice(count, func(index int) ([]any, error) {
		sequence := 1
		if index == count-1 {
			sequence = 256
		}
		traceID := capacityTraceID(index)
		return []any{traceID, capacityRunID(index), capacityChatID(index), "notebook-capacity", capacityRootSpanID(index), "nano-research-agent", 1, 1, sequence, sequence}, nil
	}))
	if err != nil || inserted != int64(count) {
		t.Fatalf("seed obs_traces inserted=%d: %v", inserted, err)
	}
	summaryColumns := []string{"trace_id", "run_id", "chat_id", "notebook_id", "root_span_id", "agent_name", "started_at_unix_nano", "last_observed_unix_nano", "ended_at_unix_nano", "duration_nanoseconds", "status", "active", "models", "input_tokens", "output_tokens", "total_tokens", "cost_known", "cost_amount", "cost_currency", "cost_source", "attempt_count", "projected_sequence"}
	inserted, err = pool.CopyFrom(ctx, pgx.Identifier{"obs_trace_summaries"}, summaryColumns, pgx.CopyFromSlice(count, func(index int) ([]any, error) {
		sequence := 1
		if index == count-1 {
			sequence = 256
		}
		started := capacityStartedAt(index)
		return []any{capacityTraceID(index), capacityRunID(index), capacityChatID(index), "notebook-capacity", capacityRootSpanID(index), "nano-research-agent", started, started + 1_000_000_000, started + 1_000_000_000, int64(1_000_000_000), "ok", false, []string{"qwen-flash"}, int64(64), int64(32), int64(96), true, 0.001, "USD", "provider_reported", 1, sequence}, nil
	}))
	if err != nil || inserted != int64(count) {
		t.Fatalf("seed obs_trace_summaries inserted=%d: %v", inserted, err)
	}
}

func seedMaximumCapacityDetail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, traceID string, spanCount int) {
	t.Helper()
	columns := []string{"trace_id", "span_id", "parent_span_id", "name", "start_sequence", "end_sequence", "started_at_unix_nano", "ended_at_unix_nano", "duration_nanoseconds", "status", "start_attributes", "end_attributes", "replay_references", "model_analysis"}
	rootSpanID := capacityRootSpanID(sprint5CapacitySummaryCount - 1)
	started := capacityStartedAt(sprint5CapacitySummaryCount - 1)
	inserted, err := pool.CopyFrom(ctx, pgx.Identifier{"obs_spans"}, columns, pgx.CopyFromSlice(spanCount, func(index int) ([]any, error) {
		spanID, parentID, name := fmt.Sprintf("span-cap-%03d", index), rootSpanID, "agent.action"
		if index == 0 {
			spanID, parentID, name = rootSpanID, "", "agent.execution"
		}
		observed := started + int64(index)*1_000_000
		return []any{traceID, spanID, parentID, name, index + 1, index + 1, observed, observed + 1_000_000, int64(1_000_000), "ok", []byte(`[]`), []byte(`[]`), []byte(`[]`), nil}, nil
	}))
	if err != nil || inserted != int64(spanCount) {
		t.Fatalf("seed obs_spans inserted=%d: %v", inserted, err)
	}
}

func seedCapacitySummary(t *testing.T, ctx context.Context, pool *pgxpool.Pool, index int, started int64, sequence int) {
	t.Helper()
	traceID := capacityTraceID(index)
	if _, err := pool.Exec(ctx, `insert into obs_traces(trace_id,run_id,chat_id,notebook_id,root_span_id,agent_name,schema_version,semantic_convention_version,committed_sequence,projected_sequence) values($1,$2,$3,'notebook-capacity',$4,'nano-research-agent',1,1,$5,$5)`, traceID, capacityRunID(index), capacityChatID(index), capacityRootSpanID(index), sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into obs_trace_summaries(trace_id,run_id,chat_id,notebook_id,root_span_id,agent_name,started_at_unix_nano,last_observed_unix_nano,ended_at_unix_nano,duration_nanoseconds,status,active,models,input_tokens,output_tokens,total_tokens,cost_known,cost_amount,cost_currency,cost_source,attempt_count,projected_sequence) values($1,$2,$3,'notebook-capacity',$4,'nano-research-agent',$5,$5,$5,0,'ok',false,array['qwen-flash'],0,0,0,true,0,'USD','provider_reported',1,$6)`, traceID, capacityRunID(index), capacityChatID(index), capacityRootSpanID(index), started, sequence); err != nil {
		t.Fatal(err)
	}
}

func assertCapacityGET(t *testing.T, target string, session *http.Cookie) {
	t.Helper()
	response := capacityGET(t, target, session)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("GET %s status=%d body=%s", target, response.StatusCode, body)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}

func capacityListPage(t *testing.T, target string, session *http.Cookie) struct {
	Data collector.TraceListResult `json:"data"`
} {
	t.Helper()
	response := capacityGET(t, target, session)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", target, response.StatusCode)
	}
	var page struct {
		Data collector.TraceListResult `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.Items) != 50 || page.Data.NextCursor == "" {
		t.Fatalf("capacity page items=%d cursor=%q", len(page.Data.Items), page.Data.NextCursor)
	}
	return page
}

func capacityGET(t *testing.T, target string, session *http.Cookie) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(session)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func measureCapacityP95(t *testing.T, samples int, operation func()) time.Duration {
	t.Helper()
	durations := make([]time.Duration, 0, samples)
	for range samples {
		started := time.Now()
		operation()
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	return durations[(samples*95+99)/100-1]
}

func capacityTraceID(index int) string    { return fmt.Sprintf("trace-cap-%06d", index) }
func capacityRunID(index int) string      { return fmt.Sprintf("run-cap-%06d", index) }
func capacityChatID(index int) string     { return fmt.Sprintf("chat-cap-%06d", index%10_000) }
func capacityRootSpanID(index int) string { return fmt.Sprintf("root-cap-%06d", index) }
func capacityStartedAt(index int) int64   { return 1_700_000_000_000_000_000 + int64(index)*1_000_000 }
