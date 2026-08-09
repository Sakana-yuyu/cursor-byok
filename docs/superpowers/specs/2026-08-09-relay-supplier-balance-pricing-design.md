# 反代供应商余额与价格适配设计（2026-08-09）

## 背景与目标

cursor-byok 允许用户配置多家上游供应商（OpenAI 兼容 / Anthropic / Gemini），需要三类能力：请求上下行转换、供应商余额/额度展示、模型价格拉取与成本估算。

现状已具备：
- 通用余额探测链 `internal/client/provider_balance.go`（`QueryProviderBalance`，策略 0-5 + 具名/New API/Token Plan/自定义）
- New API 换算 `provider_balance_newapi.go`、自定义映射 `provider_balance_configured.go`、Token Plan 窗口 `provider_balance_coding_plan.go`
- 模型目录 `internal/client/model_catalog.go`（`FetchModelCatalog`，含目录内价格解析）
- 成本估算 `internal/historymetrics/pricing.go` + 官方静态价格 `internal/modelcontext/catalog.go` + 运行时合并 `internal/app/runner.go#priceRatesFromAdapters`

本次对四个主流反代项目的调研结论（固定 commit 源码核验）：

| 项目 | canonical | 余额形态 | 价格来源 | 上下行 |
|---|---|---|---|---|
| New API | QuantumNous/new-api main@823e263 (AGPL-3.0) | 三套对象：站点用户 `/api/user/self`、API token（remain_quota/used_quota/unlimited_quota/model_limits）、channel 余额 | `/api/pricing` + model_ratio/group_ratio/cache_ratio（多维：倍率/固定价/缓存/图像/音频）；`/v1/models` 无价格 | chat/responses/messages/v1beta models，TokenAuth → 模型限流 → Distribute |
| Sub2API | Wei-Shaw/sub2api 48eb376 (0.1.173) | API Key 可访问 `/v1/usage`、`/v1/sub2api/billing`；JWT 面板 `/api/v1`；subscription/user_subscription（amount_total/amount_used/过期） | LiteLLM 风格远程价格 + 本地回退 | OpenAI 风格全兼容 |
| CodexProxy | icebear0828/codex-proxy dev@4db59c4（无 SPDX，README Non-Commercial） | 订阅窗口型：primary/secondary/code-review 百分比 + reset；探测 `/backend-api/wham/usage`、`/backend-api/codex/usage`（ChatGPT OAuth access token + ChatGPT-Account-Id + 指纹头） | 无价格 API | `/v1/responses`（强制上游 stream:true/store:false）、`/v1/chat/completions`、`/v1/models` |
| CPA/CLIProxyAPI | router-for-me/CLIProxyAPI 2e6b1d83 (v7.2.125) | 无供应商余额 API（管理 API 仅配置/认证/本地 usage 统计） | `/v1/models` 聚合无价格 | 入站 translator → executor → 反向翻译；多鉴权（Bearer/X-Goog-Api-Key/X-Api-Key/?key=） |

## 用户确认的范围

1. 余额 + 价格都做（完整方案）。
2. 架构：推荐方案 B 分步落地 —— Wave 0 行为等价重构出适配器注册表，再在其上新增能力。
3. CodexProxy 订阅窗口余额：默认关闭、仅手动启用（License 风险规避，独立实现不复制代码）。
4. CPA：保持现状（模型目录已可用，不新增专用适配）。

## 架构选型

- **方案 A**（在 `QueryProviderBalance` 继续加分支）：改动小，但入口已 845 行 + 6 文件，分支继续膨胀，价格与余额耦合。仅适合 P2 小补丁。
- **方案 B**（余额来源适配器注册表）：`BalanceSourceAdapter.Probe(ctx, creds) → BalanceSnapshot`，现有策略迁移为适配器（openai_billing / newapi / sub2api / token_plan / custom / named），新适配器注册接入；错误分类与缓存横切。**推荐，分步落地**。
- **方案 C**（A + 仅加窗口余额字段与价格同步）：风险最小，但架构未改善，CodexProxy 探测仍以 profile 分支塞入。

## 关键设计要点

### 1. 统一余额 schema 扩展（向后兼容）

`ProviderBalance`（internal/client/provider_balance.go）新增可选字段，全部指针/omitempty，不破坏现有 UI 与成本估算：

```go
// 新增（均为可选）
Windows   []UsageWindow `json:"windows,omitempty"`   // 订阅窗口明细（CodexProxy primary/secondary/code-review）
ExpiresAt string        `json:"expiresAt,omitempty"` // 订阅/套餐过期时间（Sub2API、CodexProxy reset）

type UsageWindow struct {
    Name        string  `json:"name"`                  // "primary" / "secondary" / "code_review"
    UsedPercent float64 `json:"usedPercent,omitempty"` // 已用百分比
    Used        float64 `json:"used,omitempty"`        // 已用（窗口单位）
    Total       float64 `json:"total,omitempty"`       // 窗口总额度
    ResetsAt    string  `json:"resetsAt,omitempty"`    // 重置时间
}
```

- 窗口型沿用 token_plan 的"百分比 + Currency=%"约定（Remaining/Used/Total 已是百分比），Windows 仅为明细增强。
- 前端 `SupplierDetail.vue` 在 PlanName/Message 基础上加窗口表格；`StationSpendCard.vue` 不变。
- 成本估算与余额完全解耦，不受影响。

### 2. 价格多来源合并

优先级（低 → 高）：官方静态表（`BuiltinPricingForAdapter`）< 供应商 `/api/pricing`（Source="pricing_api"）< 目录内价格字段（Source="catalog"）< 用户手填（adapter.Pricing）。

- 新增合并层：`priceRatesFromAdapters`（internal/app/runner.go）按 model 键做多来源覆盖，新增 `pricing_api` 源。
- 刷新策略：目录 5min TTL 复用；`/api/pricing` 独立 TTL（建议 1h）+ ForceRefresh。
- 已知风险：New API quota 换算因子 `newAPIQuotaPerUnit = 500000`（$1）在自定义 ratio 部署下失真，需可配置。

### 3. 上下行兼容

- auto 模式按 baseURL 后缀/模型前缀已能选 responses/chat，零改动。
- CodexProxy 预设固定 protocolGroup=responses + endpoint=/v1/responses（其强制 stream:true/store:false 与 `buildOpenAIResponsesBodyMap` 现行为一致）。
- CPA 多鉴权靠 CustomHeaders 注入（已有能力），不加代码。

## 风险清单

| 风险 | 处理 |
|---|---|
| CodexProxy 无开源许可 | 仅参考行为（endpoint/字段）独立实现，不复制代码；功能默认关闭、仅手动启用；文档注明 |
| Codex 指纹头易变/风控 | 指纹头做成用户可配置 CustomHeaders，不硬编码默认；失败降级普通 Bearer 重试；沿用 Transient keep-last-good |
| 重复计费 | OpenAI adapter 已有 emitted 标记保证"输出前才重连"，不新增重试路径；余额查询为只读 GET |
| 错误分类 | 沿用现有语义：401/403=确定性终止、非 2xx=确定性、传输错=Transient；404 视为"不支持此端点"继续探测链 |
| 百分比与金额混用 | 前端按 Currency 区分，token_plan/窗口型不得与金额混算 |

## 落地顺序（Wave + 验收）

- **Wave 0（重构，行为等价）**：整理适配器注册表，迁移 6 个文件现有实现，保留 profile/cacheKey/Transient 语义。
  验收：现有测试全绿；对 4 个已知中转站手工回归余额展示逐字一致。
- **Wave 1（窗口型余额，P0）**：新增 `Windows`/`ExpiresAt` 字段 + codex_proxy 探测适配器（wham/usage、codex/usage，OAuth 头可配置，默认关闭）。
  验收：配置 CodexProxy 后展示三窗口百分比与 reset；token_plan 展示不变；401/404/瞬时失败分类正确。
- **Wave 2（价格同步，P1）**：New API `/api/pricing` + ratio 换算，按 Source 优先级合并。
  验收：New API 渠道成本估算 Source=pricing_api、刷新 TTL 生效、官方表回退不变。
- **Wave 3（可选增强，P1/P2）**：Sub2API `/v1/sub2api/billing`（total/used/过期）、CPA 本地 usage 统计对接。
  验收：各端点成功/404/401 分类正确，Transient 语义保持。

## 证据引用

- New API：https://github.com/QuantumNous/new-api （main@823e263，AGPL-3.0）；`router/relay-router.go`、`middleware/auth.go`、`middleware/distributor.go`、`controller/billing.go`、`controller/channel-billing.go`、`controller/pricing.go`、`setting/ratio_setting/*`
- Sub2API：https://github.com/Wei-Shaw/sub2api （48eb376，VERSION=0.1.173）；`backend/internal/server/routes/*`、`backend/internal/handler/user_handler.go`、`backend/ent/schema/subscription.go`、`backend/internal/service/pricing.go`
- CodexProxy：https://github.com/icebear0828/codex-proxy （dev@4db59c4，无 SPDX）；`src/routes/responses.ts`、`src/proxy/codex-api.ts`、`src/proxy/codex-usage.ts`、`src/auth/quota-utils.ts`、`src/routes/accounts.ts`；探测：`/backend-api/wham/usage`、`/backend-api/codex/usage`、`/backend-api/codex/models`、`/backend-api/models`、`/backend-api/sentinel/chat-requirements`
- CPA：https://github.com/router-for-me/CLIProxyAPI （2e6b1d83，v7.2.125）；`sdk/cliproxy/*`、`internal/api/*`；鉴权：Bearer / X-Goog-Api-Key / X-Api-Key / ?key= / ?auth_token=

## 信息缺口

- New API `/api/pricing` 精确响应结构未在仓库内实测，实现时对照 823e263 源码补字段映射。
- CodexProxy 上游 OAuth 细节仅按公开源码行为独立实现，不复制其代码。