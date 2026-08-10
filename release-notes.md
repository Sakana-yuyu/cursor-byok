## v0.0.87

本版本聚焦两个稳定性修复：gpt-5.6 并行工具调用参数流回退安全默认，以及上下文 preflight 超限时的自动强制压缩兜底。

### 修复

- **gpt-5.6 并行工具调用回退串行默认**：v0.0.85 自动开启 `parallel_tool_calls` 后，模型偶发返回截断或错误转义的工具参数，导致工具调用残缺。本版本改为安全默认：gpt-5.6 Responses 请求默认串行（`parallel_tool_calls:false`），用户通过 extra params 显式选择并行时仍尊重其配置。
- **上下文超限自动兜底压缩（自动 `/summarize`）**：长会话中被冻结的头部历史让自动投影压缩结构性压不动，请求体积持续增长，最终以「Context Too Large After Compaction」终态失败。现在当自动压缩链穷尽、provider 请求前预算校验仍超限时，会先自动执行一次 `/summarize` 式强制压缩（覆盖所有历史轮次并真正重写会话），压缩完成后自动继续原请求；仅当压缩后仍超限才报错（每回合最多触发一次，避免死循环）。

### 说明

- 本版本同时包含 v0.0.85 的全部更新（会话级调度与隔离、全局上下文投影、委派与子代理增强、设置与历史界面优化等）。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.87-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.87-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.87-windows-arm64-installer.exe` 或 `cursor-byok-0.0.87-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.87-windows-x32-installer.exe` 或 `cursor-byok-0.0.87-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.87-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.87-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.87-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑” -> “属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 -> 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
