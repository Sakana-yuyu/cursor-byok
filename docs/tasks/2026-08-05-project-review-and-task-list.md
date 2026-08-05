# 项目审查与 Task 列表

> 生成日期：2026-08-05  
> 分支：`feat/native-byok-experience`  
> 用途：后续会话可直接引用本文档继续推进，无需重复审查。

---

## 如何使用

1. 在新会话中 `@docs/tasks/2026-08-05-project-review-and-task-list.md` 或说明「按项目审查 task 列表继续」。
2. 优先从 **P0** 开始；P0 完成后再做文档同步（P1）。
3. 每完成一项，在本文件对应 checkbox 打勾并补充 commit / 验收记录。

---

## 现状快照

| 范围 | 状态 | 备注 |
|------|------|------|
| 能力完善 Task 1–13 + 发布前强化 | ✅ 已完成 | 见 `docs/tasks/2026-07-31-cursor-byok-capability-completion.md` |
| 体验加固阶段一（可靠性） | ✅ 已完成 | commit `c6eb836` |
| 体验加固阶段二（诊断工作台） | ✅ 已完成 | commit `78f17ff`；文档已同步（P1-1） |
| 体验加固阶段三（a11y） | ✅ 已完成 | commit `41fbc73`；调用方回归已验收（P1-2，键盘实测 21 项断言通过） |
| 审查发现三项缺陷（备份陈旧 / 恢复静默失败 / 日志明文 token） | ✅ 已修复 | commit `a0bacc0` + 4 个回归用例 |
| 分支相对 `main` | +5 commits | `a0bacc0` `5fb87ad` `4747b88` `2621967` + 文档/兜底 commit |
| 监督式委派计划 | ✅ 已标记实现 | P1-3，计划文档全部勾选 |
| P0-6 真实环境手工验收 | ⏳ 待用户执行 | 见文末「手工验收指引」 |

---

## 审查发现（WIP 缺陷，需修复后再提交）

### [P2] 有效备份存在时不再刷新，官方账号切换后会恢复错误账号

- **位置**：`internal/cursor/state_db.go` — `backupCursorAuthState`
- **触发**：首次本地模式注入前备份账号 A → 恢复 A → 用户在 Cursor 内重新登录官方账号 B → 再次启动本地服务跳过备份 → 停止/切直连时仍写回 A
- **修复方向**：对比当前 `state.vscdb` 与备份中的 token/email；若已变更则刷新备份
- **测试缺口**：补「有效备份 + 官方账号已变更」回归用例

### [P2] RestoreCursorUserInfo 失败被吞掉，调用方仍报告成功

- **位置**：`internal/client/cursor.go:58`、`internal/client/config.go:64`
- **触发**：`ClearCursorSettings`（StopProxy / ShutdownForQuit）或切直连 `routingMode=upstream` 时，恢复失败仅 `logger.Errorf`，仍返回 `nil`
- **影响**：上层认为成功，但 `state.vscdb` 可能仍保留模拟 token，直连官方 401
- **修复方向**：恢复失败应向上传播错误或明确失败态，不可静默成功

### [P3] 备份成功日志明文写入 accessToken

- **位置**：`internal/cursor/state_db.go:256`
- **修复方向**：日志只记录是否存在/长度或 hash，不写完整 token

### 残留风险（非确定性缺陷）

- auth 恢复后 Cursor 可能需进程重启才生效（类似 settings.json 的「运行中需重启」）——已处理：切直连模式时探测运行态并引导重启（P2-1）
- `SaveUserConfig` 在 `LoadUserConfig` 失败（`oldErr != nil`）时跳过恢复——已处理（P2-2）

### 已核对、无缺陷项

- `RestartCursor` 前端绑定补全（正向修复）
- `buildModelTooltipMarkdown` 逻辑与单测一致
- `state_db_test.go` 核心路径（往返、无备份清空、null 备份覆盖）有覆盖

---

## Task 列表

### P0 — 收口当前 WIP（建议先做）

- [x] **Task P0-1**：修复备份陈旧问题 — 官方账号变更后应刷新 `cursor-auth-backup.json`（commit `a0bacc0`）
- [x] **Task P0-2**：`RestoreCursorUserInfo` 失败不可静默 — `ClearCursorSettings` / 切直连路径向上传播错误（commit `a0bacc0`）
- [x] **Task P0-3**：备份日志脱敏 — 移除 accessToken 明文（commit `a0bacc0`）
- [x] **Task P0-4**：补回归测试 — 「有效备份 + 账号已变更」；恢复失败传播（commit `a0bacc0`，新增 4 个用例）
- [x] **Task P0-5**：验证并提交 WIP（不含 `hello.txt`）
  - `go test ./internal/cursor/... ./internal/backend/server/upstream/...` ✅
  - `go build ./...`、`go vet ./internal/...`、`go test ./internal/...`、`npm --prefix frontend run build` ✅
  - 拆 4 commit：auth 恢复 `a0bacc0` / tooltip `5fb87ad` / RestartCursor `4747b88` / tools.json `2621967`
- [x] **Task P0-6**：手工验收（真实 Cursor 环境项待用户执行，见下方「手工验收指引」）
  - 前端可自动化部分已完成：生产构建通过；headless 键盘实测 21 项断言全通过
  - 本地模式 → 停服务/切直连 → Cursor 官方登录态恢复（需真实环境）
  - 模型选择器 tooltip 显示上下文/价格（需真实环境）
  - 首页「重启 Cursor」可用（需真实环境）

### P1 — 文档与清单同步

- [x] **Task P1-1**：更新 `docs/tasks/2026-08-04-cursor-byok-experience-hardening.md`
  - 阶段二 Task 2.1–2.5 → `[x]`，记 commit `78f17ff`
  - 阶段三 Task 3.1–3.2、3.4 → `[x]`，记 commit `41fbc73`
- [x] **Task P1-2**：Task 3.3 调用方键盘回归
  - 枚举 Modal / ActionMenu 全部调用点：`useModal` 9 个调用方 + 更新提示框（markdown 模式）；ActionMenu 仅 Home.vue 两处
  - 静态确认 confirm/cancel、遮罩关闭、菜单项点击、焦点管理无回退
  - Edge headless + CDP 键盘实测：ActionMenu 11 项 + Modal 10 项断言全通过（含 Enter 打开、方向键导航、Escape 关闭并还焦、Tab 焦点陷阱 6 轮、遮罩关闭、焦点还原）
- [x] **Task P1-3**：标记监督式委派计划已实现
  - `docs/superpowers/plans/2026-07-31-supervised-delegation.md` 全部 step 勾选，顶部补「已实现」声明（实现代码在位：`supervision.go` / `loop_detector.go` / `supervisor_coordinator.go` / `supervisor_provider.go`、config 字段、前端控件）

### P2 — 可选后续

- [x] **Task P2-1**：auth 恢复后引导重启 Cursor（对齐「修复代理后重启」交互）
  - 设计决策：覆盖切直连（upstream）路径；停止服务/退出不弹（停止常为临时操作，退出时弹窗无意义）
  - 实现：后端 `WindowService.IsCursorRunning`（复用 `client.IsCursorProcessRunning` 探测）；前端切直连成功且 Cursor 运行中时弹「已恢复官方登录态」确认框，确认后以 skipConfirm 路径重启
  - 验收：`go build ./...`、`go vet`、`wails3 generate bindings`（97 methods）、`npm run build` 全绿；浏览器预览 mock 返回 false 不触发引导
- [x] **Task P2-2**：`SaveUserConfig` 在 `LoadUserConfig` 失败时的恢复兜底（commit `42107bf`，见验收记录）
- [x] **Task P2-3**：删除 `hello.txt`

---

## 建议执行顺序

```
P0-1 → P0-2 → P0-3 → P0-4 → P0-5 → P0-6 → P1-1 → P1-2 → P1-3
```

P2 按需处理。

---

## 通用验证命令

```bash
go build ./...
go vet ./internal/...
go test ./internal/...
go test ./internal/cursor/... ./internal/backend/server/upstream/...
npm --prefix frontend run i18n:scan
npm --prefix frontend run build
```

新增 bridge 方法后额外执行：

```bash
wails3 generate bindings
```

---

## 相关文档索引

| 文档 | 说明 |
|------|------|
| `docs/tasks/2026-07-31-cursor-byok-capability-completion.md` | 能力完善（已完成） |
| `docs/tasks/2026-08-04-cursor-byok-experience-hardening.md` | 体验加固（阶段一完成，二/三待同步清单） |
| `docs/superpowers/plans/2026-07-31-supervised-delegation.md` | 监督式委派计划（实现已验收，计划未勾选） |
| `docs/superpowers/plans/2026-08-02-stability-enhancements.md` | 稳定性增强计划 |
| `docs/superpowers/plans/2026-08-01-supplier-registry-and-auto-usage.md` | 供应商注册表计划 |

---

## 验收记录

> 完成 task 后在此追加记录。

| Task | 完成日期 | Commit | 备注 |
|------|----------|--------|------|
| P0-1 ~ P0-4 | 2026-08-05 | `a0bacc0` | 备份刷新 + 错误传播 + 日志脱敏 + 4 个回归用例（`TestBackupRefreshesWhenOfficialAccountChanged` / `TestBackupKeepsValidWhenStateStillInjected` / `TestRestoreCursorUserInfoPropagatesDBError` / `TestSaveUserConfigPropagatesRestoreErrorWhenSwitchingToUpstream`） |
| P0-5 | 2026-08-05 | `a0bacc0` `5fb87ad` `4747b88` `2621967` | go build/vet/test 全绿；前端生产构建通过；4 commit 拆提，未含 hello.txt |
| P1-1 | 2026-08-05 | —（随文档 commit） | 2026-08-04 文档 2.1–2.5、3.1–3.4 勾选，3.3 补回归记录 |
| P1-2 | 2026-08-05 | —（随文档 commit） | 静态枚举 + Edge headless CDP 键盘实测 21 项断言全通过；CDP 合成 Enter 不触发按钮默认激活（headless 限制，组件显式处理路径全部正常） |
| P1-3 | 2026-08-05 | —（随文档 commit） | 监督式委派计划全部 step 勾选 + 顶部「已实现」声明 |
| P2-2 | 2026-08-05 | `42107bf` | `SaveUserConfig` 去掉 `oldErr == nil` 条件，LoadUserConfig 失败时同样尝试恢复官方态 |
| P2-1 | 2026-08-05 | 待提交 | 切直连且 Cursor 运行中时引导重启（`WindowService.IsCursorRunning` + Home.vue 确认框，对齐修复代理交互）；go build/vet、bindings 97 methods、npm build 全绿 |
| P2-3 | 2026-08-05 | — | `hello.txt` 已删除（垃圾文件） |

## 手工验收指引（P0-6，需真实 Cursor 环境，无法自动化）

1. 本地模式运行 → 停止服务（或切直连模式）→ 打开 Cursor，确认官方登录态已恢复、模型选择器非模拟账号。
2. 登录官方账号 A 使用本地模式 → 停止服务 → Cursor 中切回官方登录 → 重新登录账号 B → 再次启动本地模式 → 停止服务 → 确认恢复的是 B 而非 A（验证 P0-1 场景）。
3. 模型选择器悬停模型名，tooltip 应显示「上下文窗口 / 最大输出 / 输入价格」与用户备注。
4. 首页点「更多 → 重启 Cursor」应触发重启确认框并成功重启。
