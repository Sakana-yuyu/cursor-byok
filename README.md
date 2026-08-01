# Cursor Local Assistant

Cursor Local Assistant 是一个面向 Cursor 的本地模型适配与代理网关。它把供应商配置、模型路由、请求转发、用量统计和本地调试集中在桌面应用中，让你可以使用自己的模型 API，并保持 Cursor 的工具调用和 Agent 工作流。

## 能力

- 多供应商与 OpenAI 兼容接口适配，支持模型目录、模型选择和请求路由。
- Anthropic、Gemini 原生协议与常见第三方网关的请求和流式事件转换。
- 本地提示词、系统规则和请求上下文管理。
- 余额、token 用量和会话统计，支持按供应商和模型查看。
- 工具调用协议清理，降低不同网关对 Schema 严格校验造成的失败。
- Multitask 自动委派：在选择 Multitask 后，普通只读探索请求可以自动生成 Explorer 子代理，无需用户再次输入“使用子代理”。
- 委派状态同步：把运行中、成功、失败和取消状态通过 Cursor checkpoint 回传，保持 Cursor 中的 Task 卡片与实际执行状态一致。
- 本地 debug 日志与请求链路记录，便于定位模型、工具、流式传输和子代理问题。

## 下载

- [GitHub Releases](https://github.com/Sakana-yuyu/cursor-byok/releases)
- Windows 安装时若被 SmartScreen 拦截，选择“更多信息”后继续运行。

## 快速开始

1. 下载并启动对应平台的安装包。
2. 在供应商页面填写 API 地址、密钥和模型配置。
3. 拉取或手动维护模型目录，选择默认模型。
4. 完全退出并重新打开 Cursor，使本地代理配置生效。
5. 在 Cursor 中发送普通请求；选择 Multitask 后，探索型请求会自动创建本地 Explorer 委派。

## 调试

遇到 Cursor 显示 stopped、任务不显示结果或子代理状态不一致时，请保留以下信息：请求时间、Cursor 使用的模型、任务文本，以及应用目录下的 runtime/debug 日志。日志会记录 request_id、conversation_id、model_call_id、tool_call_id、exec_id、provider pass、checkpoint 和终态事件，便于按执行身份还原完整链路。

## 开发

后端使用 Go，桌面界面位于 `frontend`，协议定义位于 `proto`。常用检查命令：

```powershell
go test ./...
go vet ./...
go build ./...

Set-Location frontend
node ../scripts/run-vite-build.mjs --scan --mode production
```

## 社区

- [Telegram 群组](https://t.me/cursor_byok)
- [Releases](https://github.com/Sakana-yuyu/cursor-byok/releases)

## 贡献

提交 issue 或 pull request 时，请提供可复现步骤、相关日志片段和运行环境。涉及 Cursor 协议或客户端状态的问题，请同时说明 Cursor 版本与本地安装路径。
