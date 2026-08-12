## v0.0.92

### 新功能

- **安全 Cursor 协议时间线**：历史页新增“Cursor 协议”来源，以匿名请求哈希聚合隔离采集到的上下行事件、流式状态、任务模式、子代理动作、终态和解析错误。该视图只读取脱敏结构索引，不显示原始帧、正文、URL、参数、Cookie、Token、认证头或完整请求 ID。
- **Kiro CLI 应用内安装**：委派设置可由用户显式发起 Kiro CLI 安装并自动复检，不再要求先跳出软件手动下载。

### 优化

- **Grok 多代理模型提示**：当已知的 Grok 多代理变体因不支持客户端工具调用而被上游拒绝时，保留原始技术错误，并按当前界面语言提示切换到支持工具调用的模型。
- **设置返回一致性**：从任意设置分类返回都会回到主页。

### 已验证边界

- 协议时间线用于对齐真实采集到的结构事件，不提供原始抓包查看、导出、回放，也不会自动导入或写入登录凭据。
- 后台化子代理、等待子代理和 ComputerUse 的专属 Cursor 上下行仍取决于客户端实际触发；本版本不伪造这些协议消息。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.92-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.92-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.92-windows-arm64-installer.exe` 或 `cursor-byok-0.0.92-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.92-windows-x32-installer.exe` 或 `cursor-byok-0.0.92-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.92-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.92-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.92-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑” -> “属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开

当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 -> 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
