## v0.0.77

### 新功能
- **官方模型计费目录**：内置 z.ai/GLM、MiniMax、火山方舟以及 OpenCode Zen/Go 的官方模型价格。
- **OpenCode 价格同步**：根据 OpenCode 官方模型接口和价格文档识别模型、输入输出价格及缓存价格。
- **均价估算兜底**：没有可核对官方价格的模型按币种使用均价估算，并明确标记价格来源。

### 修复
- **多币种计费**：按供应商、模型和币种分组汇总，避免美元与人民币直接相加。
- **零元计费显示**：免费模型按官方零价格处理，不再显示为未知或错误金额。
- **监督处置样式**：修复三个监督开关在窄布局下挤压、错位和文字显示不完整的问题。

### 优化
- **价格来源展示**：统计页面区分官方价、中转站探测价、手动配置和均价估算。
- **响应式设置布局**：监督处置开关在不同窗口宽度下自动使用单列、双列或三列布局。

### 发布资产与平台选择
- **Windows x64（AMD64）**：下载 `cursor-byok-0.0.77-windows-amd64.zip` 或 `cursor-byok-0.0.77-windows-amd64-installer.exe`。
- **Windows ARM64**：下载 `cursor-byok-0.0.77-windows-arm64.zip` 或 `cursor-byok-0.0.77-windows-arm64-installer.exe`。
- **Windows 32 位（x86/386）**：仅下载 `cursor-byok-0.0.77-windows-386.zip` 或 `cursor-byok-0.0.77-windows-386-installer.exe`。
- **重要**：64 位 Windows 不要下载 `windows-386` 文件；386 程序只能用于 32 位 Windows。ARM64 Windows 不要下载 `windows-amd64` 或 `windows-386` 文件。
- **macOS Intel**：下载 `cursor-byok-0.0.77-darwin-amd64.dmg` 或 `cursor-byok-0.0.77-darwin-amd64.tar.gz`。
- **macOS Apple Silicon（M1/M2/M3/M4）**：下载 `cursor-byok-0.0.77-darwin-arm64.dmg` 或 `cursor-byok-0.0.77-darwin-arm64.tar.gz`。
- **Linux x64（AMD64）**：下载 `cursor-byok-0.0.77-linux-amd64.tar.gz`。

### macOS DMG 授权与首次打开
这里的“iOS DMG”应为 **macOS DMG**。当前版本构建流程只对 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择“打开”并确认；仍被拦截时，到“系统设置 → 隐私与安全性”中点击“仍要打开”。仅对确认来源可信的文件执行此操作。

正式对外分发需要：
1. 使用 Apple Developer Program 的 **Developer ID Application** 证书签名 `.app`；若分发 `.pkg`，还需要 **Developer ID Installer** 证书。
2. 使用 `xcrun notarytool submit` 提交公证，凭据应通过 GitHub Secrets 或 App Store Connect API Key 提供，例如 Apple ID、Team ID 和 app-specific password。
3. 公证成功后使用 `xcrun stapler staple` 将票据装订到 `.app`，再把已签名、已公证的 `.app` 放入 DMG；DMG 本身通常承载这个 `.app`。
4. 发布前用 `codesign --verify`、`spctl --assess` 和 `xcrun stapler validate` 验证签名、公证和票据。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
