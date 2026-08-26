## v1.0.0

### 新功能

- **新版模型选择器（effort-first）**：适配新版 Cursor 的模型参数面板——外层为 Effort（Low / Medium / High / Extra High）、Model、Fast 开关，思考强度与模型均通过二级菜单选择；Fast 能力按各适配器的 `fastMode` 声明。

### 修复

- **上下文压缩显示与重试**：修复上下文投影生效时客户端 token 显示仍按未压缩历史估算（显示远超实际上限）的问题，checkpoint 现在与实际发送给 provider 的压缩后 prompt 对齐；修复压缩摘要超时回退后同一请求反复重试摘要导致的空转循环。
- **思考强度传递**：接受新版客户端 `effort` 参数 ID，用户选择的思考强度可正确映射到下游请求。
- **会话状态水合**：修复新版客户端内容寻址 blob 会话在旧对话续用时无法从磁盘 state.vscdb 水合的问题。
- **子代理与委托稳定性**：native 子代理无 exec 心跳不再被 30 分钟看门狗误杀；上游瞬时故障（request_timeout/503/stream idle timeout）自动重派同 Task；压缩能力耗尽后自动兜底防止上下文溢出终态。

### 优化

- 委派/子代理流超时放宽；旧 Shell outputNotification 兼容归一化；CA 私钥缺失时启动自动修复并支持热重载；AV 排除项引导提示。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.0-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.0-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.0-windows-arm64-installer.exe` 或 `cursor-byok-1.0.0-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.0-windows-x32-installer.exe` 或 `cursor-byok-1.0.0-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.0-macos-arm64.dmg` 或 `cursor-byok-1.0.0-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.0-macos-x64.dmg` 或 `cursor-byok-1.0.0-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.0-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」->「属性」，查看「系统类型」。显示「64 位操作系统」下载 x64，只有显示「32 位」才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。
