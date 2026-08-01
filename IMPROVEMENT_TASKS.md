# Cursor Local Assistant 持续优化任务清单

> 目标：面向客户的易用性 + 稳定性。先修 bug 再上功能。
> 约束：**不写任何测试**；改 prompt/history/replay 遵守 prefix-cache-stability；不改已安装 Cursor 客户端。

状态图例：`[ ]` 待办 · `[~]` 进行中 · `[x]` 完成

---

## 阶段 0 · Bug 修复（先做）

### 前端
- [x] **P0-F1** `ModelEditor.vue:814` `providerIcon` 未导入 → 编辑模型渲染崩溃。修：从 `@/utils/providerMeta` 导入。
- [x] **P0-F2** `RequestMetrics.vue:159-183` 自动刷新调 `resetPage()` → 5s 翻页被打回第 1 页。修：`refresh({keepPage})`，自动刷新保留分页。
- [x] **P0-F3** `ModelConfig.vue` 忽略 `?group=` 查询 → 分组跳转失效。修：读 `route.query.group` 聚焦过滤 + 顶部提示条 + 查看全部。
- [x] **P0-F4** 文案/一致性：`RequestMetrics` 标题统一为「请求明细」；`HomeMetricsCard` 「Opus 4.7」→「Opus 4.8」。
- [x] **P0-F5** 死代码：删 `Home.handleRefreshState`；`createEmptyModelAdapter` 收敛到 appState（ModelConfig/SupplierDetail 改为 import；ModelEditor 留待阶段5）。`ModelAdapterModal.vue` 暂留（仅 i18n 生成物引用，删除会引发 catalog churn）。

### 后端
- [x] **P0-B1** `IncludeCacheWriteInHitRate` 死开关 → 命中率计算真正消费配置（report.go/aggregate.go/usage_json.go/metrics.go/app/runner.go），前后端口径一致。
- [x] **P0-B2** `SupplierDetail` 批量删除 → 新增 `deleteModelAdaptersBatch`，单次原子 save。
- [x] **P0-B3** `SupplierDetail.cancelBatchTest` → `batchCancelled` 标志，worker 每轮短路，testAll/testSelected 均生效。

---

## 阶段 1 · 计费准确性
- [x] **P1-1** 后端计费引擎：`historymetrics/pricing.go`（PriceRate/PriceLookup/Cost），读取时按 model|provider|baseURL 联结配置 pricing 计算美元；未知不臆造。`RequestMetric.costUsd/pricingKnown/currency`、`Summary.estimatedCostUsd/currency`。
- [x] **P1-2** 每中转站消耗统计：`SummarizeProviderSpend` + `GetProviderSpendSummary`（按 GroupName→baseURL host→provider 分组）；usage 事件新增 `base_url/group_name`（router identitySink→actor→token_usage 落盘）。
- [x] **P1-3** 前端统一价源：`RequestMetrics` 请求费用优先读后端 `costUsd`；`HomeMetricsCard` 移除硬编码价表，价值估算=区间内已计价请求 costUsd 合计 + 未计价提示。

## 阶段 2 · 余额/价格/模型拉取
- [x] **P2-1** `QueryProviderBalance`（`internal/client/provider_balance.go`）：OpenAI billing（subscription+usage，cents/100）+ NewAPI（/api/user/self，quota÷500000）多端点降级，统一 `{supported,source,currency,total,used,remaining,message}`。
- [x] **P2-2** 前端：`SupplierDetail` 顶部余额面板（loading/支持/不支持 + 刷新 forceRefresh）；`ModelConfig` 卡片懒查询余额按钮。
- [x] **P2-3** `model_probe` 用 typed `HTTPStatusError`（errors.As）取状态码，成功判定要求真实内容事件，去正则刮字符串。

## 阶段 3 · 本地缓存
- [x] **P3-1** 精确匹配响应缓存 `forwarder/provider_cache.go`：全量请求归一化 sha256，TTL+FIFO+maxEntries，仅成功完整流入缓存；`config.localResponseCache.enabled` 默认 false，禁用时零成本直通。
- [x] **P3-2** 元数据缓存 `client/metadata_cache.go`：catalog(~5min)/balance(~60s) TTL 缓存，apiKey 哈希入键，`forceRefresh` 绕过。
- [x] **P3-3** 命中统计 `internal/localcache` + `GetLocalCacheStats`；前端 Home 单列「本地缓存命中」与 provider prompt 缓存分开展示。

## 阶段 4 · Agent 稳定性
- [x] **P4-1** `retry.go` 实现瞬时错误（网络/429/5xx）重试 + 指数退避+jitter + Retry-After，仅重试建连前请求；`ProviderRetryAttemptSummary` 返回真实摘要。
- [x] **P4-2** `router.go` failover 改有界循环 + 退避；单通道永久错误立即返回，多通道轮换端点。
- [x] **P4-3** `openai.go` 新增 `completedOpenAIToolArgsJSON`，三处 tool-call 出口在适配器内提前校验（空→`{}`，畸形/非对象报清晰错误）。
- [x] **P4-4** `step/recorder.go` 解析失败改 `logger.Error` 告警，保留降级。

## 阶段 5 · UI/UX 打磨（随功能集成完成，非全量重设计）
- [x] **P5-1** 供应商卡片新增余额行 + 轻量层级优化（ModelConfig/SupplierDetail 加载/支持/不支持态统一）。
- [x] **P5-2** 请求明细：费用列改后端口径 + 保留异常行高亮/缓存率语义色阶（原有）。
- [x] **P5-3** Home 指标卡：本地缓存 vs provider 缓存分列、价值估算口径明确、MetricsDetail 新增站点消耗表。

---

## 变更记录
<!-- 每完成一项在此追加：任务号 · 改动文件 · 简述 -->
- **阶段0** ·
  - P0-F1 `frontend/src/views/ModelEditor.vue` — import providerIcon
  - P0-F2/F4 `frontend/src/views/RequestMetrics.vue` — refresh keepPage、标题「请求明细」
  - P0-F3 `frontend/src/views/ModelConfig.vue` — route.query.group 聚焦过滤 + 提示条；createEmptyModelAdapter 改为 import
  - P0-F4 `frontend/src/components/HomeMetricsCard.vue` — Opus 4.8 标签
  - P0-F5 `frontend/src/views/Home.vue` — 删 handleRefreshState；`frontend/src/views/SupplierDetail.vue` — createEmptyModelAdapter 改为 import
  - P0-B1 `internal/historymetrics/{report,aggregate,usage_json}.go`、`internal/bridge/metrics.go`、`internal/app/runner.go`、`scripts/historymetrics/main.go` — IncludeCacheWriteInHitRate 贯通
  - P0-B2 `frontend/src/state/appState.js` — 新增 `deleteModelAdaptersBatch`；`frontend/src/views/SupplierDetail.vue` — removeAdapters 原子化
  - P0-B3 `frontend/src/views/SupplierDetail.vue` — batchCancelled 真取消
  - 验证：`go build ./...` ✅ · `npm run build` ✅
- **阶段1** 后端 `internal/historymetrics/{pricing.go(新),aggregate,report,usage_json}.go`、`internal/bridge/metrics.go`、`internal/app/runner.go`、`internal/backend/{forwarder/{usage_store,token_usage,actor}.go,agent/model/{types,router,anthropic}.go}`；前端 `RequestMetrics.vue`、`HomeMetricsCard.vue`。
- **阶段2** 后端 `internal/client/{provider_balance.go(新),model_probe.go,service.go}`、`internal/backend/agent/model/http_error.go`、`internal/bridge/proxy.go`；前端 `SupplierDetail.vue`、`ModelConfig.vue`、`clientApi.js`、`browserBindings.js`。
- **阶段3** 后端 `internal/client/metadata_cache.go(新)`、`internal/localcache/stats.go(新)`、`internal/backend/forwarder/{provider.go,provider_cache.go(新)}`、`internal/backend/server/config/{types.go,manager.go}`、`internal/bridge/metrics.go`；前端 `Config.vue`、`MetricsDetail.vue`、`appState.js`(localResponseCache 纳入配置白名单，避免保存时被清空)。
- **阶段4** 后端 `internal/backend/agent/model/{retry,router,openai}.go`、`internal/backend/agent/step/recorder.go`。
- **绑定** `wails3 generate bindings` 重新生成（47 methods）；`clientApi.js` 新增 queryProviderBalance/fetchProviderSpendSummary/fetchLocalCacheStats。
- 最终验证：`go build ./...` ✅ · `npm run build` ✅

## 深化（实测验证 + 增强）
- **本地实测**：GUI 受限（无显示 + 实例占用端口），改用 `go build -o` 链接 75MB 完整 app 二进制成功（证明全量接线编译/可生产）；审查余额单位换算(cents/100、quota/500000)、站点身份贯通(channel→identitySink→actor→usage.json)、缓存隔离(禁用零成本直通)。
- **sub2api/cpa 覆盖**：经 WebSearch 核对，此类中转站模拟 OpenAI `/v1/dashboard/billing/{subscription,usage}` —— 正是 OpenAI-billing 策略所调；`billingAPIRoot` 剥离 `/models|/chat/completions|/responses|/messages|/completions` 保留 `/v1`，已覆盖。
- **增强1 · Config 高级缓存设置**：`Config.vue` 绑定 `appState.localResponseCache`（开关 + TTL + maxEntries 输入，0=后端默认 900/256），随 `persistUserConfig` 单一路径保存。
- **增强2 · appState 配置白名单**：`localResponseCache` 纳入 `normalizeConfig`/`buildConfigPayload`/`applyConfigToState`/`appState`，杜绝任何一次配置保存把它清空回默认值（修掉 FE-C 子代理标记的隐藏回归风险）。
- **增强3 · Home 站点消耗卡片**：新增 `components/StationSpendCard.vue`，首页一眼可见「哪个中转站用了多少额度」(Top6 + 合计花费 + 未计价降级)，每分钟刷新；`Home.vue` 挂载于 HomeMetricsCard 下。
- 最终验证（深化后）：`go build ./...` ✅ · `npm run build` ✅
