# Research: cursor-multi-account-management

## Practices
- 在现有 `cursoraccount.Manager` 上增加账户库和当前指针，继续由同一授权提供者服务 Plugins、Skills、MCP 等控制面请求 | 可复用现有 PKCE、资料补全、刷新锁和 `Authorization(ctx)`，避免产生第二套认证生命周期。
- 将“助手当前账户”和“Cursor 客户端登录态”作为两个独立状态；普通选择只更新控制面授权，真实客户端切换采用准备、确认、执行的两阶段操作 | 可防止登录或选择账户时静默关闭 Cursor、覆盖用户正在使用的官方账户。
- 对真实 Cursor 只读写 `cursorAuth/accessToken`、`cursorAuth/refreshToken`、`cursorAuth/cachedEmail` 白名单，切换前创建本次操作专属的 `state.vscdb`、`-wal`、`-shm` 文件备份并读回校验 | 现有 SQLite 能力可复用，但现有模拟账号同步会改 Statsig，现有 JSON 备份也不足以支持文件级恢复。
- Wails 与浏览器预览只传账户摘要、操作阶段和稳定错误码；token、Cookie、完整认证 JSON、数据库路径和备份正文始终留在后端 | 可沿用 `clientApi` 的错误归一化与 mock 测试，同时避免敏感凭据进入 DOM、控制台或截图。
- 旧 `cursor-account.json` 采用完成式迁移：新账户文件、索引和当前指针全部落盘成功后才移动为 legacy 备份；原单账户接口保留一个发布周期并适配当前账户 | 旧用户升级失败时仍能由旧版本读取原凭据，回退不会随机选择多账户目录中的账户。

## Constraints
- 当前实现只有一个 `cursor-account.json` 和一个内存 `credentials`，`Manager` 同时负责登录、刷新、持久化和控制面授权 | 多账户改造必须保持 `Authorization(ctx)` 的现有消费者不变，否则 Plugins、Skills、MCP 等控制面能力会回归。
- 当前登录成功、自动刷新和主动断开均围绕单份凭据；刷新请求完成时用户可能已切换当前账户 | 刷新持久化必须带原账户 ID 校验，旧账户的晚到刷新结果不得覆盖新当前账户。
- `state.vscdb` 已有跨平台定位、`modernc.org/sqlite`、2 秒 busy timeout、只读导入和事务写入；`syncCursorAuthStateDB` 同时修改 Statsig gates | 多账户切换需要新的纯认证白名单 API，不能直接复用模拟账号注入入口。
- 当前 `cursor-auth-backup.json` 只保存三个认证键，并由恢复官方登录态流程共享 | 它不能作为账户切换事务的唯一回滚证据；每次执行必须有独立 operation manifest 和数据库/WAL/SHM 文件备份。
- 项目已有 `RestartCursor`、跨平台关闭/启动 helper 和进程探测，但 `RestartCursor` 是一次性“关闭后立即启动” | 切换编排需要复用其窄能力或抽取运行时接口，在关闭与启动之间插入备份、写入、校验和失败恢复。
- 准备阶段若产生备份或关闭进程，用户仅打开确认框也会改变本机状态 | `PrepareCursorClientAccountSwitch` 必须只做校验、进程探测和影响摘要，不创建文件、不写数据库、不结束进程。
- `.gitignore` 仅覆盖通用 `*.db`、`*.sqlite` 和压缩包，没有明确覆盖 `state.vscdb*`、账户库 JSON、切换备份目录或恢复包 JSON | 实现前必须增加精确忽略规则，并在每次提交前检查暂存清单，避免凭据或真实数据库进入 Git。
- `IMPROVEMENT_TASKS.md` 写有“不写任何测试”，但更高优先级的当前用户说明要求验证，仓库 CI 也实际运行 `go test ./...`，前端提供 unit、Playwright、lint、build 和 i18n 扫描 | 本变更按当前用户指令和 CI 补充定向测试；该偏离只影响本变更，回滚为删除新增测试与实现提交，但不允许以旧文档跳过高风险恢复验证。
- 浏览器预览 mock 可验证列表、确认、错误反馈与布局，不能证明真实 Wails 绑定、Cursor 进程关闭或 `state.vscdb` 切换成功 | 交付必须分别标注 Go/构建、browser-preview、桌面绑定和经用户再次授权的真实 Cursor E2E；未经授权不得关闭用户当前 Cursor。
- 新 Wails 方法需要重新生成 bindings；四个 locale 和生成目录已有用户未提交改动 | 实现时必须与现有改动合并，不能覆盖、重置或把无关 i18n 变化混入账户主题提交。
- CI 当前实际执行 `go build ./...`、`go vet ./...`、`go test ./...`，未发现 `skipTests`；构建工作流可产出桌面包，但 push、tag、Release 均需额外授权 | 本变更可建立本地和 CI 级验证证据，不能把本地通过称为已发布。
- 本变更不引入新依赖或外部支付/平台规则，决策空间来自仓库现状与已确认设计 | 不进行外部行业调研；若后续接入 Cursor 新官方 API，再单独读取其完整官方文档。

## Open [TBD]


## Decided
- [DEC-1] 不进行外部行业调研 | decided from status quo: 无新依赖、外部协议选型或支付渠道规则，关键约束全部在现有 Go/Wails/Vue 和 Cursor 状态库实现中。
- [DEC-2] 助手当前账户与 Cursor 客户端登录态严格分离 | source: 已确认 design | rationale: 普通控制面选择不得关闭 Cursor 或覆盖客户端官方账户。
- [DEC-3] 客户端切换采用准备、确认、执行两阶段流程，准备阶段零副作用 | source: 已确认 design | rationale: 用户看到影响摘要前不得产生备份、写库或进程关闭。
- [DEC-4] 执行阶段按关闭 Cursor、文件级备份、认证白名单写入、读回校验、更新 current、重启的顺序运行，失败恢复数据库文件和旧 current | source: 已确认 design | rationale: 真实 SQLite 与进程操作必须具备可审计的反向恢复路径。
- [DEC-5] Wails DTO 只返回账户摘要和操作状态，永不返回 token、Cookie、完整认证 JSON、数据库路径或备份正文 | source: 已确认 design + status quo | rationale: 前端日志、DOM、截图和 browser-preview 不应承载可复用凭据。
- [DEC-6] 旧单账户文件完成式迁移，旧接口保留一个发布周期 | source: 已确认 design | rationale: 升级失败和版本回退均需保持可恢复，已有调用方不能在同一版本立即断裂。
- [DEC-7] 删除助手账户不隐式退出 Cursor；删除当前账户必须选择替代账户或确认清空当前指针 | source: 已确认 design | rationale: 助手账户库操作不得悄悄改变独立的客户端登录态。
- [DEC-8] 首版支持 OAuth、本机 Cursor 白名单、Token 和版本化恢复 JSON 四类导入；Token 上限 8 KiB、JSON 上限 1 MiB | source: 已确认 design | rationale: 覆盖主要本地迁移路径，同时限制异常输入与内存放大。
- [DEC-9] 实现和验证按账户库、授权/导入、切换事务、Wails/UI、最终验收分主题推进 | source: 用户要求继续推进 + 工作区约束 | rationale: 每一主题可独立测试、提交和回滚，避免 dirty worktree 中混入无关变化。
- [DEC-10] 首版允许导出包含 access token、refresh token 的版本化恢复包，但必须二次确认、限制本地文件权限、清理临时明文输入并在日志和 Wails DTO 中禁止返回凭据 | source [TBD-1] 用户选择 B | rationale: 支持跨设备迁移与灾备，同时把凭据导出明确限定为受控高风险操作；若后续发现泄露风险不可接受，可单独撤回导出能力而不影响账户库和客户端切换。
