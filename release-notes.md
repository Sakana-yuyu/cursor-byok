## v1.0.8

### 新功能

- **锚定 token 估算（移植自 Pchat++）**：对话 token 用量估算此前依赖纯启发式累加，长对话会逐渐漂移。现在首次拿到 provider 上报的真实输入用量后，会把它锚定到发送时的消息数；之后只为新增消息做增量估算，彻底消除累计漂移。锚定值在压缩/回退/上下文投影/委派等任何改写历史的场景自动失效重算。该估算同时接入 max_tokens 预算决策与上下文压力判断，长对话下的预算决策更准。
- **指标页自然语言时间范围**：统计页时间选择新增输入框，直接输中文即可筛选，如「昨天到今天」「近7天」「本周」「上月」「8月23日到8月25日」「今天9点到现在」，支持「半小时」「1个半小时」等口语表达，输入后回车或点「应用」生效。
- **年度热力日历**：指标页新增 GitHub 风格的全年调用热力图（近 365 天），按当日请求量自动分 5 档着色，一眼看出使用节奏和高峰时段。
- **底栏状态栏**：主窗口底部新增状态栏，实时显示网关运行状态（运行中/部分运行/已停止）、Cursor 进程状态与时钟。

### 优化

- 自然语言时间范围与日期选择器联动：手动改日期输入框会自动清除自然语言覆盖，两种方式互不打架；界面文案已同步翻译为英文/日文/俄文。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.8-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.8-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.8-windows-arm64-installer.exe` 或 `cursor-byok-1.0.8-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.8-windows-x32-installer.exe` 或 `cursor-byok-1.0.8-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.8-macos-arm64.dmg` 或 `cursor-byok-1.0.8-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.8-macos-x64.dmg` 或 `cursor-byok-1.0.8-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.8-linux-x64.tar.gz`
