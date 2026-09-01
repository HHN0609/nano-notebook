# Attention Research 全链路真实压缩验收设计

## 目标

围绕“Attention Is All You Need 对后续深度学习、LLM、长上下文、多模态和 Agent 系统产生了哪些关键影响？”运行一条真实 Research Mode 任务，并导出该任务压缩前、Layer 1 和 Layer 2 后实际准备发送给模型的完整 Bifrost 请求。

种子 PDF 固定为 `https://arxiv.org/pdf/1706.03762`。

## 真实性边界

- PDF 必须通过真实 HTTP 获取，并由真实 Source Map parser 处理。
- Research Planner、Executor、Archival Capsule compactor 和 Task Memory compactor 必须调用真实 Qwen。
- Web 搜索、网页读取、Evidence 检索和 rerank 必须使用当前产品链路；不得使用 stub、固定候选或手写 Tool Result。
- 不得使用重复字符、随机文本或其他 padding 扩大上下文。
- 原始 checkpoint 是不可变权威；压缩只能新增 Capsule/Task Memory 并改变投影。
- 为稳定观察两层压缩，允许在验收 replay 中降低触发阈值、Safe Input 和 Keep Recent 预算；必须同时记录生产值和验收值。研究数据和模型调用不得因此改变或重写。

## 数据流

1. 将指定 arXiv PDF 导入当前 Research session，等待 Source 达到 `ready/searchable`。
2. 执行真实 Research Plan，围绕论文的架构贡献及后续影响检索原始论文和权威资料。
3. 将真实 Tool Call/Result 持久化为 Research checkpoint，生成未压缩决策请求。
4. 通过透明 Bifrost capture proxy 记录请求后原样转发；代理不得修改请求或响应。
5. 对同一 checkpoint prefix 运行 Layer 1，真实调用 Qwen 生成 Archival Capsules，并记录 Layer 1 投影请求。
6. 对同一 checkpoint prefix 运行 Layer 2，真实调用 Qwen 生成 Task Memory，并记录最终投影请求。
7. 生成 Research 报告及证据清单。

## 产物

每个文件都保留生产 Bifrost serializer 生成的紧凑 JSON，不做格式化或省略：

- `research-report.md`
- `before-compaction.request.json`
- `after-layer-1.request.json`
- `after-layer-2.request.json`
- `archival-compactor.request.json` 与原始响应
- `task-memory-compactor.request.json` 与原始响应
- `metrics.json`：byte/token/message 数、逐层与总体压缩率、生产/验收预算
- `SHA256SUMS`
- Source、Source Map、Evidence Unit 和 checkpoint manifest

## 验收标准

- 三份主模型请求均为合法 JSON，并包含完整 `messages`、Tools 和模型参数。
- 原始上下文包含指定 PDF 的真实 `inspect_source`/`search_evidence` 轨迹或其真实 Research Tool 结果。
- 所有压缩摘要均有成功的 Qwen provider 响应和 usage 元数据。
- Layer 1 仅替换符合选择范围的旧 Result；最近轨迹保持精确 Tool 配对。
- Layer 2 使用已落库 Capsules 生成 Task Memory；最终请求可由不可变 checkpoint 与压缩产物重新构建。
- 压缩前后 checkpoint 数量、payload 和 SHA-256 完全一致。
- 每层 `after_tokens < before_tokens`，且满足当前最小收益门槛。
- 若真实轨迹无法满足某层收益门槛，不伪造结果；记录该层未触发及原因。

## 失败处理

- PDF、搜索、Evidence、模型或代理任一真实依赖失败时保留日志并停止，不降级为 stub。
- Qwen 输出不满足 Capsule/Task Memory schema 时按产品规则记录压缩失败，不手工修补。
- 任何 capture 文件缺失、截断或 SHA 校验失败都视为验收失败。

## 非目标

- 不改变生产压缩策略或默认预算。
- 不把一次验收运行当作生产性能基准。
- 不为达到目标压缩率重复无意义查询或注入合成内容。
