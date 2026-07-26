# cursor-byok 前端与逻辑持续优化计划

目标：以「面向客户、易用性 + 稳定性」为核心，先修 bug 再上功能。全程用子 agent 并行推进，产出 `IMPROVEMENT_TASKS.md` 逐项跟踪。**遵守仓库约束：不写任何测试；prompt/history/replay 改动遵守 prefix-cache-stability；不改已安装 Cursor 客户端。**

首个动作：创建 `IMPROVEMENT_TASKS.md`（下方任务清单），之后每完成一项勾选并记录改动文件。

---

## 阶段 0 · 确认 Bug 修复（先做，风险低收益高）

**前端**
- `ModelEditor.vue:814` — `providerIcon` 使用但未 import → 编辑已存在模型时渲染崩溃。修：从 `providerMeta` 导入。
- `RequestMetrics.vue:159-183` — `refresh()` 里调 `resetPage()`，5s 自动刷新导致翻页被打回第 1 页。修：自动刷新只更新数据不重置分页；仅手动刷新/换页重置。
- `ModelConfig.vue` — 忽略 `ModelGroups` 传来的 `?group=` 查询，分组跳转失效。修：读 `route.query.group` 并过滤。
- 文案/一致性：`RequestMetrics` 标题「请求明细」vs 页内「缓存上下文」；「最多 10000 条」vs `limit=0`；`HomeMetricsCard` 「Claude Opus 4.7」过期标签。统一修正。
- 死代码清理：`Home.handleRefreshState`、疑似孤儿 `ModelAdapterModal.vue`、4 处重复 `createEmptyModelAdapter`（收敛到 appState 单一来源）。

**后端**
- `IncludeCacheWriteInHitRate` 配置项存在但缓存命中率计算 (`report.go:42/51`, `aggregate.go`) 从不读取 → 死开关。修：让计算函数真正消费该配置，前后端口径一致。
- `SupplierDetail` 批量删除 N 次串行 save、非原子；`cancelBatchTest` 无法真正取消。修：批量删除走单次 save；取消用 AbortController/标志位真正短路 worker。

---

## 阶段 1 · 计费准确性（pricing 落地 + 每站点消耗统计）

现状：`/models` 的 pricing 已解析并存进 `ModelAdapterConfig.Pricing`，但**从未用于算钱**；`价值估算 $` 靠前端硬编码价表。

- **后端计费引擎**：新增按 token×单价 计算每次请求成本（input/output/cacheRead/cacheWrite 分别计价）。价格来源优先级：模型自带 pricing → 用户手填 → 内置兜底价表。写进 `usage.json` 事件，供明细与汇总使用。
- **每中转站消耗统计**：按 provider/baseURL 聚合「你在哪个中转站用了多少额度」。后端 `historymetrics` 增加 per-provider 维度；前端在 Home/请求明细新增「站点消耗」视图。
- **前端统一价源**：移除 `HomeMetricsCard`/`RequestMetrics` 各自的硬编码价表，统一读后端成本字段，口径一致。

## 阶段 2 · 余额/价格/模型拉取增强（借鉴 newapi/sub2api/cpa）

- **余额查询**：新增 `QueryProviderBalance`，按用户选择的三种协议探测：
  1. OpenAI billing：`GET /v1/dashboard/billing/subscription`（hard_limit_usd）+ `/v1/dashboard/billing/usage`（total_usage）→ 余额 = 限额 − 已用。
  2. NewAPI/OneAPI：`/api/user/self`、`/api/pricing` 等（token/密钥鉴权分别处理）。
  3. `/models` pricing 字段（已解析，接入计费）。
  多端点按序尝试，失败降级，返回统一 `{total, used, remaining, currency, source}`。
- **前端展示**：模型配置供应商卡片 + `SupplierDetail` 顶部显示余额/已用额度，带刷新与「不支持」降级态。
- **拉取健壮性**：`model_probe` 现靠正则从错误字符串刮 `status=NNN`、任何非错误事件都判为可用（假阳性）。改为结构化状态码 + 更严格成功判定。

## 阶段 3 · 本地缓存（精确匹配 + 元数据缓存）

按你的选择做**安全方案**，不碰 agent 正确性：
- **精确匹配响应缓存**：对完整请求体做规范化哈希，命中则回放缓存响应；可配置 TTL 与开关，默认关闭/保守。仅完全相同请求命中，避免污染。
- **元数据缓存**：models 列表、pricing、balance 加本地 TTL 缓存，减少重复网络往返。
- **命中统计**：区分「本地缓存命中」与「provider prompt 缓存命中」，Home 分开展示，避免和现有 95% provider 缓存率混淆。

## 阶段 4 · 自研 Agent 调用稳定性

- `retry.go`：`DoProviderRequestWithRetry` 名不副实（无重试循环）、`ProviderRetryAttemptSummary` 恒空 → `http_error.go` 重试文案是死代码。修：实现真正的瞬时错误（429/5xx/超时）重试 + 指数退避，或删除误导性死代码。
- `router.go:162-170`：单通道下故障「failover」会对同一失败端点无退避重试至 8 次。修：加退避 + 相同端点短路。
- `openai.go:888/909`：工具调用 JSON 未在适配器内校验，畸形参数下游才炸。修：仿 Anthropic `completedAnthropicToolArgsJSON` 在适配器内提前校验、清晰归因。
- `step/recorder.go:92`：assistant 记录解析失败静默降级丢结构。修：至少记录告警。

## 阶段 5 · UI/UX 打磨（产品 + UI 设计视角）

用 `product-designer` / `frontend-design` skill 指导：
- 供应商卡片信息层级（余额、模型数、健康、消耗）重排；空态/加载态/错误态统一。
- 请求明细表：列可读性、异常行高亮、cache 率语义提示。
- Home 指标卡：本地缓存 vs provider 缓存、站点消耗、价值估算口径清晰化。
- 保持现有深色 Tailwind 设计语言与 `components/ui` 原子组件，不引入组件库。

---

## 执行方式
- 每阶段用子 agent 并行推进（前端一组、后端一组），我合并 review。
- 每完成一项更新 `IMPROVEMENT_TASKS.md` 勾选 + 记录改动文件。
- 不写测试；改 prompt/replay 前遵守 prefix-cache-stability；余额/计费端点实现时子 agent 会用 WebFetch 核对 newapi/sub2api/cpa 真实接口。
- 阶段 0 完成后先给你看一版再继续后续阶段。