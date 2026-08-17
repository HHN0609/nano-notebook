package collector

import (
	"context"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

type metricTraceAnalyticsQueries struct {
	next    TraceAnalyticsQueries
	metrics *metrics.Catalog
}

func WithTraceAnalyticsMetrics(next TraceAnalyticsQueries, catalog *metrics.Catalog) TraceAnalyticsQueries {
	if next == nil || catalog == nil {
		return next
	}
	return &metricTraceAnalyticsQueries{next: next, metrics: catalog}
}

func (q *metricTraceAnalyticsQueries) Overview(ctx context.Context, value TraceAnalyticsQuery) (TraceAnalyticsOverviewResult, error) {
	started := time.Now()
	result, err := q.next.Overview(ctx, value)
	q.observe("analytics_overview", started, err)
	return result, err
}
func (q *metricTraceAnalyticsQueries) Timeseries(ctx context.Context, value TraceAnalyticsQuery) (TraceAnalyticsTimeseriesResult, error) {
	started := time.Now()
	result, err := q.next.Timeseries(ctx, value)
	q.observe("analytics_timeseries", started, err)
	return result, err
}
func (q *metricTraceAnalyticsQueries) Latency(ctx context.Context, value TraceAnalyticsQuery) (TraceAnalyticsLatencyResult, error) {
	started := time.Now()
	result, err := q.next.Latency(ctx, value)
	q.observe("analytics_latency", started, err)
	return result, err
}
func (q *metricTraceAnalyticsQueries) Breakdowns(ctx context.Context, value TraceAnalyticsQuery) (TraceAnalyticsBreakdownResult, error) {
	started := time.Now()
	result, err := q.next.Breakdowns(ctx, value)
	q.observe("analytics_breakdowns", started, err)
	return result, err
}
func (q *metricTraceAnalyticsQueries) Tools(ctx context.Context, value TraceAnalyticsQuery) (TraceAnalyticsToolsResult, error) {
	started := time.Now()
	result, err := q.next.Tools(ctx, value)
	q.observe("analytics_tools", started, err)
	return result, err
}
func (q *metricTraceAnalyticsQueries) observe(operation string, started time.Time, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	q.metrics.ClickHouseRequests.WithLabelValues(operation, outcome).Inc()
	q.metrics.ClickHouseRequestDuration.WithLabelValues(operation, outcome).Observe(time.Since(started).Seconds())
}
