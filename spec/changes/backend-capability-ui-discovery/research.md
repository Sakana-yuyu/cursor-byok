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
- `routing.mode` 目前只在保存配置、启动前检查和前端状态中被消费，未进入 MITM 的请求分流；已运行的服务会无条件写入 Cursor `http.proxy`，并把 `*.cursor.sh` 请求转发给嵌入式 backend | 因此 `upstream` 恢复官方登录态后，不能保证 Cursor 绕过本地 MITM；页面的“绕过本地代理服务”表述与实际运行链路不一致，也使镜像抓包的就绪状态缺少运行模式语义。
- 现有 `cmd/isolated-cursor-e2e` 会为进程及其 Cursor 子进程创建临时 HOME、APPDATA、LOCALAPPDATA、CA 和 loopback 端口，隔离代理设置与登录状态写入；但它以空 `historyRoot` 和 `nil` mirror 配置创建 MITM，并在源码中明确声明“不写入真实请求镜像” | 该入口可证明隔离代理启动边界，不能证明 `official.raw.jsonl` 的真实传输链路。直接启动隔离 Cursor 仍可能由用户操作触发官方网络请求，因此在“不调用官方 API”的本阶段不运行它。
- 对 Wails binding 与 `frontend/src` 的可复现静态对照显示，4 个已注册服务共有 117 个公开方法，其中 99 个已有前端源码调用；余下 18 个由 12 个 ProxyService 管理/兼容接口和 6 个 WindowService 内部装配接口组成 | 不能只以“binding 未调用”认定缺少界面，必须继续核对其实际副作用、调用者和已有工作流。
- 12 个未引用的 ProxyService 方法中，授权激活/设备绑定/设备切换固定返回“已移除”，用量记录固定返回 `UNSUPPORTED`；`SetBaseURL` 固定报不支持；`MarkCAIncomplete`、`PrepareCursorLaunch`、`ShutdownForQuit` 分别由应用启动、Cursor 启动预检和退出钩子调用；`ApplyCursorSettings`、`ClearCursorSettings` 会直接写入或清理 CA、系统 `NODE_EXTRA_CA_CERTS`、Cursor 代理及登录态；`ClearLastError` 只隐藏瞬态错误；`GetDeviceID` 仅供广告服务内部标识 | 这些方法不构成面向用户的安全 UI 缺口；已有启动、修复代理、修复 CA、停止服务和账户工作流承接可见操作。
- 6 个未引用的 WindowService 方法中，`GetMainWindowCloseAction` 由 `runner.go` 主窗口关闭 hook 消费；`SetApp`、`SetMainWindow`、`SetUpdater`、`SetCursorLaunchPreflight` 与 `SetLocale` 分别由应用装配和 `locale:changed` 事件设置 | 它们是原生窗口运行时依赖注入，不应暴露为浏览器可触发的用户动作。
- 从 `clientApi.js` 与 `runtimeControlApi.js` 再向视图、组件、布局及状态层对照，100 个服务层导出中 97 个有消费者；剩余 `invokeOperation` 是 runtimeControlApi 复用的通用执行器，`getDelegationConfig` 被总配置加载路径覆盖，`updateStatsOverlayWindow` 由 `updateStatsOverlayLayout` 间接调用 | 两层对照均未发现“后端可用、低风险、面向用户且没有前端入口”的能力；本阶段不应为了数字覆盖率新增平行入口。
- 隔离镜像验收实际启动后，临时 Cursor 的 `network-shared.log` 对 `https://api2.cursor.sh/extensions-control` 报 `net::ERR_CERT_AUTHORITY_INVALID`；启动器已写入临时 `http.proxy` 和 `NODE_EXTRA_CA_CERTS`，但 Cursor 的 Chromium 网络栈并不以 Node 环境变量信任该 CA | 未信任临时 CA 时，Cursor 官方 relay 不能稳定经 MITM，且默认镜像 hosts 只包含 OpenAI、Anthropic、Gemini，不足以捕获 Cursor 官方协议。
- Chromium 支持 `--ignore-certificate-errors-spki-list=<base64 SHA-256 SPKI>`，可只信任本次临时 CA 的公钥而不是全局忽略 TLS 错误；临时镜像配置可显式将 `api2.cursor.sh`、`api3.cursor.sh` 加入 hosts，复用现有 `isWhitelistedRelayHost` 的已知官方 relay 范围 | 此参数和 hosts 仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 的临时 Cursor 子进程生效，不写入系统证书库、真实 Cursor 设置或默认镜像配置；它使后续对接采集覆盖 Cursor relay 与原有三类官方模型 API。
- 本机实际安装 Cursor 的 bundle 同时包含 `https://api2.cursor.sh`、`https://api3.cursor.sh` 和 `https://api4.cursor.sh`；`isWhitelistedRelayHost` 已将所有 `*.cursor.sh` 纳入 MITM 解密，但隔离镜像配置仅记录显式 hosts | 若不把 `api4.cursor.sh` 加入临时镜像 hosts，经过 MITM 的该 relay 流量不会写入隔离 JSONL，导致后续对接所需协议采集不完整。
- 用户启用 Multitask 后，隔离 JSONL 出现多条互相重叠的 `RunSSE` 长连接及大量 `BidiAppend` 上行，均使用 `application/connect+proto` 或 `application/proto`；现有记录器却把原始 bytes 直接转换为字符串写入 JSON | 非 UTF-8 protobuf 字节会在 JSON 编码时替换，底层 `Read()` 的 chunk 也不等于 Connect/SSE 消息边界。现有 `BidiAppendRequest`、`AgentClientMessage` 和 `AGENT_MODE_MULTITASK` 的 protobuf 定义及解码器均已存在，但无法可靠消费已损坏的镜像载荷。
- `BidiAppendRequest` 同时承载外层 `request_id`、`append_seqno` 与内层 `data_binary` Agent 消息；Multitask 的模式、子代理创建/后台化/完成/取消等状态分布在该内层消息和 `RunSSE` 的服务端消息中 | 仅记录 URL、状态和底层分块数量无法还原父子任务关系、事件顺序或可回放格式。
- 当前真实隔离镜像已捕获 `BidiAppend` 上行与 `RunSSE` 下行原始字节：上行可提取 `exec_client_message`、`exec_client_control_message`、`kv_client_message`、心跳等客户端 oneof；下行响应头为 `text/event-stream`，但帧载荷满足 Connect 的 5 字节帧头格式 | `newMirrorRunSSEFrameDecoder` 仅依据响应头选择 SSE 解码，因而把可重组的 Connect 二进制帧记为 `runsse_sse` 与 `sse_server_message_unavailable`，原始 Base64 帧仍保真且未丢失。
- `AgentServerMessage` 的顶层 oneof 已定义 `interaction_update`、`exec_server_message`、`exec_server_control_message`、`conversation_checkpoint_update`、`kv_server_message` 与 `interaction_query`；`ExecServerMessage` 又定义工具和子代理 oneof，包括 `subagent_args`、`force_background_subagent_args`、`subagent_await_args` | 仅记录顶层服务端 oneof 无法判定 Multitask 的创建、后台化与等待关系，需在不展开正文的前提下建立二层结构索引。

## Bidirectional Protocol Indexing Design

### Goal and boundary
- 仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 的隔离镜像模式中，将真实 Cursor 上下行协议记录为“原始字节保真层 + 安全结构索引层”；普通镜像模式、前端正文边界、已安装 Cursor、真实用户配置和官方请求转发均保持不变。
- `official.raw.jsonl` 保留现有的 Base64、长度和 SHA-256 原始载荷/帧记录，确保离线按字节复核请求格式和响应帧；`protocol.timeline.jsonl` 只保存关联、方向、顺序、协议类型、结构类型、长度、哈希、终态和可解释的解码错误。
- 索引层不得写入 prompt、代码、工具参数、工作区路径、模型输出、思考文本、token、Cookie、认证头、完整 request ID 或完整 protobuf JSON；流式内容最多记录类别、增量字节数、增量 SHA-256 和完成状态。

### Upstream indexing
- 对 `BidiAppendRequest` 保留 `requestIdHash` 与 `appendSeqno`，并记录 `data` 或 `dataBinary` 的来源、字节长度和 SHA-256；继续用仓库既有 protobuf 解码器读取内嵌 `AgentClientMessage`。
- 将客户端顶层 oneof 写入 `clientMessageKind`；对 `run_request`、`exec_client_message`、`exec_client_control_message`、`kv_client_message`、conversation action 与 interaction 类消息，增加只包含 oneof 名称的细分字段。
- `agentMode`、`multitask` 和已有子代理动作摘要继续保留；对创建、后台化、完成、取消与等待等客户端可判定动作使用固定安全枚举，不展开子代理名称、提示词或参数。
- 解码失败、压缩不支持或原始载荷截断时仍写入原始保真记录，并在索引层写入稳定错误码；绝不阻塞官方直通。

### Downstream detection and indexing
- 对 `application/connect+proto` 响应直接按 Connect 重组；对 `text/event-stream` 响应先缓冲至少 5 个字节，再以合法 flags、受上限约束的 Big Endian 长度和完整帧可用性判断是否为 Connect。判定成功后回放缓冲区并持续按 Connect 重组；判定失败才按文本 SSE 的空行边界解析。
- Connect 解析记录帧序号、flags、压缩标识、原始帧长度、SHA-256、终态和解码错误；`flags & 0x02 != 0` 统一表示 `connect_end_stream`。普通 SSE 继续只提供边界与终态摘要，不假装为 protobuf。
- 对可解码的 `AgentServerMessage`，记录顶层 `serverMessageKind`；当其为 `exec_server_message` 时，再通过 protobuf reflection 记录 `ExecServerMessage` 的实际 oneof 作为 `execMessageKind`。
- `subagent_args`、`force_background_subagent_args` 和 `subagent_await_args` 作为显式子代理事件摘要，便于重建 Multitask 的创建、后台化和等待时序；`interaction_update` 的流式文本或思考仅记录其类型、字节摘要和完成状态。

### Compatibility, verification and rollback
- 记录结构只新增可选字段，旧 JSONL 仍保持可读；真实响应 tee 仅观察字节副本，解码器的任何失败都不能修改 Cursor 接收到的响应。
- 实现后先运行现有定向 Go 测试、构建、vet 与 `git diff --check`；项目约束为“不写任何测试”，因此不新增测试文件。
- 用户明确授权后才启动新的隔离 Cursor 实例进行真实 E2E：验收 `runsse_connect`、服务端顶层/Exec 内层类型、Multitask 子代理摘要、原始帧 Base64 可回放，以及时间线不含受限正文或凭据。
- 回退对应实现提交即可恢复当前按响应头选择的行为；临时隔离记录目录可整体删除，不影响真实 Cursor 登录库、配置、证书库或已安装客户端。

## Multitask Result and Interaction Coverage Design

### Confirmed status and scope
- 当前索引已通过通用 protobuf reflection 记录 `interaction_query` 的 `query` oneof 到服务端 `serverDetailKind`，并记录 `interaction_response` 的 `result` oneof 到客户端 `clientDetailKind`；本轮不重复新增同义字段，而是以真实交互确认这些字段可被触发和按 requestIdHash 关联。
- 当前客户端 `exec_client_message` 只能记录外层 oneof，例如 `subagent_result`、`force_background_subagent_result`、`subagent_await_result`；无法区分子代理成功/错误、后台化接受/未找到，或等待完成/仍在运行/未找到/错误。该二层结果是本轮唯一代码索引缺口。

### Safe result indexing
- 为 `mirrorProtocol` 与 `mirrorTimelineRecord` 新增可选 `clientResultKind`；仅当客户端顶层为 `exec_client_message`，且其实际 result 为 `subagent_result`、`force_background_subagent_result` 或 `subagent_await_result` 时填写。其他工具 result 继续只写当前 `clientDetailKind`，避免把范围扩大为所有工具结果的递归展开。
- `subagent_result` 使用其 `result` oneof，写入 `success` 或 `error`；`subagent_await_result` 使用其 `result` oneof，写入 `complete`、`still_running`、`not_found` 或 `error`；`force_background_subagent_result` 不读取业务字段，仅把枚举映射为 `accepted`、`not_found` 或 `unspecified`。
- 不记录 agent ID、tool call ID、transcript path、final message、error text、status 原始数字或任何子代理参数。nil/未知结果保留空字段，绝不影响上游转发。

### Minimal real-operation matrix
- Multitask：用户在现有隔离实例中分别执行一个可后台化的长子任务、一次等待该子任务结果的后续操作，以及一次取消或错误收口；采集时只核对 `force_background_subagent_args/result`、`subagent_await_args/result`、`subagent_result` 与相同 requestIdHash 的相邻事件，不读取任务内容。
- Interaction：用户让 Cursor 在执行前主动询问一个选择或确认，并实际选择一次；必要时触发模式切换或反馈。采集时只核对 `interaction_query`/`interaction_response` 的二层 oneof、方向、顺序和 requestIdHash 关联，不读取问题、选项或回答。
- 每个矩阵格只有在对应 oneof 真实出现且无异常解码错误时才标为验证；模型未选择该协议路径、用户界面未给出对应动作或会话提前结束均保持“未触发验证”，不视为解析失败。

## Open [TBD]

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
- [DEC-13] 将 `upstream` 的实现与界面语义统一为“业务请求回官方上游，但在服务运行且抓包开启时仍可经本地 MITM 镜像”，而不再承诺无条件绕过本地代理 | decided from status quo: `PrepareCursorLaunch` 在 upstream 模式不强制启动服务，但 `StartProxy` 不读取 routing mode，仍启动 MITM 并调用 `ApplyCursorSettings`；MITM 对 `*.cursor.sh` 无条件转 backend、对镜像域名无条件旁路直通。后续实现需让请求分流显式读取运行配置，并让状态/文案区分“未启动本地服务的官方直连”与“经本地 MITM 的官方上游镜像”。
- [DEC-14] 本阶段不运行 `cmd/isolated-cursor-e2e` 作为镜像抓包验收，也不为此新建临时测试工具 | decided from status quo: 该启动器隔离用户目录是安全的，但代码显式以空 `historyRoot` 和 `nil` mirror 配置禁用镜像记录，运行 Cursor 后仍需人工触发请求，无法在“不调用官方 API”的边界内证明 `official.raw.jsonl`。继续沿用已有单元/构建/浏览器预览证据，并将真实 Cursor 抓包保留为用户可控的最终人工验收。
- [DEC-15] 为隔离 E2E 启动器增加默认关闭、仅由 `CURSOR_E2E_MIRROR_CAPTURE=1` 显式启用的官方上游镜像模式 | source: 用户确认“进入下一步”并要求后续抓包完成 Cursor 对接；默认模式继续传入空 `historyRoot` 与 `nil` 镜像配置，维持仅验证代理链路的旧行为。启用后将记录写入临时根目录的 `history/_debug/mirror/official.raw.jsonl`，强制隔离配置为 `routing.mode=upstream` 与 `mirrorCapture.enabled=true`，并跳过本地伪账号注入，让用户自行在隔离 Cursor 中登录和发起官方请求 | reversibility: 取消环境变量或回退该启动器提交即可恢复原隔离代理模式；记录文件与 Cursor 配置均留在临时根目录，不修改真实用户目录。
- [DEC-16] 不为本轮差异审计中未引用的 18 个 binding 新增前端入口 | decided from status quo: 12 个 ProxyService 方法为已移除/不支持接口、生命周期协调、内部标识，或会裸改 Cursor/系统配置；6 个 WindowService 方法用于原生运行时装配。服务包装层 100 个导出已有 97 个被 UI/状态层消费，余下 3 个分别为内部执行器、被总配置读取覆盖的委派子树读取和由布局包装函数间接复用的浮窗更新；不存在低风险且面向用户的真实缺口。
- [DEC-17] 隔离镜像验收模式额外记录 `api2.cursor.sh`、`api3.cursor.sh` 与 `api4.cursor.sh`，并仅以本次临时 CA 的 SPKI 指纹启动临时 Cursor | decided from runtime evidence: 当前隔离 Cursor 在正确写入临时 `http.proxy` 后仍因 Chromium 不信任临时 CA 报 `ERR_CERT_AUTHORITY_INVALID`；仅捕获三类直连模型域名也无法采集 Cursor 官方 relay 协议。实际安装的客户端 bundle 还引用 `api4.cursor.sh`，而未列入镜像 hosts 的 relay 流量不会落盘。SPKI 白名单比全局 `--ignore-certificate-errors` 更窄，且环境变量未启用时不改变现有隔离或普通运行模式。
- [DEC-18] 为隔离 E2E 的显式镜像模式增加协议保真记录与 Multitask 摘要 | source: 用户选择 A，目标是采集所有调用格式以还原 Cursor 功能；仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 时保存带编码声明、长度和摘要的原始协议字节，复用仓库 protobuf 定义解析 `BidiAppend` 与 Agent 流。凭据型 URL 参数、认证头和 Cookie 继续脱敏，普通镜像模式和前端正文边界不变 | reversibility: 回退本阶段提交即可移除隔离记录中的保真字段和摘要；临时记录目录可整体删除，不影响真实 Cursor 配置、系统证书库或已安装客户端。
- [DEC-19] 隔离 E2E 的显式镜像模式启动时，只读导入真实 Cursor 的最小登录态 | source: 用户选择 A 并确认设计，目标是每次新建临时隔离实例时免除重复登录。启动器必须在替换进程 `APPDATA` 前定位真实 `%APPDATA%\Cursor\User\globalStorage\state.vscdb`，仅以 SQLite 只读方式读取 `cursorAuth/accessToken`、`cursorAuth/refreshToken` 和 `cursorAuth/cachedEmail`，然后在本次临时根目录创建只含这三项的最小状态库。不得复制约 1.15 GB 的真实状态库、Cookie、缓存、扩展、工作区、历史或其他 `globalStorage` 项；不得复用会修改 Statsig 开关的模拟账号注入函数；不得写回真实库或向日志、JSONL、Git、终端输出 token、邮箱或完整凭据。真实库忙锁、缺项或读取失败时继续启动并降级为手动登录，只输出不含敏感数据的状态摘要 | verification: 运行既有相关 Go 测试、构建、`go vet` 与 `git diff --check`；在用户授权启动新隔离 Cursor 后，确认无需重新登录，同时核对真实状态库未被修改 | reversibility: 回退该导入实现提交即可恢复每次手动登录；临时凭据仅留在该次隔离根目录，可随目录整体删除。
- [DEC-20] 隔离镜像的上下行采用“原始字节保真 + 全部已知协议结构化索引” | source: 用户确认 A 并批准设计；`official.raw.jsonl` 保存上下行 Base64、长度和 SHA-256，`protocol.timeline.jsonl` 保存 Bidi/RunSSE 的方向、顺序、顶层与 Exec 内层 oneof、Multitask 子代理事件、终态和解码错误。对 `text/event-stream` 先探测合法 Connect 帧再降级 SSE，以修复真实 Cursor 下行协议被错误分类的问题；不重复存储 prompt、模型输出、token、Cookie、完整 ID 或 protobuf JSON | verification: 定向 Go 检查后，经用户授权启动新的隔离实例，确认出现 `runsse_connect`、服务端结构类型与子代理摘要，且原始帧仍可按 Base64 复核 | reversibility: 回退后续解析实现提交即可恢复当前行为；已产生的隔离临时记录可整体删除，不影响真实 Cursor 或官方直通。
- [DEC-21] 下一轮真实协议覆盖同时优先 Multitask 生命周期与交互/确认协议 | source: 用户选择 AB；在现有隔离 Cursor 实例中，分别采集子代理后台化、等待、取消、结果回传，以及 `interaction_query`、用户确认、反馈和模式切换等事件。每项只在对应 oneof 与关联闭环真实出现时标记已验证；提示词、响应正文、工具参数、完整 ID、Cookie 和凭据仍不进入时间线或验证台账 | verification: 先设计最小用户操作矩阵，再对 `protocol.timeline.jsonl` 的结构字段、事件顺序、requestIdHash 关联、终态和敏感字段缺失做只读核验；未触发项保持未验证 | reversibility: 本阶段只新增隔离环境下的采集/索引字段与验证台账，回退对应提交即可恢复当前解析行为，临时目录可整体删除。
