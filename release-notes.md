## v0.0.99

### 修复

- **拉取模型缓存失效**：修复 New API 等中转站在 token 分组变更或点击「重新拉取」后仍显示旧模型列表的问题；进入拉取页、手动刷新、连接参数变更时均强制绕过 5 分钟进程内缓存，保存配置或切换账户时也会清空模型目录缓存。
- **有效密钥误报无效**：拉取模型失败时不再将 HTTP 401 笼统映射为「密钥无效或暂无可用额度」，改为明确的模型列表鉴权提示；每次拉取前从 sessionStorage 同步最新密钥，避免从编辑页返回后仍用旧 key。
- **New API 余额查询**：修复 profile 设为 New API 但未配置访问令牌时提前失败、无法回退渠道 sk 通用策略的问题；移除 sk 兜底请求中误填 `New-Api-User` 头的兼容逻辑。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.99-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.99-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.99-windows-arm64-installer.exe` 或 `cursor-byok-0.0.99-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.99-windows-x32-installer.exe` 或 `cursor-byok-0.0.99-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.99-macos-arm64.dmg` 或 `cursor-byok-0.0.99-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-0.0.99-macos-x64.dmg` 或 `cursor-byok-0.0.99-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-0.0.99-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键「此电脑」->「属性」，查看「系统类型」。显示「64 位操作系统」下载 x64，只有显示「32 位」才下载 x32。macOS 可在「关于本机」查看芯片是 Apple 还是 Intel。
