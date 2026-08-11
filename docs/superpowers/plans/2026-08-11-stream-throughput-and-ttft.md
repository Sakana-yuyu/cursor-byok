# 流式吞吐与 TTFT 优化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 对齐 NewAPI 总生成 TPS，增加可见正文速度与双首响指标，用渠道上限约束意外的高推理强度，并压缩陈旧 `Ls` provider 投影以降低真实 agent 请求 TTFT。

**Architecture:** 统一 provider 事件携带可获得的 usage 细节；模型测速器采集确定性时间点并计算指标；router 在渠道解析后统一决定实际推理强度；projector 只改变历史工具结果的 provider 投影。canonical history、reasoning replay、工具目录和已安装 Cursor 客户端保持不变。

**Tech Stack:** Go 1.25、Wails v3、Vue 3、Node test runner、静态 i18n 扫描、Cursor 本地模式 debug JSONL。

## Global Constraints

- 所有沟通、注释和新增文档使用中文；新增文件使用 UTF-8 无 BOM。
- PowerShell 环境，不使用 `bash -lc`；搜索优先 `rg`；手工编辑使用 `apply_patch`。
- Go 验证统一使用 `-p 1`，修改后运行 `gofmt` 和 `git diff --check`。
- 严格 TDD：每个行为先写失败测试并确认预期 RED，再写最小实现。
- 不修改已安装 Cursor 客户端、bundle、签名或 app 副本。
- 不修改 canonical `context.json.items`，不删除、重排或抑制历史 `reasoning_content`。
- 不减少任何 mode 的现有工具目录，所有已暴露能力继续真实可达。
- 本计划不实现工具 schema 稀疏激活和 48k/64k 延迟软预算。
- 每个任务独立本地 commit，不 push。

---

## 文件结构

- `internal/backend/agent/model/types.go`：统一 `ModelEvent` usage 字段。
- `internal/backend/agent/model/openai_stream_responses.go`：解析 Responses reasoning token。
- `internal/backend/agent/model/openai_stream_chat.go`：解析 Chat Completions reasoning token。
- `internal/client/model_adapter_benchmark.go`：采集首事件、首正文、usage 和双 TPS。
- `internal/client/model_adapter_benchmark_test.go`：测速计算和结果构造测试。
- `internal/backend/agent/model/reasoning_effort_limit.go`：推理强度排序和上限解析。
- `internal/backend/agent/model/reasoning_effort_limit_test.go`：覆盖所有强度组合。
- `internal/backend/agent/model/router.go`：应用渠道上限并写入 request knobs。
- `internal/backend/forwarder/tool_result_replay_truncation.go`：陈旧 `Ls` 精简投影。
- `internal/backend/forwarder/tool_result_replay_truncation_test.go`：`Ls` 当前轮、历史轮和解析失败测试。
- `internal/backend/forwarder/projector_test.go`：消息位置、tool call ID 和确定性投影测试。
- `frontend/src/utils/modelAdapter.js`：归一化与格式化新增测速字段。
- `frontend/src/utils/modelAdapter.test.js`：前端测速结果单元测试。
- `frontend/src/components/ModelAdapterTestCard.vue`：显示双 TPS、双首响、token 构成和实际强度。
- `frontend/src/components/model-editor/ModelBehaviorFields.vue`：调整字段标签。
- `frontend/src/utils/modelEditorMeta.js`：调整上限语义提示。
- `frontend/src/i18n/generated/catalog.json`、`frontend/src/i18n/locales/*.json`：同步新增 UI 文案。

---

### Task 1: NewAPI 对齐总 TPS 与可见正文指标

**Files:**
- Modify: `internal/backend/agent/model/types.go`
- Modify: `internal/backend/agent/model/openai_stream_responses.go`
- Modify: `internal/backend/agent/model/openai_stream_chat.go`
- Modify: `internal/client/model_adapter_benchmark.go`
- Modify: `internal/client/model_adapter_benchmark_test.go`
- Test: 现有 OpenAI Responses 与 Chat 流测试文件

**Interfaces:**
- Consumes: `ModelEvent.Kind`、`ModelEvent.OccurredAt`、provider usage 中的 `output_tokens` 和可选 reasoning token。
- Produces: `ModelEvent.ReasoningTokens int64`；`ModelAdapterTestResult.VisibleTokensPerSecond float64`、`FirstResponseMS int64`、`VisibleOutputTokens int64`、`ReasoningTokens int64`、`EffectiveThinkingEffort string`；`modelAdapterTestMetrics.firstResponseAt` 和 `reasoningTokens`。

- [ ] **Step 1: 为双 TPS 和双首响写失败测试**

在 `internal/client/model_adapter_benchmark_test.go` 增加：

```go
func TestBuildSuccessfulModelAdapterTestResultSeparatesTotalAndVisibleThroughput(t *testing.T) {
    startedAt := time.Unix(0, 0)
    metrics := &modelAdapterTestMetrics{
        firstResponseAt:  startedAt.Add(20 * time.Second),
        firstTextTokenAt: startedAt.Add(23 * time.Second),
        finishedAt:       startedAt.Add(24 * time.Second),
        outputTokens:     240,
        outputProvided:   true,
        reasoningTokens:  180,
    }
    _, _ = metrics.text.WriteString(strings.Repeat("a", 240))

    result, ok := buildSuccessfulModelAdapterTestResult("adapter", "hash", startedAt, metrics)
    if !ok {
        t.Fatal("expected successful result")
    }
    if math.Abs(result.TokensPerSecond-60) > 0.0001 {
        t.Fatalf("total TPS = %f, want 60", result.TokensPerSecond)
    }
    if result.FirstResponseMS != 20_000 || result.FirstTextTokenMS != 23_000 {
        t.Fatalf("latencies = response:%d text:%d", result.FirstResponseMS, result.FirstTextTokenMS)
    }
    if result.ReasoningTokens != 180 || result.VisibleTokensPerSecond <= 0 {
        t.Fatalf("usage = reasoning:%d visible_tps:%f", result.ReasoningTokens, result.VisibleTokensPerSecond)
    }
}
```

同时把原 `TestCalculateGenerationTokensPerSecondExcludesFirstTokenLatency` 改为从 `firstResponseAt` 计算，并增加正文 TPS 对 `firstTextTokenAt` 的独立边界测试。

- [ ] **Step 2: 运行测速测试并确认 RED**

Run: `go test -p 1 ./internal/client -run 'CalculateGenerationTokensPerSecond|BuildSuccessfulModelAdapterTestResult' -count=1`

Expected: FAIL，提示新字段或 `firstResponseAt` 不存在，不能因测试语法错误失败。

- [ ] **Step 3: 为 Responses 和 Chat reasoning usage 写失败测试**

Responses 终止事件使用：

```json
{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":240,"output_tokens_details":{"reasoning_tokens":180}}}}
```

断言最终 `ModelEventKindTurnFinished` 的 `OutputTokens == 240` 且 `ReasoningTokens == 180`。Chat Completions 使用 `usage.completion_tokens_details.reasoning_tokens` 做同样断言。

- [ ] **Step 4: 运行 provider 流测试并确认 RED**

Run: `go test -p 1 ./internal/backend/agent/model -run 'Responses.*ReasoningTokens|Chat.*ReasoningTokens' -count=1`

Expected: FAIL，最终事件没有 reasoning token。

- [ ] **Step 5: 实现统一 usage 和测速时间点**

在 `ModelEvent` 增加：

```go
// ReasoningTokens 表示 provider 明确返回的 reasoning token 数。
ReasoningTokens int64
```

Responses usage 增加 `output_tokens_details.reasoning_tokens`，Chat usage 增加 `completion_tokens_details.reasoning_tokens`，并在最终事件透传。测速 sink 在首个正文、思考或工具有效事件设置 `firstResponseAt`，空事件、`thinking_completed` 和 `turn_finished` 不作为首响应。

`calculateGenerationTokensPerSecond` 改用 `firstResponseAt`；新增 `calculateVisibleTokensPerSecond`，正文 token 使用 `estimateBenchmarkTextTokens(metrics.text.String())`。主路径和 endpoint fallback 共同使用 `buildSuccessfulModelAdapterTestResult`，避免指标漂移。

`modelAdapterTestMetrics` 增加 `effectiveThinkingEffort string`。OpenAI/Gemini 测速写入归一化后的 `adapter.ReasoningEffort`，Anthropic 测速写入 `thinkingEffort`；结果构造函数原样输出到 `ModelAdapterTestResult.EffectiveThinkingEffort`。该字段只描述这次独立测速实际采用的强度。真实 Cursor agent 请求的原始值、渠道上限和 actual 值由 Task 2 的 router/debug knobs 记录，两条链路不得互相推断。

- [ ] **Step 6: 运行定向测试并确认 GREEN**

Run: `go test -p 1 ./internal/client ./internal/backend/agent/model -run 'CalculateGenerationTokensPerSecond|BuildSuccessfulModelAdapterTestResult|ReasoningTokens' -count=1`

Expected: PASS。

- [ ] **Step 7: 格式化、完整模块测试并提交**

Run: `gofmt -w <本任务修改的 Go 文件>`

Run: `go test -p 1 ./internal/client ./internal/backend/agent/model -count=1`

Run: `git diff --check`

Commit: `fix(benchmark): separate total and visible throughput`

---

### Task 2: 渠道推理强度上限

**Files:**
- Create: `internal/backend/agent/model/reasoning_effort_limit.go`
- Create: `internal/backend/agent/model/reasoning_effort_limit_test.go`
- Create: `internal/backend/agent/model/router_reasoning_limit_test.go`
- Modify: `internal/backend/agent/model/router.go`
- Modify: `internal/backend/agent/model/types.go`

**Interfaces:**
- Consumes: `normalizeRuntimeThinkingEffort(string) string`、渠道 `ReasoningEffort` / `AnthropicThinkingEffort`、请求 `ThinkingEffort`。
- Produces: `resolveEffectiveThinkingEffort(runtimeValue string, configuredMaximum string) string`；`StreamRequest.ConfiguredThinkingEffortMaximum string`；request knobs 中的 `runtime_thinking_effort`、`configured_thinking_effort_maximum` 和 `effective_thinking_effort`。

- [ ] **Step 1: 为纯函数写表驱动失败测试**

```go
func TestResolveEffectiveThinkingEffortHonorsConfiguredMaximum(t *testing.T) {
    tests := []struct{ name, runtimeValue, maximum, want string }{
        {"missing runtime uses maximum", "", "medium", "medium"},
        {"lower runtime preserved", "low", "medium", "low"},
        {"equal runtime preserved", "medium", "medium", "medium"},
        {"higher runtime capped", "max", "medium", "medium"},
        {"disabled always allowed", "disabled", "medium", "disabled"},
        {"missing maximum preserves runtime", "max", "", "max"},
        {"invalid runtime uses maximum", "turbo", "high", "high"},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            if got := resolveEffectiveThinkingEffort(test.runtimeValue, test.maximum); got != test.want {
                t.Fatalf("got %q, want %q", got, test.want)
            }
        })
    }
}
```

- [ ] **Step 2: 运行纯函数测试并确认 RED**

Run: `go test -p 1 ./internal/backend/agent/model -run ResolveEffectiveThinkingEffort -count=1`

Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现最小排序与上限函数**

使用固定 rank map：`disabled=0`、`low=1`、`medium=2`、`high=3`、`xhigh=4`、`max=5`。渠道为空时返回有效运行时值；运行时为空时返回渠道值；两者都有时返回 rank 较低者。

- [ ] **Step 4: 为 router 最终请求写失败测试**

用捕获 `StreamRequest` 的 fake adapter，渠道设 `ReasoningEffort: "medium"`，请求设 `ThinkingEffort: "max"`。断言：

```go
if captured.ReasoningEffort != "medium" || captured.ThinkingEffort != "medium" {
    t.Fatalf("effective effort = thinking:%q reasoning:%q", captured.ThinkingEffort, captured.ReasoningEffort)
}
if captured.RequestKnobs["runtime_thinking_effort"] != "max" ||
    captured.RequestKnobs["configured_thinking_effort_maximum"] != "medium" ||
    captured.RequestKnobs["effective_thinking_effort"] != "medium" {
    t.Fatalf("effort knobs = %#v", captured.RequestKnobs)
}
```

另测 `disabled`、低于上限、渠道上限为空和 Anthropic 分支。

- [ ] **Step 5: 运行 router 测试并确认 RED**

Run: `go test -p 1 ./internal/backend/agent/model -run 'Router.*ThinkingEffortMaximum' -count=1`

Expected: FAIL，当前 `max` 仍覆盖 `medium`。

- [ ] **Step 6: 在 router 统一应用上限**

在 `streamChannel` 保存原始运行时值和渠道上限，调用纯函数得到 effective。OpenAI/Gemini 写入 `ReasoningEffort`，Anthropic 写入 `AnthropicThinkingEffort`；`disabled` 继续清空协议推理字段并保留 `ThinkingEffort=disabled`。`applyStreamKnobs` 分别记录原始值、上限和实际值。

- [ ] **Step 7: 运行定向和完整 model 测试并确认 GREEN**

Run: `go test -p 1 ./internal/backend/agent/model -run 'ResolveEffectiveThinkingEffort|Router.*ThinkingEffortMaximum|ThinkingDisable' -count=1`

Run: `go test -p 1 ./internal/backend/agent/model -count=1`

Expected: PASS。

- [ ] **Step 8: 格式化、检查并提交**

Run: `gofmt -w <本任务修改的 Go 文件>`

Run: `git diff --check`

Commit: `fix(provider): cap runtime reasoning effort by channel`

---

### Task 3: 陈旧 Ls 历史投影压缩

**Files:**
- Modify: `internal/backend/forwarder/tool_result_replay_truncation.go`
- Create: `internal/backend/forwarder/tool_result_replay_truncation_test.go`
- Modify: `internal/backend/forwarder/projector_test.go`

**Interfaces:**
- Consumes: `limitProjectedToolResultReplay(toolName, content, resultText string, fromStoredToolCall, historical bool) string`、`LsToolCall` proto JSON、`staleToolResultReplayLimit`。
- Produces: `compactHistoricalLsResultReplay(content string, resultText string) (string, bool)`；`projectedToolReplayLimit("Ls")` 返回受控上限。

- [ ] **Step 1: 为历史 Ls 写失败测试**

构造超过 `staleToolResultAggressiveThreshold` 的 Ls ToolCall JSON，`resultText` 为 `ls success path=E:\MyProject\cursor-byok files=768`。断言保留路径和文件数、不含完整目录标记，并含“历史 Ls 目录树已从 provider 回放中省略”。另测 `historical=false` 保留当前结果、空 `resultText` 时从结构化内容提取根路径和节点数、非法 JSON 走通用截断。

- [ ] **Step 2: 运行截断测试并确认 RED**

Run: `go test -p 1 ./internal/backend/forwarder -run 'HistoricalLs|ProjectedToolReplay.*Ls' -count=1`

Expected: FAIL，`Ls` 尚未进入 replay limit。

- [ ] **Step 3: 实现 Ls 精简投影**

在通用 edit/shell compact 后、读取 replay limit 前调用历史 Ls compact。`projectedToolReplayLimit` 为 `Ls` 返回 `projectedGrepReplayLimit`，历史大结果仍由通用逻辑降到 4 KiB。

精简函数优先使用非空 `resultText`；结构化解析只读取 `LsToolCall.args.path` 和 `result.success.directory_tree_root`，递归计数节点但不回放节点名称。解析失败返回 false。固定中文提示不包含时间和随机值。

- [ ] **Step 4: 为 projector 不变量写失败测试**

使用两轮历史 entry：第一轮 `Ls` tool call/result，第二轮 user message。连续调用两次 `ProjectPromptReplay`，断言：

- 两次输出 `reflect.DeepEqual`。
- assistant tool call ID 与 tool result ID 保持原值。
- tool result 仍位于原 assistant tool call 后、第二轮 user message 前。
- tool catalog 测试中各 mode 工具名称集合没有变化。

- [ ] **Step 5: 运行 projector 测试并确认 GREEN**

Run: `go test -p 1 ./internal/backend/forwarder -run 'HistoricalLs|Project.*Ls|ToolCatalog' -count=1`

Expected: PASS。

- [ ] **Step 6: 用真实样本离线量化 token 降幅**

对 `C:\Users\Administrator\.cursor-local-assistant-v2\history\8bdf2bce-54d7-4632-b742-122491a4573d` 运行现有 projector/historymetrics 路径，记录修改前后 provider-visible 字符数和估算 token。不得修改该 history。预期三个陈旧 `Ls` 不再贡献约 77k 完整目录字符，输入估算明确下降。

- [ ] **Step 7: 格式化、完整 forwarder 测试并提交**

Run: `gofmt -w <本任务修改的 Go 文件>`

Run: `go test -p 1 ./internal/backend/forwarder -count=1`

Run: `git diff --check`

Commit: `fix(context): compact stale ls replay`

---

### Task 4: 前端指标、上限语义与全链路验收

**Files:**
- Modify: `frontend/src/utils/modelAdapter.js`
- Create: `frontend/src/utils/modelAdapter.test.js`
- Modify: `frontend/src/components/ModelAdapterTestCard.vue`
- Modify: `frontend/src/components/model-editor/ModelBehaviorFields.vue`
- Modify: `frontend/src/utils/modelEditorMeta.js`
- Modify generated: `frontend/src/i18n/generated/catalog.json`、`frontend/src/i18n/locales/*.json`

**Interfaces:**
- Consumes: Task 1 的新增 `ModelAdapterTestResult` JSON 字段；Task 2 的上限语义。
- Produces: 完整前端结果对象；摘要“总生成 N t/s | 正文 M t/s | 首响应 ... | 首字 ...”；测试卡详细指标。

- [ ] **Step 1: 为前端归一化和摘要写失败测试**

```js
import test from "node:test";
import assert from "node:assert/strict";
import { formatModelAdapterTestSummary, normalizeModelAdapterTestResult } from "./modelAdapter.js";

test("模型测速同时展示总生成和正文速度", () => {
  const result = normalizeModelAdapterTestResult({
    status: "success",
    tokensPerSecond: 60.5,
    visibleTokensPerSecond: 126.2,
    firstResponseMS: 24616,
    firstTextTokenMS: 27870,
    outputTokens: 197,
    visibleOutputTokens: 70,
    reasoningTokens: 127,
    effectiveThinkingEffort: "medium",
  });
  assert.equal(result.visibleTokensPerSecond, 126.2);
  assert.equal(result.reasoningTokens, 127);
  assert.match(formatModelAdapterTestSummary(result), /总生成 61 t\/s/);
  assert.match(formatModelAdapterTestSummary(result), /正文 126 t\/s/);
  assert.match(formatModelAdapterTestSummary(result), /首响应 24.6 s/);
});
```

- [ ] **Step 2: 运行前端单元测试并确认 RED**

Run from `frontend`: `npm run test:unit -- --test-name-pattern='模型测速'`

Expected: FAIL，新字段未归一化且摘要缺少指标。

- [ ] **Step 3: 实现前端归一化和紧凑展示**

`normalizeModelAdapterTestResult` 对新增数字字段做非负归一化，对 `effectiveThinkingEffort` 做字符串归一化。`ModelAdapterTestCard` 使用稳定网格显示总生成 TPS、正文 TPS、首响应、首字、总输出/reasoning/正文估算 token 和实际推理强度。缺少 reasoning usage 时显示 `-`，不显示推算值；保持卡片圆角不超过 8px，不嵌套新卡片。

- [ ] **Step 4: 修改上限标签与提示并同步 i18n**

标签改为“推理强度上限”和“思考强度上限”。提示为：

```text
客户端可以请求更低强度或关闭，但实际请求不会超过此上限。较高强度通常更稳，也会增加首响应时间。
```

先运行 `npm run i18n:scan` 生成 catalog，再补齐所有已注册 locale 的翻译，不得只修改中文源文案。

- [ ] **Step 5: 运行前端测试、扫描和 production build**

Run from `frontend`: `npm run test:unit`

Run from `frontend`: `npm run i18n:scan`

Run from `frontend`: `npm run build`

Expected: 全部 PASS，无缺失翻译和构建失败。

- [ ] **Step 6: 运行 Go 全仓验证**

Run: `go test -p 1 ./...`

Run: `go vet -p 1 ./...`

Run: `go build -p 1 ./...`

Run: `git diff --check`

Expected: 全部 PASS。

- [ ] **Step 7: 启动修复后二进制并执行真实 Cursor E2E**

使用当前 worktree 构建并启动本地后端，确认 Cursor 实际连接修复后二进制。对同一模型、渠道和等价短请求运行：

1. Cursor `disabled`，渠道上限 `medium`。
2. Cursor `medium`，渠道上限 `medium`。
3. Cursor `max`，渠道上限 `medium`。

从新生成的 `history/<conversationId>/debug/provider.jsonl`、`runtime.jsonl`、`runsse.jsonl` 和 state/context 记录：

- `runtime_thinking_effort`、`configured_thinking_effort_maximum`、`effective_thinking_effort`。
- prompt tokens、reasoning tokens、output tokens。
- started、first provider event、first text、finished 时间。
- upstream text delta、forwarder delta、RunSSE delta 数。
- 正文拼接 hash 或逐段比较结果。

验收：`max` 实际发送 `medium`；`disabled` 不启用 reasoning；三层正文 delta 数一致且文本无差异；本地转发 P95 不显著高于修复前约 0.535 ms；同一历史下输入 token 因 `Ls` 精简明确下降。

- [ ] **Step 8: 提交前端与验收辅助修改**

Run: `git status --short`，确认没有日志、history、密钥或构建产物被纳入。

Commit: `feat(ui): explain throughput and reasoning limits`

- [ ] **Step 9: 最终审计本地提交历史**

Run: `git log --oneline --decorate 8ccf764..HEAD`

Run: `git status --short --branch`

Expected: 工作树干净，包含设计、指标、cache、推理上限、`Ls` 裁剪和 UI 提交；不 push。

---

## 完成标准

- NewAPI 对齐 TPS 与正文 TPS 使用不同、正确的时间起点和 token 口径。
- UI 不再用单一 t/s 数字暗示用户看到的正文速度。
- 渠道 `medium` 能真实限制 Cursor `max`，debug 可追踪原始值、上限和实际值。
- 陈旧 `Ls` 不再把完整大型目录树送入 provider，canonical history 与消息顺序不变。
- 所有工具目录集合保持不变，工具链真实可达。
- Go 全仓 test/vet/build、前端 unit/build/i18n、`git diff --check` 全部通过。
- 修复后二进制完成真实 Cursor E2E，三层 delta 与文本一致。
- 所有修改已本地提交，工作树干净且未 push。
