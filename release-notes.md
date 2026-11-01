## v1.0.17

### 优化

- **MCP 连接共享连接池**：所有 HTTP / SSE 类 MCP 服务器现在共用一个底层连接池（请求头仍逐连接独立注入）。此前每次建连都会新建一个连接池，MCP 服务器频繁断线重连时旧连接要等约 90 秒才释放，长时间运行后可能堆积大量空闲 socket。系统代理设置变更后连接池会自动重建并释放旧连接。
- 清理委派任务标题的冗余代码，行为不变。

### 修复

- 修复一个在 CI 快机上偶发失败的测试时序竞态（`TestProviderResumeStartsNextPassWithoutFixedDelay`），不影响运行时行为。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.17-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.17-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.17-windows-arm64-installer.exe` 或 `cursor-byok-1.0.17-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.17-windows-x32-installer.exe` 或 `cursor-byok-1.0.17-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.17-macos-arm64.dmg` 或 `cursor-byok-1.0.17-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.17-macos-x64.dmg` 或 `cursor-byok-1.0.17-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.17-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
