## v1.0.7

### 修复

- **Cursor 设置页模型列表为空**：修复 Cursor 设置页 Models 页面显示 "No models available"、看不到任何模型的问题。原因是模型列表响应中 `visibleInRoutedModelView` 字段被误标为 true（该标记表示模型折叠进 Auto 路由视图、不单独列出），客户端设置页会过滤掉所有带此标记的模型。现已恢复默认值，全部模型重新在设置页正常显示；聊天中的模型选择器不受影响。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.7-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.7-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.7-windows-arm64-installer.exe` 或 `cursor-byok-1.0.7-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.7-windows-x32-installer.exe` 或 `cursor-byok-1.0.7-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.7-macos-arm64.dmg` 或 `cursor-byok-1.0.7-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.7-macos-x64.dmg` 或 `cursor-byok-1.0.7-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.7-linux-x64.tar.gz`
