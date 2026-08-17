# Trace Analytics Dashboard 补充设计

**日期：** 2026-08-17

**状态：** 待评审

**关联文档：** `docs/implementation/observability-kafka-clickhouse-evaluation-report.md`

## 1. 目标

Nano Notebook 现有 Trace 页面已经能够筛选 Trace、查看调用树和时间线、分析单条 Trace 的耗时、Token、费用，并在权限允许时读取 Replay。下一阶段不是简单地给列表多加几个筛选条件，而是补充一层跨 Trace 的分析能力，让使用者先发现“哪个 Agent、模型或工具出现了异常”，再下钻到具体 Trace 定位原因。

本设计把两类看板明确分开：

- 产品 Trace Dashboard 面向 Agent 开发和效果分析，通过受控 API 查询 ClickHouse 中的高基数 Trace 数据；
- Grafana 面向系统运行和告警，通过 Prometheus 查看 Kafka、Processor、ClickHouse 和服务进程是否健康。

两者可以使用相同的数据链路，但不承担相同职责。产品 Dashboard 不直接查询 Grafana，浏览器也不直接连接 ClickHouse。

## 2. 当前已经具备的能力

### 2.1 Trace Explorer

当前 `/admin/traces` 已具备：

- 按时间、Trace/Run/Chat 标识、Agent、模型、状态和活跃状态筛选；
- 展示开始时间、状态、Agent/模型、总耗时、Token 和费用；
- 查看单条 Trace 的调用树、时间线、Span 属性、事件、链接、RAG 阶段和模型调用明细；
- 在能力授权和存储支持的前提下读取 Replay。

它适合作为分析结果的证据下钻页，但尚不能回答“过去一天成功率如何变化”“哪个模型 p95 延迟最高”这类跨 Trace 问题。

### 2.2 ClickHouse 数据

当前 `obs_trace_summaries` 已提供首版聚合需要的核心字段：

- 时间、Agent、模型、状态和是否仍在执行；
- Trace 总耗时和 Attempt 数；
- 输入、输出、总 Token；
- 已知费用、币种和费用来源；
- Notebook、Run、Chat 和 workload 标识。

`obs_trace_records_raw` 保存事件级事实和规范化 payload，可用于继续提取工具、错误、委派、RAG 等维度。

现有冻结测试说明 ClickHouse 的定位应当保持克制：它在当前 Trace 列表/详情查询上比 PostgreSQL 慢，但在跨 Trace 的时间桶和分位数聚合上明显更快。因此，现有 Explorer 查询和新增 Analytics 查询必须是两个独立的读模型，不能把全部请求统一改成 ClickHouse 扫描。

### 2.3 Prometheus 指标

当前指标目录已经覆盖 HTTP、SSE、Agent Attempt、模型调用、工具执行、检索阶段、端到端耗时、错误、Worker 状态和数据库连接池。它们适合构建服务健康、容量和 SLO 看板。

现阶段缺少的是 Kafka 到 ClickHouse 这一段的运维闭环，而不是缺少另一套按用户、Run 或 Trace 打标签的 Prometheus 指标。

## 3. 必须补充的产品分析能力

### 3.1 统一分析口径

聚合接口实现前必须先固定指标语义，否则不同图表会产生互相矛盾的数字。

首版统一采用以下定义：

| 指标 | 定义 |
|---|---|
| Run 数 | 时间范围内开始的 Agent Run Trace 数；不把 Source Processing 混入 Agent Run 指标 |
| 完成数 | `active = false` 的 Run 数 |
| 成功率 | 终态 Run 中 `status = ok` 的比例；活跃 Run 不进入分母 |
| 错误率 | 终态 Run 中 `status = error` 的比例 |
| 延迟 | 只统计具有非空 `duration_nanoseconds` 的终态 Run，展示 p50、p95、p99 |
| 重试率 | `attempt_count > 1` 的终态 Run 比例 |
| Token | 已记录的 input、output 和 total Token 之和，同时返回有值样本占比 |
| 费用 | 只汇总 `cost_known = true` 且币种一致的记录；同时展示费用覆盖率，禁止把未知费用当作 0 |

所有时间在服务端按 UTC 计算，响应返回明确的时间边界和 bucket；前端只负责本地化展示。

### 3.2 分析接口

在 Collector 增加独立的 `TraceAnalyticsQueryStore`，在 Control Plane 增加对应代理接口。不要继续扩张 `TraceListQuery`，也不要提供任意 SQL 或任意字段 group-by。

首版接口建议为：

| 接口 | 用途 | 主要返回值 |
|---|---|---|
| `GET /api/admin/trace-analytics/overview` | 顶部概览卡片 | Run、成功率、错误率、重试率、p95、Token、费用及覆盖率 |
| `GET /api/admin/trace-analytics/timeseries` | 趋势图 | 按时间桶聚合的 Run、状态、延迟、Token 和费用 |
| `GET /api/admin/trace-analytics/latency` | 延迟分析 | 总耗时 p50/p95/p99，按 Agent 或模型拆分 |
| `GET /api/admin/trace-analytics/breakdowns` | 排名和构成 | 按 Agent、模型、状态和错误类型统计的 Top N |
| `GET /api/admin/trace-analytics/tools` | 工具分析，第二阶段 | 工具调用量、成功率、p95 和错误类型 |

所有接口共享一套受限查询参数：

- `started_after`、`started_before` 和 `bucket`；
- `agent`、`model`、`status` 和 workload 类型；
- 授权范围内的 Notebook/tenant；
- `group_by` 只能选择接口预定义维度，`limit` 只能用于有上限的 Top N。

首版默认查询最近 24 小时，交互式查询最长 30 天；更长时间范围在完成独立容量测试前不开放。bucket 只能使用预定义值，并由服务端校验时间范围与粒度组合，避免生成超大响应。

响应除业务数据外必须包含：

- `schema_version`：稳定客户端契约；
- `generated_at`：查询生成时间；
- `fresh_through`：当前聚合数据可搜索到的水位；
- `filters` 和 `bucket`：回显实际执行的查询语义；
- `coverage`：Token、费用等可空数据的样本覆盖率。

### 3.3 Dashboard 页面

在现有 Trace Explorer 上方增加 Analytics 入口，两页复用相同筛选栏和 URL 查询参数。

首版页面补充：

- 概览卡片：Run 数、成功率、错误率、重试率、p95 延迟、Token、已知费用；
- 趋势：Run 数与状态趋势、p50/p95/p99 延迟趋势、Token/费用趋势；
- Breakdown：按 Agent、模型、状态和错误类型排序；
- 数据水位：明确显示“数据更新至”，当 Processor 积压时提示分析数据可能延迟；
- 下钻：点击某个时间桶、Agent、模型或错误类型后，跳转到现有 Trace Explorer，并带上可表达的筛选条件。

首版不展示原始 Prompt、模型输出或 Replay 内容，也不把用户输入文本作为聚合维度。空状态、局部接口失败、超时、无费用数据和数据延迟必须有独立状态，不能统一显示成 0。

## 4. 必须补充的数据模型

### 4.1 可直接使用的维度

以下分析可先基于 `obs_trace_summaries` 完成，无需扩展 raw schema：

- Run/状态/成功率趋势；
- Agent、模型和状态分布；
- 总耗时分位数；
- Attempt 与重试率；
- input/output/total Token；
- 已知费用与费用覆盖率。

首版先直接执行受限 ClickHouse 聚合查询，并以实际负载验证延迟。已有基准显示这些聚合具有足够潜力，不应在没有证据时先引入复杂的预聚合体系。

### 4.2 需要从 payload 提升为类型化列的维度

以下能力不能长期依赖每次查询 `canonical_payload`：

- 模型 provider、请求模型和最终选择模型；
- cached Token、reasoning Token；
- 工具/MCP 名称、调用结果、耗时和错误类别；
- Agent 终止原因、达到最大步骤、循环保护结果；
- 委派目标、委派结果和交接状态；
- 错误层级、稳定 error code 和是否可重试；
- Agent Definition、Prompt 和配置版本；
- RAG 检索阶段、降级原因和引用发布结果。

新增字段必须来自已有语义约定或版本化事件，不从 `name` 字符串猜测。Processor 在写入时完成类型化投影，保留原始 payload 作为审计事实。字段升级需要兼容旧记录的 unknown 状态，不能把缺失值映射成成功或 0。

### 4.3 查询布局与预聚合

当前 Summary 表按 `trace_id` 排序，适合身份读取但不是最优的时间范围聚合布局。第一阶段先保留现表并测量真实查询；如果 30 天查询或并发测试不能满足目标，再增加面向分析的 Projection 或独立聚合表，其排序键以授权域、时间、Agent/状态为主。

只有满足以下任一条件时才引入 Materialized View 或 `AggregatingMergeTree`：

- 受限查询经过索引/Projection 调整后仍无法达到延迟目标；
- 同一固定时间桶聚合被高频重复执行；
- 原始保留期和长期趋势保留期需要分离。

预聚合必须处理 Kafka 重投、ReplacingMergeTree 版本行、迟到事件和 Trace 状态修订。不能直接对尚未收敛的物理行做不可逆累加，否则重放会重复计数。

## 5. 必须补充的 Grafana 运维看板

Grafana 首先解决“分析链路是否可信”，不复制产品级 Agent 分析。

### 5.1 Kafka 与 Processor

需要新增或接入：

- Consumer Group lag、最老未处理消息年龄和分区分布；
- 消费、校验、持久化、重试和 quarantine 结果计数；
- 批次行数、字节数、处理耗时和失败率；
- rebalance 次数、分区撤销期间未完成工作和 offset commit 失败；
- Kafka 已确认时间到 ClickHouse 可查询时间的 searchable freshness。

### 5.2 ClickHouse

需要监控：

- insert/query 请求量、错误率和 p50/p95/p99；
- insert 批次大小、parts 数量、merge backlog 和磁盘使用；
- 查询超时、内存限制触发和连接池状态；
- raw 与 summary 最新时间水位差；
- TTL 删除和后续 purge 处理是否积压。

优先使用 ClickHouse 官方系统表或 exporter 提供的低基数指标；Nano 只补充业务链路特有的水位和处理结果。

### 5.3 告警

首版至少配置：

- Consumer lag 或最老消息年龄持续超过阈值；
- searchable freshness 超过五秒目标；
- Processor 持久化持续失败或 quarantine 突增；
- ClickHouse insert 错误率、查询错误率或磁盘占用异常；
- raw/summary 水位长期分离；
- 产品 Analytics API 错误率或 p95 延迟超标。

阈值应在压测和稳定运行后固化，文档中的“五秒”来自现有迁移验收目标，不代表其他阈值已经确定。

Prometheus 标签必须维持低基数。禁止把 `trace_id`、`run_id`、`chat_id`、`user_id`、`notebook_id` 或原始错误消息加入标签；此类定位信息保留在 Trace/日志系统中。

## 6. 权限与数据治理

Analytics 接口沿用当前平台管理员入口，但新增 `platform.trace.analytics` 能力，与查看单条 Trace 的 `platform.trace.read` 分离。迁移时现有 Trace 管理员可以同时授予两项能力，之后允许只读单条 Trace 和查看跨 Trace 统计分别授权。

每个查询必须在服务端注入授权范围，不能信任客户端提交的 Notebook/tenant。Collector 只接受 Control Plane 使用服务身份调用，浏览器不持有 ClickHouse 凭证。

费用、错误和模型分布属于平台级敏感运营数据。接口响应禁止包含原始 Prompt、输出、Replay 和任意 payload；后续若支持导出，需要单独的权限、审计和结果大小限制。

## 7. 性能与正确性验收

以下是首版建议验收目标，不是当前已经实现的指标：

- 最近 24 小时的 overview、timeseries 和 latency 接口在冻结数据集上 p95 不超过 500 ms；
- 30 天受限查询 p95 不超过 2 s，且响应点数受 bucket 限制；
- 同一筛选条件下，overview 总量与 Trace Explorer 可表达范围的计数一致；
- Kafka 重投、重复物理行和迟到终态更新不会造成重复计数；
- active Run 变为终态后，成功率、延迟和费用能在 freshness 目标内收敛；
- unknown Token/费用/错误类型始终通过 coverage 或 unknown bucket 暴露；
- 无权限用户不能通过筛选、Top N 或错误信息推断其他授权域的数据；
- Dashboard 局部请求失败时保留其他已成功区域，并显示水位和错误状态。

如果直查 Summary 无法达到目标，再启用第 4.3 节的 Projection 或预聚合方案，不能通过放宽查询边界或隐藏超时来通过验收。

## 8. 测试补充

### 8.1 后端

- 指标定义、时间边界、UTC bucket、空数据、unknown coverage 和币种处理的单元测试；
- 每个允许维度及其组合的 SQL 参数化和查询上限测试；
- 权限域注入、越权参数、超长范围和非法 bucket 测试；
- Kafka 重投、迟到事件、版本替换和 active-to-terminal 收敛测试；
- ClickHouse 集成测试和保留的 PostgreSQL Trace Explorer 回归测试；
- 固定数据集下的结果快照与 p95/p99 性能基准。

### 8.2 前端

- 筛选条件与 URL 同步、清空和刷新恢复；
- 图表点击后生成正确的 Explorer 下钻条件；
- loading、empty、partial error、stale 和 unknown coverage 状态；
- 大数值、长 Agent/模型名称、多币种和本地时区展示；
- 没有 Analytics 权限但仍有 Trace Read 权限时的导航行为。

### 8.3 监控

- 指标名称和标签白名单测试；
- 不允许高基数身份进入 Prometheus 标签的注册测试；
- 使用故障注入验证 lag、freshness、insert failure 和 quarantine 告警；
- 验证告警能够关联到日志或 Trace 查询，而不是只给出不可定位的数值。

## 9. 分阶段交付

### P0：建立可用的跨 Trace 概览

1. 固定指标语义和共享查询参数。
2. 实现 overview、timeseries、latency 三类 ClickHouse 聚合查询及 Control Plane API。
3. 用现有 Summary 字段完成概览卡、状态趋势和延迟分位数。
4. 展示 `fresh_through`，并支持从聚合图表下钻到 Trace Explorer。
5. 为 Kafka、Processor、ClickHouse 和 Analytics API 补齐核心 Grafana 看板与 freshness 告警。

### P1：补充定位维度

1. 将稳定的 error code、tool、provider 和模型 Token 明细提升为类型化分析列。
2. 实现 breakdowns 和 tools 接口及 Top N 视图。
3. 加入费用覆盖率、错误分布、工具成功率与工具 p95。
4. 依据 P0 压测结果决定是否增加 ClickHouse Projection。

### P2：补充 Agent 行为与质量分析

1. 增加 Agent/Prompt/配置版本、停止原因、步骤数和委派结果。
2. 增加 RAG 阶段与降级分析，但保持在线运行指标和离线 RAG Eval 的边界。
3. 只有接入真实反馈或评测结果后才增加质量趋势；不能用成功状态代替答案质量。
4. 根据查询量和长期趋势需求决定是否建设小时/天级预聚合与更长保留期。

## 10. 当前不纳入本次实现

- 不用 ClickHouse 全面替换 PostgreSQL 的 Trace 列表、详情和 Replay 查询；
- 不让产品前端直接访问 ClickHouse 或 Grafana；
- 不提供任意 SQL、任意 group-by 或无限时间范围；
- 不把 Trace、用户或 Notebook 身份写入 Prometheus 标签；
- 不在产品 Dashboard 展示原始 Prompt、模型输出或 Replay；
- 不把 OTel 日志/Trace、purge 和 ClickHouse Replay 描述成已经完成，它们仍是现有迁移的独立发布门槛；
- 不在没有用户反馈或 Eval 数据时声称衡量了 Agent 答案质量。

## 11. 完成定义

本设计对应的功能只有在以下条件全部满足时才算完成：

- 产品 Dashboard 能从总体异常下钻到一组 Trace，再进入单条 Trace；
- ClickHouse 聚合 API 有稳定、受限、版本化且受权限保护的契约；
- 概览、趋势、延迟、Token 和费用都使用统一口径并暴露数据覆盖率；
- Kafka 到 ClickHouse 的 lag、freshness、失败和 quarantine 在 Grafana 中可见并可告警；
- 聚合查询没有引入高基数 Prometheus 标签或浏览器直连数据仓库；
- 正确性、权限、迟到数据、重复投递、前端状态和性能测试通过；
- 文档、Runbook 和 Dashboard 面板说明能够让开发者区分产品分析异常与基础设施故障。
