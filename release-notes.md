## v0.0.83

### 修复
- **修复「CA 材料不完整导致应用打不开」的崩溃**：v0.0.82 起，当本地 CA 证书/密钥只剩其一（升级遗留、杀毒软件误删、写入中断等历史原因）时应用会启动即退出、无法自救。本版本改为：
  - **降级启动**：CA 异常时应用照常打开，本地代理自动停用，首页显示红色警示条
  - **一键修复**：首页「一键修复」按钮自动备份残留文件（`.corrupt-时间戳.bak`）并重新生成 CA，重启应用即恢复正常
- **CA 写入原子化**：证书/密钥改为临时文件 + 原子替换写入，密钥写入失败自动回滚证书，从源头杜绝「只剩一半材料」的状态
- **修复 CA 错误提示文案方向**：证书残留时错误提示曾误报「证书缺失」（会误导删错文件），已纠正
- **空证书防御**：降级启动时拒绝用空证书覆盖真实 CA 文件

### 优化
- CA 任何初始化失败都不再阻断应用启动（权限、磁盘等异常同样可在首页看到具体错误）

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.83-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.83-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Win 电脑）**：下载 `cursor-byok-0.0.83-windows-arm64-installer.exe` 或 `cursor-byok-0.0.83-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.83-windows-x32-installer.exe` 或 `cursor-byok-0.0.83-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.83-macos-arm64.dmg`
- **macOS Intel**：下载 `cursor-byok-0.0.83-macos-x64.dmg`
- **Linux 64 位**：下载 `cursor-byok-0.0.83-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」→「属性」，看「系统类型」显示"64 位操作系统"就下载 x64，显示"32 位"才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。

### macOS DMG 授权与首次打开
当前版本构建流程只给 `.app` 使用 ad-hoc 临时签名，未执行 Apple Developer ID Application 正式签名、未公证，也没有装订 Apple 公证票据。因此，首次打开时如果 macOS 阻止启动，请在 Finder 中右键应用选择"打开"并确认；仍被拦截时，到"系统设置 → 隐私与安全性"中点击"仍要打开"。仅对确认来源可信的文件执行此操作。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」后选择「仍要运行」。