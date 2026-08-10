## v0.0.86

本版本是基于 v0.0.85 的热修复版本，仅修复 OpenAI Responses 工具参数异常，不包含主分支在 v0.0.85 之后的其他功能改动。

### 修复
- **修复 gpt-5.6 工具调用参数偶发损坏**：OpenAI Responses 请求不再默认开启并行工具调用，避免同一轮多个工具的参数流出现 JSON 截断、错误转义或无法解析，导致 `Ls`、`Shell` 等工具报 `incomplete or malformed tool input`。
- **保留显式配置能力**：如果用户通过 OpenAI extra params 或请求体覆盖显式设置 `parallel_tool_calls`，仍尊重用户配置。

### 测试与质量
- 新增串行默认值、请求体覆盖、extra params 显式 true/false、非 Responses 请求、空工具列表及非 gpt-5.6 模型的回归覆盖。
- 完成全仓 Go 测试和版本/tag 一致性检查。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.86-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.86-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.86-windows-arm64-installer.exe` 或 `cursor-byok-0.0.86-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.86-windows-x32-installer.exe` 或 `cursor-byok-0.0.86-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.86-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.86-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.86-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑” -> “属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 -> 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。