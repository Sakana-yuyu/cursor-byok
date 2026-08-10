## v0.0.89

本版本修复上游中转站「静默卡死」导致整轮请求以 `provider stream idle timeout` 终态失败的问题。

### 修复

- **上游静默卡死不再一次性判死**：上游中转站偶发「连接已建立但长时间不返回任何内容」时，provider 流空闲看门狗会在阈值（父代理默认 90s，长任务/子代理放宽至 240s/600s）后判定失败。此前该失败被归类为不可恢复的终态错误，整个请求直接失败、用户需手动重试。实测（会话 4958d320）一天内因此出现多次 `provider stream idle timeout after 240s without effective content` 终态失败。现在该失败被识别为 pre-output 瞬时故障：尚未向客户端转发任何内容前断开，重发不会产生重复输出，因此 router 会自动重试（最多 2 次），重试使用更短的空闲阈值（45s）让仍卡死的上游快速失败、已恢复的上游立即继续；同时跳过常规重试的 45s 预算门槛（静默后的耗时必然已超预算）。仅重试耗尽仍卡死才报错，且错误文本保持不变。native 子代理的瞬时故障重试同步覆盖该错误。

### 说明

- 本版本同时包含 v0.0.88 的全部修复（native 子代理瞬时上游故障重试、legacy Shell 工具调用解析崩溃）、v0.0.87（gpt-5.6 并行工具调用回退串行、上下文 preflight 超限自动压缩、长时子代理不再被看门狗误杀）与 v0.0.85 的全部更新。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.89-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.89-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.89-windows-arm64-installer.exe` 或 `cursor-byok-0.0.89-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.89-windows-x32-installer.exe` 或 `cursor-byok-0.0.89-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.89-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.89-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.89-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑” -> “属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 -> 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。

---

## v0.0.88

本版本聚焦两个稳定性修复：native 子代理瞬时上游故障自动重试，以及历史会话 legacy Shell 工具调用解析崩溃。

### 修复

- **native 子代理瞬时上游故障自动重试**：上游中转站出现瞬时 `request_timeout` / `503 Connection refused` 时，父 agent 靠模型重试能扛住，而 Cursor 原生子代理一次 provider 调用失败即被判死。现在子代理因可判定的瞬时上游错误失败时，会自动重新派发同一次 Task（最多 2 次重试、共 3 次尝试），上游恢复后即可成功；仅重试耗尽或非瞬时错误（如 `context_too_large`、工具逻辑失败）才按原样失败。
- **历史会话 legacy Shell 工具调用解析崩溃修复**：旧版本写入的 Shell 工具调用把 `outputNotification` 存成 message 对象，而当前格式是 base64 bytes，导致带旧历史的会话在启动或失败收尾发布 checkpoint 时解析崩溃（报 `invalid value for bytes field outputNotification`），表现为「An unexpected error occurred」且反复重试。现在所有历史解析路径都会把 legacy 对象格式自动升级，旧会话恢复正常。

### 说明

- 本版本同时包含 v0.0.87 的全部修复（gpt-5.6 并行工具调用回退串行、上下文 preflight 超限自动压缩、长时子代理不再被看门狗误杀）与 v0.0.85 的全部更新（会话级调度与隔离、全局上下文投影、委派与子代理增强、设置与历史界面优化等）。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.88-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.88-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.88-windows-arm64-installer.exe` 或 `cursor-byok-0.0.88-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.88-windows-x32-installer.exe` 或 `cursor-byok-0.0.88-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.88-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.88-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.88-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑” -> “属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 -> 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
