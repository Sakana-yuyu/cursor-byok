## v1.0.5

### 修复

- **OpenAI 兼容模型安全默认值**：将未显式配置的默认 `max_tokens` 从 `65536` 调整为 `4096`，避免部分供应商直接返回超限错误。
- **超限自动恢复**：识别供应商返回的实际 Token 上限，自动收敛请求预算、持久化限制并有限重试，减少模型因额度保护策略无法使用的问题。

### 新功能

- **Daoxe 供应商模板**：支持从 Daoxe 动态拉取模型目录，并按其实际的 `/v1/chat/completions` 模式创建模型。
- **Daoxe 协议自动适配**：供应商模板优先于通用模型名称推断，`gpt-oss-*` 等模型不会被错误路由到 Responses 端点。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.5-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.5-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.5-windows-arm64-installer.exe` 或 `cursor-byok-1.0.5-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.5-windows-x32-installer.exe` 或 `cursor-byok-1.0.5-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.5-macos-arm64.dmg` 或 `cursor-byok-1.0.5-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.5-macos-x64.dmg` 或 `cursor-byok-1.0.5-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.5-linux-x64.tar.gz`
