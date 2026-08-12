# 隔离 Cursor 最小登录态导入实施计划

> **供自主代理执行：** 必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 逐任务实施本计划，并用复选框（`- [ ]`）追踪步骤。

**Goal:** 在显式隔离镜像抓包模式启动时，只读导入真实 Cursor 的最小登录态，避免用户每次新建隔离实例都手动登录。

**Architecture:** 在 `internal/cursor` 中新增职责单一的最小状态库导入 API：它从调用方传入的真实状态库绝对路径只读三项白名单键，并在调用方传入的隔离状态库路径创建最小 `ItemTable`。`cmd/isolated-cursor-e2e` 在覆盖进程环境变量前捕获真实状态库路径，仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 时调用该 API；任意导入错误均仅降级为手动登录，不阻断隔离抓包启动。

**Tech Stack:** Go 1.26、`database/sql`、`modernc.org/sqlite`、现有 Cursor 状态库与隔离 E2E 启动器。

## Global Constraints

- 不修改已安装 Cursor、真实 Cursor 配置、系统证书库或真实 `state.vscdb`。
- 仅处理 `cursorAuth/accessToken`、`cursorAuth/refreshToken`、`cursorAuth/cachedEmail`；不得复制 Cookie、缓存、扩展、工作区、历史或其他状态键。
- 仅在 `CURSOR_E2E_MIRROR_CAPTURE=1` 启用；普通隔离模式及现有模拟账号路径保持不变。
- 不在日志、JSONL、终端、测试失败信息或 Git 中输出 token、邮件、完整凭据或完整状态库内容。
- `IMPROVEMENT_TASKS.md` 约束为“不写任何测试”：不得创建或修改测试文件，只运行已有验证。
- 当前运行的隔离 Cursor 不得被停止或重启；真实 E2E 验收必须等用户明确授权后启动新的实例。
- 每个已完成任务单独提交；不得暂存 `.playwright-cli/`、`frontend/.playwright-cli/` 或 `output/`。

---

### Task 1: 最小只读登录态导入

**Files:**
- Modify: `internal/cursor/state_db.go`
- Modify: `cmd/isolated-cursor-e2e/main.go`
- Modify: `spec/changes/backend-capability-ui-discovery/research.md`（仅在实现与设计出现需要澄清的差异时）
- Test: 仅运行既有 `internal/cursor`、`internal/mitm`、`internal/backend/agent/protocol` 与 `cmd/isolated-cursor-e2e` 覆盖；不新增测试文件。

**Interfaces:**
- Consumes: 真实状态库路径、隔离状态库路径，以及 `cursorAuthBackupKeys` 的三项白名单。
- Produces: `cursor.ImportCursorAuthState(sourcePath, destinationPath string) (CursorAuthImportResult, error)`；结果只包含非敏感的已导入键数和降级原因类别，不包含键值。
- Consumes: `run(sourceConfigPath, cursorPath string) error` 在调用 `applyIsolatedEnvironment(dirs)` 前保存的真实状态库绝对路径。
- Produces: 镜像模式下的最小隔离 `state.vscdb`；导入失败时继续执行现有启动链路。

- [ ] **Step 1: 确认调用边界和不变量**

阅读 `cmd/isolated-cursor-e2e/main.go` 的 `run`、`applyIsolatedEnvironment`、`cursor.WriteUserProxySettings` 与模拟账号注入分支，确认真实路径必须在环境替换前取得，且只在镜像模式调用。阅读 `internal/cursor/state_db.go` 的 `cursorAuthBackupKeys`、`syncCursorAuthStateDB` 与 `restoreCursorAuthStateDB`，确认新导入 API 不调用 `disableCursorStatsigGates`。

验收：调用图为 `run -> 捕获真实路径 -> applyIsolatedEnvironment -> ImportCursorAuthState -> 启动 Cursor`；普通模式仍为 `InjectCursorUserInfo`。

- [ ] **Step 2: 实现纯白名单状态库 API**

在 `internal/cursor/state_db.go` 增加导出的 `CursorAuthImportResult` 与 `ImportCursorAuthState`。读取端使用显式只读 SQLite DSN、单连接和 2 秒 busy timeout；逐项查询三项键，缺失、空值或读取失败返回不携带原始值的错误。写入端创建隔离目录、最小 `ItemTable` 与三项键，写入完成后关闭数据库。

实现必须满足以下结构：

```go
type CursorAuthImportResult struct {
    ImportedKeyCount int
}

func ImportCursorAuthState(sourcePath, destinationPath string) (CursorAuthImportResult, error) {
    // sourcePath 只读打开；destinationPath 仅写入隔离目录。
    // 不调用 syncCursorAuthStateDB，避免修改 Statsig 配置。
}
```

错误文本只描述路径类别或失败阶段，例如“读取真实 Cursor 登录态失败”或“真实 Cursor 登录态不完整”；不得拼入原始 SQLite 值。

- [ ] **Step 3: 接入隔离镜像启动链路与降级日志**

在 `run` 的开始处、`applyIsolatedEnvironment(dirs)` 前计算真实状态库绝对路径；在镜像模式 `cursor.WriteUserProxySettings` 完成后计算隔离目标路径并调用 `cursor.ImportCursorAuthState`。成功时仅输出已导入键数；失败时仅输出“自动导入未完成，已降级为手动登录”及已脱敏错误类别，随后继续启动 Cursor。

普通模式保持原有代码：

```go
if !mirrorCaptureEnabled {
    if err := cursor.InjectCursorUserInfo(localruntime.InjectAccountEmail, localruntime.InjectAuthToken); err != nil {
        return fmt.Errorf("注入隔离 Cursor 状态失败: %w", err)
    }
}
```

镜像模式不得调用模拟账号注入；导入成功或失败均不影响 `command.Start()`。

- [ ] **Step 4: 运行现有非敏感验证**

运行：

```powershell
go test ./internal/cursor ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e
go build ./cmd/isolated-cursor-e2e
go vet ./internal/cursor ./internal/mitm ./internal/backend/agent/protocol ./cmd/isolated-cursor-e2e
git diff --check
```

预期：四条命令均退出码 0。若已有测试失败，记录原始失败模块、失败现象和是否与本变更有关，不把失败表述为通过。

- [ ] **Step 5: 审核暂存区并提交功能**

运行：

```powershell
git status --short
git add -- internal/cursor/state_db.go cmd/isolated-cursor-e2e/main.go
git diff --cached --name-only
git diff --cached --check
git commit -m 'feat(e2e): import isolated cursor auth state'
```

预期：暂存区只包含本任务的 Go 文件；不包含三个用户未跟踪目录。

### Task 2: 用户授权后的真实隔离验证

**Files:**
- Modify: 无代码修改。
- Evidence: 新隔离根目录下的最小 `appdata/roaming/Cursor/User/globalStorage/state.vscdb`、启动器无敏感输出、镜像记录目录。

**Interfaces:**
- Consumes: 已构建的 `isolated-cursor-e2e`、用户明确允许的新实例启动，以及真实状态库启动前后非内容型元数据。
- Produces: “自动导入已真实验证”或“仍需手动登录”的运行结论，不输出敏感数据。

- [ ] **Step 1: 等待用户明确授权启动新的隔离实例**

不得停止当前运行的实例。获得授权后，在启动前记录真实 `state.vscdb` 的长度、最后修改时间和 SHA-256；这些值仅用于同次前后比较，不写入 Git 或公开回复。

- [ ] **Step 2: 启动新实例并检查非敏感行为**

使用当前显式镜像环境变量启动新构建，确认启动器报告自动导入成功或已降级，且不出现 token、邮箱、Cookie、请求正文或完整 ID。检查新隔离 Cursor 是否直接进入已登录状态。

- [ ] **Step 3: 核对真实源库未变化并报告边界**

在新实例启动后再次采集真实源库的长度、最后修改时间和 SHA-256，并与启动前完全一致的值比较。若一致，报告“真实状态库未被写入”；若不一致，停止将其归因于本功能，报告差异和并发真实 Cursor 活动的影响。

- [ ] **Step 4: 不为运行证据创建提交**

真实 E2E 只形成用户侧验证结论，不提交临时根目录、数据库、日志或凭据。

## Self-Review

- Spec coverage: DEC-19 的显式镜像范围、最小白名单、纯只读源库、失败降级、敏感数据边界、静态验证、真实 E2E 与回滚均分别由 Task 1 或 Task 2 覆盖。
- Placeholder scan: 未使用占位符、延期实施语句或“适当处理”等未定义实现步骤。
- Type consistency: 启动器调用的 `cursor.ImportCursorAuthState(sourcePath, destinationPath)` 与 Task 1 中定义的导出 API 完全一致；结果只暴露 `ImportedKeyCount`。
