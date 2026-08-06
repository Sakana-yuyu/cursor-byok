# 云端提示词端点验证记录（A2 实测结果）

> 日期：2026-08-06 · 账号：官方账号（已登录） · 客户端版本：Cursor 3.14.7（api2.cursor.sh）

## 结论摘要

**Cursor 3.14.7 的服务端没有可用的"主 agent 提示词下发"端点。** 主 agent 系统提示词由服务端在会话流（StreamChat/BidiAppend）中生成并固化到 `conversation_state.root_prompt_messages`，客户端本地不含模板、也不存在独立的提示词拉取 RPC。A3（promptsync 云端拉取）在端点开放前无法获得数据源。

## 各端点实测结果（官方 token + 完整客户端头，认证均通过）

| 端点 | 结果 | 说明 |
|---|---|---|
| `AiService/GetChatPrompt`（两种路径） | **404 Route not found** | 路由不存在（proto 提取的 `GetChatPromptResponse` 为客户端旧消息定义，服务端已无该路由） |
| `AiService/GetSimplePrompt` | **200** | 语义为"根据 query 生成回答占位符"，返回的是 `answer_placeholder` 的加工文本，非系统提示词；不带客户端版本头时返回 `resource_exhausted`（风控） |
| `AiService/GetPassthroughPrompt` | **unimplemented** | 路由存在但服务端未实现 |
| `AiService/StreamPriomptPrompt`（3 种 props type name） | **unimplemented** | Cursor 的 Priompt 提示词组装端点，服务端未启用 |

## 关键经验

1. **请求头完整性**：api2.cursor.sh 对缺失客户端身份头（`x-cursor-client-version`/`x-cursor-client-commit`/`x-cursor-client-type` 等）的请求返回 `resource_exhausted` 风控。补齐版本/设备/时区头后认证正常。头清单来自客户端 bundle 的请求构造代码（`workbench.glass.main.js`）。
2. **客户端本地确无提示词模板**（43 个 js + 全部扩展穷尽搜索），`system_reminder`/`Request ID:` 等命中均为消息字段或 UI 文案。

## 获取官方提示词的唯一可行路径（可选后续）

在**官方模式**（不连 BYOK）下用 Cursor 客户端跑一次真实对话，抓取发往 `api2.cursor.sh` 的 `StreamChat`/`BidiAppend` 请求/响应，从会话流或返回的 checkpoint（`root_prompt_messages`）中提取服务端组装的 system prompt。工具：项目 MITM（`internal/mitm`）或 Fiddler/Charles。此路径消耗一次真实会话额度。

## 后续建议

- A3 `promptsync` 保留为"端点开放/协议变化时"的接入点（`cmd/fetch-native-prompt` 脚本已就绪，可随时重试）
- 本地逆向资产（Auto-review 分类器、工具定义）不受影响，B 期接线成果完整可用