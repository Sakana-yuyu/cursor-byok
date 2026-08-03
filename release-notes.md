## v0.0.78

### 新功能
- **读图 MCP 一键启用**：内置 `vision_mcp_server.py`（随程序打包，无需再安装 image-see 技能），一键把读图 MCP（`vision-reader`）写入 Cursor 全局配置，纯文本模型也能通过 MCP 工具识图
- **视觉委派 ↔ 读图 MCP 自动联动**：在「模型与委派 → 视觉委派」启用并选择识图模型后，自动把识图模型的网关地址 / API Key / 模型名 / 请求协议同步到读图 MCP，MCP 与委派打同一个网关、用同一种协议（chat/completions 或 Responses），委派失败时无缝兜底
- **委派失败自动兜底**：视觉委派识别失败时，自动回退为「带图片路径的占位说明」，主模型仍可通过读图 MCP 读取该图片，不再丢失图片信息
- **官方模型计费目录**：内置 z.ai/GLM、MiniMax、火山方舟以及 OpenCode Zen/Go 的官方模型价格
- **OpenCode 价格同步**：根据 OpenCode 官方模型接口和价格文档识别模型、输入输出价格及缓存价格
- **供应商图标**：新增多供应商品牌图标与统计图表组件

### 修复
- **视觉委派被多任务委派总开关误关**：仅开启「视觉委派」而未开启多任务委派时，纯文本主模型现在也能正常识图
- **识图结果被丢弃**：识图结果文本现在正确同步进用户消息，纯文本主模型能真正"看到"识图结果，不再只收到原始文字
- **未知模型不再上传图片**：能力目录未登记的模型按「保守不支持视觉」处理，图片走委派/占位，避免纯文本模型收到图片返回 400
- **MCP 连接失败不可排查**：stdio 连接增加 Windows PATH 兜底与子进程 stderr 诊断，错误信息保留命令名（不再被脱敏成 `[redacted]`）
- **读图 MCP 依赖外部脚本**：不再要求预先安装 image-see 技能

### 优化
- **多币种计费**：按供应商、模型和币种分组汇总，避免美元与人民币直接相加
- **价格来源展示**：统计页面区分官方价、中转站探测价、手动配置和均价估算
- **响应式设置布局**：监督处置开关在不同窗口宽度下自动使用单列、双列或三列布局

### 发布资产与平台选择
- **Windows x64（AMD64）**：下载 `cursor-byok-0.0.78-windows-amd64.zip` 和 `cursor-byok-0.0.78-windows-amd64-installer.exe`
- **Windows ARM64**：下载 `cursor-byok-0.0.78-windows-arm64.zip` 和 `cursor-byok-0.0.78-windows-arm64-installer.exe`
- **Windows 32 位（x86/386）**：仅下载 `cursor-byok-0.0.78-windows-386.zip` 和 `cursor-byok-0.0.78-windows-386-installer.exe`
- **重要**：64 位 Windows 不要下载 `windows-386` 文件（386 程序只能用于 32 位 Windows）。ARM64 Windows 不要下载 `windows-amd64` 和 `windows-386` 文件
- **macOS Intel**：下载 `cursor-byok-0.0.78-darwin-amd64.dmg` 和 `cursor-byok-0.0.78-darwin-amd64.tar.gz`
- **macOS Apple Silicon（M1/M2/M3/M4）**：下载 `cursor-byok-0.0.78-darwin-arm64.dmg` 和 `cursor-byok-0.0.78-darwin-arm64.tar.gz`
- **Linux x64（AMD64）**：下载 `cursor-byok-0.0.78-linux-amd64.tar.gz`

### macOS DMG 授权与首次打开
这里的"iOS DMG"应为 **macOS DMG**。当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

正式对外分发需要：
1. 使用 Apple Developer Program 的 **Developer ID Application** 证书签名 `.app`；若分发 `.pkg`，还需要 **Developer ID Installer** 证书
2. 使用 `xcrun notarytool submit` 提交公证，凭据应通过 GitHub Secrets 或 App Store Connect API Key 提供，例如 Apple ID、Team ID 和 app-specific password
3. 公证成功后使用 `xcrun stapler staple` 将票据装订到 `.app`，再把已签名、已公证的 `.app` 放入 DMG；DMG 本身通常承载这个 `.app`
4. 发布前用 `codesign --verify`、`spctl --assess` 和 `xcrun stapler validate` 验证签名、公证和票据

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。