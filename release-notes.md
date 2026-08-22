## v0.0.98

### 新功能

- **Cursor 多账户管理**：控制中心支持保存多个 Cursor 账号——官方 OAuth 登录、从本地 Cursor 客户端导入登录态、Token 导入、恢复包导入/导出；账号支持标签管理，可一键「设为控制面当前账号」或安全切换 Cursor 客户端登录态，切换后自动刷新各厂商余额。
- **模型来源隔离**：第三方 API 模型与 Cursor 账户模型在配置、凭据、缓存与轮询游标上彻底分域——渠道身份纳入模型来源，账户凭据不会注入第三方适配器，账户模型禁止配置第三方接口地址/API Key/自定义请求头，本地响应缓存也按来源隔离，同名模型不再互相污染健康状态。
- **智能渠道路由**：模型候选渠道按用量窗口快照自适应打分排序，策略持久化保存并应用到实时流，高频使用与低失败率的渠道自动获得更高优先级。
- **免凭据 Profile**：模型配置可保存为不含 API Key 的 profile 并一键应用到其他模型；控制中心支持 profile JSON 导入。
- **控制中心完整开放**：所有页签与配套 API 全部启用，含请求比对实验室（对比脱敏后的官方与本地请求形态）与委派快照安全控制台。
- **Agent 机制移植与厂商优化**：移植 E1-E6 六项 agent/模型机制，叠加 V1-V4 厂商专属流式与请求优化（含双向工具 admission、schema 400 自动恢复、多协议兼容强化）。
- **计费查询优化**：余额查询全局开关、失败负缓存（查询失败不再反复打爆供应商接口）、内置常见模型价格补全。

### 修复

- **OpenAI Responses 流终态收紧**： Responses 协议流的完成判定更严格，避免异常截断被误判为成功。
- **本地响应缓存默认关闭**：只有用户显式开启才改变 provider 请求路径，持久化开关支持热加载。
- **账户模型诊断阻断**：账户模型不再触发第三方连通性测试/探测，订阅通知编排事件正确接纳。
- **E/V 系列全面修复**：对移植的 E1-E6 与 V1-V4 机制做了集中 bug 修复与评审收口。

> **升级提示**：本版本渠道身份算法纳入了模型来源，历史未显式指定 id 的第三方模型首次加载会重新生成渠道 id，对应渠道的健康状态与本地缓存会重置一次，属预期行为。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击“更多信息”后选择“仍要运行”。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-0.0.98-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-0.0.98-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-0.0.98-windows-arm64-installer.exe` 或 `cursor-byok-0.0.98-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-0.0.98-windows-x32-installer.exe` 或 `cursor-byok-0.0.98-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-0.0.98-macos-arm64.dmg` 或 `cursor-byok-0.0.98-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-0.0.98-macos-x64.dmg` 或 `cursor-byok-0.0.98-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-0.0.98-linux-x64.tar.gz`

**如何判断自己的 Windows 是多少位**：右键“此电脑”->“属性”，查看“系统类型”。显示“64 位操作系统”下载 x64，只有显示“32 位”才下载 x32。macOS 可在“关于本机”查看芯片是 Apple 还是 Intel。
