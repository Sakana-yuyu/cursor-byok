# Design: model-context-budget

## Architecture

模型能力目录只描述已知能力及识别来源；渠道设置保留用户主动缩小的限制；Cursor 参数描述可选档位；每轮运行保存用户选择并计算有效预算。压缩、路由、发送前检查和 checkpoint 使用同一个有效预算，不各自放大窗口。

## Interfaces

- Cursor 入站沿用 `RequestedModel.parameters` 的 `id="context"`，值为正的十进制 token 字符串。不改变生成协议文件。
- `InboundIntent.RequestedContextWindowTokens`、`ActiveStream.RequestedContextWindowTokens`、`ProviderRequest.RequestedContextWindowTokens`、`modeladapter.StreamRequest.RequestedContextWindowTokens` 使用 `int`，零表示本轮未显式指定。非法、重复冲突或溢出的参数返回错误。
- `ProviderRequest.ContextWindowTokens`、`modeladapter.StreamRequest.ContextWindowTokens` 使用 `int`，表示 forwarder 本次编译采用的有效窗口硬上限；零仅兼容旧调用方。
- `modelcontext.ResolveContextBudget(ContextBudgetInput) ContextBudget` 为纯预算解析函数。输入字段为 `ModelID`、`ConfiguredTokens`、`RequestedTokens`、`CeilingTokens`；输出包含 `CapabilityTokens`、`ConfiguredTokens`、`RequestedTokens`、`CeilingTokens`、`EffectiveTokens`、`Source`。
- 有效窗口取已知正上限的最小值；全部未知时采用现有保守缺省 200000。显式小值不可被缺省或 MaxMode 放大。
- Router 必须对实际渠道重新取最小值，并对更小渠道执行发送前预算校验。发生预算不足时不发送超限请求，返回可识别的预算错误供 forwarder 有界压缩重试。

## Data Model

- 内置模型能力的唯一数据源为 `internal/modelcontext/models.json`，前端复用数据和明确的匹配规则，不再维护独立容量目录。
- 模型识别结果（能力/来源）、用户渠道限制和请求选择分别拥有数据；旧 `contextWindowTokens` 继续作为渠道限制使用，不能因重新识别被自动扩大。
- 请求上下文选择属于当前 run/turn，不是进程全局状态。续传继承当前回合；独立新回合缺省选择不继承上一轮显式小值。
- 会话 `TokenDetailsMaxTokens` 保存当前有效窗口；预算变化时重新检查压缩投影与锚点，checkpoint 用量与最终发送的投影一致。

## Key Decisions

1. 保留小档与精确上限。标准档位为 32000、64000、128000、200000、256000、500000、1000000，按有效上限过滤，再加入精确上限和合法自定义值；默认不擅自提升。参数枚举和完整变体组合来自同一档位集合。
2. 以最终请求而非菜单证明生效。每次压缩后重新编译，计入系统提示、工具、图片、输出预算及余量。保留原始历史和工具配对，无法装下不可压缩输入则报错。启发式估算保守使用，不声称逐 token 精确计数。

## Verification

覆盖目录前后端一致、小值保留、完整变体匹配、run/prewarm/续传、新回合与会话隔离、MaxMode、路由降级、预算变化和压缩投影失效。通过协议请求捕获 mock provider 的实际消息与预算，断言超限时确实压缩、压缩后满足约束，且 checkpoint 的窗口和用量同步更新。