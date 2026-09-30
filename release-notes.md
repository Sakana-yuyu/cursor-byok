## v1.0.12

### 修复

- **拉取模型失败不再一律显示「服务发生异常」**：错误文本现在保留底层网络原因与 HTTP 状态码，前端同步补齐中文网络措辞识别。此前本地代理未启动、密钥无权限这类常见故障，在「拉取模型」页只会看到笼统的兜底文案，无法定位问题。
- **密钥错误现在能被正确识别**：模型列表接口返回 401/403 时，页面显示「密钥无效或暂无可用额度」而非「服务发生异常」。

### 优化

- **拉取模型失败原因可直接看到**：错误横幅在友好提示后附带脱敏的技术原因（如 `dial tcp 127.0.0.1:7890: connect: connection refused`，可直接看出是本地代理端口不通），凭证与查询串参数一律抹除，不泄露密钥。
- **应用日志记录拉取失败明细**：每个模型目录候选地址的失败原因（网络错误 / HTTP 状态 / 响应解析失败）都会写入 `logs/app.log`，日志中的候选地址已去掉查询串与 userinfo。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.12-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.12-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.12-windows-arm64-installer.exe` 或 `cursor-byok-1.0.12-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.12-windows-x32-installer.exe` 或 `cursor-byok-1.0.12-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.12-macos-arm64.dmg` 或 `cursor-byok-1.0.12-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.12-macos-x64.dmg` 或 `cursor-byok-1.0.12-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.12-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
