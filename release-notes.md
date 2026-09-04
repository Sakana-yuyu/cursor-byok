## v1.0.6

### 新功能

- **新手使用引导**：首页新增「使用引导」入口，分步引导核心流程（配置模型 → 启动服务 → 在 Cursor 中使用）。引导为交互式：点击驱动前进、醒目高亮与箭头指向，引导即操作；完成后入口常驻，可随时重复观看。

### 修复

- **思考模型零输出长思循环**：修复 glm-5.3 / glm-5.3-flash 等思考模型因输出预算被纯思考耗尽，出现「长时间思考无任何回复」的问题；模型目录补充 GLM-5.3 系列官方规格（1M 上下文 / 128K 最大输出）。

### 优化

- **输出预算自适应重构**：max_tokens 自适应改为「错误驱动、有界适应」——仅当预算来自协议安全默认值时按模型目录上限抬升；渠道显式配置与学习到的真实限制优先于启发式，不再持续放大预算，消除无效的重试循环。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.6-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.6-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.6-windows-arm64-installer.exe` 或 `cursor-byok-1.0.6-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.6-windows-x32-installer.exe` 或 `cursor-byok-1.0.6-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.6-macos-arm64.dmg` 或 `cursor-byok-1.0.6-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.6-macos-x64.dmg` 或 `cursor-byok-1.0.6-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.6-linux-x64.tar.gz`
