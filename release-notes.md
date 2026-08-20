## v0.0.97

### 修复

- **损坏会话自愈**：本地会话历史文件（`state.json`/`context.json`）因断电、磁盘写入被拒等原因损坏时，不再让该会话永远报「服务发生异常」。现在会自动把损坏文件隔离为 `.corrupt-<时间戳>` 备份，并从 Cursor 客户端自带的会话状态重建，旧对话可直接继续使用，无需手动删目录。
- **失败原因可查**：回合失败时日志新增 `forwarder turn failed ... cause=...`，记录真实根因（如 JSON 解析错误、`Access is denied` 等），不再只有通用文案，远程排障不再靠猜。
- **日志脱敏**：「自动配对上下文窗口」探测失败日志中的供应商 API key 现在只保留首尾少量字符，其余掩码，`app.log` 不再出现明文密钥。

> **提示**：若遇到新对话正常、旧对话报错的情况，安装本版本后在旧对话里直接发一条消息即可自动恢复。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.97-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.97-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.97-windows-arm64-installer.exe` 或 `cursor-byok-0.0.97-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.97-windows-x32-installer.exe` 或 `cursor-byok-0.0.97-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.97-macos-arm64.dmg` 或 `cursor-byok-0.0.97-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-0.0.97-macos-x64.dmg` 或 `cursor-byok-0.0.97-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-0.0.97-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑”→“属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。
