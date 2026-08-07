# 委派 Worker 上下文压缩 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给本地委派 worker（`localDelegatedAgentAdapter`）接入上下文压缩：主动阈值压缩（snip 长工具结果 + 丢弃早期消息对）与超限自救（context_too_large 时压缩重试，上限 2 次），降低子代理因上下文膨胀导致的 failed。

**Architecture:** 新增 `internal/backend/forwarder/delegation_compaction.go` 承载全部压缩纯函数（预算、snip、丢弃、错误匹配、统计），在 `delegation_local.go` 的 `Execute` 循环两处接线：每轮 provider pass 前主动压缩、`runProviderPass` 返回超限错误时压缩重试。压缩直接作用于 `messages []modeladapter.Message`，不触碰 `ConversationFile.Entries` 与主进程 compaction 流程。

**Tech Stack:** Go 1.26，`internal/backend/forwarder` 包（已有 `estimateModelMessagesTokens`、`resolveContextWindowTokens` 可复用），`testing` 标准库。

## Global Constraints

- 不做 LLM 摘要压缩（worker 内不额外调用模型）。
- 不接主进程 `ConversationFile.Entries` 重编译流程。
- 不改 `maxPasses` 语义。
- 不做 config.yaml contextWindowTokens 减半持久化。
- `role=="system"` 消息永不丢弃；worker 首条 `user` 消息（任务 prompt）尽力保留；最近一轮（最后一个 assistant/tool 对）不压缩。
- `budget = 0.8 × window − 10000`（`compactionAutoReserveTokens`），下限保护 `budget ≥ 16000`；`window≤0` 时不主动压缩。
- snip 规则：`role=="tool"` 且 `len(Content) > 16*1024` 的消息截断到 `4*1024` 字节并追加占位文本（工具名 + "输出过长已省略"）。
- 丢弃规则：从最旧开始成对丢弃 `assistant`+紧随其后的 `tool` 消息，保留最近 4 轮。
- 超限自救：错误文本匹配 `context_too_large` / `context_length_exceeded` / `exceeds the context window`，每任务重试上限 2 次。
- 压缩发生时打 INFO 日志 `forwarder delegated context compacted task_id=... provider_pass=... snip=... dropped=... msgs=... tokens=...`。

---

### Task 1: 预算与错误匹配纯函数

**Files:**
- Create: `internal/backend/forwarder/delegation_compaction.go`
- Test: `internal/backend/forwarder/delegation_compaction_test.go`

**Interfaces:**
- Produces:
  - `const delegatedSnipThresholdBytes = 16 * 1024`
  - `const delegatedSnipTargetBytes = 4 * 1024`
  - `const delegatedCompactionBudgetFloor = int64(16_000)`
  - `const delegatedCompactionWindowRatio = 0.8`
  - `const delegatedCompactionRetryLimit = 2`
  - `func delegatedContextBudgetForWindow(window int64) int64` —— 返回 `0.8*window - 10000`，结果 `< 16000` 时返回 `16000`；`window <= 0` 返回 0。
  - `func delegatedContextOverflowError(err error) bool` —— 错误文本（`err.Error()`，含包裹错误）匹配 `context_too_large`、`context_length_exceeded`、`exceeds the context window` 之一返回 true。

- [ ] **Step 1: 写失败测试**

`internal/backend/forwarder/delegation_compaction_test.go`：

```go
package forwarder

import (
	"errors"
	"fmt"
	"testing"
)

func TestDelegatedContextBudgetForWindow(t *testing.T) {
	cases := []struct {
		name   string
		window int64
		want   int64
	}{
		{"zero window disables proactive compaction", 0, 0},
		{"negative window disables", -1, 0},
		{"floor protection", 10_000, delegatedCompactionBudgetFloor},
		{"normal budget", 272_000, int64(0.8*272_000) - 10_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := delegatedContextBudgetForWindow(tc.window); got != tc.want {
				t.Fatalf("delegatedContextBudgetForWindow(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestDelegatedContextOverflowError(t *testing.T) {
	overflowErrors := []error{
		errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model"),
		errors.New("context_length_exceeded"),
		fmt.Errorf("wrapped: %w", errors.New("input exceeds the context window")),
	}
	for _, err := range overflowErrors {
		if !delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%q) = false, want true", err)
		}
	}
	notOverflow := []error{
		errors.New("request_timeout: stream closed before response.completed"),
		errors.New("network error"),
		nil,
	}
	for _, err := range notOverflow {
		if delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%v) = true, want false", err)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/backend/forwarder/ -run 'TestDelegatedContext' -v`
Expected: 编译失败，`undefined: delegatedContextBudgetForWindow`。

- [ ] **Step 3: 最小实现**

`internal/backend/forwarder/delegation_compaction.go`：

```go
package forwarder

import (
	"strings"
)

const (
	delegatedSnipThresholdBytes = 16 * 1024
	delegatedSnipTargetBytes    = 4 * 1024
	delegatedCompactionBudgetFloor = int64(16_000)
	delegatedCompactionWindowRatio = 0.8
	delegatedCompactionRetryLimit  = 2
)

// delegatedContextBudgetForWindow 由上下文窗口推导 worker 压缩预算：
// budget = 0.8*window - compactionAutoReserveTokens；下限 protected，
// window<=0 表示无窗口信息，返回 0（关闭主动压缩，超限自救仍可用）。
func delegatedContextBudgetForWindow(window int64) int64 {
	if window <= 0 {
		return 0
	}
	budget := int64(delegatedCompactionWindowRatio*float64(window)) - compactionAutoReserveTokens
	if budget < delegatedCompactionBudgetFloor {
		return delegatedCompactionBudgetFloor
	}
	return budget
}

// delegatedContextOverflowError 判断错误是否属于上下文窗口超限（可触发压缩重试）。
func delegatedContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context_too_large") ||
		strings.Contains(text, "context_length_exceeded") ||
		strings.Contains(text, "exceeds the context window")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/backend/forwarder/ -run 'TestDelegatedContext' -v`
Expected: PASS（2 个测试全绿）。

- [ ] **Step 5: 提交**

```bash
git add internal/backend/forwarder/delegation_compaction.go internal/backend/forwarder/delegation_compaction_test.go
git commit -m "feat(forwarder): 委派 worker 上下文压缩预算与超限错误匹配"
```

---

### Task 2: snip 超长工具结果

**Files:**
- Modify: `internal/backend/forwarder/delegation_compaction.go`
- Test: `internal/backend/forwarder/delegation_compaction_test.go`

**Interfaces:**
- Consumes: `delegatedSnipThresholdBytes`、`delegatedSnipTargetBytes`（Task 1）
- Produces:
  - `type delegatedCompactionStats struct { SnipCount int; DroppedCount int; BeforeTokens int64; AfterTokens int64 }`
  - `func delegatedToolResultOmittedText(toolName string) string` —— 返回 `"[工具输出过长已省略（工具: <toolName>）]"`。
  - `func snipDelegatedOversizedToolResults(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool)` —— 估算超预算时，从旧到新截断 `role=="tool"` 且 `len(Content)>delegatedSnipThresholdBytes` 的消息 Content 到 `delegatedSnipTargetBytes` 后追加 `delegatedToolResultOmittedText(Name)`；最近一轮（最后一条 `role=="tool"` 消息及其前驱 assistant）不 snip；每次截断后重估，直到预算内或无可截断。返回压缩后 messages 与是否发生变化。`stats` 可为 nil。

- [ ] **Step 1: 写失败测试**

追加到 `internal/backend/forwarder/delegation_compaction_test.go`：

```go
func TestDelegatedToolResultOmittedText(t *testing.T) {
	got := delegatedToolResultOmittedText("Read")
	if !strings.Contains(got, "Read") || !strings.Contains(got, "输出过长已省略") {
		t.Fatalf("unexpected omitted text: %q", got)
	}
}

func TestSnipDelegatedOversizedToolResults(t *testing.T) {
	big := strings.Repeat("x", 40*1024)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task prompt"},
		{Role: "assistant", Content: "let me read", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: big, ToolCallID: "c1", Name: "Read"},
		{Role: "assistant", Content: "done", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "small result", ToolCallID: "c2", Name: "Shell"},
	}
	// 预算极小，强制压缩（但最近一轮不 snip）
	stats := &delegatedCompactionStats{}
	out, changed := snipDelegatedOversizedToolResults(messages, 100, stats)
	if !changed {
		t.Fatal("expected compaction to happen")
	}
	if stats.SnipCount != 1 {
		t.Fatalf("SnipCount = %d, want 1", stats.SnipCount)
	}
	// 第一条 tool（Read）被截断；最后一条 tool（Shell）不截断
	if out[3].Content == big {
		t.Fatal("oldest oversized tool result should be snipped")
	}
	if len(out[3].Content) > delegatedSnipTargetBytes+len(delegatedToolResultOmittedText("Read")) {
		t.Fatalf("snipped content too long: %d", len(out[3].Content))
	}
	if !strings.Contains(out[3].Content, "输出过长已省略") {
		t.Fatal("snipped content missing omitted marker")
	}
	if out[5].Content != "small result" {
		t.Fatal("recent turn tool result must not be snipped")
	}
}

func TestSnipNoOpWithinBudget(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
	}
	out, changed := snipDelegatedOversizedToolResults(messages, 1_000_000, nil)
	if changed {
		t.Fatal("expected no change within budget")
	}
	if len(out) != len(messages) {
		t.Fatal("messages length changed unexpectedly")
	}
}
```

（`TestSnipNoOpWithinBudget` 里 `out` 可能为 nil 拷贝，断言长度即可。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/backend/forwarder/ -run 'TestSnipDelegated|TestDelegatedToolResultOmitted' -v`
Expected: 编译失败，`undefined: snipDelegatedOversizedToolResults`。

- [ ] **Step 3: 实现**

在 `delegation_compaction.go` 顶部 import 块追加（与 Task 1 的 import 合并为一个 import 块）：

```go
import (
	"strings"

	modeladapter "cursor/internal/backend/agent/model"
)
```

再在文件内追加：

```go
type delegatedCompactionStats struct {
	SnipCount    int
	DroppedCount int
	BeforeTokens int64
	AfterTokens  int64
}

func delegatedToolResultOmittedText(toolName string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return "[工具输出过长已省略（工具: " + name + "）]"
}

// snipDelegatedOversizedToolResults 把超长 tool result 截断到预算内。
// 最近一轮（最后一条 role=tool 消息）不截断，保护当前正在使用的上下文。
func snipDelegatedOversizedToolResults(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) == 0 {
		return messages, false
	}
	changed := false
	if stats != nil {
		stats.BeforeTokens = estimateModelMessagesTokens(messages)
	}
	// 找出最近一轮 tool 消息的索引（不截断它）
	lastToolIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == "tool" {
			lastToolIdx = i
			break
		}
	}
	for estimateModelMessagesTokens(messages) > budget {
		target := -1
		for i := 0; i < len(messages); i++ {
			if i == lastToolIdx {
				continue
			}
			if strings.TrimSpace(messages[i].Role) != "tool" {
				continue
			}
			if len(messages[i].Content) > delegatedSnipThresholdBytes {
				target = i
				break
			}
		}
		if target < 0 {
			break
		}
		snipped := messages[target].Content[:delegatedSnipTargetBytes] + delegatedToolResultOmittedText(messages[target].Name)
		messages[target].Content = snipped
		changed = true
		if stats != nil {
			stats.SnipCount++
		}
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(messages)
	}
	return messages, changed
}
```

（注意：`messages` 是切片，修改元素要避免共享底层数组副作用——上层传入的是 `cloneDelegatedMessages` 后的独立切片，安全。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/backend/forwarder/ -run 'TestSnipDelegated|TestDelegatedToolResultOmitted' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/backend/forwarder/delegation_compaction.go internal/backend/forwarder/delegation_compaction_test.go
git commit -m "feat(forwarder): 委派 worker snip 超长工具结果"
```

---

### Task 3: 丢弃早期消息对 + 组合入口

**Files:**
- Modify: `internal/backend/forwarder/delegation_compaction.go`
- Test: `internal/backend/forwarder/delegation_compaction_test.go`

**Interfaces:**
- Consumes: `delegatedCompactionStats`、`snipDelegatedOversizedToolResults`（Task 2）
- Produces:
  - `const delegatedCompactionKeepTurns = 4`
  - `func dropDelegatedEarlyMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool)` —— 仍超预算时，从最旧开始成对丢弃 `assistant`+紧随其后的 `tool`（按消息顺序找到 assistant 索引、其下一跳是 tool 才成对丢弃）；`role=="system"` 永不丢；首条 `user` 保留（丢弃到只剩 system+首条 user 为止停止）；保留最近 `delegatedCompactionKeepTurns` 轮。逐对丢弃直到预算内或无可丢。
  - `func maybeCompactDelegatedMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool)` —— 组合入口：`budget<=0` 直接返回；先 `snipDelegatedOversizedToolResults`，仍超预算再 `dropDelegatedEarlyMessages`。返回最终 messages 与是否发生变化。

- [ ] **Step 1: 写失败测试**

追加：

```go
func TestDropDelegatedEarlyMessages(t *testing.T) {
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task prompt"},
		{Role: "assistant", Content: "a1", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1", Name: "Shell"},
		{Role: "assistant", Content: "a2", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2", Name: "Shell"},
		{Role: "assistant", Content: "a3", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c3"}}},
		{Role: "tool", Content: "r3", ToolCallID: "c3", Name: "Shell"},
		{Role: "assistant", Content: "a4", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c4"}}},
		{Role: "tool", Content: "r4", ToolCallID: "c4", Name: "Shell"},
		{Role: "assistant", Content: "a5", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c5"}}},
		{Role: "tool", Content: "r5", ToolCallID: "c5", Name: "Shell"},
	}
	stats := &delegatedCompactionStats{}
	out, changed := dropDelegatedEarlyMessages(messages, 10, stats)
	if !changed {
		t.Fatal("expected drop to happen")
	}
	if stats.DroppedCount == 0 {
		t.Fatal("DroppedCount should be > 0")
	}
	// system 与首条 user 必须保留
	if out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("system/user must be preserved, got roles %q %q", out[0].Role, out[1].Role)
	}
	// 剩余消息必须保持 tool 前有对应 assistant（成对性）
	for i := 1; i < len(out); i++ {
		if out[i].Role == "tool" && out[i-1].Role != "assistant" {
			t.Fatalf("tool message at %d not preceded by assistant", i)
		}
	}
	// 最近 4 轮保留：a2/a3/a4/a5 及其 tool 都在
	kept := 0
	for _, m := range out {
		if m.Role == "assistant" {
			kept++
		}
	}
	if kept != 4 {
		t.Fatalf("expected 4 assistant turns kept, got %d", kept)
	}
	last := out[len(out)-1]
	if last.Role != "tool" || last.Content != "r5" {
		t.Fatalf("recent turn must be preserved, got %+v", last)
	}
}

func TestMaybeCompactDelegatedMessages(t *testing.T) {
	big := strings.Repeat("y", 20*1024)
	messages := []modeladapter.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "a1", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c1"}}},
		{Role: "tool", Content: big, ToolCallID: "c1", Name: "Read"},
		{Role: "assistant", Content: "a2", ToolCalls: []modeladapter.ToolCallDescriptor{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2", Name: "Shell"},
	}
	stats := &delegatedCompactionStats{}
	out, changed := maybeCompactDelegatedMessages(messages, 100, stats)
	if !changed {
		t.Fatal("expected compaction")
	}
	if len(out) < 2 || out[0].Role != "system" {
		t.Fatal("system message must remain")
	}
	if stats.SnipCount == 0 && stats.DroppedCount == 0 {
		t.Fatal("expected snip or drop to have run")
	}
	// budget<=0 时不压缩
	if _, c := maybeCompactDelegatedMessages(messages, 0, nil); c {
		t.Fatal("budget<=0 must not compact")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/backend/forwarder/ -run 'TestDropDelegated|TestMaybeCompact' -v`
Expected: 编译失败，`undefined: dropDelegatedEarlyMessages` / `maybeCompactDelegatedMessages`。

- [ ] **Step 3: 实现**

在 `delegation_compaction.go` 追加：

```go
const delegatedCompactionKeepTurns = 4

// dropDelegatedEarlyMessages 从最旧开始成对丢弃 assistant+tool 消息，直到预算内。
// 索引 0（system）与索引 1（首条 user）永不丢弃；保留最近 delegatedCompactionKeepTurns 轮。
func dropDelegatedEarlyMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) <= 2 {
		return messages, false
	}
	changed := false
	if stats != nil && stats.BeforeTokens == 0 {
		stats.BeforeTokens = estimateModelMessagesTokens(messages)
	}
	// 计算保留起点 keepStart：从尾部数第 delegatedCompactionKeepTurns 个 assistant 的索引。
	// 不足该轮数则 keepStart 保持 0（无可丢区间）。
	keepStart := 0
	seenTurns := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == "assistant" {
			seenTurns++
			if seenTurns == delegatedCompactionKeepTurns {
				keepStart = i
				break
			}
		}
	}
	for estimateModelMessagesTokens(messages) > budget {
		dropped := false
		for i := 2; i < keepStart; i++ { // 索引 0=system、1=首条 user 永不丢
			if strings.TrimSpace(messages[i].Role) != "assistant" {
				continue
			}
			if i+1 >= len(messages) || strings.TrimSpace(messages[i+1].Role) != "tool" {
				continue
			}
			messages = append(messages[:i], messages[i+2:]...)
			dropped = true
			changed = true
			if stats != nil {
				stats.DroppedCount++
			}
			if keepStart >= 2 {
				keepStart -= 2 // 删除发生在保留起点之前，起点前移 2
			}
			break
		}
		if !dropped {
			break
		}
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(messages)
	}
	return messages, changed
}

// maybeCompactDelegatedMessages 是主动阈值压缩组合入口：snip → drop。
func maybeCompactDelegatedMessages(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool) {
	if budget <= 0 || len(messages) == 0 {
		return messages, false
	}
	changed := false
	var out []modeladapter.Message
	out, snipChanged := snipDelegatedOversizedToolResults(messages, budget, stats)
	changed = changed || snipChanged
	if estimateModelMessagesTokens(out) > budget {
		var dropChanged bool
		out, dropChanged = dropDelegatedEarlyMessages(out, budget, stats)
		changed = changed || dropChanged
	}
	return out, changed
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/backend/forwarder/ -run 'TestDropDelegated|TestMaybeCompact' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/backend/forwarder/delegation_compaction.go internal/backend/forwarder/delegation_compaction_test.go
git commit -m "feat(forwarder): 委派 worker 丢弃早期消息对与组合压缩入口"
```

---

### Task 4: 接入 Execute（主动阈值 + 超限自救）

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go`（adapter 结构、`newLocalDelegatedAgentAdapter`、`Execute` 循环）
- Test: `internal/backend/forwarder/delegation_local_compaction_test.go`（新文件）

**Interfaces:**
- Consumes: `maybeCompactDelegatedMessages`、`delegatedContextBudgetForWindow`、`delegatedContextOverflowError`、`delegatedCompactionRetryLimit`、`delegatedCompactionStats`（Task 1-3）、`service.resolveContextWindowTokens(modelID string) uint32`、`estimateModelMessagesTokens`
- Produces:
  - `localDelegatedAgentAdapter` 新增字段 `resolveContextWindow func(string) uint32`
  - `newLocalDelegatedAgentAdapter` 注入 `service.resolveContextWindowTokens`

- [ ] **Step 1: 写失败测试**

`internal/backend/forwarder/delegation_local_compaction_test.go`（构造最小 adapter + fake provider/compiler）：

```go
package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

type fakeDelegatedCompiler struct{}

func (fakeDelegatedCompiler) Compile(_ *ConversationFile, _ agentv1.AgentMode, _ string, _ string, _ string) (CompiledConversation, error) {
	return CompiledConversation{
		Messages: []modeladapter.Message{{Role: "system", Content: "sys"}},
		Tools:    []json.RawMessage{},
	}, nil
}

func (fakeDelegatedCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

// fakeDelegatedProvider 前 errorsBeforeSuccess 次调用返回超限错误，之后成功。
type fakeDelegatedProvider struct {
	errorsBeforeSuccess int
	callCount           int
}

func (f *fakeDelegatedProvider) StartStream(_ context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	f.callCount++
	if f.callCount <= f.errorsBeforeSuccess {
		return errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model")
	}
	if err := sink(modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTurnFinished}); err != nil {
		return err
	}
	return nil
}

// overflowlessProvider 返回非超限错误，用于验证「不触发压缩重试」。
type overflowlessProvider struct{}

func (overflowlessProvider) StartStream(context.Context, ProviderRequest, func(modeladapter.ModelEvent) error) error {
	return errors.New("boom: request_timeout")
}

func newCompactionTestAdapter(provider ProviderGateway) *localDelegatedAgentAdapter {
	return &localDelegatedAgentAdapter{
		compiler: fakeDelegatedCompiler{},
		provider: provider,
		toolExecutor: func(context.Context, delegation.TaskRequest, runtimecore.ToolInvocation) (string, error) {
			return "ok", nil
		},
		maxPasses:            10,
		resolveContextWindow: func(string) uint32 { return 272_000 },
	}
}

func TestExecuteRecoversFromContextOverflow(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 1}
	adapter := newCompactionTestAdapter(provider)
	req := delegation.TaskRequest{ID: "t1", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error != nil {
		t.Fatalf("expected recovery, got error: %v", result.Error)
	}
	if provider.callCount != 2 {
		t.Fatalf("callCount = %d, want 2 (one fail + one retry)", provider.callCount)
	}
}

func TestExecuteOverflowRetryLimit(t *testing.T) {
	provider := &fakeDelegatedProvider{errorsBeforeSuccess: 5}
	adapter := newCompactionTestAdapter(provider)
	req := delegation.TaskRequest{ID: "t2", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error == nil {
		t.Fatal("expected failure after retry limit")
	}
	// 首次调用 + 2 次重试 = 3 次；超过则失败
	if provider.callCount != 1+delegatedCompactionRetryLimit {
		t.Fatalf("callCount = %d, want %d", provider.callCount, 1+delegatedCompactionRetryLimit)
	}
}

func TestExecuteNonOverflowErrorFailsImmediately(t *testing.T) {
	adapter := newCompactionTestAdapter(overflowlessProvider{})
	req := delegation.TaskRequest{ID: "t3", Prompt: "do the thing", ModelID: "m1", ModelName: "gpt-5.6-luna"}
	result := adapter.Execute(context.Background(), req)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "boom") {
		t.Fatalf("expected immediate non-overflow failure, got %v", result.Error)
	}
}
```

（`fakeDelegatedProvider.errorsBeforeSuccess=0` 时不会失败——`TestExecuteNonOverflowErrorFailsImmediately` 里实际用的是 `overflowlessProvider`，删除未用变量。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/backend/forwarder/ -run 'TestExecuteRecoversFromContextOverflow|TestExecuteOverflowRetryLimit|TestExecuteNonOverflowErrorFailsImmediately' -v`
Expected: FAIL（`Execute` 尚未实现重试逻辑，`callCount=1`；或编译失败因 `json`/`runtimecore` 未导入——按编译错误补 import）。

- [ ] **Step 3: 实现接线**

在 `delegation_local.go`：

**3a. adapter 结构体新增字段**：

```go
type localDelegatedAgentAdapter struct {
	store              *ConversationFileStore
	usageStore         *UsageFileStore
	compiler           PromptCompiler
	provider           ProviderGateway
	recorder           modeladapter.LLMArtifactObserver
	resolveBudget      func(string, string, *ConversationFile, CompiledConversation) (int, map[string]any)
	toolExecutor       LocalDelegatedToolExecutor
	maxPasses          int
	sequence           atomic.Uint64
	resolveContextWindow func(string) uint32
}
```

**3b. `newLocalDelegatedAgentAdapter` 注入**：

```go
	resolveContextWindow: service.resolveContextWindowTokens,
```

**3c. `Execute` 循环改造**（替换现有 `for providerPass := 1; providerPass <= maxPasses; providerPass++` 循环体头部与失败分支）：

```go
	overflowRetries := 0
	for providerPass := 1; providerPass <= maxPasses; providerPass++ {
		if err := ctx.Err(); err != nil { /* 保持原样 */ }
		delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusRunning, providerPass, nil, nil, "delegated worker is running", "")

		// 主动阈值压缩：每轮 pass 前检查，超预算则压缩（snip + 丢弃早期消息）。
		messages = compactDelegatedMessagesBeforePass(adapter, request, conversation, messages, providerPass)

		pass, err := adapter.runProviderPass(ctx, request, identity, conversation, compiled, messages, providerPass)
		if err != nil {
			if delegatedContextOverflowError(err) && overflowRetries < delegatedCompactionRetryLimit {
				overflowRetries++
				compactedMessages := compactDelegatedMessagesBeforePass(adapter, request, conversation, messages, providerPass)
				if !sameDelegatedMessages(compactedMessages, messages) {
					messages = compactedMessages
				}
				log.Printf("forwarder delegated context overflow retry task_id=%s provider_pass=%d retry=%d/%d", strings.TrimSpace(identity.taskID), providerPass, overflowRetries, delegatedCompactionRetryLimit)
				continue
			}
			/* 原失败分支保持不变 */
			delegation.PublishTaskCheckpoint(ctx, request, delegation.SupervisionStatusFailed, providerPass, nil, nil, "delegated provider failed", delegation.SanitizeSupervisorText(err.Error(), request.WorkspaceHint))
			return delegation.TaskResult{Error: err, Output: strings.TrimSpace(pass.text), ToolCallCount: toolCallCount, Metadata: identity.metadata(providerPass)}
		}
		/* 其余保持原样 */
	}
```

**3d. 新增两个 helper**（`delegation_local.go` 文件内）：

```go
// compactDelegatedMessagesBeforePass 执行主动阈值压缩并打日志，返回压缩后的 messages。
func compactDelegatedMessagesBeforePass(adapter *localDelegatedAgentAdapter, request delegation.TaskRequest, conversation *ConversationFile, messages []modeladapter.Message, providerPass int) []modeladapter.Message {
	if adapter == nil {
		return messages
	}
	window := int64(0)
	if adapter.resolveContextWindow != nil {
		window = int64(adapter.resolveContextWindow(strings.TrimSpace(request.ModelID)))
	}
	budget := delegatedContextBudgetForWindow(window)
	if budget <= 0 {
		return messages
	}
	stats := &delegatedCompactionStats{}
	out, changed := maybeCompactDelegatedMessages(messages, budget, stats)
	if changed {
		log.Printf("forwarder delegated context compacted task_id=%s provider_pass=%d snip=%d dropped=%d msgs=%d->%d tokens=%d->%d",
			strings.TrimSpace(request.ID), providerPass, stats.SnipCount, stats.DroppedCount, len(messages), len(out), stats.BeforeTokens, stats.AfterTokens)
	}
	return out
}

func sameDelegatedMessages(a, b []modeladapter.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content || a[i].ToolCallID != b[i].ToolCallID {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/backend/forwarder/ -run 'TestExecuteRecoversFromContextOverflow|TestExecuteOverflowRetryLimit|TestExecuteNonOverflowErrorFailsImmediately' -v`
Expected: PASS。

- [ ] **Step 5: 全量编译 + 相关测试回归**

Run: `go build ./... && go test ./internal/backend/forwarder/ -run 'TestDelegated|TestExecuteRecovers|TestExecuteOverflow|TestExecuteNonOverflow|TestSnip|TestDrop|TestMaybeCompact' -count=1`
Expected: 全部 PASS；`go build ./...` 无错误。

- [ ] **Step 6: 提交**

```bash
git add internal/backend/forwarder/delegation_local.go internal/backend/forwarder/delegation_local_compaction_test.go
git commit -m "feat(forwarder): 委派 worker 接入主动压缩与超限自救"
```
