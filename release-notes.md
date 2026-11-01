## v1.0.16

### 修复

- **识图代理异常不再导致应用闪退**：自动识图处理单条消息时若发生内部异常，现在会兜底保留原消息继续转发，而不是让整个后台进程崩溃、所有对话掉线。
- **余额汇总异常时不再出现空白行**：批量查询供应商余额时某个渠道内部异常，列表仍会显示该渠道的名称与地址，异常也改为错误级别日志便于排查。

### 优化

- **委派任务后台轮询随服务关闭退出**：子代理进度保活、看门狗等后台轮询在保存配置重建 / 退出应用时会立即停止，避免旧服务实例的后台任务残留。
- **外部 API 流式转发更省内存**：长 SSE 流转发复用读缓冲区，不再每个数据块重新分配 32KB，降低长对话下的内存分配与 GC 压力。
- **会话记录清洗提速**：清理 Cursor 会话记录中的上下文标签前先做快速预检，不含对应标签时跳过正则扫描，长会话记录处理更快。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.16-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.16-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.16-windows-arm64-installer.exe` 或 `cursor-byok-1.0.16-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.16-windows-x32-installer.exe` 或 `cursor-byok-1.0.16-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.16-macos-arm64.dmg` 或 `cursor-byok-1.0.16-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.16-macos-x64.dmg` 或 `cursor-byok-1.0.16-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.16-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
