# Kiro CLI 可达性与证据记录

## 前置说明

- 任务目标：将 Kiro CLI 从通用 custom CLI 的手工配置路径提升为内置、可探测、可调度的执行器，并明确其非交互协议的能力边界。
- 访问日期：2026-08-11。
- 检索关键词：Kiro CLI headless mode、Kiro CLI commands、KIRO_API_KEY、no-interactive。
- 采用来源：Kiro 官方文档 [Headless mode](https://kiro.dev/docs/cli/headless/) 和 [CLI commands](https://kiro.dev/docs/reference/cli-commands/)。采用原因是其定义了命令、认证、权限与退出语义；未采用第三方教程。
- 降级说明：官方文档只承诺 `--no-interactive` 将首个回答打印到 STDOUT，未承诺 JSONL 增量事件。适配器因此只返回结束后的最终文本，绝不把批量 stdout 误报为实时流。

## 已实现的命令契约

- 执行：`kiro-cli chat --no-interactive --trust-tools=read,grep <prompt>` 用于只读任务，写入任务使用 `--trust-all-tools`。
- 认证：执行进程继承 `KIRO_API_KEY` 与用户配置中允许继承的环境变量名；不保存或输出密钥值。无 API key 或未登录会明确显示为需操作。
- 探测：先执行 `--version`，再执行 `chat --list-models --format json`。缺失可执行文件、认证缺失、协议不兼容和探测异常分别返回不同状态。
- 安装：设置页在 Kiro 未安装时提供官方安装页链接，不自动下载、安装或提升权限。
- 取消：进程上下文取消或超时原样保留，不触发故障转移重试。限流和可恢复的进程失败才标记为可切换。

## 本机检查结果

- `kiro` 与 `kiro-cli` 不在 PATH。
- 只发现 `%APPDATA%/Kiro/User` 用户数据目录，未发现可调用的 Kiro CLI 可执行文件。
- 因此本机实际状态应为“未安装”，不能声称已经完成 Kiro 真实调用或认证验证。

## 风险与回滚

- 写入任务的 `--trust-all-tools` 与官方 headless 用法一致，但具有完整工具权限。仅在委派任务原本允许写入时使用；只读任务限制为 `read,grep`。
- Kiro 不提供本适配器可验证的增量输出协议，长任务的过程可见性仍受限于 CLI 最终文本；后续若官方发布稳定事件协议，再独立实现增量 stdout 消费。
- 回滚：移除 `kiro.go`、Host 注册项、保留 ID 列表和前端 Kiro 行即可恢复到通用 custom CLI 路径；不影响既有 Claude、Codex、Gemini 或 Cursor 执行器。

## CLI 输出延迟修复

- 审计结论：此前 `ProcessRunner` 会等待子进程退出，再把完整 stdout 交给 Claude、Codex、Gemini 和 custom JSONL 解析器。因此即使 CLI 自己输出 JSONL，界面也只能在结束后批量回放。
- 修复：`ProcessRunner` 现在在 stdout 到达时按行、脱敏后回调。支持 JSONL 的执行器在回调中只发布用户可见文本；进程结束后仍以完整受限缓冲计算终态、用量和失败原因。
- 边界：行回调沿用相同的凭据脱敏规则；原始 stdout 仍受既有 4 MiB 上限保护。取消、超时和终态错误分类没有变化。Kiro 官方只承诺最终 stdout，仍不接入该 JSONL 实时路径。
