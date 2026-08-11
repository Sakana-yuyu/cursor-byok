# Verification Ledger: backend-capability-ui-discovery

## Round 0 - propose

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-1 | necessity | major | fixed(r0) | 不为已移除的授权/设备操作或不支持的用量查询建立 UI。 | `internal/client/license.go` 的对应方法固定返回“已移除”或 `UNSUPPORTED`；镜像记录已有完整后端接线但无前端引用。 | 提案只保留镜像记录开关，并在“Not in this change”排除这些接口。 |
| V-2 | regression-compat | major | fixed(r0) | 不新增测试文件，避免违反项目任务清单的“不写任何测试”约束。 | `IMPROVEMENT_TASKS.md` 明示不写测试；初版提案包含“补充浏览器预览测试”。 | 改为运行既有 lint、i18n、构建和浏览器预览冒烟检查。 |
| V-3 | regression-compat | minor | fixed(r1) | `mirrorCapture` 必须贯通归一化、持久化 payload、状态回填和缓存，否则保存其他设置可能重置开关或 hosts。 | `b662392` 将该配置加入 `normalizeConfig`、保存 payload、缓存 payload、状态回填和 browser-preview 默认配置；`8ca55f9` 的开关只更新 `enabled` 并保留 hosts。2026-08-12 复核 `yarn test:unit`（29/29）、`yarn lint`、`yarn build` 均通过；全量 E2E 为 68/69，唯一失败是无关的更新说明 Markdown 弹窗。四个语言包均为 1,435 个目录键，且 `missing/extra/empty/placeholder_mismatch` 均为 0。 | 保存路径保留既有 hosts，开关默认关闭；不新增测试文件，符合 `IMPROVEMENT_TASKS.md`。 |

审查说明：当前会话未提供独立审查代理调度工具，按可用能力降级为基于代码与提案的必要性、回归兼容双视角审查。真实桌面端的官方请求写入 `history/_debug/mirror/official.raw.jsonl` 尚未在本次会话中人工验证；浏览器预览与既有 E2E 只能证明前端路径和 mock 保存行为。

## Round 1 - implementation evidence

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-4 | correctness | minor | open | 全量浏览器 E2E 未全绿：更新说明 Markdown 弹窗未进入 DOM。该失败不经过本变更的高级设置、镜像状态 API、目录打开 binding 或浏览器 preview mock。 | `frontend/yarn test:e2e -- --workers=1` 退出码 1；68 通过、1 失败。失败固定在 `frontend/e2e/modal-markdown-lazy.spec.mjs:25`，预期 `发现新版本` 对话框中的 `性能优化` 标题，实际找不到该元素。 | 不在本镜像抓包变更中夹带修复；保留为基线验证缺口。 |
| V-5 | runtime-evidence | minor | open | 真实桌面端尚未证明 Cursor 的一次官方模型请求已写入本地记录。 | 本轮未改动真实 Cursor 配置、未启动桌面代理、未向官方 API 发起测试请求；浏览器 preview 仅注入 `fileExists/sizeBytes/modifiedAtUnixMs` 元数据。 | 需用户在桌面端显式开启镜像记录、启动服务或修复代理、重启 Cursor，并自行发起一次官方模型请求后确认状态变为“已记录”。 |

本轮证据：

- `go test ./internal/bridge ./internal/client`、`go build ./...`、`go vet ./...`：退出码 0。
- `frontend/yarn lint`：退出码 0。
- `frontend/yarn test:unit`：29/29 通过。
- `frontend/yarn build`：退出码 0，i18n 扫描与构建输出断言通过。
- 语言包结构化核对：`zh-CN`、`en-US`、`ja-JP`、`ru-RU` 均为 1,435 键，`missing=0`、`extra=0`、`empty=0`、`placeholder_mismatch=0`。
- 浏览器 preview：未启用态显示“未启用/刷新/打开记录目录”；注入只读元数据后显示“已记录”、`1.5 KB` 和更新时间；375px 宽度 `scrollWidth=375`、`innerWidth=375`；页面中未出现样例正文、HTTP URL 或 `authorization`；点击目录按钮记录到 `OpenMirrorCaptureDirectory` mock，控制台错误数为 0。
- `ast-grep` 未安装，未执行 AST 规则包；已对 `AdvancedSettings.vue`、`clientApi.js`、`browserBindings.js` 进行 charter 手工模式扫描。新增的状态/操作错误均写入界面错误状态，未发现“新逻辑失败后静默回退旧逻辑”或数据写入路径吞错返回默认值。
- 独立 `spec-verifier` agent 调度工具在当前会话不可用，因此本轮没有独立验证结论；上述检查为实现会话的自检证据，不能替代独立审查。

## Round 2 - mirror exchange correlation evidence

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-6 | correctness | major | fixed(r2) | 镜像记录的请求、响应起始、流式片段与截断标记没有共同事务标识；并发请求只能按时间猜测关联。 | `51a1b80` 在 `internal/mitm/service.go` 的镜像请求分支以 `goproxy.ProxyCtx.Session` 创建仅进程内的 `mirror-<session>` ID 并存入 `UserData`；响应过滤器只读取同一具名 `*mirrorExchange`。`internal/mitm/mirror.go` 为四类记录写入 `exchangeId` 和 `phase`，并将请求体或 Gemini URL 的模型名传递给响应记录。 | JSONL 可按 `exchangeId` 关联；阶段限定为 `request`、`response_start`、`response_chunk`、`response_truncated`。 |
| V-7 | security-boundary | major | fixed(r2) | 关联元数据不得改变官方请求，且不能扩大前端对抓包原文的读取边界。 | 静态检索显示 `internal/mitm/service.go` 仅有一处 `ctx.UserData = exchange`，位于镜像请求分支；新增代码没有 `Header.Set` 或 `Header.Add`。`frontend/src` 对 `exchangeId` 与四种阶段均无消费者。 | `exchangeId` 只落本地 JSONL；现有前端继续仅显示记录文件元数据与本地目录入口。 |
| V-8 | runtime-evidence | minor | open | 未在真实 Cursor 桌面端产生一条带关联字段的官方请求记录。 | 本轮没有启动桌面代理、修改真实 Cursor 配置或主动调用官方 API；`go test ./internal/mitm` 覆盖既有 recorder/响应包装路径，但项目 `IMPROVEMENT_TASKS.md` 要求不新增测试，未添加新测试文件或测试用例。 | 用户需在桌面端显式开启镜像记录、确认本地服务/MITM/代理均就绪、重启 Cursor 后用官方 key 发起一次模型请求；应检查 `history/_debug/mirror/official.raw.jsonl` 的同一交换记录具备相同 `exchangeId`，依次出现 `request`、`response_start` 及零到多个 `response_chunk` 或 `response_truncated`。 |
| V-9 | concurrency-evidence | minor | open | 当前环境无法运行 Go race detector，为并发关联提供额外运行时证据。 | `go test -race ./internal/mitm` 未启动，报错 `-race requires cgo`；`go env` 显示 `CGO_ENABLED=0`、`GOOS=windows`、`GOARCH=amd64`。 | 在具备 CGO 的 Windows Go 环境运行 `go test -race ./internal/mitm`；正常模块测试、`go vet ./internal/mitm` 与 `go build ./...` 已通过。 |

本轮证据：

- `go test ./internal/mitm`：退出码 0。
- `go vet ./internal/mitm`：退出码 0。
- `go build ./...`：退出码 0。
- `git diff --check`：退出码 0；代码提交前仅暂存 `internal/mitm/mirror.go` 与 `internal/mitm/service.go`。
- 关联 ID 基于 goproxy 的单调 `ProxyCtx.Session`，未进入 HTTP header、URL 或正文；镜像开关在响应过滤器仍保留原有检查，关闭后新到达的响应不再包装记录。

## Round 3 - routing semantics investigation

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-10 | correctness | major | open | `upstream` 的前端说明承诺绕过本地代理，但运行中服务仍会让 Cursor 经过本地 MITM；`routing.mode` 未参与 MITM 请求分流。 | `internal/client/lifecycle.go` 的 `StartProxy` 无条件启动 MITM 并调用 `ApplyCursorSettings`；`internal/client/cursor.go` 会写入 Cursor `http.proxy`；`internal/mitm/service.go` 对 `*.cursor.sh` 无条件转 backend，且仅镜像 host 旁路直通；`internal/bridge/proxy.go` 的 `PrepareCursorLaunch` 仅在 upstream 时跳过启动前就绪检查。 | 已将语义差异和目标行为写入研究、提案与设计；下一实现提交需先以现有 Go 测试描述 routing mode 对 MITM 分流的期望，再最小化接入配置读取和前端运行态文案。 |

本轮为静态请求链路追踪，未启动桌面代理、未修改真实 Cursor 设置、未调用官方 API。它确认了配置消费点与代理行为之间的不一致，但不能代替真实 Cursor 的端到端抓包验证。

## Round 4 - official upstream UI and status contract

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-11 | correctness | major | fixed(r4) | 镜像抓包就绪条件未包含 `routing.mode`，界面仍把 `upstream` 表述为无条件绕过本地代理。 | `6119826` 已让 MITM 在官方上游模式将 Cursor relay 直通官方；`ff223b4` 的 `GetMirrorCaptureStatus` 返回 `routingMode`，并仅在 `enabled && routingMode == upstream && backendRunning && proxyRunning && cursorSettingsApplied` 时标记 `ready`。首页、状态面板、确认弹窗与四个语言包统一为“官方上游模式”。 | local 模式显示“需要官方上游模式”，且不显示启动本地服务或修复代理这类无效动作；官方上游模式才按服务、代理接入和记录文件继续细分状态。 |
| V-12 | regression-compat | minor | fixed(r4) | browser-preview 的 `getProxyState`、`startProxyService` 和 `stopProxyService` 返回固定快照，绕过已有可变 mock；点击“启动本地服务”后状态无法前进。 | 浏览器复现后，`frontend/src/services/clientApi.js` 改为调用 `GetState`、`StartProxy`、`StopProxy` binding。清空因凭据保护而无法启动的预览配置后，Playwright 实际点击“启动本地服务”，控制台出现 `StartProxy response`，状态由“等待本地服务”变为“等待官方模型请求”。 | 预览 mock 与桌面调用合同保持一致，不影响真实 Wails binding。 |
| V-13 | runtime-evidence | minor | open | 本轮浏览器验证不等同于真实 Cursor 桌面端抓包。 | browser-preview 未修改真实 Cursor 配置，未启动真实桌面代理，也未向官方 API 发送请求；仅验证 local、official upstream、服务未启动与服务已启动的状态分支。 | 用户需在桌面端明确开启镜像记录、启动服务、重启 Cursor，并自行发起官方模型请求后确认 `history/_debug/mirror/official.raw.jsonl` 写入且页面显示“已记录”。 |

本轮证据：

- `frontend/yarn lint`：退出码 0。
- `frontend/yarn build`：退出码 0；保留既有 router 静态/动态导入与 chunk 大小警告。
- `go test ./internal/bridge ./internal/mitm ./internal/backend/server/config -count=1`：退出码 0。
- `go vet ./internal/bridge ./internal/mitm ./internal/backend/server/config`：退出码 0。
- `go build .`、`go build ./cmd/...`、`go build ./internal/bridge ./internal/mitm ./internal/backend/server/config`：均退出码 0。`go build ./...` 曾在前端构建期间扫描 `//go:embed all:frontend/dist` 的资源目录时报告 `frontend/dist/supplier-icons` 路径不可访问，复跑根程序和相关包构建均成功，不将此环境性竞态表述为全量构建通过。
- 语言目录结构化核对：`en-US`、`ja-JP`、`ru-RU` 各 1,436 键，`missing=0`、`extra=0`、`empty=0`、`placeholderMismatch=0`。
- Playwright browser-preview：local 模式开启镜像记录后显示“需要官方上游模式”，无启动/修复代理动作；官方上游模式开启记录后先显示“等待本地服务”，启动 mock 服务后显示“等待官方模型请求”。375px 视口两次检查均为 `scrollWidth=375`、`clientWidth=375`，未出现横向溢出。
- `git diff --check` 与暂存后 `git diff --cached --check` 均退出码 0；代码提交 `ff223b4` 仅包含本轮 11 个 UI、状态、桥接与 i18n 文件。

## Round 5 - isolated E2E boundary audit

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-14 | runtime-evidence | minor | open | 项目已有 `cmd/isolated-cursor-e2e`，但它不是镜像 recorder 的隔离 E2E。 | 该入口在临时目录设置 `USERPROFILE`、`HOME`、`APPDATA`、`LOCALAPPDATA`，并为 Cursor 子进程设置隔离 CA；但 `cmd/isolated-cursor-e2e/main.go` 以空 `historyRoot` 和 `nil` mirror 配置调用 `mitm.NewProxyServer`，且源码注释明确“不写入真实请求镜像”。因此它不会产生 `official.raw.jsonl`。 | 不将该启动器的代理启动结果冒充为抓包成功；本阶段不运行它，因为启动 Cursor 后由人工操作产生的官方请求不满足“不调用官方 API”的验证边界。 |
| V-15 | safety-boundary | minor | fixed(r5) | 隔离入口不会直接写入真实用户的 Cursor 配置或状态。 | `applyIsolatedEnvironment` 和 `buildCursorChildEnvironment` 同时覆盖临时 HOME、APPDATA、LOCALAPPDATA；代理设置与状态注入发生在这些变量已替换之后，监听地址严格分配为 `127.0.0.1` 临时端口。 | 当前审计没有运行该入口，也没有修改真实 Cursor 配置、已安装客户端或官方 API；真实抓包继续由用户明确授权并操作。 |

本轮证据：

- 静态读取 `cmd/isolated-cursor-e2e/main.go`、`internal/mitm/service.go`、`internal/mitm/mirror.go`、`internal/cursor/settings.go` 与 `internal/cursor/state_db.go`，确认隔离目录、代理设置、状态注入、MITM recorder 的创建参数和记录条件。
- `rg` 命中 `cmd/isolated-cursor-e2e/main.go:101-102`：该命令明确传入空 `historyRoot` 和 `nil` mirror 配置；`internal/mitm/service.go:888` 要求 mirror 配置和 recorder 同时存在才启用记录。
- 未新增测试文件，符合 `IMPROVEMENT_TASKS.md`；未启动 Cursor、未启动代理、未调用任何官方 API。

## Round 6 - opt-in isolated mirror capture

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-14 | runtime-evidence | minor | fixed(r6) | 隔离 E2E 启动器此前固定禁用镜像 recorder，不能留下官方请求镜像。 | `72a45e8` 在 `cmd/isolated-cursor-e2e/main.go` 增加严格等于 `CURSOR_E2E_MIRROR_CAPTURE=1` 的显式开关。启用后配置强制为 `mirrorCapture.enabled=true` 和 `routing.mode=upstream`，MITM 接收临时 `history` 及隔离配置管理器；未启用时仍传入空 `historyRoot` 与 `nil` 配置。 | 启动器输出的 `mirror_record` 固定为 `<isolated_root>/history/_debug/mirror/official.raw.jsonl`；默认行为不变。
| V-16 | runtime-evidence | minor | open | 尚未由隔离 Cursor 真实登录并发起一次官方模型请求，因此没有运行时证据证明 `official.raw.jsonl` 已实际写入。 | 本轮未启动 Cursor、未写入真实用户目录、未调用官方 API。`go test ./cmd/isolated-cursor-e2e ./internal/mitm -count=1`、`go vet ./cmd/isolated-cursor-e2e ./internal/mitm` 与 `go build ./cmd/isolated-cursor-e2e` 均退出码 0，只证明本地代码路径可构建和既有覆盖通过。 | 用户可在明确授权的隔离会话中设置 `CURSOR_E2E_MIRROR_CAPTURE=1`，在临时 Cursor 内自行登录并发起官方模型请求，然后核对输出的 `mirror_record` 存在，且同一交换记录使用一致的 `exchangeId` 与 `phase`。
| V-17 | safety-boundary | minor | fixed(r6) | 启用镜像模式不应把本地伪账号写入隔离 Cursor，避免误将本地模式身份用于官方上游调用。 | `72a45e8` 仅在镜像模式关闭时调用 `cursor.InjectCursorUserInfo`；所有配置、代理设置、CA、history 和 Cursor 子进程环境继续锚定在 `isolated_root`。 | 镜像模式由用户在临时 Cursor 中自行登录；回退该提交或取消环境变量即可恢复原隔离代理验证路径。

本轮证据：

- `go test ./cmd/isolated-cursor-e2e ./internal/mitm -count=1`：退出码 0。
- `go vet ./cmd/isolated-cursor-e2e ./internal/mitm`：退出码 0。
- `go build ./cmd/isolated-cursor-e2e`：退出码 0。
- `git diff --check`：退出码 0；功能提交 `72a45e8` 仅包含启动器和本轮 Spec 批准标记。
- 未新增测试文件，遵循 `IMPROVEMENT_TASKS.md`；当前会话没有可调用的独立 `spec-verifier` 调度工具，因此以上是实施自检，不代替独立审查。
