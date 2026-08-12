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

## Round 7 - backend capability and UI coverage audit

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- |
| V-18 | necessity | minor | fixed(r7) | 不能把未被 `frontend/src` 调用的 Wails binding 机械补成新界面。 | 对 `ProxyService`、`MetricsService`、`WindowService`、`AdService` 公开方法及 generated binding 做静态对照：117 个公开方法中 99 个已有 `frontend/src` 调用，未引用的 18 个只包括 12 个已移除/不支持/生命周期/高副作用 ProxyService 接口和 6 个 WindowService 运行时装配接口。 | 排除这些 binding；不增加必然失败、重复或会裸改本地 Cursor 配置的 UI。 |
| V-19 | usability | minor | fixed(r7) | 仅检查 Wails binding 的引用不足以证明最终用户路径存在，需再检查服务包装层是否由页面、组件、布局或状态层消费。 | 对 `clientApi.js` 和 `runtimeControlApi.js` 的 100 个导出向 `frontend/src` 非 services 消费者对照，97 个有实际消费者；`invokeOperation` 是 runtimeControlApi 内部通用执行器，`getDelegationConfig` 被总配置读取覆盖，`updateStatsOverlayWindow` 由 `updateStatsOverlayLayout` 间接调用。 | 未发现后端已可用、低风险、面向用户却没有界面的能力；维持现有入口，不为覆盖率新增平行操作台。 |

本轮证据：

- 使用 PowerShell 从 `frontend/bindings/cursor/internal/bridge/{proxyservice,metricsservice,windowservice,adservice}.js` 提取公开 binding，并扫描 `frontend/src`：117 个方法，99 个有调用、18 个无调用。
- 逐项回溯 `internal/bridge/proxy.go`、`internal/client/license.go`、`internal/client/cursor.go`、`internal/client/lifecycle.go`、`internal/bridge/window.go` 与 `internal/app/runner.go`，确认未引用项的固定失败、配置副作用和内部调用者。
- 使用 PowerShell 从 `frontend/src/services/{clientApi,runtimeControlApi}.js` 提取导出并扫描非 services 消费者：100 个导出，97 个有消费者；余下 3 个均有明确的内部/间接复用理由。
- 本轮仅更新 Spec 研究与验证台账，不新增测试文件、不修改业务代码、不启动 Cursor、不改真实用户配置或发起官方 API 请求。

## Round 8 - isolated relay capture runtime evidence

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-16 | runtime-evidence | minor | fixed(r8) | 隔离镜像 recorder 是否能接收真实 Cursor 官方 relay 流量此前没有运行时证据。 | 以 `CURSOR_E2E_MIRROR_CAPTURE=1` 启动新的隔离实例后，临时根目录的 `history/_debug/mirror/official.raw.jsonl` 非空。结构化逐行解析得到 72 条记录，全部属于 `api2.cursor.sh` 或 `api3.cursor.sh`；18 个交换同时包含 `request` 与 `response_start`，29 条为 `response_chunk`，且每条均带 `exchangeId`。 | 已证明临时 CA 信任参数、relay hosts 与镜像 recorder 在真实 Cursor 网络栈中联通；记录内容未读取、未输出、未提交。 |
| V-20 | security-boundary | major | fixed(r8) | 仅临时实例应信任本次 CA，不能使用全局 TLS 忽略或改动真实 Cursor/系统证书库。 | 临时 Cursor 的 network utility 进程命令行包含 `--ignore-certificate-errors-spki-list=<本次临时 CA 的 SPKI 哈希>`，未使用 `--ignore-certificate-errors`；用户数据目录位于本次 `isolated_root`。临时 Cursor 日志内 `ERR_CERT_AUTHORITY_INVALID` 计数为 0。 | SPKI 白名单仅随隔离子进程传递；真实用户配置、Windows 证书库与已安装 Cursor 均未修改。 |
| V-21 | data-protection | major | fixed(r8) | 真实 relay 抓包不能把凭据型 URL 查询参数以明文带入证据或文档。 | 对临时 JSONL 仅做结构化解析与参数值检查：`key`、`api_key`、`token`、`secret`、`signature`、密码等敏感查询参数的未脱敏值计数为 0。未读取或打印请求/响应正文、认证头、Cookie、token 或完整 URL。 | 验证台账仅保留计数、host、phase 与关联结论；原始 JSONL 继续只留在临时目录。 |
| V-22 | runtime-evidence | minor | open | 目前捕获的是 Cursor 启动期的官方 relay 流量，尚未由用户在隔离 Cursor 完成登录后主动发起模型或 Agent 请求。 | 本轮启动后未替用户登录、未代表用户提交模型提示词，也未读取业务请求正文；因此不能将 relay 通信等同于完整业务请求验收。 | 保持当前隔离 Cursor 运行。用户在该临时窗口自行登录并发起一次模型或 Agent 请求后，应再次只做 JSONL 结构化核验，确认相应 exchange 的 `request`、`response_start` 与可选流式片段。 |

本轮证据：

- `go test ./cmd/isolated-cursor-e2e ./internal/mitm -count=1`：退出码 0。
- `go vet ./cmd/isolated-cursor-e2e ./internal/mitm`：退出码 0。
- `go build ./cmd/isolated-cursor-e2e`：退出码 0。
- 代码提交 `c43caf3` 仅修改隔离启动器；`git diff --check`、暂存后 `git diff --cached --check` 均退出码 0。
- 本轮仍未新增测试文件，遵循 `IMPROVEMENT_TASKS.md`。
- 临时 Cursor 保持运行，用于用户控制的登录后业务请求验证；停止时必须按已记录 launcher PID 的后代树精确结束，不能按 `Cursor.exe` 进程名批量结束。

## Round 9 - authenticated Agent protocol capture

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-22 | runtime-evidence | minor | fixed(r9) | 隔离 Cursor 登录后主动发起的 Agent 业务请求此前尚无抓包运行时证据。 | 用户在隔离 Cursor 中自行发起请求后，对临时 `official.raw.jsonl` 做了不读取正文的逐行结构化核验：快照时共有 1,326 行，`invalidJsonLines=0`、`missingExchangeId=0`。`mirror-590` 位于 `api2.cursor.sh`，具有 `request`、`response_start`、`response_chunk` 三阶段，HTTP `200`，共 63 个流式片段、65 条关联记录；另有 11 个关联的 `BidiAppend` 交换，均含 `request`、`response_start` 且 HTTP `200`。 | 已证明隔离 CA 信任、MITM、api2 relay 镜像、请求/响应关联与 Agent 流式记录在真实登录后的业务请求中协同工作。 |
| V-23 | data-protection | major | fixed(r9) | 真实 Agent 协议核验不得因定位端点而读取或输出业务正文、认证头、Cookie、token 或完整 URL。 | 本轮只读取 JSONL 的结构化元数据并按 `exchangeId` 聚合；核验凭据型 URL 查询参数未脱敏值为 0。临时 Cursor 日志中 `ERR_CERT_AUTHORITY_INVALID` 计数为 0。响应记录按设计不重复保存 URL，因此以请求记录识别端点、再以同一 `exchangeId` 回溯响应阶段和状态。 | 验证台账只保留交换数、阶段、状态码和片段计数；原始 JSONL 继续仅保留在临时隔离目录，未加入仓库或前端读取路径。 |

本轮证据：

- 用户在隔离 Cursor 内自行登录和发起 Agent 请求；本会话没有代替用户登录、提交业务提示词或读取业务正文。
- 镜像快照逐行 JSON 解析通过，所有记录都有 `exchangeId`；敏感查询参数检查结果为 0 个未脱敏值。
- `RunSSE` 与 `BidiAppend` 仅按请求 URL 的 host/path 识别，响应阶段通过同一 `exchangeId` 关联，符合 recorder 的记录格式。
- 临时 Cursor 仅携带本次临时 CA 的 SPKI 白名单；未使用全局忽略 TLS 错误参数，且日志中 `ERR_CERT_AUTHORITY_INVALID=0`。
- 这证明官方 Cursor Agent 协议抓包基础设施已真实工作；不代表本仓库本地模式 forwarder、具体模型供应商或完整端到端兼容性均已验证。

## Round 10 - protocol fidelity and Multitask timeline

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- |
| V-24 | runtime-evidence | minor | open | 新增的 `protocol.timeline.jsonl` 尚未在用户控制的隔离 Cursor Multitask 交互中重新核验。 | 本轮没有启动 Cursor、代替用户登录或代表用户发起官方请求；只以临时本地 protobuf 回环构造 Multitask `BidiAppend`、同一 request ID 的 RunSSE Connect 请求与终止帧。 | 用户在隔离 Cursor 中开启 Multitask 并自行发起一次 Agent 请求后，仅做结构化检查：`official.raw.jsonl` 的 Bidi/RunSSE 记录含保真字段，`protocol.timeline.jsonl` 以同一 `requestIdHash` 关联上行、下行和终止事件，且索引不含完整 request ID、正文或原始帧 Base64。 |
| V-25 | independent-review | minor | open | 本轮无法执行 spec-workflow 要求的独立 `spec-verifier` 审查。 | `C:\Users\Administrator\.codex\agents\spec-verifier.toml` 不存在；本会话可用工具没有 `spawn_agent`，仅有用户显式创建新任务的接口，不能替代独立审查。 | 已记录为降级验证限制；不得将本轮自检表述为独立审查通过。后续在具备 `spawn_agent` 和已安装 verifier 定义的会话重新运行 `$spec-verify`。 |

本轮证据：

- `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`：退出码 0。
- `go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`：退出码 0。
- `git diff --check`：退出码 0；四个实现提交分别为 `9198840`、`26a2db0`、`50299fb`、`77a34df`，未混入 `.playwright-cli/`、`frontend/.playwright-cli/` 或 `output/`。
- 临时本地回环（执行后已删除，未进入提交）：非法 UTF-8 body 的 Base64/长度/SHA-256 可逆；畸形 Bidi 内层消息写入 `agent_client_unmarshal_failed` 且不阻断上游；两个 Connect 帧共用一个读取块仍写为两条完整协议帧，不完整尾帧写 `connect_frame_incomplete`；Multitask Bidi、RunSSE 请求和终止帧以相同哈希关联，索引不含完整 request ID 或 `frameBase64`。
- 历史 Round 9 的真实隔离 Agent 抓包证据仍只证明旧的交换关联与流式记录链路；它不能替代本轮新增保真字段和时间线索引的真实 Multitask 验收。
- `ast-grep` 未安装，未执行 AST 规则包；已对 `internal/mitm/mirror.go` 的新增路径做 charter 手工扫描。记录/解析失败仅记录稳定错误标签并保持代理直通，未发现以旧逻辑作为静默回退或以默认业务结果掩盖失败的分支。

## Round 11 - bidirectional protocol indexing real E2E

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-24 | runtime-evidence | minor | fixed(r11) | 真实 Cursor `RunSSE` 的响应头为 `text/event-stream`，但实际载荷是 Connect 帧；旧记录器会把这些帧降级为不可解析 SSE。 | 用户在新的隔离 Cursor 实例主动发送普通请求与 Multitask 请求后，临时 `protocol.timeline.jsonl` 有 844 条结构记录：724 条 `runsse_connect`、106 条 `bidi_append`、14 条 `runsse_request`。响应同时出现 `identity` 与 `gzip` Connect 压缩标识。 | `text/event-stream` 先进入待判定缓冲，只有合法且完整的 Connect 帧才切换 Connect 重组；当前真实流不再产生旧的 `runsse_sse` 降级记录。 |
| V-26 | runtime-evidence | minor | fixed(r11) | 上下行结构索引需证明能覆盖真实流式输出、工具执行和 Multitask 子代理创建，而不输出正文。 | 服务端顶层类型包含 `interaction_update`、`kv_server_message`、`exec_server_message` 与 `conversation_checkpoint_update`；二层出现 `thinking_delta`、`text_delta`、`tool_call_started`、`tool_call_completed`、`grep_args`、`shell_stream_args`，以及 `subagent_args -> create`。上行出现 `kv_client_message`、`exec_client_message`、`exec_client_control_message`、`client_heartbeat`，并记录 payload 来源、长度和 SHA-256。 | 时间线只保留结构名称、长度和哈希；prompt、工具参数、模型输出、token 内容、路径和完整稳定 ID 不进入索引。 |
| V-27 | data-protection | major | fixed(r11) | 原始帧必须可字节级复核，时间线不得复制原始字节或敏感字段。 | 对 724 个 `protocolFrame` 逐条 Base64 解码并重算长度、SHA-256：`valid=724`、`invalid=0`。对 844 条时间线记录按属性名检查，`body`、`bodyBase64`、`frameBase64`、`prompt`、`output`、`cookie`、`authorization`、`path`、`url`、`token`、`accessToken`、`refreshToken`、完整 `requestId` 的出现数均为 0。 | 原始帧仅保留在临时 `official.raw.jsonl`，结构时间线与 Git 提交均不承载正文、凭据或原始帧。 |
| V-28 | coverage | minor | open | 本次真实 Multitask 仅触发子代理创建，未观察到后台化或等待分支。 | `execMessageKind=subagent_args` 与 `subagentAction=create` 各 1 条；`force_background_subagent_args`、`subagent_await_args`、`subagentAction=background`、`subagentAction=await` 均为 0。 | 解析逻辑已按已知 proto oneof 支持这两类事件，但当前只能标记为未触发验证；后续需由用户实际触发后台化/等待工作流后再做结构化复核。 |

本轮证据：

- 新隔离目录为 `C:\Users\Administrator\AppData\Local\Temp\cursor-byok-e2e-2309467229`。已有 Cursor 实例未被终止；本轮仅启动新的隔离实例。
- 用户自行在新实例发起业务请求。本会话未读取或输出请求/响应正文、认证头、Cookie、token、完整 request ID 或完整 URL。
- 实现前已运行 `go test ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e`、`go build ./cmd/isolated-cursor-e2e`、`go vet ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e` 与 `git diff --check`，均退出码 0。
- 本轮真实 E2E 仅更新 Spec 台账；临时抓包数据没有暂存、提交、移动到 history 或暴露到前端。

## Round 12 - Multitask 并行、流式与交互闭环备注

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-29 | runtime-evidence | minor | fixed(r12) | 本次真实 Multitask 会话已覆盖多路子代理创建与成功结果回传，但协议索引不能把所有事件逐项绑定到截图中的 4 个 UI 子任务。 | 隔离实例时间线统计：`subagent_args=5`、`subagent_result=5`、`clientResultKind=success=4`（统计窗口内）；截图显示 `4 Working` 与 4 个并行审查标题。 | 保留安全结构索引；后续接入使用进程内父/子任务关联模型，不落盘任务正文、标题、完整 ID 或凭据。 |
| V-30 | runtime-evidence | minor | fixed(r12) | 用户交互询问与响应已形成可关联闭环。 | `interaction_query=3`、`interaction_response=3`；`web_search_request_query -> web_search_request_response` 2 组，`web_fetch_request_query -> web_fetch_request_response` 1 组；方向为 `runsse_connect -> bidi_append`，共享 `requestIdHash`。 | 前端应分别呈现等待询问、用户响应和继续运行状态；`requestIdHash` 只作为流级关联，不当作子代理 ID。 |
| V-31 | coverage | minor | open | 当前证据仍不能确认后台化、等待、取消/错误和父级 `Stop All` 的真实 oneof、终态与控制链路。 | `force_background_subagent_*`、`subagent_await_*` 以及取消/错误收口未在本次统计窗口中确认；截图中的 `Stop All` 仅证明 UI 控件存在。 | 用户后续实际触发对应动作后，再按上下行方向、oneof、终态、关联键和 `decodeError` 复核；在此之前保持未验证。 |
| V-32 | data-protection | major | fixed(r12) | 本次结构化索引未暴露敏感数据或正文。 | 对最近约 `11,498` 条时间线记录扫描，`body`、`bodyBase64`、`frameBase64`、`prompt`、`output`、`cookie`、`authorization`、`path`、`url`、`token`、`accessToken`、`refreshToken`、`requestId` 均为 0；`decodeError=0`。 | 原始 JSONL 继续只留在临时隔离目录，不进入 Git、研究文档或前端状态。 |

本轮对接结论：已可作为 Cursor Multitask 的协议状态采集基础，覆盖并行任务创建、流式思考/文本/工具调用、交互询问/响应、子代理成功回传、步骤完成和流关闭；尚不足以宣称完成逐子任务归属、后台化/等待、取消/错误及 `Stop All` 的完整还原。

## Round 13 - 取消、工具审批与 IDE 内 Playwright

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-31 | coverage | minor | fixed(r13) | 单子代理取消和父级 `Stop All` 的真实上行格式及基本收口已确认。 | 单子代理操作产生 `conversation_action.cancel_subagent_action` 和 `subagentAction=cancel`；父级停止时同一秒出现 4 条独立 `conversation_action.cancel_action`，随后有 `stream_close`、`step_completed`、`turn_ended` 和 terminal。 | 前端区分单任务取消与父级全量停止；逐任务 UI 归属仍需匿名关联模型，不能按 requestIdHash 代替。 |
| V-33 | runtime-evidence | minor | fixed(r13) | IDE 内 Playwright 浏览器操作通过 MCP 工具链执行，不是 `computer_use`。 | 真实窗口有 `mcp_allowlist_precheck_args/result=10/10`、`mcp_args=10`、`mcp_result=9`、`mcp_state_exec_args=1`；同时有 Shell 审批 `shell_allowlist_precheck_args/result=10/10` 与 `shell_stream_args=9`。 | 以 MCP 审批、MCP 调用/结果和 Shell 审批/流输出作为独立 UI 状态；不把 MCP 批准误标为终端批准。 |
| V-34 | data-protection | major | fixed(r13) | Playwright 会话的结构索引继续不暴露浏览器或 MCP 敏感内容。 | 仅记录 oneof、方向、长度/哈希和匿名 requestIdHash；不保存 MCP 服务名、浏览器页面、URL、页面内容、操作参数、结果正文或凭据。 | 原始保真数据继续局限于临时隔离目录；后续如需任务卡片归属，只增加不可逆匿名关联键。 |
| V-35 | coverage | minor | open | 子代理后台化和等待结果的专属工具分支尚未由真实 Cursor 调度器触发。 | 多轮真实长任务、父级等待和并行审查均未出现 `force_background_subagent_*` 或 `subagent_await_*`；出现的 `background_shell_spawn_*` 属于后台终端而非后台子代理。 | 保持当前解析支持但不得标为运行时已验证；待客户端实际下发相应 Exec 工具后复核。 |

本轮证据：

- IDE 内 Playwright 操作的 MCP 证据来自 `2026-08-12 14:55:41` 后的隔离时间线；相关请求可在安全索引中按匿名哈希关联，未读取或写入网页/命令正文。
- 截图中 `Allow / Stop` 的 Shell 审批已确认一组 `shell_allowlist_precheck_args -> shell_allowlist_precheck_result` 闭环；该闭环本身不携带用户最终选择的业务内容。
- 本轮文档前的最新 Playwright 窗口未出现解码错误；临时 JSONL 未暂存、未提交，也未暴露到前端。

## Round 14 - cursor-ide-browser 工具级调用矩阵

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-36 | runtime-evidence | minor | fixed(r14) | `cursor-ide-browser` 的实际后台浏览器调用已获得工具级计数。 | 从 `15:20:43` 后的保真 Connect 帧只提取 `McpArgs.provider_identifier/tool_name`：`cursor-ide-browser` 的 `browser_click=13`、`browser_cdp=12`、`browser_lock=4`、`browser_navigate=4`、`browser_snapshot=4`、`browser_tabs=4`，合计 41。 | 对接层按服务和工具名聚合浏览器执行状态；不保存 `args`、页面内容、URL、点击位置、tool call ID 或结果正文。 |
| V-37 | runtime-evidence | minor | fixed(r14) | 本轮浏览器控制直接走 MCP，而没有走 ComputerUse 或新的逐工具审批。 | 结构时间线有 `mcp_args=41`、`mcp_result=19`、`mcp_state_exec_args=1`、`tool_call_started/completed=44/42`、`step_completed/turn_ended/terminal=5/5/5`；`computer_use_args/result=0`，MCP/Shell allowlist 预检均为 0。 | 浏览器 UI 将直接 MCP 调用与经 ComputerUse 转发、需要审批的 MCP/Shell 调用明确区分。 |
| V-38 | data-protection | major | fixed(r14) | 工具级聚合过程未扩大原始抓包的内容暴露范围。 | 临时解析器仅输出服务标识、工具名和计数，完成后已删除；当前工作树没有该临时文件，原始 JSONL 未暂存或提交。 | 后续实现仅允许同等级非内容字段进入聚合指标；原始帧继续只保留在临时隔离目录。 |

本轮证据：

- 工具矩阵来自 `C:\Users\Administrator\AppData\Local\Temp\cursor-byok-e2e-171758565` 的隔离 `official.raw.jsonl` 与 `protocol.timeline.jsonl`，解析过程未输出参数或网页内容。
- `git diff --check` 通过；临时聚合程序已删除，未混入本轮文档提交。

## Round 15 - cursor-ide-browser 自述核验边界

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-39 | runtime-evidence | minor | fixed(r15) | Cursor 对 `cursor-ide-browser` 的工具流程自述与已捕获的非内容工具矩阵一致。 | 已捕获 provider=`cursor-ide-browser`，以及 `browser_tabs`、`browser_lock`、`browser_navigate`、`browser_snapshot`、`browser_click`、`browser_cdp`；无 `computer_use_args/result`。 | 后续对接以直接 MCP 浏览器调用建模，不误标为 OS 鼠标操作或 ComputerUse 协议。 |
| V-40 | evidence-boundary | minor | open | 具体前台 `viewId`、snapshot ref、点击目标、CDP 脚本、截图和解锁调用仅来自客户端自述，未由当前安全索引独立证实。 | 时间线不保存 MCP args、URL、DOM、页面文本、脚本、tool call ID 或结果正文；保真帧未向文档输出这些内容。 | 保留为客户端自述；若未来确需验证，新增仅保存不可逆标签页关联与工具种类的索引，不保存页面或认证数据。 |
| V-41 | data-protection | major | fixed(r15) | 客户端自述提及登录态写入，不能成为自动复制或落盘凭据的实现依据。 | 当前研究与时间线均未保存 token 值、认证请求体、存储键或 URL；原始数据仍局限于临时隔离目录。 | 对接界面仅呈现需确认的认证状态变更，不实现或记录自动凭据注入。 |

## Round 16 - 后续调度、流式与终态覆盖

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- |
| V-42 | runtime-evidence | minor | fixed(r16) | 用户执行三组后续操作后，当前索引继续覆盖子代理创建/结果、MCP、Shell、流式状态和终态收口。 | 以 `2026-08-12 15:45:22 +08:00` 为起点的只读窗口有 `subagent_args=3`、`subagent_result.success=2`、`subagent_result.error=1`、`mcp_args=23`、`mcp_result=10`、`mcp_state_exec_args=4`、`shell_stream_args=4`、`shell_stream=11`；下行有 `thinking_delta=1122`、`text_delta=3064`、`tool_call_started/completed=305/290`、`step_completed/turn_ended=9/9` 和 `terminal=8`。 | 对接层可继续以已捕获的状态机驱动子代理、工具、Shell 与流式 UI；这只是协议采集/解析证据，不等同于产品对接完成。 |
| V-43 | coverage | minor | open | 本轮仍未触发后台化、等待、ComputerUse 或新的审批预检专属协议。 | `force_background_subagent_args/result`、`subagent_await_args/result`、`computer_use_args/result`、`mcp_allowlist_precheck_args/result` 和 `shell_allowlist_precheck_args/result` 均为 `0`；`subagentAction` 仅为 `create=3`。 | 判定为 Cursor 调度器未选择这些 oneof，而非当前统计遗漏。待真实 UI/工具环境自然发出对应操作后，再标记为运行时已验证。 |
| V-44 | parser-resilience | minor | open | 本轮下行出现至少一条不完整 Connect 帧，但解析器将其显式标记而未静默丢弃。 | 时间线记录 `connect_frame_incomplete`，同时后续仍有流式、步骤完成、回合结束和 terminal 结构事件。 | 保留稳定错误标签并继续透传；在出现可重复的完整业务终态缺失证据前，不将单条截断记录判为协议解析失败。 |
| V-45 | data-protection | major | fixed(r16) | 后续统计未扩大安全时间线的数据范围。 | 时间线字段名扫描中 `body`、`bodyBase64`、`frameBase64`、`prompt`、`output`、`cookie`、`authorization`、`path`、`url`、`token`、`accessToken`、`refreshToken`、`requestId` 和 `args` 均为 `0`。 | 本轮只写入事件类型、计数、终态和错误标签；原始抓包继续留在临时隔离目录，未暂存或提交。 |

本轮证据：

- 隔离时间线在核验时已持续写入至 `2026-08-12 16:10:54`；统计使用实际 `ts` 字段，而非不存在的 `timestamp` 字段。
- 本会话未读取或输出请求/响应正文、MCP 或 Shell 参数、页面内容、完整 ID、Cookie、认证头或凭据。
- 这轮没有代码改动；本提交仅记录经只读抓包核验的覆盖范围和未触发分支。

## Round 17 - 安装版兼容适配与生命周期投影

| ID | Lens | Severity | Status | Finding | Evidence | Resolution |
| --- | --- | --- | --- | --- | --- | --- |
| V-46 | correctness | minor | fixed(r17) | 浏览器模式此前按 MCP 服务名称模糊选择，可能将不兼容服务或多个坐标型服务错误用于 ComputerUse。 | e336c6a 新增运行时 descriptor 驱动的 profile 解析。go test ./internal/computeruse ./internal/backend/forwarder -count=1 退出码 0，覆盖 IDE profile 优先、名称单独匹配拒绝、锁定/点击/解锁序列和实际 descriptor 选择。 | browser 模式仅接受完整的 cursor_ide_browser 或唯一坐标型 profile；不兼容、未连接或歧义均以稳定错误返回，不回退到 DesktopExecutor。 |
| V-47 | correctness | minor | fixed(r17) | 等待仍运行、后台化和 allowlist 的终态判断散落在执行桥分支中，后续状态展示容易偏离真正收口语义。 | 生命周期分类器只输出 kind、phase、terminal。go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder -count=1 退出码 0，覆盖 await_still_running 非终态、等待完成、后台化接受/未找到、allowlist 放行/拒绝及已观察未知结果的既有终态兼容。 | ApplyExecClientMessage 保留现有 payload 与 ToolCall 构造，只复用分类器的终态判断；分类器不读取或返回 agent ID、tool call ID、参数、错误正文或转录路径。 |
| V-48 | runtime-evidence | minor | open | 静态安装扫描和单元测试不能证明当前 Cursor 版本已经真实下发后台化、等待或 ComputerUse oneof。 | Round 16 的实际隔离时间线中 force_background_subagent_args/result、subagent_await_args/result、computer_use_args/result 均为 0；本轮没有伪造协议消息。 | 用户在隔离 Cursor 中自然触发对应功能后，继续只读核验上下行 oneof、终态和安全索引；在此之前仅声明本地映射已测试。 |

本轮证据：

- 安装版只读扫描已在 D:\cursor 的本地副本完成，报告只保留版本、能力 marker 和不可逆安装根哈希；不修改安装、登录态或运行进程。
- 当前隔离 worktree 缺少被 .gitignore 排除的 protobuf 生成目录，已按项目 build/Taskfile.yml 的现有 protoc 命令仅在本地再生；gen/ 保持 ignored，未暂存或提交。
- 以上为静态能力、单元和定向集成验证，不替代真实 Cursor E2E；临时抓包、凭据、Cookie、Token、URL、正文和 MCP 参数均未写入本轮提交。
