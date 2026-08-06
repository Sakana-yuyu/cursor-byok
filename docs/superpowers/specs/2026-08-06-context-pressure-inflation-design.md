# 上下文压力膨胀（Context Pressure Inflation）设计评估

> 日期：2026-08-06 · 状态：评估完成，建议暂缓实施 · 关联：`docs/cursor2api-gateway-comparison-and-optimization.md` D3
> 参考：cursor2api v2.7.8「上下文压力膨胀」机制（虚增报告给客户端的 input_tokens 系数，如 1.35，让客户端提前触发自动压缩，从根源缓解 max_output_token 截断）
> 约束：不写测试；改 prompt/history/replay 遵守 prefix-cache-stability；不改已安装 Cursor 客户端。

---

## 1. 机制概述

cursor-byok 现有防截断链路是**恢复型**（事后）：

```
provider 拒绝/截断 → forwarder 检测（context_overflow.go 减半重试 / max_tokens_recovery.go 降级 / compaction.go 强制压缩）
```

cursor2api 的膨胀是**预防型**（事前）：在响应体里把 input_tokens 报告值 × 系数（1.35），Cursor 客户端据此认为上下文占用更高，**主动提前触发其自动压缩**，使输出在完整写完前先腾出空间。

## 2. 代码事实核查（2026-08-06）

| 项 | 结论 |
|---|---|
| usage 响应体序列化点 | 适配器内（`openai.go:848-1058` 等处的 `PromptTokens/CompletionTokens` 结构，`anthropic.go`/`gemini.go` 同理），写响应体时生成 |
| 落库值来源 | `recordTurnUsageSnapshot` 的 usage 来自 TurnFinished 事件的 `completion.Usage`（actor.go:1005），由适配器解析上游响应后流出 |
| 统计隔离可行性 | **可行**：膨胀只作用于「响应体序列化」函数，落库走独立字段，两者可分离；膨胀值不进 usage.json |
| prefix-cache 影响 | **零**：膨胀只改响应体 usage 数字，不改请求体/历史/重放 |
| 与现有机制的关系 | 膨胀触发的是「客户端侧自动压缩」（黑盒）；现有 `compaction.go` 是服务端强制压缩（注入 summary + 减半重试）。两者机制不同，可能叠加触发 |

## 3. 技术方案（若实施）

1. **配置**：渠道级可选开关 + 系数（如 `contextPressureInflation`，默认关闭，0 = 不膨胀）。放置于 `config/types.go` 的模型/渠道配置，与现有 `MaxCompletionTokens` 等并列。
2. **膨胀点**：三适配器（openai/anthropic/gemini）的响应体 usage 构造处，对 `prompt_tokens`（及 anthropic 的 `input_tokens`）乘系数后取整；`completion_tokens`/缓存 token 不动。
3. **落库**：`recordTurnUsageSnapshot` 继续使用 adapter 原始解析值，不读取膨胀后的响应体值。
4. **边界**：膨胀仅对「报告给客户端的 usage」生效；调试日志与 artifact（`buildLLMSummaryPayload`）用真实值。

## 4. 风险与不确定项

| 风险 | 说明 | 缓解 |
|---|---|---|
| **客户端压缩阈值黑盒** | Cursor 客户端基于 usage 计算上下文占比的阈值/策略未知，系数有效性无法静态验证 | 需真实环境实验：分别用 1.0/1.15/1.3 跑长输出任务，观察是否更少截断 |
| **过度压缩成本** | 系数过大 → 客户端过早/过频压缩，每次压缩都是一次昂贵的摘要调用且重建 prompt cache | 系数保守（≤1.3）、默认关闭、按渠道单独开启 |
| **与现有压缩叠加** | 客户端自动压缩 + forwarder 强制压缩可能连环触发，出现「压缩→又膨胀→再压缩」振荡 | 实验阶段监控 compaction 触发频率；必要时在膨胀开启时调低 forwarder 压缩激进程度 |
| **统计口径漂移** | 若实现时误把膨胀值带入 usage.json，费用估算虚高 | 落库点用 adapter 原始值（§3.3），代码评审重点核对 |
| **调试困惑** | 请求明细显示的 token 与模型侧报告不一致 | artifact 与日志保留真实值，膨胀仅在客户端可见层 |

## 5. 决策建议

**暂缓实施**（文档状态：评估完成，列为可选实验项）。理由：

1. 收益依赖 Cursor 客户端黑盒行为，且与现有 `compaction.go` 强制压缩机制功能重叠——现有机制已能在截断前救回；
2. 风险（压缩振荡、统计口径、调参成本）在单机场景下收益/风险比不佳；
3. 若后续真实用户反馈「长输出仍频繁截断」，可按 §3 方案在实验分支实施，先验证 Cursor 压缩行为再决定是否默认开启。

## 6. 相关文档

- 移植来源：`docs/cursor2api-gateway-comparison-and-optimization.md` D3（状态同步更新为「已评估，暂缓」）
- 既有防截断机制：`context_overflow.go`、`max_tokens_recovery.go`、`compaction.go`、`tool_result_snip.go`