## v1.0.19

### 修复

- **GPT 思考框只显示「模型正在加密推理中」**：这句提示是在上游没返回推理摘要时补上的占位，并不是模型还在思考，容易被误解。现在会请求更详细的推理摘要（`reasoning.summary=detailed`）；上游确实没返回摘要时，会明确说明原因（OpenAI 原始推理是加密数据，本地无法解密显示）。

### 优化

- **Gemini 原生接口显示思考过程**：开启思考时自动带上 `includeThoughts`，思考摘要会显示在思考框里（以前 Gemini 默认不返回思考内容）。
- **Gemini OpenAI 兼容端点显示思考过程**：用 `generativelanguage.googleapis.com/v1beta/openai` 接入时自动开启 `include_thoughts`，并识别流式返回里带 `extra_content.google.thought` 标记的思考内容，不再混进正文。你在额外参数里自己写过 `include_thoughts` 的话，以你的值为准。
- **Claude 隐藏思考有提示**：上游只返回签名（display=omitted）或返回 `redacted_thinking` 时，思考框会说明「上游未返回本轮可读思考内容」，不再什么都不显示。这条提示只用于界面显示，不写入历史，也不会在下一轮发回给模型，不影响前缀缓存命中率。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.19-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.19-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.19-windows-arm64-installer.exe` 或 `cursor-byok-1.0.19-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.19-windows-x32-installer.exe` 或 `cursor-byok-1.0.19-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.19-macos-arm64.dmg` 或 `cursor-byok-1.0.19-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.19-macos-x64.dmg` 或 `cursor-byok-1.0.19-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.19-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
