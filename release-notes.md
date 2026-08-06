## v0.0.84

### 新功能
- **原生 Cursor 工具 prompts 增强**：Agent 上下文新增 `<custom_subagents>` 片段——子代理以 name/描述/工具摘要形式注入，供模型选择（prompt 全文由 Task 工具执行时按名加载），避免把子代理系统提示词灌入主上下文；单条片段带 800 字符截断保护，上下文保持稳定
- **AGENTS.md 兜底注入**：当客户端规则信息不完整时，自动扫描 workspace 根目录的 `AGENTS.md`/`CLAUDE.md`/`GEMINI.md` 作为 `non_file_rules` 补充注入（客户端已提供完整规则时完全不输出，保持 prompt 前缀稳定）
- **官方 prompt 同步**：新增直连 api2.cursor.sh 拉取官方提示词的同步模块（缓存到 appdata/native-prompts/），配套 `cmd/fetch-native-prompt` 提取/同步工具

### 优化
- MCP 注册与资产增强、编译与上下文溢出处理优化

### 移除
- **移除混合模式（官方模型透传）**：经实测官方拒绝透传请求（Update Required），已从前后端完整移除混合模式开关与相关逻辑，恢复纯本地模式；相关工作保留在 `feature/hybrid-mode-official` 分支

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.84-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.84-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Win 电脑）**：下载 `cursor-byok-0.0.84-windows-arm64-installer.exe` 或 `cursor-byok-0.0.84-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.84-windows-x32-installer.exe` 或 `cursor-byok-0.0.84-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.84-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.84-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.84-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」→「属性」，看「系统类型」显示"64 位操作系统"就下载 x64，显示"32 位"才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。
