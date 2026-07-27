# Cursor助手

一个本地运行的 Cursor API 适配工具：把模型配置、请求转发、提示词注入和使用统计集中到一个桌面应用里，让你自由选择模型 API，不被单一平台、订阅或计费方式绑定。

## 它能做什么

- **自带 Key，自由切换模型**：把 OpenAI、Anthropic、Gemini 以及任意 OpenAI 兼容接口接入 Cursor，不再绑定单一平台。
- **多供应商兼容**：内置 Kimi、OpenRouter、DeepSeek、通义千问 / DashScope、GLM、MiniMax、StepFun、xAI / Grok、GitHub Copilot 等常见网关的兼容规则。
- **Gemini 原生协议**：支持 `gemini_native` 协议组，覆盖请求构造、流式文本 / 思考 / 工具调用 / 用量事件的完整桥接。
- **请求转发与提示词注入**：在本地统一接管 Cursor 的请求，可注入自定义系统提示。
- **使用统计与余额查询**：按模型、按供应商统计请求量与 token，并支持自定义余额查询接口。
- **工具 Schema 自动清理**：统一修复 `required: null`、空 parameters 等严格校验问题，减少兼容网关返回 400。

## 下载

- [Releases](https://github.com/Sakana-yuyu/cursor-byok/releases)
- 应用内支持自动更新，默认从本仓库的 Releases 检查新版本。

> **Windows 用户**：安装时若被 SmartScreen 拦截，点击「更多信息」->「仍要运行」即可。

## 使用教程

[详细教程](https://dcne38qm5vlg.feishu.cn/wiki/JeP7wdGnziBXuikNaF5czWbrn8c)

## 为什么做这个项目

公司喜欢把 Agent 服务和模型绑定在一起，让用户只能在指定模型、指定订阅、指定计费方式下使用工具。这个项目的目标是把模型选择权还给用户：开发者应该能把自己的模型 API 接入任何 IDE、Chat、Agent 或开发工具，也可以自托管整套服务，避免被单一平台锁定。

## 后续

- 支持更多 IDE 接入
- 支持更多 Chat 与 Agent 工具
- 更完善的自托管部署方式
- 持续优化不同模型 API 的兼容性，降低接入成本

## 作者

原作者主页：[Bilibili](https://space.bilibili.com/311706663/upload/video)