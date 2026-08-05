## v0.0.82

### 新功能
- **官方模型混合模式**：开启后可在本地代理下同时使用 Cursor 官方模型——模型选择器出现官方模型列表（GPT-5.x / Claude Opus / Sonnet / Grok 等），选择官方模型时请求透传官方执行（消耗官方账号额度），其余模型继续走你的自定义中转配置；官方模型目录会随账号登录自动刷新
- **官方账号自动导入**：服务启动时自动读取已备份的官方登录态（免手动 PKCE 登录），断开账号后不会重复导入
- **Agent 会话摘要**：任务回合结束时按官方时序注入会话摘要事件，长任务续跑上下文更完整
- **跨工具扫描面扩展**：技能与 MCP 扫描新增 Trae（`.trae/skills`）、Windsurf（`.windsurf/skills`、`~/.codeium/windsurf`）、Gemini CLI（`~/.gemini/skills`）、GitHub Copilot / VS Code（`~/.copilot/skills`、`.vscode/mcp.json`）、Cline（`.cline/skills`）等来源，其他工具里配好的技能/MCP 也能在 Cursor 中调用
- **调试日志开关**：高级设置新增「记录调试日志」开关，关闭后立即停止写入，避免日志目录无限膨胀

### 优化
- **切直连引导重启**：切到直连模式且 Cursor 正在运行时，自动提示重启使官方登录态完整生效（与「修复代理后重启」同一交互模式）
- **账号恢复兜底**：配置读取异常时同样尝试恢复官方登录态，避免残留模拟账号导致官方连接 401
- **技能来源分类展示**：技能管理页按来源工具分类显示扫描结果

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.82-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.82-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Win 电脑）**：下载 `cursor-byok-0.0.82-windows-arm64-installer.exe` 或 `cursor-byok-0.0.82-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.82-windows-x32-installer.exe` 或 `cursor-byok-0.0.82-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.82-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.82-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.82-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」→「属性」，看「系统类型」显示"64 位操作系统"就下载 x64，显示"32 位"才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。