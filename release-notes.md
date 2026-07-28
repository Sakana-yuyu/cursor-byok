## v0.0.56

### 修复
- **任务卡死永久"运行中"**：修复转发层因 append 序列失配导致 Cursor 工具结果被持续误判为 stale 而丢弃、回合永久卡死（空转数十分钟无返回）的问题。新增 turn-staleness 看门狗，两段式自救：先重对齐 append 序列 + 宽限等待真实结果，仍卡住则强制收口未完成工具调用并自动继续 provider，保证任务最终完成而非永久中断。默认 120 秒无进展触发（`turnStaleTimeout` 可配置）。
- **context_length_exceeded 不再直接失败**：当 provider 返回上下文超限错误时（常因 `contextWindowTokens` 配置偏大），自动强制压缩上下文并重试 provider（每轮最多 2 次），而非整轮失败。模型收到工具超时错误后自行重试/换方案，任务能跑完。

### 新功能
- **自动配对模型上下文窗口**：新增「自动配对上下文窗口」能力，按内置模型目录为所有已存储模型适配器配对正确的 `contextWindowTokens`--目录命中则覆盖（修正误填的过大值如 1M），目录未命中则探测 provider `/models` 接口回填。支持 config.yaml 开关（`autoMatchContextWindow`，默认开启）、启动时自动执行、前端「一键配对上下文」按钮三种触发方式。MaxMode 自动取该模型目录最大值。
- **内置 findskills + superpowers 技能**：在 `.agents/skills/` 内置 vercel-labs 的 `find-skills`（技能发现元技能）与 obra/superpowers 全套 14 个软件开发方法论技能（TDD、systematic-debugging、brainstorming、writing-plans、verification-before-completion 等），供开发 agent 直接使用。均含 MIT 许可声明。

### 优化
- **gpt-5.6-luna 上下文窗口修正**：将误配的 1M 上下文窗口修正为合理值，避免压缩预算计算偏高导致 context 超限。
- **前端配置流转完善**：`turnStaleTimeout` 等新增配置项纳入前端归一化与持久化白名单，保存时不再丢失。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。
