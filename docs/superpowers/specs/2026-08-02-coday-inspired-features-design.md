# 设计文档：coday 启发功能移植（检查器 / 自启动 / 图像源 / 身份注入 / 价格覆盖）

- 日期：2026-08-02
- 分支：`feature/coday-inspired`（worktree：`.worktrees/coday-inspired`）
- 状态：已获用户批准（范围与关键决策均确认）

## 1. 背景与目标

对标商业软件 Coday（HyperClockUp/Coday，专有、Rust、加壳）的功能设计，为 cursor-byok（Go + Wails + Vue）移植五类功能。Coday 为专有软件且整体加壳，无法也不应提取代码；本设计仅借鉴**功能形态与配置模型**，全部用 Go/Vue 重新实现。

五类功能（用户确认的最终范围）：

1. GUI 请求/响应检查器 —— 仅捕获「发送给上游 provider 的请求/响应」
2. 开机自启动 —— Windows 注册表 Run 键 + macOS LaunchAgent
3. 独立图像生成端点 —— 图像生成请求路由到单独配置的源，失败回退主渠道
4. 可配置身份 + 配额注入 —— 复用现有 auth mock，注入内容用户可配
5. 全局价格覆盖表 —— 按模型名模式匹配覆盖单价，与现有渠道级定价叠加

## 2. 现状（基线调查结论）

| 功能 | 现状 | 关键文件 |
|---|---|---|
| 成本核算 | ✅ 渠道级 `Pricing`（input/output/cacheRead/cacheWrite/currency）+ `PriceLookup.Cost` + 前端价格编辑 UI | `internal/historymetrics/pricing.go`、`internal/runtime/local_runtime.go:37`、`frontend/src/views/ModelEditor.vue` |
| 身份注入 | 🟡 已 mock `/oauth/token`、`GetEmail`、StripeProfile、Poll 等 auth 接口，内容写死 | `internal/backend/server/upstream/action.go`、`internal/backend/host.go:459-474` |
| 图像生成 | 🟡 Responses API `image_generation` 工具自动注入，走当前渠道 | `internal/backend/agent/model/openai.go:273-364` |
| 请求检查 | ❌ 仅 usage.json 元数据 + runtime/debug 文件日志 | `internal/historymetrics/usage_json.go` |
| 自启动 | ❌ 无（托盘已有） | `internal/app/runner.go:301` |

关键架构事实：

- 所有 provider 请求的唯一出口在 `internal/backend/agent/model/retry.go:87`（`client.Do`）与 `gzip_request.go`（压缩分支）—— 检查器挂载点。
- backend 的 auth mock 路由注册在 `internal/backend/host.go`，响应由 `upstream/action.go` 的 `MockBuilder` 构造。
- 配置中心 `internal/backend/server/config/types.go` 的 `Config` 结构，经 `NormalizeConfig` 归一化，前端经 Wails bridge 读写。

## 3. 架构总览

```
新增 Go 包:
  internal/inspector    —— 上游请求/响应环形缓冲 + 捕获钩子
  internal/autostart    —— 自启动注册（Windows/macOS）
新增配置:
  IdentityConfig / ImageSourceConfig / PriceOverrides（挂到 Config）
改造:
  retry.go / gzip_request.go  —— 捕获钩子注入（旁路）
  upstream/action.go           —— mock 身份从配置读取
  openai.go                    —— 图像请求路由分支
  bridge（internal/bridge/）    —— 新 Wails 方法 + 事件
前端:
  Inspector.vue（新页面 /inspector）+ 设置页三个新区块
```

原则：

- **旁路优先**：检查器/自启动任何失败不得影响主代理链路（recover + 忽略）。
- **配置向后兼容**：新配置字段全部 optional，缺省行为与现状一致。
- **不写测试**：遵循仓库 `test-requirements` 规则；验证 = `go build ./...` + `go vet ./...` + `vite build` + 手动联调。

## 4. 功能设计

### 4.1 GUI 请求/响应检查器（仅上游）

**新包 `internal/inspector`**：

- `Record` 结构：`id`（自增）、`ts`、`task_id`、`conversation_id`、`model`、`provider`、`baseURL`、`method`、`path`、`status_code`、`duration_ms`、`req_headers`（脱敏：Authorization/Bearer 打码）、`req_body`（截断 64KB）、`resp_body`（截断 64KB）、`error`、`stream`（是否流式）、`finalized`。
- 环形缓冲：默认容量 200 条，超限覆盖最旧；`Put` 并发安全；新记录通过订阅回调推 bridge。
- 捕获钩子 `CaptureFunc func(req *http.Request, resp *http.Response, err error, start time.Time)`：
  - 请求侧：在 `retry.go` `client.Do` 前读取并**恢复** body（`io.NopCloser(bytes.NewReader(payload))`），记录请求体与头。
  - 响应侧：`resp.Body` 包一层 `teeReadCloser`，边读边喂 recorder（流式累积），`Close` 时 finalize 写入缓冲；错误路径记录 error。
  - 整体 `defer recover()`，捕获失败仅丢弃该条记录。
- 挂载：`retry.go` 构造 `http.Client` 处注入（默认 no-op，由 bridge/backend 启动时装配为真实实现）。

**bridge**：`ListInspectorRecords(limit, offset)`、`GetInspectorRecord(id)`、`ClearInspectorRecords()`；事件 `inspector:new`（含记录 id，前端拉详情）。

**前端**：新路由 `/inspector` → `Inspector.vue`：

- 列表：时间 / 模型 / provider / 状态码 / 耗时 / 是否流式；异常状态标红。
- 详情抽屉：请求 Tab（method、URL、headers、JSON body）/ 响应 Tab（status、headers、body，流式响应展示累积文本）；复制按钮、清空按钮。

### 4.2 开机自启动

**新包 `internal/autostart`**：

- `Enable() / Disable() / Enabled() bool`：
  - Windows：`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，值名 `cursor-byok`，数据 = 当前 exe 路径 + ` --hidden`。
  - macOS：`~/Library/LaunchAgents/com.cursor-byok.plist`（`RunAtLoad`），写入后 `launchctl load`。
  - 其他平台：返回「不支持」错误。
- 使用 `golang.org/x/sys/windows/registry`（已有依赖则复用，否则 `internal/winreg` 薄封装避免引入新依赖——实现时确认）。

**bridge**：`GetAutoStart() / SetAutoStart(enabled bool)`。

**前端**：`GeneralSettings.vue` 新增「开机自启动」开关；设置失败 toast 提示，不阻塞。

### 4.3 独立图像生成端点

**配置**（全局，挂 `Config`）：

```go
type ImageSourceConfig struct {
    Enabled bool   `json:"enabled"`
    BaseURL string `json:"baseURL"`
    APIKey  string `json:"apiKey"`
    ModelID string `json:"modelID"`
}
```

**路由**（`openai.go`）：在 `ensureOpenAIResponsesImageGenerationTool` 判定为图像生成请求、且 `ImageSourceConfig.Enabled` 时：

1. 请求 URL 替换为图像源 `BaseURL`（追加 `/v1/responses`，若 BaseURL 已含路径则原样用）。
2. `model` 字段替换为图像源 `ModelID`。
3. `Authorization` 替换为图像源 `APIKey`。
4. 其余参数（尺寸/数量等）保持透传。

**回退**：图像源不可达或返回错误（非 2xx）时，**回退主渠道**重发一次，并在 inspector 记录 `error` 注明回退；仍失败则返回上游错误。回退仅在「图像源已配置」时尝试，未配置则完全走现状。

### 4.4 可配置身份 + 配额注入

**配置**（全局，挂 `Config`）：

```go
type IdentityConfig struct {
    Enabled         bool   `json:"enabled"`
    DisplayName     string `json:"displayName"`
    Email           string `json:"email"`
    AvatarURL       string `json:"avatarURL"`
    Plan            string `json:"plan"`
    Tier            string `json:"tier"`
    TeamName        string `json:"teamName"`
    QuotaTokens     int64  `json:"quotaTokens"`
    QuotaRemaining  int64  `json:"quotaRemaining"`
    PlanExpiresAt   int64  `json:"planExpiresAt"`
}
```

**改造 `upstream/action.go`**：5 个 mock（OAuth / FullStripeProfile / StripeProfile / Poll / Email）的 `MockBuilder` 从配置读取身份字段：

- 现有 mock 响应字段保持不变（兼容 Cursor 客户端），仅把写死的 `email`/`plan`/`quota` 等替换为配置值。
- `Enabled=false` 或字段为空 → 使用现有默认值兜底（行为与现状完全一致）。
- 需要逐一核对 mock 响应 JSON 中哪些字段映射到这些配置项（实现时对照 `handleMockOAuth` / `handleMockAuthFullStripeProfile` / `handleMockAuthStripeProfile` / `handleMockAuthPoll` / `handleMockAuthEmail` 的 payload 结构）。

**前端**：设置页新增「账号身份」区块（表单：显示名 / 邮箱 / 头像 URL / Plan / Tier / 团队 / 配额总量 / 剩余配额 / 到期时间戳 + 启用开关）。

### 4.5 全局价格覆盖表

**配置**（全局，挂 `Config`）：

```go
type PriceOverrideRule struct {
    ModelPattern   string  `json:"modelPattern"`   // glob，如 gpt-4o*、claude-3.5*
    InputPerM      float64 `json:"inputPerM"`
    OutputPerM     float64 `json:"outputPerM"`
    CacheReadPerM  float64 `json:"cacheReadPerM"`
    CacheWritePerM float64 `json:"cacheWritePerM"`
    Currency       string  `json:"currency"`
}
```

**优先级**（从高到低）：

1. 渠道级 `ModelAdapterConfig.Pricing`（现状，保持不变）
2. 全局覆盖表（`ModelPattern` 用 `path.Match` 风格 glob 匹配模型名，不区分大小写）
3. 内置目录价（现状）

**集成**：bridge 构建 `PriceLookup` 处（`historymetrics.NewPriceLookup` 调用点）——先按现状用渠道 Pricing 构建；再遍历覆盖表，把「渠道未覆盖且模式命中」的条目补进 lookup；未命中的模型仍走目录价。`RequestMetrics.vue`、`MetricsDetail.vue`、首页成本卡片自动受益，无需改前端展示逻辑。

**前端**：设置页新增「价格覆盖」区块：规则列表（模型模式 / 四档单价 / 币种），增删改；每条规则旁显示「匹配测试」输入框（输入模型名，高亮命中的规则）。

## 5. 前端页面与路由改动汇总

| 改动 | 文件 |
|---|---|
| 新页面（检查器） | `frontend/src/views/Inspector.vue` + `router/index.js` 注册 `/inspector` + 侧边导航入口 |
| 自启动开关 | `frontend/src/components/settings/categories/GeneralSettings.vue` |
| 身份/价格配置区块 | 设置页新增区块（建议放 `Settings.vue` 现有分类内或新分类） |
| bridge 客户端封装 | `frontend/src/services/browserBindings.js`（检查器/自启/配置方法） |
| i18n | `frontend/src/i18n/` 各 locale + 静态扫描（`npm run i18n:scan` 同步 catalog） |

## 6. 错误处理与降级

- 检查器：捕获钩子内 panic 全部 recover；缓冲写入失败丢弃该条；不影响 `retry.go` 主流程。
- 自启动：注册表/plist 写入失败返回错误给 UI（toast），应用继续运行。
- 图像源：不可用回退主渠道（见 4.3）。
- 身份注入：配置缺省时用默认 mock 值，行为与现状一致。
- 价格覆盖：glob 解析失败（如非法模式）跳过该条并在前端校验时提示。

## 7. 验证方式（仓库禁止写测试）

1. `go build ./...`、`go vet ./...` —— 后端编译与静态检查
2. `npm run build`（frontend，`run-vite-build.mjs --scan --mode production`）—— 前端编译 + i18n 扫描
3. 手动联调：
   - 检查器：发一条 Cursor 请求 → `/inspector` 看到请求/响应体与流式累积
   - 自启动：开启后重启应用 → 进程自动拉起（`--hidden` 隐藏窗口）
   - 图像源：配置独立端点后发图像生成请求 → 请求打到图像源 URL；停用图像源 → 自动回退主渠道
   - 身份：配置身份后 Cursor 状态栏显示新账号信息
   - 价格覆盖：配置 `gpt-4o*` 覆盖 → RequestMetrics 成本按新单价计算

## 8. 提交与集成

- 全部改动提交到 `feature/coday-inspired` 分支（worktree `.worktrees/coday-inspired`），main 工作区及其未提交改动保持原样。
- 完成后由用户决定合并方式（rebase/merge 到 main）。

## 9. 不做的事（YAGNI）

- 不做 Remote-SSH 反向隧道（用户未选）
- 不做多 IDE 协议适配（用户未选）
- 检查器不做请求/响应持久化（仅内存环形缓冲；如需落盘后续再加）
- 身份注入不做配额自动扣减（仅展示配置值）
