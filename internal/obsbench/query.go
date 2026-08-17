package obsbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ProductQueryKind string

const (
	QueryRecentList     ProductQueryKind = "recent_list"
	QueryCombinedFilter ProductQueryKind = "combined_filter"
	QueryDeepCursor     ProductQueryKind = "deep_cursor"
	QueryExactIdentity  ProductQueryKind = "exact_identity"
	QueryOrdinaryDetail ProductQueryKind = "ordinary_detail"
	QueryComplexDetail  ProductQueryKind = "complex_detail"
)

const traceQueryPath = "/internal/agent-observability/v1/traces"

type ProductQuery struct {
	Kind ProductQueryKind `json:"kind"`
	Path string           `json:"path"`
}

type ProductQueryRunnerConfig struct {
	BaseURL     string
	Token       string
	Plan        []ProductQuery
	Requests    int
	Concurrency int
	HTTPClient  *http.Client
}

type ProductQueryKindResult struct {
	Completed int     `json:"completed"`
	Errors    int     `json:"errors"`
	Latency   Latency `json:"latency"`
}

type ProductQueryResult struct {
	Completed int                                         `json:"completed"`
	Errors    int                                         `json:"errors"`
	Latency   Latency                                     `json:"latency"`
	ByKind    map[ProductQueryKind]ProductQueryKindResult `json:"by_kind"`
}

type querySample struct {
	kind    ProductQueryKind
	latency time.Duration
	err     error
}

func RunProductQueries(ctx context.Context, config ProductQueryRunnerConfig) (ProductQueryResult, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.Token = strings.TrimSpace(config.Token)
	if config.BaseURL == "" || config.Token == "" || len(config.Plan) == 0 || config.Requests < 1 ||
		config.Concurrency < 1 || config.Concurrency > 256 || config.HTTPClient == nil {
		return ProductQueryResult{}, errors.New("product query runner configuration is incomplete or unbounded")
	}
	if _, err := url.ParseRequestURI(config.BaseURL); err != nil {
		return ProductQueryResult{}, fmt.Errorf("parse product query base URL: %w", err)
	}
	cursor := ""
	for _, query := range config.Plan {
		if query.Kind == QueryDeepCursor {
			var err error
			cursor, err = loadDeepCursor(ctx, config)
			if err != nil {
				return ProductQueryResult{}, err
			}
			break
		}
	}

	samples := make(chan querySample, config.Concurrency)
	var next atomic.Int64
	var workers sync.WaitGroup
	for worker := 0; worker < config.Concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= config.Requests {
					return
				}
				query := config.Plan[index%len(config.Plan)]
				path := strings.ReplaceAll(query.Path, "__CURSOR__", url.QueryEscape(cursor))
				latency, err := executeProductQuery(ctx, config.HTTPClient, config.BaseURL+path, config.Token)
				samples <- querySample{kind: query.Kind, latency: latency, err: err}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(samples)
	}()

	result := ProductQueryResult{ByKind: make(map[ProductQueryKind]ProductQueryKindResult)}
	allLatencies := make([]time.Duration, 0, config.Requests)
	latenciesByKind := make(map[ProductQueryKind][]time.Duration)
	for sample := range samples {
		kindResult := result.ByKind[sample.kind]
		if sample.err != nil {
			result.Errors++
			kindResult.Errors++
		} else {
			result.Completed++
			kindResult.Completed++
			allLatencies = append(allLatencies, sample.latency)
			latenciesByKind[sample.kind] = append(latenciesByKind[sample.kind], sample.latency)
		}
		result.ByKind[sample.kind] = kindResult
	}
	if len(allLatencies) > 0 {
		result.Latency, _ = SummarizeQueryLatencies(allLatencies)
	}
	for kind, kindResult := range result.ByKind {
		if len(latenciesByKind[kind]) > 0 {
			kindResult.Latency, _ = SummarizeQueryLatencies(latenciesByKind[kind])
			result.ByKind[kind] = kindResult
		}
	}
	return result, nil
}

func SummarizeQueryLatencies(samples []time.Duration) (Latency, error) {
	if len(samples) == 0 {
		return Latency{}, errors.New("query latency samples are empty")
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	nearestRank := func(percent int) float64 {
		index := (percent*len(sorted)+99)/100 - 1
		return float64(sorted[index]) / float64(time.Millisecond)
	}
	return Latency{
		P50Milliseconds: nearestRank(50), P95Milliseconds: nearestRank(95), P99Milliseconds: nearestRank(99),
	}, nil
}

func loadDeepCursor(ctx context.Context, config ProductQueryRunnerConfig) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.BaseURL+traceQueryPath+"?page_size=50", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("load deep Trace cursor: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("load deep Trace cursor: HTTP %d", response.StatusCode)
	}
	var body struct {
		Data struct {
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024))
	if err := decoder.Decode(&body); err != nil {
		return "", fmt.Errorf("decode deep Trace cursor: %w", err)
	}
	if body.Data.NextCursor == "" {
		return "", errors.New("deep Trace cursor is unavailable")
	}
	return body.Data.NextCursor, nil
}

func executeProductQuery(ctx context.Context, client *http.Client, endpoint, token string) (time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	startedAt := time.Now()
	response, err := client.Do(request)
	latency := time.Since(startedAt)
	if err != nil {
		return latency, err
	}
	defer response.Body.Close()
	_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 8*1024*1024))
	if copyErr != nil {
		return latency, copyErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return latency, fmt.Errorf("product query returned HTTP %d", response.StatusCode)
	}
	return latency, nil
}

// NewProductQueryPlanV1 freezes one 100-request cycle of the accepted current-
// product Trace query corpus. The caller repeats this immutable cycle.
func NewProductQueryPlanV1(workload Workload, seed string, rootCount uint64) ([]ProductQuery, error) {
	if workload.CycleSize() != 100 || seed == "" || rootCount < 100 {
		return nil, errors.New("product query plan requires the reference workload and at least 100 roots")
	}
	grouped := make([]ProductQuery, 0, 100)
	for index := 0; index < 25; index++ {
		grouped = append(grouped, ProductQuery{Kind: QueryRecentList, Path: traceQueryPath + "?page_size=50"})
	}
	combined := url.Values{
		"active": {"false"}, "agent": {"nano-default-agent"}, "model": {"benchmark-model"},
		"page_size": {"50"}, "status": {"ok"},
	}.Encode()
	for index := 0; index < 20; index++ {
		grouped = append(grouped, ProductQuery{Kind: QueryCombinedFilter, Path: traceQueryPath + "?" + combined})
	}
	for index := 0; index < 10; index++ {
		grouped = append(grouped, ProductQuery{Kind: QueryDeepCursor, Path: traceQueryPath + "?cursor=__CURSOR__&page_size=50"})
	}
	for index := 0; index < 15; index++ {
		identity, err := workload.RootIdentity(seed, uint64(index)%rootCount)
		if err != nil {
			return nil, err
		}
		grouped = append(grouped, ProductQuery{Kind: QueryExactIdentity, Path: traceQueryPath + "?identity=" + url.QueryEscape(identity.TraceID) + "&page_size=50"})
	}
	for index := 0; index < 20; index++ {
		identity, err := workload.RootIdentity(seed, uint64(index%90)%rootCount)
		if err != nil {
			return nil, err
		}
		grouped = append(grouped, ProductQuery{Kind: QueryOrdinaryDetail, Path: fmt.Sprintf("%s/%s", traceQueryPath, url.PathEscape(identity.TraceID))})
	}
	for index := 0; index < 10; index++ {
		identity, err := workload.RootIdentity(seed, uint64(90+index)%rootCount)
		if err != nil {
			return nil, err
		}
		grouped = append(grouped, ProductQuery{Kind: QueryComplexDetail, Path: fmt.Sprintf("%s/%s", traceQueryPath, url.PathEscape(identity.TraceID))})
	}

	plan := make([]ProductQuery, len(grouped))
	for index := range plan {
		plan[index] = grouped[(index*37)%len(grouped)]
	}
	return plan, nil
}
