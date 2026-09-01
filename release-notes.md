## v1.0.4

### 修复

- **排队对话自动续跑**：区分 RunSSE 重连与新排队消息，已完成的同一请求可自动重开下一回合，避免界面一直停在 `Starting Multitask`。
- **会话标题摘要**：补齐原生 `NameTab` 接口，通过模型生成简短任务标题，并在模型不可用时提供本地摘要，侧边栏不再直接显示整段用户输入。
- **Canvas 链接打开**：受管 `.canvas.tsx` 链接会投影为绑定当前任务的 Canvas bundle 别名，点击蓝色链接可打开对应 Canvas 页面。

### 优化

- **历史与上下文稳定性**：Canvas 链接兼容只作用于客户端 checkpoint 展示，保留原始历史和模型回放内容，避免影响后续上下文。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.4-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.4-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.4-windows-arm64-installer.exe` 或 `cursor-byok-1.0.4-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.4-windows-x32-installer.exe` 或 `cursor-byok-1.0.4-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.4-macos-arm64.dmg` 或 `cursor-byok-1.0.4-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.4-macos-x64.dmg` 或 `cursor-byok-1.0.4-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.4-linux-x64.tar.gz`
