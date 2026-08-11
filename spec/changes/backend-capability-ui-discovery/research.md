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

## Open [TBD]
(无开放决策。)

## Decided
- [DEC-1] 不进行外部行业调研 | decided from status quo: 本变更只涉及当前 Vue/Wails 前端对本仓库既有服务能力的发现、分层和交互入口，不引入新依赖或外部平台规则。
- [DEC-2] 以“后端 binding 与 frontend/src 调用点的静态差异”作为研究起点，而不是把所有后端函数视为缺界面 | decided from status quo: 已有大量服务通过 `clientApi`、设置页和路由暴露，未调用项中也包含只应由生命周期协调的内部方法。
- [DEC-3] 首批仅补经能力审计确认、终端用户需要且可安全确认的授权/设备、Cursor 配置与用量入口；内部生命周期方法继续不公开 | source [TBD-1] | rationale: 用户选择 A，以可控范围优先验证核心工作流并限制误操作面。
- [DEC-4] 镜像记录抓包能力首批仅提供高级设置中的默认关闭开关、记录位置与敏感内容警告；不做原文查看或对比页 | source [TBD-4] | rationale: 用户选择 A，保留可用的调试入口，同时避免将可能含提示词和模型回复的记录扩大为应用内常规可见数据。
- [DEC-5] 首次执行和高影响的 Cursor 配置或账号绑定操作均要求明确确认，并展示影响与恢复步骤；低风险只读操作不增加确认 | source [TBD-2] | rationale: 用户选择 A，在保留日常可用性的同时，为本地配置与账户状态修改建立可追溯的误操作防护。
- [DEC-6] 先完成可发现性与低风险只读能力，再分批接入经确认保护的高影响写操作；每一步独立提交 | source [TBD-3] | rationale: 用户选择 A 并要求一步一提交，以缩小验证和回滚范围。
