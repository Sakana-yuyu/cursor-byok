# Daoxe 模型自动适配设计

## 目标

让 `https://api.daoxe.com` 能通过现有模型配置流程自动拉取模型、选择正确的 OpenAI 兼容协议、探测可请求模型，并避免未配置渠道上限时发送 `max_tokens=65536` 导致 Neurons 返回 `limit (4096)`。

本设计不把用户提供的 API Key 写入源码、设计文档、测试夹具、日志或提交；真实 Key 只用于本次临时外部探测。

## 已确认的接口行为

- `GET /v1/models` 返回 OpenAI 风格模型目录，共 49 个模型。
- `POST /v1/chat/completions` 是当前可用的文本模型请求入口。
- `POST /v1/responses` 返回 `500 not implemented`，不能作为 Daoxe 的默认入口。
- 部分模型接受 `max_tokens=3`，但拒绝 `max_tokens=65536`，错误文本包含真实限制 `4096`。
- 其他模型可以接受更大的输出上限，因此不能把 4096 当作所有模型的永久能力值。

## 方案选择

### 方案一：所有供应商固定 4096

实现最简单，但会无条件压低官方 OpenAI、Anthropic 和其他高上限模型的能力，放弃。

### 方案二：保持 65536，仅在 400 后降级

可以保留大模型能力，但 Daoxe 的 4096 模型第一次请求必然失败，且会把 provider 错误暴露到 Cursor 回合，放弃。

### 方案三：按协议设置安全默认值，错误时按渠道学习真实上限

采用该方案：

1. 新增 Daoxe 供应商模板，固定 `https://api.daoxe.com/v1/models`、`/v1/chat/completions` 和 `chat_completions`。
2. 未显式设置 `maxCompletionTokens` 的 OpenAI/Gemini 渠道使用 4096 的安全默认输出预算；Anthropic 的默认预算保持不变。
3. 用户显式配置的上限仍然有效。
4. provider 返回更小上限时，复用已有错误解析、当前回合重试和按渠道持久化机制；只缩小上限，不放大已知更小上限。
5. 模型目录、模型名推断、并发可用性探测和批量添加继续走现有路径，不新增第二套模型注册或请求系统。

## 数据流

```text
Daoxe 模板
  -> GET /v1/models
  -> 按模型名/渠道推断 provider 与 chat_completions
  -> 轻量探测（小 max_tokens、短提示、并发受限）
  -> 用户选择或探测成功后批量写入 adapter
  -> 请求解析时使用显式上限或 4096 默认值
  -> provider 若返回真实更小上限
  -> 解析限制、当前回合重试、按 channelID 持久化
```

## 组件边界

- `frontend/src/utils/supplierCatalog.js`：只登记 Daoxe 的展示、连接和目录元数据，不保存凭据。
- `internal/backend/server/config/resolver.go`：为未配置渠道提供按现有协议结构兼容的安全默认输出上限。
- `internal/client/model_adapter_benchmark.go`：模型测试默认预算同步降低，避免“测试能发出 65536、正式请求才失败”的分裂行为。
- `internal/backend/forwarder/max_tokens_recovery.go`：沿用现有 400 识别、限制解析、重试和渠道持久化；仅补充回归覆盖。
- 现有 `model_catalog.go`、`model_probe.go`、`router.go` 与 OpenAI 适配器保持协议职责不变。

## 错误与边界

- `/responses` 不支持时，Daoxe 模板不会先选择该入口；其他自动模式仍可使用现有端点回退。
- 模型目录中的 embedding、图像、语音等非文本模型可能被 Chat Completions 拒绝；探测失败时只排除该模型，不把供应商判定为不可用。
- 超时、额度不足、模型不存在、模型自身不支持文本请求分别记录为失败原因，不把它们误记成 max_tokens 上限。
- 不修改用户显式配置的大上限；如果它确实超过 provider 真实限制，按已有恢复机制在错误后缩小并保存。
- 所有测试与日志不得输出完整 API Key。

## 验收标准

1. Daoxe 模板可被供应商选择器发现，目录地址和请求模式正确。
2. 未配置 OpenAI 输出上限的渠道不会再默认发送 65536；Anthropic 默认行为不变。
3. `max_tokens (65536) exceeds limit (4096)` 可被识别并解析为 4096，重试预算不超过 4096。
4. 现有模型目录和可用性探测测试通过。
5. 使用本次账户做低预算真实探测，记录 49 个目录模型的成功、明确不支持、额度不足和超时分类；不把未经充分重试的超时声明为模型永久不可用。
6. Go 单元测试、前端单元测试、前端 lint/build 与 `git diff --check` 通过。

## 风险与回滚

- 风险：未配置上限的 OpenAI/Gemini 渠道默认输出从 65536 降为 4096，换取兼容中转站的首请求成功；用户可显式填写更高值。
- 风险：真实模型探测会消耗少量供应商额度，因此只使用最短提示和极小输出预算，并限制并发。
- 回滚：删除 Daoxe 模板并恢复三个默认常量即可回退代码行为；不涉及数据库迁移，也不修改已安装 Cursor 客户端。
