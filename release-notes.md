## v1.0.2

### 新功能

- **测试失败自动停用开关**：模型配置页顶部新增开关，用户可自行开启。开启后，测试失败的模型会自动停用，不会进入 Cursor 模型列表；测试成功后自动恢复。
- **停用模型手动恢复**：模型卡片显示“已停用”状态，并提供“启用”按钮。

### 修复

- **模型路由一致性**：停用渠道同时从 Cursor 模型列表和请求路由中排除，避免客户端选择不可用模型。
- **配置持久化**：测试结果触发的启用状态变更会等待配置落盘，且状态未变化时跳过重复写入。
- **界面细节**：继续优化模型配置页、首页主题卡片、侧边栏滚动条和重复导航元素。

### 优化

- **启动与运行效率**：保留并延续 v1.0.1 的启动并行化、轮询降频、缓存去重和 SVG 图表优化。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.2-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.2-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.2-windows-arm64-installer.exe` 或 `cursor-byok-1.0.2-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.2-windows-x32-installer.exe` 或 `cursor-byok-1.0.2-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.2-macos-arm64.dmg` 或 `cursor-byok-1.0.2-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.2-macos-x64.dmg` 或 `cursor-byok-1.0.2-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.2-linux-x64.tar.gz`
