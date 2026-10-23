## v1.0.14

### 新功能

- **模型能力目录补充国内外新模型**：新增 Claude Opus 5.5 / Sonnet 5.5 / Haiku 5.5、GPT-6 Luna、GPT-6.1 Sol、Grok 4.6 / 4.7 / 4.7 Fast、Composer 2.5、MiniMax-M3.1-Flash、DeepSeek V4.1-Flash、Qwen3.8-Flash、Doubao Seed 2.1 Pro 的上下文窗口、思考档位与官方价格，并兼容 Cursor 渠道 `cursor-grok-4.5-*` 命名；此前这些模型会落入兜底规则拿到错误的窗口与价格。

### 优化

- **控制中心余额按站点去重**：「厂商余额同步」把同一中转站 URL 的全部模型通道合并为一张卡片（显示站点名称、host 与通道数），不再逐通道平铺几十上百张重复卡片；统计行改为「已同步 X / Y 个中转站（共 N 个通道）」。
- **诊断页大量渠道视觉优化**：「供应商运行状态」按安全端点分组折叠展示，组头显示端点与渠道/冷却计数，含冷却渠道的分组自动展开并排在最前；渠道行改为紧凑单行布局（未配置凭据时黄色提示），并新增按模型 / 分组 / 端点的筛选框与「全部展开 / 收起」按钮。渠道较少时保持原来一眼全览的展示。
- 补充上述新增界面词条的英文 / 日文 / 俄文翻译。

### 更新说明

- **Cursor 巡检结论：无需协议适配**。本机与官方当前版本均为 3.22.12，安装包 bundle 与上一轮适配记录逐字节一致，能力清单零漂移，proto / forwarder 回归全部通过（详见 `docs/upstream-sync-2026-10-23.md`）。官方近期更新均为云侧功能（Cloud Agent Canvases、Origin 应用市场、iOS 远程控制等），不影响本地模式。
- 已知口径差异：火山方舟 `doubao-seed-2.1-pro` 定价与方舟产品页存在出入（¥3/¥15 vs ¥6/¥30），待按官方页复核后统一。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.14-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.14-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.14-windows-arm64-installer.exe` 或 `cursor-byok-1.0.14-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.14-windows-x32-installer.exe` 或 `cursor-byok-1.0.14-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.14-macos-arm64.dmg` 或 `cursor-byok-1.0.14-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.14-macos-x64.dmg` 或 `cursor-byok-1.0.14-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.14-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
