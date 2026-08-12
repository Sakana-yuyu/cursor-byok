# 内置 CLI 官方安装入口证据

## 记录信息

- 检索日期：2026-08-11
- 检索方式：PowerShell Invoke-WebRequest 访问公开官方页面并检查 HTTP 响应；同时核对仓库中的执行器命令契约。
- 目的：为设置页的“官方下载”入口提供可追溯、非敏感的官方地址，避免前端按执行器 ID 分散硬编码。

## 采用来源

| 执行器 | 采用地址 | 证据 | 备注 |
| --- | --- | --- | --- |
| Claude Code | https://code.claude.com/docs/en/quickstart | 官方 code.claude.com 页面 GET 返回 200 | 旧版 Anthropic 文档路径返回 404，因此采用当前官方文档域名的快速开始页。 |
| Codex CLI | https://developers.openai.com/codex/cli/ | 官方 OpenAI Developers 页面 HEAD 返回 200 | 与 Codex CLI 官方开发者文档域名一致。 |
| Gemini CLI | https://github.com/google-gemini/gemini-cli | Google Gemini 官方 GitHub 仓库页面 HEAD 返回 200 | 仓库同时是适配器已核对的官方 @google/gemini-cli 项目来源。 |
| Kiro CLI | https://cli.kiro.dev/install | Kiro 官方安装地址已在既有可达性记录中核对 | 本次 PowerShell TLS HEAD 检查失败，不据此否定既有官方地址；未声称本机可访问 Kiro 服务。 |
| Cursor Agent | https://www.cursor.com/downloads | Cursor 官方下载页 HEAD 返回 200 | 仅在未检测到 Cursor 编辑器时展示；检测到编辑器但无活动 Agent 连接时继续提示连接状态，不误导为需要重装。 |

## 应用内安装（2026-08-12）

- Gemini CLI 继续由软件内的固定 npm 包 `@google/gemini-cli` 安装；前端只提交内置执行器 ID，安装完成后立即重新探测。
- Kiro CLI 的 Windows x64 安装改为软件内受控流程：读取 Kiro 官方稳定清单 `https://prod.download.cli.kiro.dev/stable/latest/manifest.json`，只选择清单中的 Windows x64 MSI，校验路径、声明大小和 SHA-256 后以固定 `msiexec.exe /i <临时 MSI> /quiet /norestart` 参数安装。
- Kiro 官方文档的 Windows 11 PowerShell 指令为 `irm 'https://cli.kiro.dev/install.ps1' | iex`，但应用不执行远程脚本文本，避免把下载内容直接交给 shell。
- 设置页不再显示会触发内嵌浏览器窗口的“官方下载”链接。受支持 CLI 以带可访问名称的安装按钮触发；Cursor Agent 与未受支持的执行器不显示下载入口。
- 安装仍必须由用户点击发起。同一执行器并发安装会被拒绝；安装失败会显示现有错误区域，安装成功后按当前探测结果显示可用、需要登录/API Key 或不兼容状态。

## 实现约束

- 地址只作为公开安装导航元数据传递到前端，不包含 API Key、认证头或用户配置。
- 自定义 CLI 不自动生成安装地址；只有内置执行器声明官方入口。
- 仅当探测状态为“未安装”时显示受支持的应用内安装按钮，避免对已安装执行器造成干扰。Cursor Agent 检测到编辑器但未连接时属于“需要操作”，不显示安装入口。
