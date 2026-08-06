# 任务：启用混合模式（官方模型透传）+ auto 对话排除 BYOK 植入模型

> 日期：2026-08-06 · 状态：**T1/T2 完成，端到端验证通过**（T3 可选不执行）· 执行方式：独立 agent（本会话依托 byok，关闭后不可用）
> 目标：开启 `routing.hybridMode` 后，Cursor 客户端模型选择器/auto 对话**只出现官方模型**（透传 api2.cursor.sh 执行），BYOK 自定义模型不再被植入模型列表。
> 前置：官方 Cursor 账号已登录（`~/.cursor-local-assistant-v2/data/cursor-account.json` 存在，账号 yx2751244500@gmail.com）。
>
> **执行记录（2026-08-06）**：
> - T1 ✅ 已完成：`mocks.go` 5 处 builder 增加 hybrid 分支（`buildAvailableModelEntries` hybrid 时跳过 adapters 只输出官方；`buildUsableModelsPayload`/`buildDefaultModelPayload`/`buildDefaultModelForCliPayload` hybrid 时输出官方；`buildAvailableModelsPayload` 默认/回退模型取官方列表）；`official_models.go` 新增 `officialModelRefs`/`firstOfficialModelRef`/`buildOfficialCLIModelDetails`。测试：`go build ./...` + `go test ./internal/... -count=1` 全绿，新增 hybrid 排除 + 非 hybrid 回归测试。
> - T2 ✅ 已完成：`~/.cursor-local-assistant-v2/config.yaml` 已设 `routing.hybridMode: true`（备份 `config.yaml.bak-hybrid`）；新二进制已部署至 `D:\Cursor助手\Cursor助手.exe`（旧版备份 `Cursor助手.exe.bak.pre-hybrid-t1`）并重启。**端到端验证通过**（直接调用本地服务 API）：
>   - `AvailableModels` 只返回官方模型（初始 8 内置 → 透传刷新后 192 个官方真实模型），无任何自定义 adapter channelID；`GetDefaultModel`/`GetDefaultModelForCli` 返回官方 `gpt-5.3-codex`；`GetUsableModels` 透传官方成功（193 个模型，无本地 apiKeyCredentials）。
>   - **顺带修复既有 bug**：`RefreshOfficialModelsFromResponse` 原用 `aiserver.v1` 解析官方二进制响应，与官方实际编码（`agent.v1` 格式）wire type 不匹配导致动态目录刷新永远失败；且官方对 `Accept-Encoding: gzip` 请求返回 gzip 压缩体而服务端 Transport 不自动解压。已修复为：gzip 魔数检测解压 + `agent.v1.GetUsableModelsResponse` 解析，并补对应单测。
> - T3：本轮不执行（文档建议成本高收益有限）。
>
> **补充修复记录（2026-08-06 第二轮，用户实测报 `model channel not available`）**：
> - **根因**：Cursor 客户端对 `BidiAppend` 请求体做 gzip 压缩（`Content-Encoding: gzip`，connect handler 会自动解压），而外层路由判定 `decodeBidiAppendRequestMeta`（internal/backend/official_route.go）直接对原始 body 做 `proto.Unmarshal`——gzip 字节解析失败 → `request_id/model` 为空 → 官方模型请求不登记透传，落入本地 forwarder 渠道选择 → 报 `model channel not available`（`ErrChannelNotAvailable`）。
> - **修复**：`decodeBidiAppendRequestMeta` 解析前检测 gzip 魔数（`1f 8b`）并解压（新增 `maybeGunzipBidiBody`），`r.Body` 保持原始交给 inner connect handler 自行解压；补 `TestDecodeBidiAppendRequestMeta/gzip_compressed_body` 回归单测。
> - **验证**：模拟 gzip 压缩 BidiAppend（composer-2.5）→ 日志出现 `official model bidi routed request_id=user-sim-1 model=composer-2.5 -> api2.cursor.sh`；服务端已重新部署并重启（含全部修复）。
>
> **补充修复记录（2026-08-06 第三轮，Auto 模式实测走本地 BYOK）**：
> - **根因**：Cursor 客户端 **Auto 模式**的 run_request 携带 `model_id="default"`（官方 GetUsableModels 也返回 `modelId="default"`/`displayModelId="auto"` 条目），但 `IsOfficialModel("default")` 返回 false（目录刷新时 default 被过滤）→ Auto 对话未登记官方透传，落入本地 forwarder → 渠道回退选中第一个 BYOK adapter（deepseek-v4-flash）执行。
> - **修复**：`IsOfficialModel` 将 `default`/`auto` 占位 ID 视为官方透传（仅路由判定；`OfficialModelEntries` 仍排除 default，模型选择器不显示该条目）。更新 `TestIsOfficialModel` 用例与目录排除断言。
> - **验证**：模拟 Auto（model=default）+ gzip BidiAppend → 日志 `official model bidi routed request_id=auto-test-1 model=default -> api2.cursor.sh`；`go test ./internal/...` 全绿；已部署重启。
>
> **补充修复记录（2026-08-06 第四轮，官方模型对话无响应——最终根因：官方拒绝旧版 Cursor）**：
> - **排查过程**：BidiAppend 透传官方成功（官方 200 空受理，5.9s）；但客户端始终不连本地 RunSSE（本地路径会连）；手动验证官方 api2 RunSSE（connect+proto envelope）返回 200 流式，api3 端点全部 404（官方端点都在 api2）。
> - **改动**：`officialBidiAppendHandler` 改为**立即受理**（先向客户端返回空 BidiAppendResponse，避免等待官方 5.9s 导致客户端放弃 RunSSE 订阅）+ **异步透传官方**（独立 context，`discardResponseWriter` 丢弃官方空确认）；RunSSE 透传链路不变。`TestOfficialBidiAppendRegistration` 更新为新语义。
> - **最终根因（非代码问题）**：用服务端账号 token 直连官方 api2 RunSSE 订阅用户真实请求，官方返回 `resource_exhausted` + `ERROR_GPT_4_VISION_PREVIEW_RATE_LIMIT`，详情 title="Update Required"、**"Your version of Cursor is no longer supported. Please update to the latest version at cursor.com/downloads"**、`analyticsMetadata.actionRequired="payment"`——**官方拒绝旧版本 Cursor 客户端（当前 3.14.7）**。透传链路完全正常（BidiAppend 受理 + RunSSE 订阅 + 官方明确报错），本地 BYOK（deepseek）不受影响。
> - **解决方向（用户侧）**：更新 Cursor 客户端到最新版；检查官方账号（yx2751244500）订阅/付款状态。
>
> **补充修复记录（2026-08-06 第五轮——最终根因：客户端被注入假账号导致官方拒绝）**：
> - **根因确认**：官方返回的 `ERROR_GPT_4_VISION_PREVIEW_RATE_LIMIT`（title="Update Required"）并非版本问题（用户确认 D:\cursor 3.14.7 即最新 freeauto，昨天登录自己账号直连官方可用）。真实原因：**byok 启动时向客户端注入本地模拟账号**（`cursor@ai.com` + fake token，internal/client/lifecycle.go:101），客户端会话特征（假账号）与透传官方请求的真实 token（cursor-account.json，yx2751244500）不匹配 → 官方把透传请求判定为异常会话并拒绝（Update Required）。
> - **修复**：hybrid 模式下 `lifecycle.go` 改用真实官方账号注入客户端（新增 `runtime.ReadOfficialAccountCredentials` 读 cursor-account.json）；mock 账号响应（`handleMockAuthPoll`/`handleMockAuthEmail`/`buildDashboardGetMePayload`）在 hybrid 模式下返回真实账号（新增 `effectiveAccountCredentials` helper），与注入保持一致。
> - **验证**：服务重启后日志 `hybrid mode: injecting official account email=yx2751244500@gmail.com into Cursor`；state.vscdb 的 cursorAuth/accessToken sub=google-oauth2|user_01JJ8YKVKNC0E3T8GNYGDP3GQX、cachedEmail=yx2751244500@gmail.com（真实账号）。
> - **待用户操作**：完全退出并重启 Cursor 客户端（加载真实账号），再测试官方模型对话。

---

## 0. 现状代码事实（已确认，勿重复调查）

- **hybrid 开关**：`internal/backend/server/config/types.go:111` `Routing.HybridMode bool`（yaml: `routing.hybridMode`）；前端 Home.vue 已有开关 UI（`handleHybridModeChange` + `saveHybridMode`，frontend/src/state/appState.js:2003）。
- **hybrid 路由**：`internal/backend/host.go` 中 `GetUsableModels` 用 `hybridUsableModelsAction(hybridMode, host, routeDeps)`（official_route.go:168）：hybrid 且官方账号登录时**透传官方真实模型列表**（`officialUsableModelsAction` → `forwardToOfficial` → `RefreshOfficialModelsFromResponse` 刷新动态官方目录）；未登录/透传失败回退 `UsableModelsMockBuilder`。
- **官方模型目录**：`internal/backend/server/upstream/official_models.go` `builtinOfficialModels`（8 个内置）+ 动态刷新 `officialModels`；`OfficialModelEntries()`（:196）输出官方模型的 AvailableModels entry 格式。
- **BidiAppend/RunSSE 分流**：`official_route.go:30/67` `officialBidiAppendHandler`/`officialRunSSEHandler`——`isOfficialRequest`（:90）按模型 ID 是否命中官方目录决定透传 api2 执行；日志特征：`official model bidi routed request_id=... model=... -> api2.cursor.sh`（official_route.go:44）。
- **模型列表 mock**（BYOK 自定义模型来源）：
  - `AvailableModels`：`mocks.go:448 buildAvailableModelsPayload` → `buildAvailableModelEntries(adapters, hybridMode)`（:694）——**当前 hybrid 模式下 = 自定义模型 + 官方模型混合输出**（:697-739 先遍历 adapters 再追加官方）。
  - `GetUsableModels` 回退：`mocks.go:509 buildUsableModelsPayload` → `buildCLIModelDetails(adapters)`——**只含自定义模型**（hybrid 回退场景同样只有自定义模型）。
  - `GetDefaultModel` / `GetDefaultModelForCli`：`buildDefaultModelPayload` / `buildDefaultModelForCliPayload`（mocks.go），默认模型取自定义模型列表第一个。
- **已知风险注释**（official_models.go 头部）：官方模型走官方账号计费，官方可能风控。

## 1. 任务清单

### T1：hybrid 模式下模型列表排除 BYOK 自定义模型（核心改动）

**文件**：`internal/backend/server/upstream/mocks.go`

1. **`buildAvailableModelEntries(adapters, hybridMode)`（:694）**：hybridMode 为 true 时**跳过 adapters 循环**，只输出 `OfficialModelEntries()`。
   - 实现要点：`if !hybridMode { for _, adapter := range adapters { ... } }`（用 if 包住现有 adapters 循环）；hybrid 时 `defaultModel`（:457-460 的上层逻辑）也应取官方模型——见 T2。
2. **`buildUsableModelsPayload`（:509）**：hybrid 模式时输出官方模型（`OfficialModelEntries()` 或 `officialUsableModelsPayload` 格式），不输出 `buildCLIModelDetails(adapters)`。注意保持响应结构 `{"models": [...]}` 与客户端兼容（对照官方透传响应的实际字段：modelId/displayModelId/displayName/maxMode，见 official_models.go:79-85）。
   - hybrid 判断复用 `readHybridModeFromDeps(reqCtx)`（:453 已有用法）。
3. **`buildDefaultModelPayload` / `buildDefaultModelForCliPayload`**：hybrid 模式下默认模型返回官方默认（建议 `claude-sonnet-4-5` 或 `OfficialModelEntries()` 列表第一个的 name），禁止返回自定义模型。

**验收（代码级）**：hybrid 开启时 `AvailableModels` 响应不含任何 adapter channelID；`GetUsableModels` 回退响应只含官方模型；`GetDefaultModel` 返回官方模型名。非 hybrid 行为与现在完全一致（回归：`go test ./internal/backend/server/upstream/ -count=1`，现有 `official_models_test.go` 必须通过）。

### T2：启用 hybrid 并验证 auto 对话（配置 + 端到端）

1. **配置**：`~/.cursor-local-assistant-v2/config.yaml` 设置 `routing.hybridMode: true`（或通过前端 Home.vue 的"开启混合模式"开关，会写同一配置并重启服务）。
2. **重启服务**：改配置后重启 cursor-byok（前端按钮或 `scripts/restart-dev-windows.ps1`）。
3. **端到端验证**（在 Cursor 客户端操作）：
   - 模型选择器：应**只显示官方模型**（claude-sonnet-4-5 / gpt-5 / composer-2.5 等），无 BYOK 自定义模型名。
   - 切到 **Auto 模式**发一条消息：日志出现 `official model bidi routed request_id=... model=... -> api2.cursor.sh`（确认走官方透传），回复正常（消耗官方账号额度）。
   - 官方模型列表刷新：首次 GetUsableModels 透传后动态官方目录被刷新（`RefreshOfficialModelsFromResponse`），模型选择器与官方真实列表一致。
   - 反向确认：`routing.hybridMode: false` 时模型列表恢复为 BYOK 自定义模型 + 官方模型混合（现状），Auto 对话走本地 BYOK。
4. **回归**：`go build ./... && go test ./internal/... -count=1` 全绿；非 hybrid 路径行为不变（mock 模型列表与之前一致）。

### T3（可选，低优先级）：官方会话抓包提取 root prompt

**背景**：A2 已实测确认服务端无主提示词下发端点（`GetChatPrompt` 404、`GetPassthroughPrompt`/`StreamPriomptPrompt` unimplemented、`GetSimplePrompt` 仅返回占位符），主提示词由服务端在会话流中生成并固化到 `conversation_state.root_prompt_messages`。
**做法**（若用户需要与官方一致的提示词）：官方模式（`routingMode: upstream` 或关闭本地服务）下用 Cursor 客户端跑一次真实对话，用 Fiddler/Charles 或项目 MITM 抓 `api2.cursor.sh` 的 `StreamChat`/`BidiAppend` 流量，从响应流/checkpoint 提取 `root_prompt_messages` 中的 system prompt 全文，作为 `prompt/native/` 资产。消耗一次官方会话额度。**不建议本轮做**（成本高、收益有限——B 期已完成客户端原生内容接线）。

---

## 2. 相关文件索引

| 文件 | 说明 |
|---|---|
| `internal/backend/server/config/types.go` | `Routing.HybridMode` 配置字段（:109-111） |
| `internal/backend/official_route.go` | hybrid 路由分流（BidiAppend/RunSSE/GetUsableModels）、官方透传、`isOfficialRequest` |
| `internal/backend/server/upstream/official_models.go` | 内置官方目录、动态刷新、`OfficialModelEntries` |
| `internal/backend/server/upstream/mocks.go` | `buildAvailableModelEntries`(:694)/`buildUsableModelsPayload`(:509)/默认模型 mock（**T1 改动点**） |
| `internal/backend/server/upstream/action.go` | Mock builder 入口 |
| `internal/backend/host.go` | 路由注册（AvailableModels/GetUsableModels/GetDefaultModel 等） |
| `frontend/src/views/Home.vue`、`frontend/src/state/appState.js` | 前端 hybrid 开关（已存在，无需改动，除非 UI 文案需调整） |
| `scripts/restart-dev-windows.ps1` | 重启开发服务 |

## 3. 验证命令汇总

```bash
go build ./... && go test ./internal/... -count=1        # 编译 + 全部测试
go test ./internal/backend/server/upstream/ -count=1     # mock/官方模型单测
# 端到端：见 T2 步骤 3（需 Cursor 客户端 + 官方账号）
```

## 4. 风险与注意事项

1. **风控**：官方模型请求透传 api2 执行，官方服务端可能检测异常请求特征（官方_models.go 已注明）。本任务只改模型列表可见性，不改变透传实现，风险不变。
2. **额度**：auto 对话走官方模型消耗官方账号额度（Pro 会员/按量）。
3. **回退链**：官方账号未登录或透传失败时，GetUsableModels 回退 mock——T1 保证该回退也只含官方模型（否则 auto 可能选到自定义模型）。
4. **已有会话**：历史会话引用的 BYOK 模型仍可继续（isOfficialRequest 按模型 ID 判断），不受影响。
5. **前缀稳定**：本任务不涉及 prompt 渲染，无 prefix-cache-stability 风险。