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

<!-- contributors-start -->
<table><tr>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/e91e20e8d5f83234900a3878086e1fe7?d=identicon&s=80" width="48" height="48" alt="呆呆可达鸭鸭" title="呆呆可达鸭鸭 (214 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/e32a23a62c503dd189268d84dbd12c2d?d=identicon&s=80" width="48" height="48" alt="hudawang" title="hudawang (7 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/4fa4116634f7a272554140a170e7e300?d=identicon&s=80" width="48" height="48" alt="DedSecer" title="DedSecer (5 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/92ab25d105c69286f299bdc514bef2ca?d=identicon&s=80" width="48" height="48" alt="philau2512" title="philau2512 (3 次提交)"/></a></td>
<td><a href="https://github.com/kael-odin"><img src="https://avatars.githubusercontent.com/kael-odin?v=4&s=80" width="48" height="48" alt="kael-odin" title="kael-odin (3 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/3f43639246884941cd37c80aaf1c8293?d=identicon&s=80" width="48" height="48" alt="上玄" title="上玄 (2 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/2d94f41c79230c6c7afb2023d8250167?d=identicon&s=80" width="48" height="48" alt="杨超" title="杨超 (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://avatars.githubusercontent.com/u/266937838?v=4&s=80" width="48" height="48" alt="octo-patch" title="octo-patch (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/a86b8f4b14ce67e4e6a4f3b25612e99c?d=identicon&s=80" width="48" height="48" alt="lixingcheng" title="lixingcheng (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/edd1b95493e930ffec5730df1d8ae4d7?d=identicon&s=80" width="48" height="48" alt="lixiangwuxian" title="lixiangwuxian (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://avatars.githubusercontent.com/u/41898282?v=4&s=80" width="48" height="48" alt="github-actions[bot]" title="github-actions[bot] (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/eaa039f995d17d9c5bc80586f4523276?d=identicon&s=80" width="48" height="48" alt="aike1202" title="aike1202 (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/3972eaf41431b5a058211d1262fbd2a3?d=identicon&s=80" width="48" height="48" alt="TigerWang" title="TigerWang (1 次提交)"/></a></td>
<td><a href="https://github.com/Sakana-yuyu/cursor-byok"><img src="https://secure.gravatar.com/avatar/38f67cb0e7464d31dc810917801e61ae?d=identicon&s=80" width="48" height="48" alt="GGHansome" title="GGHansome (1 次提交)"/></a></td>
</tr></table>
<!-- contributors-end -->

