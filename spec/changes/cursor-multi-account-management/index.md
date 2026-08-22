# Index: cursor-multi-account-management

Requirement source: 用户会话，2026-08-13；已确认的 `design.md` 作为方案资产，不反向伪造用户逐字需求

## Requirements
- R-1: 现在这个软件还有什么可以拓展的地方？ | source: 用户消息
- R-2: 好的使用这个技能扩展这些功能。 | source: 用户消息
- R-3: 继续推进 | source: 用户消息

## Assets
- A-1: `internal/cursoraccount/manager.go` 单账户 PKCE、资料补全、自动刷新与原子凭据写入 | use: extend
- A-2: `internal/client/cursor_account.go` 与 `internal/bridge/proxy.go` 账户 Wails 调用链 | use: extend
- A-3: `internal/cursor/state_db.go` 跨平台状态库定位、认证白名单读写与 SQLite 事务 | use: extend
- A-4: `internal/bridge/window.go`、`cursor_kill_*` 与 `cursor_launch_*` 的 Cursor 探测、关闭和重启能力 | use: reuse
- A-5: `frontend/src/services/clientApi.js` 的桌面绑定、浏览器预览 mock、错误归一化和敏感参数摘要 | use: extend
- A-6: `frontend/src/components/CursorAccountCard.vue` 现有控制面账户卡片 | use: extend
- A-7: `frontend/src/i18n` 静态扫描、locale 目录与 Playwright browser-preview 测试链路 | use: reuse
- A-8: `internal/cursor.syncCursorAuthStateDB` | use: rejected: 同时修改 Statsig gates，不能用于只替换真实 Cursor 认证白名单的账户切换
- A-9: `cursor-auth-backup.json` 认证键备份 | use: rejected: 不是每次切换独立的 `state.vscdb`、WAL、SHM 文件级恢复包
- A-10: `docs/superpowers/plans/2026-08-12-cursor-multi-account.md` | use: pattern: 可参考拆分与验证，但其中凭据导出等内容未获本次需求来源直接支持

## Exemplars
- E-1: 多账户持久化与当前账户授权 -> `internal/cursoraccount/manager.go` 的锁、临时文件加 rename、刷新与脱敏模式
- E-2: 真实 Cursor 认证白名单适配 -> `internal/cursor/state_db.go` 的只读 SQLite、事务写入和恢复错误传播模式
- E-3: 高风险 Cursor 切换操作 -> `internal/bridge/window.go` 的显式重启结果与 `frontend/src/views/Home.vue` 的确认交互
- E-4: 多账户前端面板 -> `frontend/src/components/CursorAccountCard.vue` 与 `frontend/src/services/clientApi.js`
