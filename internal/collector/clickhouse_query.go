package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
)

type ClickHouseTraceQueryStore struct {
	connection driver.Conn
	raw        *ClickHouseStore
}

func NewClickHouseTraceQueryStore(connection driver.Conn) (*ClickHouseTraceQueryStore, error) {
	if connection == nil {
		return nil, errors.New("Collector ClickHouse Trace query database is required")
	}
	raw, err := NewClickHouseStore(connection)
	if err != nil {
		return nil, err
	}
	return &ClickHouseTraceQueryStore{connection: connection, raw: raw}, nil
}

func (s *ClickHouseTraceQueryStore) List(ctx context.Context, query TraceListQuery) (TraceListResult, error) {
	if s == nil || s.connection == nil {
		return TraceListResult{}, errors.New("nil Collector ClickHouse Trace query Store")
	}
	if query.PageSize == 0 {
		query.PageSize = 50
	}
	if query.PageSize < 1 || query.PageSize > 100 || len(query.IdentityExact) > 128 || len(query.IdentityPrefix) > 128 ||
		len(query.AgentName) > 160 || len(query.ModelName) > 160 || len(query.Status) > 32 || len(query.Cursor) > 512 {
		return TraceListResult{}, errors.New("Collector Trace list query bounds are invalid")
	}
	if query.StartedAfterUnixNano != nil && query.StartedBeforeUnixNano != nil && *query.StartedAfterUnixNano >= *query.StartedBeforeUnixNano {
		return TraceListResult{}, errors.New("Collector Trace time range is invalid")
	}
	clauses := make([]string, 0, 8)
	args := make([]any, 0, 12)
	bind := func(clause string, values ...any) {
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	if query.StartedAfterUnixNano != nil {
		bind("s.started_at_unix_nano >= ?", *query.StartedAfterUnixNano)
	}
	if query.StartedBeforeUnixNano != nil {
		bind("s.started_at_unix_nano < ?", *query.StartedBeforeUnixNano)
	}
	if query.IdentityExact != "" {
		bind("(s.trace_id = ? OR s.workload_id = ? OR s.run_id = ? OR s.chat_id = ?)",
			query.IdentityExact, query.IdentityExact, query.IdentityExact, query.IdentityExact)
	}
	if query.IdentityPrefix != "" {
		bind("(startsWith(s.trace_id, ?) OR startsWith(s.workload_id, ?) OR startsWith(s.run_id, ?) OR startsWith(s.chat_id, ?))",
			query.IdentityPrefix, query.IdentityPrefix, query.IdentityPrefix, query.IdentityPrefix)
	}
	if query.AgentName != "" {
		bind("s.agent_name = ?", query.AgentName)
	}
	if query.ModelName != "" {
		bind("has(s.models, ?)", query.ModelName)
	}
	if query.Status != "" {
		bind("s.status = ?", query.Status)
	}
	if query.Active != nil {
		bind("s.active = ?", *query.Active)
	}
	if query.Cursor != "" {
		cursor, err := decodeTraceCursor(query.Cursor)
		if err != nil {
			return TraceListResult{}, err
		}
		bind("(s.started_at_unix_nano, s.trace_id) < (?, ?)", cursor.StartedAtUnixNano, string(cursor.TraceID))
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, query.PageSize+1)
	rows, err := s.connection.Query(ctx, `
		SELECT trace_id, workload_kind, workload_id, run_id, chat_id, notebook_id,
			root_span_id, agent_name, started_at_unix_nano, last_observed_unix_nano,
			ended_at_unix_nano, duration_nanoseconds, status, active, models,
			input_tokens, output_tokens, total_tokens, cost_known, cost_amount,
			cost_currency, cost_source, attempt_count, committed_sequence, projected_sequence
		FROM obs_trace_summaries AS s FINAL
		`+where+`
		ORDER BY started_at_unix_nano DESC, trace_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return TraceListResult{}, fmt.Errorf("query Collector ClickHouse Trace summaries: %w", err)
	}
	defer rows.Close()
	result := TraceListResult{Items: make([]TraceListItem, 0, query.PageSize)}
	for rows.Next() {
		var item TraceListItem
		var traceID, workloadKind, rootSpanID, status string
		var attemptCount, committedThrough, projectedThrough uint32
		summary := &item.Summary
		if err := rows.Scan(
			&traceID, &workloadKind, &summary.WorkloadID, &summary.RunID, &summary.ChatID,
			&summary.NotebookID, &rootSpanID, &summary.AgentName, &summary.StartedAtUnixNano,
			&summary.LastObservedUnixNano, &summary.EndedAtUnixNano, &summary.DurationNanoseconds,
			&status, &summary.Active, &summary.Models, &summary.InputTokens, &summary.OutputTokens,
			&summary.TotalTokens, &summary.Cost.Known, &summary.Cost.Amount, &summary.Cost.Currency,
			&summary.Cost.Source, &attemptCount, &committedThrough, &projectedThrough,
		); err != nil {
			return TraceListResult{}, err
		}
		summary.TraceID = agentobs.TraceID(traceID)
		summary.WorkloadKind = WorkloadKind(workloadKind)
		summary.RootSpanID = agentobs.SpanID(rootSpanID)
		summary.Status = agentobs.Status(status)
		summary.AttemptCount = int(attemptCount)
		item.CommittedThrough = int(committedThrough)
		item.ProjectedThrough = int(projectedThrough)
		item.ProjectionLagged = item.ProjectedThrough < item.CommittedThrough
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return TraceListResult{}, err
	}
	if len(result.Items) > query.PageSize {
		result.Items = result.Items[:query.PageSize]
		last := result.Items[len(result.Items)-1].Summary
		result.NextCursor = encodeTraceCursor(traceCursor{StartedAtUnixNano: last.StartedAtUnixNano, TraceID: last.TraceID})
	}
	return result, nil
}

func (s *ClickHouseTraceQueryStore) Detail(ctx context.Context, traceID agentobs.TraceID) (ProjectedTrace, error) {
	if s == nil || s.raw == nil || traceID == "" || len(traceID) > 128 {
		return ProjectedTrace{}, errors.New("Collector ClickHouse Trace detail query is invalid")
	}
	stored, err := s.raw.LoadTrace(ctx, traceID)
	if err != nil {
		return ProjectedTrace{}, err
	}
	if stored.CommittedThrough == 0 {
		return ProjectedTrace{}, ErrTraceNotFound
	}
	projection, err := BuildTraceProjection(stored)
	if err != nil {
		return ProjectedTrace{}, ErrProjectionPending
	}
	return ProjectedTrace{
		Projection: projection, CommittedThrough: stored.CommittedThrough,
		ProjectedThrough: stored.CommittedThrough,
	}, nil
}

func (s *ClickHouseTraceQueryStore) Replay(context.Context, agentobs.TraceID, agentobs.SpanID, string) (OpaqueReplay, error) {
	return OpaqueReplay{}, ErrReplayNotFound
}
