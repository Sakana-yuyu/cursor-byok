# Research: backend-capability-ui-discovery

## Practices
- 以现有路由、设置分类和 `clientApi` 包装层作为能力入口，而不是直接在页面调用 Wails bindings：现有操作已经统一承接桌面端与浏览器预览 mock、错误归一化和运行时健康上报 | 复用后可避免新增页面在预览或桌面故障时产生不一致行为。
- 将用户主动发起的修复、配置和账户操作放在有状态说明、确认与结果反馈的页面流程中；不要把高影响操作作为静默初始化副作用 | 当前代理、证书、终端环境和工作区 MCP 都按显式按钮、状态和部分确认交互呈现，适合作为新增入口的交互基准。
- 先做按价值和风险筛选的能力清单，再分批补齐界面，不将全部内部 RPC 自动暴露 | 静态对照发现部分 ProxyService 方法没有前端调用点，但其中含授权、设备绑定和 Cursor 配置改写，全部暴露会扩大误操作面。

## Constraints
- 项目已有 Vue 3/Wails 前端、路由级视图、设置分类、`clientApi` 服务层与浏览器预览 mock；新入口需复用这些边界 | 绕过服务层会失去桌面/预览分流、运行时故障记录和统一错误呈现。
- 模型、供应商、诊断、终端环境、Skills/MCP、历史和委派已有页面或设置入口；本次研究不把已有功能重复实现为平行操作台 | 重复入口会使配置来源、操作结果和用户认知分裂。
- 静态调用对照显示 `ActivateLicense`、`BindLicenseDevice`、`SwitchLicenseDevice`、`QueryUsageRecords`、`ApplyCursorSettings`、`ClearCursorSettings`、`GetDeviceID` 等 binding 在 `frontend/src` 中尚无调用点；`PrepareCursorLaunch`、`SetBaseURL`、`MarkCAIncomplete`、`ShutdownForQuit` 属于生命周期或内部协调语义 | 未调用不等于应该公开；需先按用户价值、权限和副作用筛选，避免把内部生命周期方法错误变成用户按钮。
- `ApplyCursorSettings` 与 `ClearCursorSettings` 会改变 Cursor 配置，设备/授权相关操作会影响账户状态；任何后续界面必须在执行前说明对象、影响和恢复路径，并以用户显式确认触发 | 否则可能改变用户本地开发环境或授权绑定且难以追溯。
- 当前 CI 定义会执行前端 lint、build、Playwright 和 Go build/vet/test；Playwright 在 browser-preview mock 中运行，不会验证真实 Wails 绑定或实际 Cursor 配置改变 | 后续交付需分别报告静态/浏览器预览验证与桌面端真实操作验证，不能将 mock 通过等同于真实修改已验证。
- 本仓库的 `IMPROVEMENT_TASKS.md` 写有“不写任何测试”，但当前 package scripts 与 CI 已存在单元和 E2E 测试 | 若进入实现，须先以用户/项目优先级确认是仅运行现有测试，还是允许为新增界面补充覆盖，不能自行扩大测试范围。
- 抓包/镜像记录后端已完整接线，但 `frontend/src` 当前没有 `mirrorCapture`、`official.raw.jsonl` 或“镜像记录官方请求”的调用与界面引用；它通过 MITM 对默认 OpenAI、Anthropic、Gemini 官方域名解密后旁路记录请求和响应，写入 `history/_debug/mirror/official.raw.jsonl`，默认关闭且可热加载 | 它应被视为尚无界面的高敏感调试能力，而不是已有的“请求明细”功能。
- 镜像记录会脱敏常见鉴权头，并对请求体和响应体分别限制为 128 KiB 与 1 MiB，但记录内容仍可能包含用户提示词、模型回复和业务上下文 | 后续若提供开关或查看入口，必须以调试用途、数据位置和本地敏感信息风险明确告知，且不得默认启用或将原文直接暴露到常规统计页面。
- 进一步核对发现 `ActivateLicense`、`BindLicenseDevice` 与 `SwitchLicenseDevice` 在本地客户端明确返回“已移除”，`QueryUsageRecords` 固定返回 `UNSUPPORTED`；这些 binding 是兼容残留而非可补齐界面的可用能力 | 为它们新增前端入口只会生成必然失败的操作，必须从首批候选排除。
- 镜像记录是否能写入取决于本地服务、MITM 监听、Cursor 代理设置和开关同时满足；`ProxyState` 已给出前三项，记录文件本身可通过元数据确认，但前端当前没有组合展示 | 仅显示总服务状态会把“服务运行”误解为“抓包已就绪”，从而无法定位 Cursor 未接入或尚未发生官方请求。
- `official.raw.jsonl` 不是会话调试文件白名单的一部分，现有会话详情读取器不能安全地作为镜像记录查看器复用 | 将正文接入详情页会扩大提示词和响应在应用内的可见面；本阶段只能返回固定路径、存在性、大小和最后修改时间。
- `WindowService` 已有跨平台目录打开能力，但只公开日志根目录 | 为镜像目录提供独立且显式的打开入口，能让用户用本地工具查看后续抓包文件，又不要求前端读取或传输任何正文。
- `EnableReaderMCP` 虽仍保留 Wails binding 和浏览器预览 mock，但保存已启用的视觉委派配置时会自动同步同一识图模型的网关、密钥、模型和协议端点至 `~/.cursor/mcp.json`；视觉委派面板已向用户说明该自动同步和 MCP 兜底 | 另设手动入口会以默认 `/v1/chat/completions` 重写全局 Cursor MCP 配置，既与当前委派端点保持一致的自动路径重复，也会扩大误操作和密钥写入风险。
- `ClearLastError` 仅清空进程内 `ProxyState.lastError` 并发送状态事件；成功启动、停止或 CA 重载已自行清除此状态，首页错误横幅同时提供启动、修复代理和修复 CA 等实际恢复动作 | 单独提供“清除错误”只能隐藏仍未解决的故障，不增加诊断或恢复能力，且服务重启后也不应将该错误作为持久状态保留。
- 镜像记录对 `Authorization` 等 HTTP 头已脱敏，但请求记录的 `url` 直接来自 `req.URL.String()`；Gemini 等官方接口可在查询参数中传递 `key`，因此现有头部脱敏不能覆盖 URL 凭据 | 本地调试 JSONL 不能保存 API key、token、签名或其他凭据型查询参数的明文；保留路径与非敏感查询项即可支持端点和协议形态对比。

## Open [TBD]
- (无开放决策。)

## Decided
- [DEC-1] 不进行外部行业调研 | decided from status quo: 本变更只涉及当前 Vue/Wails 前端对本仓库既有服务能力的发现、分层和交互入口，不引入新依赖或外部平台规则。
- [DEC-2] 以“后端 binding 与 frontend/src 调用点的静态差异”作为研究起点，而不是把所有后端函数视为缺界面 | decided from status quo: 已有大量服务通过 `clientApi`、设置页和路由暴露，未调用项中也包含只应由生命周期协调的内部方法。
- [DEC-3] 首批仅补经能力审计确认、终端用户需要且可安全确认的授权/设备、Cursor 配置与用量入口；内部生命周期方法继续不公开 | source [TBD-1] | rationale: 用户选择 A，以可控范围优先验证核心工作流并限制误操作面。
- [DEC-4] 镜像记录抓包能力首批仅提供高级设置中的默认关闭开关、记录位置与敏感内容警告；不做原文查看或对比页 | source [TBD-4] | rationale: 用户选择 A，保留可用的调试入口，同时避免将可能含提示词和模型回复的记录扩大为应用内常规可见数据。
- [DEC-5] 首次执行和高影响的 Cursor 配置或账号绑定操作均要求明确确认，并展示影响与恢复步骤；低风险只读操作不增加确认 | source [TBD-2] | rationale: 用户选择 A，在保留日常可用性的同时，为本地配置与账户状态修改建立可追溯的误操作防护。
- [DEC-6] 先完成可发现性与低风险只读能力，再分批接入经确认保护的高影响写操作；每一步独立提交 | source [TBD-3] | rationale: 用户选择 A 并要求一步一提交，以缩小验证和回滚范围。
- [DEC-7] 首批不为已移除的授权/设备操作或不支持的用量查询新增 UI；先实施已完整接线的镜像记录调试开关 | decided from status quo: 对应 client 方法固定返回移除或不支持状态，镜像记录具备后端配置、热加载、MITM 记录与脱敏/截断实现，但无前端入口。
- [DEC-8] 为镜像抓包补充只读“就绪与命中”状态和独立打开记录目录入口，不提供正文读取、导出或应用内浏览 | source R-2 | escalated | 用户需要后续以抓包完成 Cursor 对接；状态必须能区分未启用、服务未就绪、Cursor 未接入、等待官方请求和已产生记录，同时维持原始记录的本地敏感数据边界 | if wrong: 仅增加一个设置区块与只读 binding，可回退本阶段提交恢复原开关行为。
- [DEC-9] 为官方镜像 JSONL 增加本地 `exchangeId`、`phase` 与尽力提取的 `model` 字段 | source [TBD-5] | rationale: 用户选择“全部使用推荐”；请求、响应起始、响应片段和截断标记必须以同一事务 ID 稳定关联，模型字段方便后续与 `provider.jsonl` 检索和对比；关联数据只落入本地 JSONL，不添加官方请求头，也不进入前端状态。
- [DEC-10] 不为 `EnableReaderMCP` 增加独立的手动 UI 入口 | decided from status quo: `SaveDelegationConfig` 会在视觉委派启用且识图模型有效时自动调用 `syncVisionReaderFromDelegation`，以该模型实际的网关、密钥和请求端点更新 `vision-reader`；设置页已将这项同步与回退能力告知用户。独立入口会重复写入 `~/.cursor/mcp.json`，并可能用默认端点覆盖与视觉委派不一致的配置。
- [DEC-11] 不为 `ClearLastError` 增加 UI 入口 | decided from status quo: 它只清空瞬态错误文本；成功的启动、停止和 CA 重载已自动清除该状态，首页现有启动、代理修复与 CA 修复路径才会处理根因。
- [DEC-12] 镜像 JSONL 的请求 URL 必须脱敏凭据型查询参数 | decided from status quo: `mirrorRecord.URL` 当前直接使用请求 URL，而仅 headers 经过脱敏。记录器将保持 URL 路径与非敏感参数，以支持对比；`key`、API key、token、secret、签名与密码等参数的值统一替换为 `[REDACTED]`，不修改实际直通请求。
