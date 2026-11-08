## v1.0.20

### 新功能

- **会话粘性（保护上游提示词缓存）**：借鉴 magpie 网关的 prompt cache 亲和设计——同一对话在 5 分钟内的连续请求优先复用上次成功应答的渠道与密钥，不再每轮轮换，避免密钥轮换稀释上游 prompt cache，提升缓存命中率、降低 token 费用。粘性渠道失败、冷却或本轮已重试过时自动交回正常轮换，不会原地打转；新渠道成功后自动接管粘性。重启后粘性失效属预期（上游缓存同样已冷）。
- **单密钥并发上限**：模型编辑器新增「单密钥并发上限」配置——限制同一把密钥（含备用密钥池）同时在途的请求数，防止并发子代理把单把密钥打爆触发上游限流。超出上限的请求在本地排队等待（最长 30 秒），排队不算失败、不触发冷却；等待超时自动换下一个候选渠道。留空或 0 = 不限制（默认行为不变）。

### 优化

- **限流冷却更聪明**：429 且上游未给 Retry-After 时，冷却时长按连续失败次数指数退避（1 分钟 → 2 → 4 → …封顶 30 分钟），不再固定 1 分钟「冷却一结束立刻撞墙」；30 分钟无新失败自动清零计数，限流恢复后立即回归正常轮换。上游给了 Retry-After 时仍优先尊重上游信号。
- **余额/配额耗尽（402）冷却延长到 15 分钟**：比鉴权错误冷却更久，减少反复撞计费墙。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.20-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.20-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.20-windows-arm64-installer.exe` 或 `cursor-byok-1.0.20-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.20-windows-x32-installer.exe` 或 `cursor-byok-1.0.20-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.20-macos-arm64.dmg` 或 `cursor-byok-1.0.20-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.20-macos-x64.dmg` 或 `cursor-byok-1.0.20-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.20-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
