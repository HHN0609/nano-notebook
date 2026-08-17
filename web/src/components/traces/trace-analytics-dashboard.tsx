import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { MaterialSymbol } from "../icons/material-symbol";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";

type Locale = "en" | "zh";
type Filters = { range: string; agent: string; model: string; status: string };
type Coverage = { total_samples: number; token_samples?: number; cost_samples?: number };
type Metadata = { schema_version: number; generated_at?: string; fresh_through?: string | null; coverage: Coverage };
type CurrencyTotal = { currency: string; amount: number };
type Overview = Metadata & { data: { run_count: number; completed_count: number; success_rate: number | null; error_rate: number | null; retry_rate: number | null; p95_duration_nanoseconds: number | null; total_tokens: number | null; costs: CurrencyTotal[] } };
type Point = { bucket_started_at: string; run_count: number; success_count: number; error_count: number; active_count: number; p50_duration_nanoseconds: number | null; p95_duration_nanoseconds: number | null; p99_duration_nanoseconds: number | null; total_tokens: number | null; costs: CurrencyTotal[] };
type Timeseries = Metadata & { data: Point[] };
type LatencyRow = { value: string; sample_count: number; p50_duration_nanoseconds: number; p95_duration_nanoseconds: number; p99_duration_nanoseconds: number };
type BreakdownRow = { value: string; run_count: number; success_rate: number | null; error_rate: number | null; retry_rate: number | null; p95_duration_nanoseconds: number | null };
type ToolRow = { value: string; call_count: number; success_rate: number | null; error_rate: number | null; p95_duration_nanoseconds: number | null };

const text = {
  en: { title: "Trace Analytics", library: "Back to Library", analytics: "Analytics", explorer: "Trace Explorer", restricted: "Analytics access restricted", restrictedBody: "A platform.trace.analytics grant is required.", agent: "Agent", model: "Model", status: "Status", range: "Time range", all: "All", apply: "Apply filters", clear: "Clear", runs: "Runs", success: "Success rate", errors: "Error rate", retries: "Retry rate", p95: "p95 latency", tokens: "Tokens", costs: "Known cost", freshness: "Data updated through", stale: "Analytics data may be delayed", overviewUnavailable: "Overview is temporarily unavailable.", trend: "Run and latency trend", trendUnavailable: "Trend is temporarily unavailable.", emptyTrend: "No Runs in this range.", latency: "Latency by Agent", latencyUnavailable: "Latency is temporarily unavailable.", breakdowns: "Breakdowns", behavior: "Behavior & RAG", behaviorDimension: "Behavior dimension", breakdownUnavailable: "Breakdown is temporarily unavailable.", tools: "Tools", toolsUnavailable: "Tool analytics is temporarily unavailable.", noTools: "No tool calls in this range.", unknown: "Unknown", openTraces: "Open Traces for", tokenCoverage: "Token coverage", costUnavailable: "Cost unavailable", coverage: "coverage", calls: "calls" },
  zh: { title: "Trace 分析", library: "返回笔记库", analytics: "分析", explorer: "Trace 调试台", restricted: "分析权限受限", restrictedBody: "需要 platform.trace.analytics 授权。", agent: "Agent", model: "模型", status: "状态", range: "时间范围", all: "全部", apply: "应用筛选", clear: "清除", runs: "Run 数", success: "成功率", errors: "错误率", retries: "重试率", p95: "p95 延迟", tokens: "Token", costs: "已知费用", freshness: "数据更新至", stale: "分析数据可能延迟", overviewUnavailable: "概览暂时不可用。", trend: "Run 与延迟趋势", trendUnavailable: "趋势暂时不可用。", emptyTrend: "当前范围内没有 Run。", latency: "按 Agent 的延迟", latencyUnavailable: "延迟分析暂时不可用。", breakdowns: "维度拆解", behavior: "行为与 RAG", behaviorDimension: "行为维度", breakdownUnavailable: "维度拆解暂时不可用。", tools: "工具", toolsUnavailable: "工具分析暂时不可用。", noTools: "当前范围内没有工具调用。", unknown: "未知", openTraces: "打开 Trace：", tokenCoverage: "Token 覆盖率", costUnavailable: "费用不可用", coverage: "覆盖率", calls: "次调用" }
} satisfies Record<Locale, Record<string, string>>;

type Props = { locale: Locale; canAnalytics: boolean; canRead: boolean; onNavigate: (path: string) => void; onLibrary: () => void };

export function TraceAnalyticsDashboard(props: Props) {
  const t = text[props.locale];
	if (!props.canAnalytics) return <main className="trace-shell trace-restricted"><Button variant="ghost" onClick={props.onLibrary}><MaterialSymbol name="arrow_back" size={18} />{t.library}</Button><section><h1>{t.restricted}</h1><p>{t.restrictedBody}</p></section></main>;
	return <TraceAnalyticsContent {...props} />;
}

function TraceAnalyticsContent(props: Props) {
	const t = text[props.locale];
  const initial = filtersFromURL();
  const [draft, setDraft] = useState(initial);
  const [filters, setFilters] = useState(initial);
	const [behaviorDimension, setBehaviorDimension] = useState("provider");
	const [queryNow] = useState(() => Date.now());
	const query = analyticsQuery(filters, queryNow);
  const overview = useAnalytics<Overview>("overview", query);
  const timeseries = useAnalytics<Timeseries>("timeseries", query);
  const latency = useAnalytics<Metadata & { data: LatencyRow[] }>("latency", `${query}&group_by=agent`);
  const agents = useAnalytics<Metadata & { data: BreakdownRow[] }>("breakdowns", `${query}&group_by=agent&limit=10`);
  const models = useAnalytics<Metadata & { data: BreakdownRow[] }>("breakdowns", `${query}&group_by=model&limit=10`);
  const statuses = useAnalytics<Metadata & { data: BreakdownRow[] }>("breakdowns", `${query}&group_by=status&limit=10`);
  const errors = useAnalytics<Metadata & { data: BreakdownRow[] }>("breakdowns", `${query}&group_by=error_code&limit=10`);
  const behavior = useAnalytics<Metadata & { data: BreakdownRow[] }>("breakdowns", `${query}&group_by=${behaviorDimension}&limit=10`);
  const tools = useAnalytics<Metadata & { data: ToolRow[] }>("tools", `${query}&limit=10`);

  function apply(event: FormEvent) {
    event.preventDefault();
    setFilters(draft);
    replaceFilterURL(draft);
  }
  function clear() {
    const next = { range: "24h", agent: "", model: "", status: "" };
    setDraft(next); setFilters(next); replaceFilterURL(next);
  }
  function drilldown(extra: Partial<Filters>) {
    const next = { ...filters, ...extra };
    const parameters = filterParameters(next);
    props.onNavigate(`/admin/traces?${parameters}`);
  }

	const stale = overview.data?.fresh_through ? queryNow - new Date(overview.data.fresh_through).getTime() > 5_000 : false;
  return <main className="trace-shell analytics-shell">
    <AnalyticsTopbar title={t.title} backLabel={t.library} onBack={props.onLibrary} />
    <nav className="trace-product-tabs" aria-label="Trace views"><button aria-current="page">{t.analytics}</button>{props.canRead ? <button onClick={() => props.onNavigate("/admin/traces")}>{t.explorer}</button> : null}</nav>
    <form className="analytics-filters" onSubmit={apply} aria-label="Analytics filters">
      <label><span>{t.range}</span><select value={draft.range} onChange={(event) => setDraft({ ...draft, range: event.target.value })}><option value="1h">1h</option><option value="24h">24h</option><option value="168h">7d</option><option value="720h">30d</option></select></label>
      <label><span>{t.agent}</span><input value={draft.agent} onChange={(event) => setDraft({ ...draft, agent: event.target.value })} /></label>
      <label><span>{t.model}</span><input value={draft.model} onChange={(event) => setDraft({ ...draft, model: event.target.value })} /></label>
      <label><span>{t.status}</span><select value={draft.status} onChange={(event) => setDraft({ ...draft, status: event.target.value })}><option value="">{t.all}</option><option value="ok">OK</option><option value="error">Error</option><option value="cancelled">Cancelled</option></select></label>
      <div><Button>{t.apply}</Button><Button type="button" variant="secondary" onClick={clear}>{t.clear}</Button></div>
    </form>
    {overview.data?.fresh_through ? <div className={`analytics-freshness${stale ? " stale" : ""}`}><span>{t.freshness} {new Date(overview.data.fresh_through).toLocaleString()}</span>{stale ? <strong>{t.stale}</strong> : null}</div> : null}
    {overview.isError ? <RegionError>{t.overviewUnavailable}</RegionError> : null}
    {overview.data ? <OverviewCards overview={overview.data} t={t} /> : null}
    <AnalyticsSection title={t.trend}>{timeseries.isError ? <RegionError>{t.trendUnavailable}</RegionError> : timeseries.data && timeseries.data.data.length === 0 ? <Empty>{t.emptyTrend}</Empty> : timeseries.data ? <TrendChart points={timeseries.data.data} onDrilldown={(point) => drilldownRange(point.bucket_started_at)} /> : <Loading />}</AnalyticsSection>
    <div className="analytics-two-column">
      <AnalyticsSection title={t.latency}>{latency.isError ? <RegionError>{t.latencyUnavailable}</RegionError> : latency.data ? <LatencyTable rows={latency.data.data} unknown={t.unknown} onOpen={(value) => drilldown({ agent: value })} openLabel={t.openTraces} /> : <Loading />}</AnalyticsSection>
      <AnalyticsSection title={t.tools}>{tools.isError ? <RegionError>{t.toolsUnavailable}</RegionError> : tools.data && tools.data.data.length === 0 ? <Empty>{t.noTools}</Empty> : tools.data ? <ToolTable rows={tools.data.data} unknown={t.unknown} /> : <Loading />}</AnalyticsSection>
    </div>
    <AnalyticsSection title={t.breakdowns}><div className="analytics-breakdowns">{([[t.agent, agents, "agent"], [t.model, models, "model"], [t.status, statuses, "status"], [t.errors, errors, "status"]] as const).map(([label, result, filter]) => <article key={label}><h3>{label}</h3>{result.isError ? <RegionError>{t.breakdownUnavailable}</RegionError> : result.data ? <BreakdownTable rows={result.data.data} unknown={t.unknown} openLabel={t.openTraces} onOpen={(value) => drilldown({ [filter]: value === "unknown" ? "" : value })} /> : <Loading />}</article>)}</div></AnalyticsSection>
    <AnalyticsSection title={t.behavior}>
      <label className="analytics-behavior-filter"><span>{t.behaviorDimension}</span><select aria-label={t.behaviorDimension} value={behaviorDimension} onChange={(event) => setBehaviorDimension(event.target.value)}>{behaviorDimensions(props.locale).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      {behavior.isError ? <RegionError>{t.breakdownUnavailable}</RegionError> : behavior.data ? <PlainBreakdownTable rows={behavior.data.data} unknown={t.unknown} /> : <Loading />}
    </AnalyticsSection>
  </main>;

  function drilldownRange(bucketStartedAt: string) {
    const parameters = filterParameters(filters);
    parameters.set("started_after", bucketStartedAt);
    props.onNavigate(`/admin/traces?${parameters}`);
  }
}

function OverviewCards({ overview, t }: { overview: Overview; t: typeof text.en }) {
  const coverage = overview.coverage.total_samples;
  const tokenCoverage = percentage(overview.coverage.token_samples ?? 0, coverage);
  const costCoverage = percentage(overview.coverage.cost_samples ?? 0, coverage);
  const values: Array<[string, ReactNode, string?]> = [
    [t.runs, formatNumber(overview.data.run_count)], [t.success, formatPercent(overview.data.success_rate)], [t.errors, formatPercent(overview.data.error_rate)], [t.retries, formatPercent(overview.data.retry_rate)],
    [t.p95, formatDuration(overview.data.p95_duration_nanoseconds, t.unknown)], [t.tokens, overview.data.total_tokens === null ? t.unknown : formatNumber(overview.data.total_tokens), `${t.tokenCoverage} ${tokenCoverage}`],
    [t.costs, overview.data.costs.length ? overview.data.costs.map((cost) => `${formatNumber(cost.amount)} ${cost.currency}`).join(" · ") : t.costUnavailable, `${overview.data.costs.length ? "" : `${t.costUnavailable} · `}${costCoverage} ${t.coverage}`]
  ];
  return <section className="analytics-overview" aria-label={t.title}>{values.map(([label, value, note]) => <article key={label}><span>{label}</span><strong>{value}</strong>{note ? <small>{note}</small> : null}</article>)}</section>;
}

function TrendChart({ points, onDrilldown }: { points: Point[]; onDrilldown: (point: Point) => void }) {
  const maximum = Math.max(1, ...points.map((point) => point.run_count));
  return <div className="analytics-trend" role="img" aria-label="Run trend">{points.map((point) => <button key={point.bucket_started_at} onClick={() => onDrilldown(point)} title={new Date(point.bucket_started_at).toLocaleString()}><i style={{ height: `${Math.max(4, point.run_count / maximum * 100)}%` }}><b style={{ height: `${point.run_count ? point.success_count / point.run_count * 100 : 0}%` }} /></i><span>{new Date(point.bucket_started_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span><small>{point.run_count}</small></button>)}</div>;
}

function LatencyTable({ rows, unknown, onOpen, openLabel }: { rows: LatencyRow[]; unknown: string; onOpen: (value: string) => void; openLabel: string }) { return <table className="analytics-table"><thead><tr><th>Agent</th><th>p50</th><th>p95</th><th>p99</th></tr></thead><tbody>{rows.map((row) => <tr key={row.value}><td><button aria-label={`${openLabel} ${row.value}`} onClick={() => onOpen(row.value)}>{row.value || unknown}</button></td><td>{formatDuration(row.p50_duration_nanoseconds, unknown)}</td><td>{formatDuration(row.p95_duration_nanoseconds, unknown)}</td><td>{formatDuration(row.p99_duration_nanoseconds, unknown)}</td></tr>)}</tbody></table>; }
function ToolTable({ rows, unknown }: { rows: ToolRow[]; unknown: string }) { return <table className="analytics-table"><thead><tr><th>Tool</th><th>Calls</th><th>Success</th><th>p95</th></tr></thead><tbody>{rows.map((row) => <tr key={row.value}><td>{row.value || unknown}</td><td>{formatNumber(row.call_count)}</td><td>{formatPercent(row.success_rate)}</td><td>{formatDuration(row.p95_duration_nanoseconds, unknown)}</td></tr>)}</tbody></table>; }
function BreakdownTable({ rows, unknown, openLabel, onOpen }: { rows: BreakdownRow[]; unknown: string; openLabel: string; onOpen: (value: string) => void }) { return <table className="analytics-table"><thead><tr><th>Value</th><th>Runs</th><th>Success</th></tr></thead><tbody>{rows.map((row) => <tr key={row.value}><td><button aria-label={`${openLabel} ${row.value || unknown}`} onClick={() => onOpen(row.value)}>{row.value || unknown}</button></td><td>{formatNumber(row.run_count)}</td><td>{formatPercent(row.success_rate)}</td></tr>)}</tbody></table>; }
function PlainBreakdownTable({ rows, unknown }: { rows: BreakdownRow[]; unknown: string }) { return <table className="analytics-table"><thead><tr><th>Value</th><th>Runs</th><th>Success</th></tr></thead><tbody>{rows.map((row) => <tr key={row.value}><td>{row.value || unknown}</td><td>{formatNumber(row.run_count)}</td><td>{formatPercent(row.success_rate)}</td></tr>)}</tbody></table>; }
function AnalyticsSection({ title, children }: { title: string; children: ReactNode }) { return <section className="analytics-section"><h2>{title}</h2>{children}</section>; }
function RegionError({ children }: { children: ReactNode }) { return <Alert variant="destructive" className="analytics-region-state"><AlertDescription>{children}</AlertDescription></Alert>; }
function Empty({ children }: { children: ReactNode }) { return <p className="analytics-region-state">{children}</p>; }
function Loading() { return <p className="analytics-region-state" role="status">Loading…</p>; }
function AnalyticsTopbar({ title, backLabel, onBack }: { title: string; backLabel: string; onBack: () => void }) { return <header className="trace-topbar"><Button variant="ghost" onClick={onBack}><MaterialSymbol name="arrow_back" size={19} />{backLabel}</Button><h1>{title}</h1><span className="trace-live-dot" aria-hidden="true" /></header>; }

function useAnalytics<T>(endpoint: string, query: string) { return useQuery({ queryKey: ["trace-analytics", endpoint, query], queryFn: () => analyticsJSON<T>(`/api/admin/trace-analytics/${endpoint}?${query}`), retry: false }); }
async function analyticsJSON<T>(path: string): Promise<T> { const response = await fetch(path, { credentials: "include" }); if (!response.ok) throw new Error("trace analytics unavailable"); return response.json() as Promise<T>; }
function filtersFromURL(): Filters { const values = new URLSearchParams(window.location.search); return { range: values.get("range") ?? "24h", agent: values.get("agent") ?? "", model: values.get("model") ?? "", status: values.get("status") ?? "" }; }
function filterParameters(filters: Filters) { const values = new URLSearchParams(); values.set("range", filters.range); if (filters.agent) values.set("agent", filters.agent); if (filters.model) values.set("model", filters.model); if (filters.status) values.set("status", filters.status); return values; }
function replaceFilterURL(filters: Filters) { const url = new URL(window.location.href); url.search = filterParameters(filters).toString(); window.history.replaceState(null, "", url); }
function analyticsQuery(filters: Filters, now: number) { const hours = Math.min(720, Math.max(1, Number.parseInt(filters.range, 10) || 24)); const values = new URLSearchParams(); values.set("started_after", new Date(now - hours * 3_600_000).toISOString()); values.set("started_before", new Date(now).toISOString()); values.set("bucket", hours <= 24 ? "1h" : hours <= 168 ? "6h" : "1d"); values.set("workload", "agent_run"); if (filters.agent) values.set("agent", filters.agent); if (filters.model) values.set("model", filters.model); if (filters.status) values.set("status", filters.status); return values.toString(); }
function behaviorDimensions(locale: Locale): Array<[string, string]> { return locale === "zh" ? [["provider", "Provider"], ["stop_reason", "停止原因"], ["agent_definition", "Agent Definition"], ["prompt_version", "Prompt 版本"], ["configuration_version", "配置版本"], ["delegation_target", "委派目标"], ["delegation_outcome", "委派结果"], ["rag_stage", "RAG 阶段"], ["rag_degradation", "RAG 降级"], ["citation_outcome", "引用结果"]] : [["provider", "Provider"], ["stop_reason", "Stop reason"], ["agent_definition", "Agent Definition"], ["prompt_version", "Prompt version"], ["configuration_version", "Configuration version"], ["delegation_target", "Delegation target"], ["delegation_outcome", "Delegation outcome"], ["rag_stage", "RAG stage"], ["rag_degradation", "RAG degradation"], ["citation_outcome", "Citation outcome"]]; }
function percentage(sample: number, total: number) { return `${total ? Math.round(sample / total * 100) : 0}%`; }
function formatPercent(value: number | null) { return value === null ? "Unknown" : `${Math.round(value * 1000) / 10}%`; }
function formatNumber(value: number) { return new Intl.NumberFormat().format(value); }
function formatDuration(value: number | null, unknown: string) { if (value === null) return unknown; const seconds = value / 1_000_000_000; return seconds < 1 ? `${Math.round(value / 1_000_000)} ms` : `${seconds.toFixed(2)} s`; }
