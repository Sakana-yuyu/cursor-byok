# 云端原生提示词获取（A2/A3）落地说明

> 日期：2026-08-06 · 关联：docs/superpowers/specs/2026-08-06-native-cursor-tools-prompts-design.md §4（A 期）
> **A2 已验证完成：Cursor 3.14.7 服务端不存在主提示词下发端点，A3 数据源缺失。详见 [protocol-notes.md](./protocol-notes.md)。**

## 1. 已确认的事实（逆向结论）

- **主 agent 系统提示词不在本地客户端**：主 bundle 43 个 js + 全部 26 个扩展均无明文模板。
- **提示词由服务端下发**：`aiserver.v1.AiService` 的 unary RPC（`GetChatPromptResponse{prompt, token_count}` 消息已提取，RPC 提取不完整；已确认存在的相关 RPC：`GetSimplePrompt(query, answer_placeholder)`、`GetPassthroughPrompt(query, model_name)`）。
- **BYOK 模式下客户端不拉提示词**：本地 backend 的 `AiHandler`（`internal/backend/forwarder/ai_handler.go`）对未注册的 AiService 方法返回 404，客户端在 BYOK 下走本地提示词（即 cursor-byok 自编资产）。
- **获取云端提示词的唯一路径**：用官方账号 token 直连 `api2.cursor.sh` 的 AiService RPC。项目已有完整账号能力：`internal/cursoraccount`（AccessToken/RefreshToken 管理，`backendURL = https://api2.cursor.sh`）。

## 2. A2：协议验证（已完成，2026-08-06 实测）

实测结论（官方账号 + 完整客户端头，详见 `docs/native-prompts/protocol-notes.md`）：

| 端点 | 结果 |
|---|---|
| `GetChatPrompt` | 404（路由不存在） |
| `GetSimplePrompt` | 200（返回 answer 占位符，非系统提示词） |
| `GetPassthroughPrompt` | unimplemented |
| `StreamPriomptPrompt`（Priompt 组装端点） | unimplemented（服务端未启用） |

**结论**：主 agent 提示词由服务端在会话流中生成并固化到 `conversation_state.root_prompt_messages`，无独立下发端点。A3 数据源不存在。

**经验**：api2 对缺失客户端身份头（`x-cursor-client-version` 等）的请求返回 `resource_exhausted` 风控；补齐版本/设备/时区头后认证正常。

## 3. A3：promptsync（端点开放前的接入点，已就绪）

已实现：`internal/backend/promptsync/` + `cmd/fetch-native-prompt`
- `Fetch(ctx, mode)`：依次尝试 GetChatPrompt → GetSimplePrompt → StreamPriomptPrompt → GetPassthroughPrompt，任一成功即返回
- `Save(mode, result)` / `Load(mode)`：缓存到 `appdata/native-prompts/`
- 服务端开放提示词端点或协议变化时，直接重跑 `go run ./cmd/fetch-native-prompt --sync` 即可接入；`DefaultPromptCompiler` 的"原生模板模式"渲染接入点按 protocol-notes.md 中的结论在端点可用后再实现