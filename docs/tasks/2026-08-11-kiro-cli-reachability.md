# Kiro CLI 可达性与证据记录

## 前置说明

- 任务目标：将 Kiro CLI 从通用 custom CLI 的手工配置路径提升为内置、可探测、可调度的执行器，并明确其非交互协议的能力边界。
- 访问日期：2026-08-11。
- 检索关键词：Kiro CLI headless mode、Kiro CLI commands、KIRO_API_KEY、no-interactive。
- 采用来源：Kiro 官方文档 [Headless mode](https://kiro.dev/docs/cli/headless/) 和 [CLI commands](https://kiro.dev/docs/reference/cli-commands/)。采用原因是其定义了命令、认证、权限与退出语义；未采用第三方教程。
- 降级说明：官方文档只承诺 `--no-interactive` 将首个回答打印到 STDOUT，未承诺 JSONL 增量事件。适配器因此只返回结束后的最终文本，绝不把批量 stdout 误报为实时流。
- 反代调查：本次额外检索了 Kiro 官方文档中的 Base URL、endpoint override、proxy、HTTP proxy 与 `KIRO_API_BASE_URL`。`https://kiro.dev/docs/cli/headless` 可访问，但官方文档未声明上述自定义上游地址或 HTTP 代理的 CLI 契约；推测性加入环境变量会制造“已支持反代”的错误预期，因此暂不实现。

## 已实现的命令契约

- 执行：`kiro-cli chat --no-interactive --trust-tools=read,grep <prompt>` 用于只读任务，写入任务使用 `--trust-all-tools`。
- 认证：执行进程继承 `KIRO_API_KEY` 与用户配置中允许继承的环境变量名；不保存或输出密钥值。无 API key 或未登录会明确显示为需操作。
- 探测：先执行 `--version`，再执行无副作用的 `chat --help`，确认官方 Headless 所需的 `--no-interactive` 与权限参数。随后仅检查 `KIRO_API_KEY` 是否存在，不发起付费模型请求。缺失可执行文件、认证缺失、协议不兼容和探测异常分别返回不同状态。
- 安装：设置页在 Kiro 未安装时提供官方安装页链接，不自动下载、安装或提升权限。
- 取消：进程上下文取消或超时原样保留，不触发故障转移重试。限流和可恢复的进程失败才标记为可切换。

## 本机检查结果

- `kiro` 与 `kiro-cli` 不在 PATH。
- 只发现 `%APPDATA%/Kiro/User` 用户数据目录，未发现可调用的 Kiro CLI 可执行文件。
- 因此本机实际状态应为“未安装”，不能声称已经完成 Kiro 真实调用或认证验证。

## Claude Code 与 Codex CLI 契约复核

- 复核日期：2026-08-11。仅使用本机 CLI 的帮助、登录状态和最小只读请求；不读取或输出账户、认证信息、密钥或模型完整正文。
- Claude Code `2.1.226`：`--help` 已确认 `-p`、`--output-format`、`--no-session-persistence`、`--permission-mode` 和 `--disallowedTools` 参数存在；`auth status --json` 成功返回已认证形态。因此当前适配器的探测与只读执行参数契约可用。
- Codex CLI `0.147.0`：`--help` 与 `exec --help` 已确认 `--sandbox`、`exec --json`、`--ephemeral`、`--color` 参数存在，`login status` 为已认证形态。最小只读请求在约 7.95 秒内成功结束，实测 JSONL 类型为 `thread.started`、`turn.started`、`item.completed`、`turn.completed`；其中含最终 `agent_message` 和完成事件，和适配器解析器一致。
- 输出边界：本机 Codex CLI 的这次实测只在 `item.completed` 提供最终 `agent_message`，没有正文 token 级事件。因此适配器可以实时传递 CLI 的行级事件，但不能把 Codex CLI 不提供的 token 增量伪造成实时输出。
- Claude Code 的真实生成 JSONL 本轮未能闭环：最初短请求在 124 秒内未结束；随后三种受控采集方式分别受包装命令路径、PowerShell 异步回调无 Runspace、环境命令策略限制，未产生可用的 CLI 输出证据。它们均为观测脚本限制，不能据此归因于 Claude Code 协议或本项目适配器。后续应在允许独立子进程输出重定向的终端中，用同一只读参数复测 `assistant` 与 `result` 事件。
- 吞吐链路审计：`ProcessRunner` 的 stdout 行回调发生在子进程存活期间；委派可见进度写入 broker 仅追加 backlog 并以非阻塞信号唤醒订阅者；RunSSE 网络发送和调试落盘不在该回调的同步等待路径上。结合前述异步 actor mailbox 优化，当前没有证据表明这段本地链路会将上游 60--70 t/s 稳定压低到约 20 t/s。

## 风险与回滚

- 写入任务的 `--trust-all-tools` 与官方 headless 用法一致，但具有完整工具权限。仅在委派任务原本允许写入时使用；只读任务限制为 `read,grep`。
- Kiro 不提供本适配器可验证的增量输出协议，长任务的过程可见性仍受限于 CLI 最终文本；后续若官方发布稳定事件协议，再独立实现增量 stdout 消费。
- Kiro 反代能力尚未获得官方 CLI 文档证实。当前适配器只继承用户显式允许的环境变量与 `KIRO_API_KEY`，不解释或注入未验证的 Base URL、endpoint override 或 HTTP proxy 配置；后续如官方发布正式契约，应以官方变量名、作用范围和认证边界为准另行实现并增加真实连通性测试。
- 回滚：移除 `kiro.go`、Host 注册项、保留 ID 列表和前端 Kiro 行即可恢复到通用 custom CLI 路径；不影响既有 Claude、Codex、Gemini 或 Cursor 执行器。

## CLI 输出延迟修复

- 审计结论：此前 `ProcessRunner` 会等待子进程退出，再把完整 stdout 交给 Claude、Codex、Gemini 和 custom JSONL 解析器。因此即使 CLI 自己输出 JSONL，界面也只能在结束后批量回放。
- 修复：`ProcessRunner` 现在在 stdout 到达时按行、脱敏后回调。支持 JSONL 的执行器在回调中只发布用户可见文本；进程结束后仍以完整受限缓冲计算终态、用量和失败原因。
- 边界：行回调沿用相同的凭据脱敏规则；原始 stdout 仍受既有 4 MiB 上限保护。取消、超时和终态错误分类没有变化。Kiro 官方只承诺最终 stdout，仍不接入该 JSONL 实时路径。
