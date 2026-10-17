## v1.0.13

### Cursor 新版兼容

- **适配 Cursor 3.22.12 协议格式**：支持新版 webpack 模块封装与紧凑 protobuf 描述，更新本地协议，并保留旧会话回放所需的兼容类型和模型上下文元数据。
- **修复 Shell 工具通知与历史回放兼容性**：使用新版结构化通知配置，兼容旧会话中的编码格式，保留历史参数精度与稳定性。
- **完善 MCP 错误反馈**：需要认证的 MCP 服务会向模型明确提示；服务目录和定向工具查询保留对应错误，避免查询失败时混入其他服务的工具。
- **改善 Canvas 状态提示**：识别新版工作文件路径，区分异步处理、保存失败与未知保存状态，并完善诊断文本截断。

### 新功能

- **本机 External API**：可在设置中开启 `/byok/v1` 接口，让本机应用使用已启用的第三方 OpenAI / Anthropic 模型通道。
- 支持模型列表、OpenAI Chat Completions / Responses 和 Anthropic Messages 接口，按已配置协议严格转发，支持流式响应和取消请求。
- 默认关闭，只接受环回连接并要求本机 API Key；设置页支持生成 Key、显式保存和复制接口地址。

### 更新说明

- 保持现有 Go / Wails / Vue 架构；本次核对的上游新增提交为文档更新。
- 新版协议中尚未接入本地执行链的工具仍按能力清单标注，不承诺所有 Cursor 新工具均已支持。
- 已完成本地协议提取、自动化测试和构建检查；真实供应商调用及完整 Cursor 桌面联调仍需在实际使用环境验证。

> **Windows 用户注意**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

### 下载哪个文件？（按系统选择）

> 名字里的 x64 / x32 / arm64 表示 CPU 架构，认准自己系统的类型下载即可。

- **Windows 64 位（绝大多数 Windows 电脑）**：下载 `cursor-byok-1.0.13-windows-x64-installer.exe`（安装版，推荐）或 `cursor-byok-1.0.13-windows-x64.zip`（绿色版）
- **Windows ARM64（骁龙/麒麟等 ARM 处理器的 Windows 电脑）**：下载 `cursor-byok-1.0.13-windows-arm64-installer.exe` 或 `cursor-byok-1.0.13-windows-arm64.zip`
- **Windows 32 位（很老的低配电脑才需要）**：下载 `cursor-byok-1.0.13-windows-x32-installer.exe` 或 `cursor-byok-1.0.13-windows-x32.zip`
- **macOS Apple Silicon（M1/M2/M3/M4 芯片）**：下载 `cursor-byok-1.0.13-macos-arm64.dmg` 或 `cursor-byok-1.0.13-macos-arm64.tar.gz`
- **macOS Intel**：下载 `cursor-byok-1.0.13-macos-x64.dmg` 或 `cursor-byok-1.0.13-macos-x64.tar.gz`
- **Linux 64 位**：下载 `cursor-byok-1.0.13-linux-x64.tar.gz`

Windows 系统类型可在「设置 → 系统 → 关于 → 系统类型」查看；macOS 在「关于本机」查看芯片或处理器。
