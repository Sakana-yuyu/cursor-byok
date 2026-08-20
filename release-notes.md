## v0.0.96

### 修复

- **macOS 设置入口**：macOS 标题栏右侧新增独立的设置按钮，点击即可进入设置页；不再显示 Windows 风格的最小化/最大化/关闭按钮，窗口操作保留 macOS 原生红黄绿按钮。窄窗口下标题与按钮不会互相遮挡或产生横向滚动。
- **macOS 包版本号**：修复应用包内版本信息长期停留在旧版本（Finder/关于本机显示 `0.0.43`）的问题，打包时自动写入当前发布版本。
- **平台识别**：修正 iPhone/iPad 浏览器预览被误判为 macOS 导致界面异常的问题。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.96-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.96-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.96-windows-arm64-installer.exe` 或 `cursor-byok-0.0.96-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.96-windows-x32-installer.exe` 或 `cursor-byok-0.0.96-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.96-macos-arm64.dmg` 或 `cursor-byok-0.0.96-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-0.0.96-macos-x64.dmg` 或 `cursor-byok-0.0.96-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-0.0.96-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑”→“属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。
