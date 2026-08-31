# Skills 默认关闭与界面提示设计

日期：2026-08-09
状态：已批准

## 背景

当前 Skills 与 MCP 页面使用 `disabledSkills` 黑名单表达技能开关：未出现在黑名单中的技能默认启用。用户本机已有大量 Cursor、Claude、Codex 或共享技能时，首次扫描会让大量技能同时成为稀疏激活候选，增加扫描、索引和提示词处理开销。

页面上的技能开关只控制 cursor-byok 的跨工具扫描与自动激活。Cursor 客户端仍可通过请求中的 `SkillOptions`、`AgentSkills` 或技能规则显式附带技能；这些内容沿原生 request context 链路进入模型提示，不受当前扫描开关约束。页面需要把这个边界明确告诉用户。

## 目标

- 所有扫描技能采用显式启用模式，默认关闭。
- 升级后的现有技能同样全部关闭，不继承旧黑名单下的隐式启用状态。
- 以后新安装或新扫描到的技能天然保持关闭，直到用户逐项启用。
- 页面说明默认关闭的性能原因，并说明客户端显式附带技能仍可能生效。
- MCP 的默认状态、逐项开关和连接行为保持不变。

## 配置模型

在 `SkillMCPScanConfig` 中增加：

```go
EnabledSkills map[string]bool `json:"enabledSkills,omitempty" yaml:"enabledSkills,omitempty"`
```

技能名按现有规则去除首尾空白并转为小写。只有 `enabledSkills[name] == true` 的技能才会进入激活候选。字段缺失、空 map、未知技能名或值为 false 均表示未启用。

原 `disabledSkills` 字段保留反序列化兼容，避免旧配置成为未知字段，但不再决定技能启用状态，也不会迁移为白名单。这样升级后所有现有技能都会关闭。后续保存设置时可以保留旧字段，避免引入与本功能无关的配置清理；运行时仅以 `enabledSkills` 为准。

顶部 `skillMcpScan.enabled` 总开关语义不变：关闭时整条 Skills/MCP 自动扫描注入链路停用；开启时只允许扫描和处理，其中 Skills 仍需通过逐项白名单才能参与激活。MCP 继续使用现有 `disabledMcpServers` 语义。

## 后端数据流

1. 配置管理器暴露 `EnabledSkills` 快照。
2. `enrichRequestContextWithScannedAssets` 将总开关、来源开关和技能白名单同步到 `SkillStore`。
3. `SkillStore.ScanForWorkspace` 扫描多来源技能后，仅保留 `EnabledSkills` 中显式为 true 的技能。
4. `SkillActivator` 只对过滤后的候选构建或复用 BM25 索引并执行 Top-K 激活。
5. 客户端显式传入的技能继续走 request context 回放，不纳入该白名单过滤；页面提示清楚说明这一边界。

`find-skills` 等现有常驻或强制激活逻辑也必须先通过白名单过滤。用户未明确启用时，它们不能绕过默认关闭策略。历史上的 `goal-loop` 已在 2026-08-31 移除，`/goal` 交给 Cursor 原生能力处理。

## 前端行为

Skills 列表的逐项状态改为读取 `enabledSkills`：

- 未配置的技能显示“已停用”。
- 用户开启技能时写入 `enabledSkills[normalizedName] = true`。
- 用户关闭技能时删除该 key，保持配置紧凑。
- 保存失败时恢复原状态，沿用现有串行保存和错误展示逻辑。

在 Skills 标签的列表控制区加入持续可见的说明文字：

> 技能默认关闭，仅启用的技能会参与 BYOK 扫描和自动激活，以减少扫描与提示词开销。此开关只控制 BYOK 扫描；Cursor 客户端显式附带的技能仍可能生效。

说明只在 Skills 标签显示，避免让用户误以为 MCP 也改为白名单。顶部扫描总开关附近应保留现有布局，不增加阻断式弹窗或首次启动确认。

## 兼容与错误处理

- 旧配置没有 `enabledSkills` 时按空白名单处理，所有技能关闭。
- 旧 `disabledSkills` 不参与迁移，不会意外重新启用技能。
- 白名单中暂时扫描不到的技能名可以保留；技能以后重新出现时恢复用户此前的明确启用选择。
- 无效技能 manifest 仍按现有诊断逻辑展示，不能被开启。
- 配置保存失败时前端回滚当前开关，后端继续使用上一次成功保存的快照。

## 测试与验证

- 配置默认值和缺失 `enabledSkills` 时不激活任何扫描技能。
- 白名单中的有效技能可以进入激活候选，未列出的技能不会进入。
- 后来新增的技能默认关闭。
- `find-skills` 未在白名单时不会被强制注入；历史 `goal-loop` 不再作为内置技能发布。
- 旧 `disabledSkills` 配置不会产生隐式启用。
- MCP 扫描、禁用和连接行为保持原样。
- 前端浏览器绑定与真实 API 使用相同的 `enabledSkills` 结构，逐项开关正确保存和回滚。
- 运行相关 Go 测试、前端测试和生产构建；构建触发 i18n 扫描，并核对所有语言目录无空翻译、占位符一致。
- 运行 `go vet ./...`、`go build ./...` 和 `git diff --check`。
