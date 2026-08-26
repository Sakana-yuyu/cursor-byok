# Shell required_permissions 计划批次 1 报告

## 改动

- 在以下现有 Shell prompt schema 中增加可选 `required_permissions` 数组，元素枚举为 `full_network`、`all`，并说明权限映射及 inspect/readonly 不可借此提权：
  - `E:\MyProject\cursor-byok\prompt\agent\tools.json`
  - `E:\MyProject\cursor-byok\prompt\ask\tools.json`
  - `E:\MyProject\cursor-byok\prompt\debug\tools.json`
  - `E:\MyProject\cursor-byok\prompt\multitask\tools.json`
  - `E:\MyProject\cursor-byok\prompt\plan\tools.json`
- 未向 `E:\MyProject\cursor-byok\prompt\subagent\tools.json` 添加 Shell。
- 在 `E:\MyProject\cursor-byok\internal\backend\agent\bridge\exec\exec_open_shell.go` 中解析 `required_permissions`：
  - `all` 映射 `SandboxPolicy_TYPE_INSECURE_NONE`，并设置 `network_access=true`；
  - 否则包含 `full_network` 映射 `SandboxPolicy_TYPE_WORKSPACE_READWRITE`，并设置 `network_access=true`；
  - 未知、空值、缺失保持 nil；`all` 优先于 `full_network`；
  - `openShell` 将策略写入 `ShellArgs.RequestedSandboxPolicy`。
- 在 `E:\MyProject\cursor-byok\internal\backend\forwarder\readonly_shell_policy.go` 中先剥离 `required_permissions`，再执行既有 profile/notify_on_output 归一化、工作区约束和 Shell 白名单校验，防止 inspect/readonly 提权。
- 在 `E:\MyProject\cursor-byok\internal\backend\forwarder\tool_catalog.go` 的 readonly schema 改写中删除 `required_permissions` 属性及 required 列表引用。
- 新增/更新测试覆盖权限映射、all 优先级、无效输入、openShell proto 字段、readonly 剥离与白名单仍生效、schema optional/enum 和 subagent 不含 Shell。

## 测试命令与结果

- `go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder`
  - 通过。
- `python` JSON schema 校验脚本，检查 agent/ask/debug/multitask/plan 的 Shell schema，以及 subagent 无 Shell
  - 通过：`prompt schema validation passed for agent/ask/debug/multitask/plan; subagent has no Shell`
- `git diff --check`
  - 通过；仅显示现有工作树文件的 LF/CRLF 转换提示，无 whitespace error。

## 批次 1 审查修复

- `decodeShellRequestedSandboxPolicy` 现在严格匹配原始 enum 字符串，不再对权限值执行 `TrimSpace`；`" all "`、`" full_network "`、非字符串和 malformed 非数组值均不会授权。
- 增加 malformed/带空白权限值测试。
- `openShell` 测试现在分别断言 `full_network` 和 `all` 的 proto enum 与 `network_access=true`，包括 `all -> INSECURE_NONE`。
- 强化 readonly schema 测试，断言 `required_permissions`、`notify_on_output`、`profile` 同时从 properties 和 required 列表移除，并通过 `DefaultToolCatalog.Load(PLAN, "explore")` 集成路径确认 `command`、`working_directory`、`block_until_ms` 等既有字段仍保留。

### 审查修复命令与结果

- `go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder -run 'Test(DecodeShellArgsRequiredPermissions|OpenShellIncludesRequestedSandboxPolicy|RewriteReadonlyShellToolRemovesUnsupportedSchemaFields|ReadonlyExploreShellSchemaRemovesEscalationAndUnsupportedFields)'`
  - 通过。
- `go test ./internal/backend/agent/bridge/exec ./internal/backend/forwarder`
  - 通过。
- prompt/schema JSON 校验：agent、ask、debug、multitask、plan 的 Shell `required_permissions` 为 optional array，枚举为 `full_network`/`all`；subagent 无 Shell
  - 通过。
- `git diff --check`
  - 通过。

## 遗留疑问

- 未运行整个仓库的 `go test ./...`；本批次运行了受影响的 exec 和 forwarder 包测试及 prompt/schema 校验。
- 未恢复或修改 `stash@{0}`，也未引入 Rust/Tauri 文件。
