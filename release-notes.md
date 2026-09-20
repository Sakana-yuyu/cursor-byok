## v1.0.11

### 修复

- **安全收口（移植自上游）**：模型回复被 provider 内容策略拦截（finish_reason=content_filter）时，回合按正常结束收口，不再照常执行其中已出现的工具调用——被过滤的工具调用参数往往已被截断或污染，继续执行会绕过安全策略。
- **工具参数别名兼容（移植自上游）**：Read / Write / Delete 路径参数接受 Claude Code 习惯的 `file_path` / `filePath` 写法，Write 内容接受 `content` 别名（修复模型发 `content` 时写出空文件但界面显示正常的隐蔽问题），WebSearch 接受 `query` 作为搜索词别名，Claude 系模型不再频繁报"参数缺失"。
- **MCP 截断提示补全（移植自上游）**：工具结果文本因总预算被部分截短时，现在会附带一条汇总截断提示，模型不再误以为拿到完整内容；多条被跳过的结果合并为一条提示，不再刷屏。
- **MCP 截断提示单位修正（移植自上游）**：ListMcpResources 的截断提示按实际成因报告——个数上限触顶按「资源个数」、字节预算裁剪按「字节 + 资源数」，不再把资源个数错标成字节数误导模型。
- **Anthropic 渠道上下文超限自动恢复（移植自上游）**：溢出识别新增 Anthropic 原生报错措辞（`prompt is too long: N tokens > M maximum`、`model_context_window_exceeded`），Anthropic 协议渠道超限时同样触发强制压缩并自动重试，不再直接失败终态；结构性报错（如不支持 assistant prefill）不受影响。

### 优化

- 合并上游修复批次时补充 8 个回归测试，锁定上述行为（content_filter 收口、参数别名、截断提示、溢出措辞识别）。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.11-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.11-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.11-windows-arm64-installer.exe` 或 `cursor-byok-1.0.11-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.11-windows-x32-installer.exe` 或 `cursor-byok-1.0.11-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.11-macos-arm64.dmg` 或 `cursor-byok-1.0.11-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.11-macos-x64.dmg` 或 `cursor-byok-1.0.11-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.11-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
