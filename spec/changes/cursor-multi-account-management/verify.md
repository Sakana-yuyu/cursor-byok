---
change: cursor-multi-account-management
round: 0
date: 2026-08-13
conclusion: pass
issues: { critical: 0, major: 0, minor: 0, open: 0 }
---

# Verify: cursor-multi-account-management

## Findings
| ID | Severity | Location | Finding | Status | Rounds |
|----|----------|----------|---------|--------|--------|
| V-1 | major | `design.md` Interfaces | 新会话式 OAuth 若继续使用 `StartCursorAccountLogin` 会改变旧 Wails 方法返回合同，违反“保留一个发布周期”。 | fixed(r0) | r0 |
| V-2 | major | `design.md` Architecture | 账户切换不能直接调用 `internal/bridge` 的未导出进程 helper，否则会形成错误依赖或复制关闭/启动逻辑。 | fixed(r0) | r0 |
| V-3 | major | `proposal.md` What/How | 含 token 的恢复包若作为 Wails 返回值交给前端保存，会进入 JS 内存、日志和浏览器 mock，扩大凭据暴露面。 | fixed(r0) | r0 |
| V-4 | minor | `proposal.md` What | 标签和批量账户能力没有独立业务证据，但它们是多账户可识别性与最多 100 项删除的现有设计组成；保留时必须不扩大凭据 DTO。 | fixed(r0) | r0 |

## Evidence (round 0)
necessity self-review -> 凭据导出由用户明确选择 B；客户端切换、迁移和兼容路径分别保护多账户核心、状态恢复和既有消费者，未发现可删除且不改变目标的 What 项。
regression-compat self-review -> 将新 OAuth 方法更名为 `BeginCursorAccountLogin`，明确旧断开语义，并在任务中要求从现有 bridge helper 抽取共享窄运行时适配。
security self-review -> 恢复包改为后端直写用户目标文件，Wails 只返回操作结果；首版明文文件风险已在 proposal Risk 与 HARD GATE 升级决定中公开。
not run: independent critique agents — 当前会话未提供独立子代理或 critic 调度工具；本轮为同一会话按 necessity 与 regression-compat 锁定视角的降级自审，不能冒充独立审查。
