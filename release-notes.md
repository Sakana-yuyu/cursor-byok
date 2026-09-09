## v1.0.9

### 修复

- **Windows 运行期间黑窗反复闪现**：修复状态栏每 15 秒检测 Cursor 存活时弹出命令行窗口的问题；代理修复和重启等待中的同一检测也改为后台静默执行。
- **后台辅助命令静默运行**：证书与 Defender 检测、更新辅助进程统一禁止创建控制台窗口；指定 Shell 解释器时，内层进程也静默启动。继续保留命令输出、错误反馈和必要的系统权限确认。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.9-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.9-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.9-windows-arm64-installer.exe` 或 `cursor-byok-1.0.9-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.9-windows-x32-installer.exe` 或 `cursor-byok-1.0.9-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.9-macos-arm64.dmg` 或 `cursor-byok-1.0.9-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.9-macos-x64.dmg` 或 `cursor-byok-1.0.9-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.9-linux-x64.tar.gz`


Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
