# 流式吞吐与 TTFT 优化设计

## 背景

用户反馈同一模型在 NewAPI 测速约为 60-70 t/s，但通过本软件使用时界面只显示约 20 t/s，且实际等待和输出体感明显更慢。

对 NewAPI 源码和一次真实 Cursor 请求进行链路对齐后，已确认两类问题：

1. 测速指标口径不同。NewAPI 使用总 `output_tokens /（首个有效上游 SSE 到流结束的时间）`，其中 `output_tokens` 包含 reasoning token；本软件当前从首段可见正文开始计时，但仍可能使用包含 reasoning 的总输出 token，因此无法同时准确表达 NewAPI 总生成吞吐和用户看到的正文速度。
2. 真实请求的主要延迟不是本地 SSE 转发，而是上游首响应前等待。样本请求的 TTFT 为 24.6 秒，输入约 69k token，Cursor 明确提交 `thinking_effort=max`；该运行时值会覆盖渠道配置的 `reasoningEffort=medium`。历史中三个 `Ls` 结果又回放了约 77k 字符的完整目录树。

真实样本的 69 个可见正文 delta 在上游、forwarder 和 RunSSE 三层完全一致，本地转发延迟 P95 约 0.535 ms。因而本设计不增加本地批处理、人工节流或刷新定时器，而是减少无效输入、约束意外的高强度推理，并把测速指标拆分为可解释的独立维度。

## 目标

- 让软件测速提供与 NewAPI 可直接比较的总生成 TPS。
- 同时提供反映用户可见输出速度的正文 TPS，避免把 reasoning token 计入正文速度。
- 分别显示首个有效事件延迟和首个可见正文延迟。
- 让渠道配置能够限制客户端请求的最高推理强度，避免 Cursor 的 `max` 无意覆盖用户配置的 `medium`。
- 在不修改 canonical history、不删除历史 reasoning、不重排消息的前提下，缩短陈旧 `Ls` 结果的 provider 投影。
- 保持工具调用 ID、消息相对位置、重试确定性和所有现有工具的真实可达性。

## 非目标

- 本批不实现工具 schema Top-K 或稀疏激活。当前没有完整的 provider 可调用工具发现入口，贸然移除 schema 会破坏低频工具可达性。
- 本批不自动降低当前 Cursor 会话中用户显式选择且不超过渠道上限的推理强度。
- 本批不修改已安装 Cursor 客户端、bundle 或签名。
- 本批不删除、重排或抑制历史 `reasoning_content`。
- 本批不立即引入 48k/64k 延迟软预算；先用 `Ls` 裁剪和推理上限完成真实 E2E，再根据收益和 prefix-cache 证据决定是否单独设计。

## 方案概览

本次采用三项相互独立、可分别回滚的修改：

1. **双 TPS 与双首响指标**：测速层分别记录首个有效 provider 事件和首个可见正文事件，分别计算总生成 TPS 与正文 TPS。
2. **渠道推理强度上限**：将现有渠道 `reasoningEffort` / `anthropicThinkingEffort` 解释为允许的最高强度。运行时请求可选择更低强度或 `disabled`，但不能高于渠道上限。
3. **陈旧 `Ls` 投影压缩**：当前轮保留原始结果；历史轮的大型 `Ls` 结果优先使用已有 `result_text`，并附带根路径、文件数和截断说明。只改变 provider 投影，不修改 `context.json`。

## 指标设计

### 时间点

测速过程记录以下时间点：

- `startedAt`：请求开始。
- `firstResponseAt`：收到首个有效 provider 流事件，包括 reasoning、正文或工具相关有效输出；空心跳和纯协议终止事件不算。
- `firstTextTokenAt`：收到首个非空可见正文 delta。
- `finishedAt`：流正常结束或确认终止。

### Token 计数

- `outputTokens`：provider usage 返回的总输出 token；若 provider 未提供，则使用现有文本估算并明确标记为估算值。
- `visibleOutputTokens`：仅对累计可见正文进行估算。provider 通常不单独返回正文 token，因此该指标必须标记为估算值。
- `reasoningTokens`：provider usage 可用时记录；不可用时为空，不用总输出减正文估算值制造伪精确结果。

### 公式

- NewAPI 对齐总生成 TPS：`outputTokens / (finishedAt - firstResponseAt)`。
- 可见正文 TPS：`visibleOutputTokens / (finishedAt - firstTextTokenAt)`。
- 首响应延迟：`firstResponseAt - startedAt`。
- 首字延迟：`firstTextTokenAt - startedAt`。

分母小于等于零、缺少必要时间点或 token 数小于等于零时返回 0，不生成无穷值或负值。

### 兼容性

保留现有 `tokensPerSecond` JSON 字段，但将其固定定义为 NewAPI 对齐总生成 TPS，避免前端和现有绑定失效。新增字段：

- `visibleTokensPerSecond`
- `firstResponseMS`
- `visibleOutputTokens`
- `reasoningTokens`
- `effectiveThinkingEffort`

现有 `firstTextTokenMS`、`totalDurationMS` 和 `outputTokens` 保留。前端摘要优先显示总生成 TPS、正文 TPS、首响应和首字；详细区域显示 token 构成与实际推理强度。

## 推理强度上限

### 强度顺序

统一顺序为：

`disabled < low < medium < high < xhigh < max`

### 解析规则

1. 渠道配置值作为 `configuredMaximum`。
2. Cursor 未提交有效运行时强度时，使用渠道配置值。
3. Cursor 提交 `disabled` 时始终允许关闭。
4. Cursor 提交的强度低于或等于渠道上限时，使用运行时值。
5. Cursor 提交的强度高于渠道上限时，使用渠道上限。
6. 渠道值为空时沿用当前行为，允许有效运行时值直接生效，避免对旧配置引入隐式限制。
7. OpenAI/Gemini 与 Anthropic 共用比较规则，但继续通过现有协议字段分别下发。

最终生效值必须写入现有 request knobs 和 debug 记录，至少区分：

- `runtime_thinking_effort`：Cursor 原始请求值。
- `configured_thinking_effort_maximum`：渠道上限。
- `effective_thinking_effort`：实际发送给 provider 的值。

这样测速、请求指标和 debug 日志都能解释为何 `max` 最终按 `medium` 执行。

### UI 语义

模型编辑器中的“推理强度”改为“推理强度上限”，“思考强度”改为“思考强度上限”。提示明确说明：客户端可以请求更低强度或关闭，但不能超过此值；留空配置继续使用兼容行为。

当前配置结构仍要求有效枚举值，因此本批不新增“无限制”选项，也不迁移已有配置。已有 `medium` 配置自然变为 `medium` 上限。

## 陈旧 Ls 投影压缩

### 适用条件

- 工具名为 `Ls`。
- 结果被 `isHistoricalReplayToolResult` 判定为历史结果。
- 内容超过现有陈旧工具结果阈值。

当前轮和最近需要继续决策的结果不做激进裁剪，避免模型刚列完目录就丢失所需文件名。

### 输出优先级

1. 若历史 entry 已有非空 `result_text`，以该文本作为主体。
2. 从结构化 `Ls` 结果中提取根路径和文件总数；解析失败时不猜测字段。
3. 附加固定截断说明，告知模型精确目录树已从历史投影省略，需要时重新调用 `Ls`。
4. 最终仍受 `staleToolResultReplayLimit` 限制。

示例语义：

```text
ls success path=E:\MyProject\cursor-byok files=768

[历史 Ls 目录树已从 provider 回放中省略；如需精确内容请重新调用 Ls]
```

### Prefix cache 约束

- 不写回或重写 `context.json.items`。
- 不改变 entry 顺序、tool call ID、tool result ID 或相对消息位置。
- 相同 canonical history 必须产生确定性一致的投影。
- 不基于当前时间、随机数或运行期不可重复状态生成裁剪文本。

## 数据流

1. Cursor 的 `run_request` 进入 forwarder，提取运行时 thinking effort。
2. projector 从 append-only history 构造 provider replay，并在历史 `Ls` 工具结果处生成精简投影。
3. compiler 继续加载完整的当前 mode 工具目录，本批不移除任何工具 schema。
4. router 解析渠道配置，将运行时强度限制在渠道上限内，并记录原始值、上限和实际值。
5. provider adapter 发送最终请求。
6. 测速或真实请求观察器分别记录首个有效事件、首段正文和结束时间。
7. UI 展示总生成 TPS、正文 TPS、两类首响和实际推理强度。

## 错误处理

- provider 不返回 usage：总输出 token 回退现有估算逻辑，并标记 `tokensEstimated=true`。
- provider 不返回 reasoning token：字段保持 0 或未提供状态，不推算。
- 流只有 reasoning、没有正文：可计算总生成 TPS，但正文 TPS 为 0，并保持“无正文返回”的现有错误语义。
- `Ls` 结构化结果解析失败：使用 `result_text`；若也为空，则按通用陈旧结果中间截断，不丢弃整个结果。
- 未识别的运行时强度：视为未提交，使用渠道配置。
- 渠道上限无效：配置归一化继续拒绝保存，不在请求路径静默修复。

## 测试策略

### 单元测试

- TPS：reasoning 先于正文时，总 TPS 从首个 reasoning 事件计算，正文 TPS从首段正文计算。
- TPS：usage 包含 reasoning token 时，不再把总输出 token 除以正文跨度。
- TPS：缺失 usage、缺失正文、零时长和负时长边界。
- 推理上限：覆盖所有六档的低于、等于、高于、关闭、空配置和非法运行时输入。
- Provider：OpenAI/Gemini/Anthropic 最终请求字段与 `effective_thinking_effort` 一致。
- `Ls`：当前轮保持完整；陈旧大结果使用 `result_text`；根路径和文件数保留；解析失败走安全回退。
- Prefix cache：同一历史重复投影字节一致，消息顺序和 tool call ID 不变。
- 工具目录：所有 mode 现有工具名称集合保持不变。

### 静态验证

```text
gofmt
go test -p 1 ./...
go vet -p 1 ./...
go build -p 1 ./...
git diff --check
前端 production build 与 i18n 静态扫描
```

### 真实 Cursor E2E

使用同一渠道、模型和等价请求分别执行 `disabled`、`medium`、`max`：

- 渠道上限设为 `medium` 时，确认 `max` 请求的实际 provider 参数为 `medium`。
- 对比修改前后的 prompt tokens、TTFT、首字延迟和总耗时。
- 核对上游正文 delta 数、forwarder delta 数和 RunSSE delta 数完全一致。
- 核对正文无拼接差异、无本地批处理等待、无工具不可达回归。
- 对比相邻请求最长公共前缀，确认历史投影稳定且未破坏 prefix cache。

首批验收不要求所有模型达到固定 TTFT 数值，因为上游负载会波动；要求同一历史投影的输入 token 明确下降，推理上限真实生效，且本地流转不增加延迟。

## 发布与回滚

修改拆成可独立回滚的提交：

1. 指标口径和 UI 展示。
2. 推理强度上限。
3. 历史 `Ls` 投影压缩。

任一行为出现兼容性问题时，可直接 revert 对应提交。所有修改仅影响本地模型请求与配置展示，不涉及支付、资金、数据库迁移或生产数据。当前任务只创建本地 commit，不 push。

## 后续工作

真实 E2E 完成后，再以独立设计评估：

- 普通请求 64k、`max` 推理 48k 的延迟软预算。
- 后台 sidecar projection 的生成时机、指纹和失效机制。
- 核心常驻、状态强制激活、BM25 Top-K 与真实工具发现入口组成的工具 schema 稀疏激活。

这些后续优化必须继续满足 canonical history append-only、reasoning replay 完整和所有工具真实可达三个约束。
