## v1.0.23

### 修复

- **启动失败不再遗留半启动服务**：代理端口被占用、登录态备份失败等情况下，逆序清理本轮启动的组件并恢复登录态，不停止原本已运行的后端；恢复失败会明确报错。
- **保护 Cursor 原有配置**：配置无法解析时保留原文件，不再删除重建。代理设置与 `NODE_EXTRA_CA_CERTS` 在修改前保存恢复快照，停止时只恢复仍由助手接管的值，保留用户期间的修改；未接管时清理不再删除原有配置或登录态。
- **连续保存设置可能互相覆盖**：改为按字段排队保存，在执行时读取最新配置，避免旧快照撤销刚保存的其他设置；模型列表操作也纳入同一写入队列。
- **Token 统计可能重复或短暂显示旧值**：用量事件按 ID 合并最新值后再统计，统一缓冲区与磁盘口径。
- **快速离开请求明细页后仍在后台刷新**：忽略离页后的异步结果，不再重新启动轮询或注册事件。
- **取消子代理后实际并发可能超限**：任务可及时显示取消，但并发名额会等执行器真正退出后才释放。
- **修复 CA 后代理仍使用旧证书**：停止服务后重建代理并同步签名证书；运行中修复会明确提示先停止服务，避免显示修复成功但代理仍使用旧 CA。
- **限流冷却结束后退避历史被提前清空**：冷却到期只恢复渠道可选性，保留遗忘窗口内的失败历史；统一普通路由和会话粘性的判断。
- **单密钥并发上限热更新不准确**：扩容优先唤醒已排队请求，缩容等待在途请求释放，已取消请求不再抢占空闲名额。
- **显式指定的 Skills 可能未被激活**：改进 `/技能名`、`$技能名` 及中文相邻文本的识别，多个显式技能按提及顺序保留，并遵守技能开关、来源和排除设置；统一技能目录与使用说明。

### 工程验证

- 新增配置并发保存、宿主设置恢复、启动故障回滚、用量去重、取消并发及 CA 实际 TLS 握手等回归测试。
- 将前端单元测试接入 CI 必经验证，与现有 lint、浏览器 E2E 及后端验证共同阻断失败构建。
- 升级存在已知漏洞的依赖，并固定修复后的 Go 工具链。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，请先确认下载来源和文件可信；可信时可点击「更多信息」->「仍要运行」。

### 下载哪个文件？（按系统选择）

- **Windows 64 位**：`cursor-byok-1.0.23-windows-x64-installer.exe`（安装版）或 `cursor-byok-1.0.23-windows-x64.zip`（绿色版）
- **Windows ARM64**：`cursor-byok-1.0.23-windows-arm64-installer.exe` 或 `cursor-byok-1.0.23-windows-arm64.zip`
- **Windows 32 位**：`cursor-byok-1.0.23-windows-x32-installer.exe` 或 `cursor-byok-1.0.23-windows-x32.zip`
- **macOS Apple Silicon**：`cursor-byok-1.0.23-macos-arm64.dmg` 或 `cursor-byok-1.0.23-macos-arm64.tar.gz`
- **macOS Intel**：`cursor-byok-1.0.23-macos-x64.dmg` 或 `cursor-byok-1.0.23-macos-x64.tar.gz`
- **Linux 64 位**：`cursor-byok-1.0.23-linux-x64.tar.gz`

## v1.0.22

### 修复

- **对话经常显示「上游对话被截断」**：中转站在回复中途断流时，之前直接判定本轮失败。现在会保留已输出的内容，提示模型从断点继续，按 2/5/10 秒退避自动续写，最多 3 次；本轮已执行过工具时不续写，避免重复执行。
- **长回复被输出上限截断**：回复因 `max_tokens` 截断且已有正文时，自动续写（最多 3 次），用尽后才提示截断。
- **Claude 拒绝回答时可能反复请求、工具照样执行**：Anthropic 返回 `refusal`（Gemini 安全拦截同理）时不再续写和缓存；被拒回合里流出的工具调用一律丢弃，不会执行。
- **中途发新消息后，后台子代理的结果要再等一轮才看得到**：子代理在新一轮进行中跑完时，结果会在下一次请求模型前补进当前回合。
- **上下文用量一直显示 0**：部分中转站/本地模型不返回 usage，现在用请求前的估算值兜底，自动压缩也能正常触发。
- **ReadLints 传多个文件只检查了第一个**：结果里会注明哪些文件没有检查，避免模型误以为它们没有报错。
- **思考强度在模型名和菜单里显示不一致**：参数 ID 与新版 Cursor 保存的键对齐。

### 兼容

- **适配 Cursor 3.27.22**：协议提取工具适配新版打包格式，并按新版安装包重新生成协议；旧版 Cursor 客户端仍兼容。
- **MCP 工具报错更可读**：附带 HTTP 状态、建议重试间隔，以及工具定义变化（新的参数格式 / 可用工具列表），模型可以自行纠正调用。
- **命令因内存不足被终止时会注明原因**，不再只看到截断输出和退出码。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.22-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.22-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.22-windows-arm64-installer.exe` 或 `cursor-byok-1.0.22-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.22-windows-x32-installer.exe` 或 `cursor-byok-1.0.22-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.22-macos-arm64.dmg` 或 `cursor-byok-1.0.22-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.22-macos-x64.dmg` 或 `cursor-byok-1.0.22-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.22-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。

## v1.0.21

### 修复

- **双击图标主窗口不出来了**：之前「静默启动」设置会作用于每一次启动，勾选后双击图标主窗口直接隐藏到托盘。现在静默只在开机自启动等明确标记的场景生效，**手动双击永远显示主窗口**。
- **运行中再双击没反应**：之前第二次启动只会记一条日志然后被忽略，看起来像软件打不开。现在会把已存在的主窗口直接唤起并聚焦（开机自启动的静默拉起除外）。
- **最小化后点托盘图标窗口消失**：之前托盘点击会把「最小化」误判成「可见」而把窗口彻底藏掉，现在最小化状态下点托盘是恢复并聚焦。
- **模型目录批量添加会把主窗口关掉**：之前「快速添加模型」完成后直接 Close 当前窗口，而该页面可从主窗口侧边栏进入——现在主窗口内完成后返回模型配置页，只有独立弹出的编辑窗才会自动关闭。

### 兼容

- **Claude 系模型发 `Bash` 工具调用不再报错**：Claude Code 习惯把 Shell 工具写成 `Bash`（移植上游 leookun/cursor-byok 修复），工具名大小写变体（`shell`/`SHELL`/`Bash`）统一归一，不再返回 `unsupported exec tool`。
- **Shell `block_until_ms` 严格校验**：只接受整数值毫秒数（整数值浮点如 `15000.0` 合法），小数、负数、超出范围的值不再被静默截断，而是明确报参数错误。
- **Claude Code 伪装指纹升级**：渠道请求 UA 更新为 `claude-cli/2.1.177`，自定义 Headers 默认预设补齐官方 `anthropic-beta` 能力声明（context-1m、interleaved-thinking、prompt-caching 等），更接近真实 Claude Code 客户端。
- **供应商模型预设刷新**：智谱 GLM 更新到 5.3/5.2/4.7，Kimi 更新到 K3（Kimi For Coding 端点补 `k3`/`kimi-for-coding`），DeepSeek 更新到 V4 Pro/Flash。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.21-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.21-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.21-windows-arm64-installer.exe` 或 `cursor-byok-1.0.21-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.21-windows-x32-installer.exe` 或 `cursor-byok-1.0.21-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.21-macos-arm64.dmg` 或 `cursor-byok-1.0.21-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.21-macos-x64.dmg` 或 `cursor-byok-1.0.21-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.21-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。

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
