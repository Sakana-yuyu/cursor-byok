package forwarder

import (
	"context"
	"fmt"
	"strings"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
	"cursor/internal/logger"
)

const (
	delegatedSnipThresholdBytes    = 16 * 1024
	delegatedSnipTargetBytes       = 4 * 1024
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
		// The floor is a large-window quality guard, never permission to send an
		// input that leaves no room for the provider output safety reserve.
		maximumInputBudget := window - providerOutputSafetyTokens
		if maximumInputBudget < 1 {
			return 1
		}
		if maximumInputBudget < delegatedCompactionBudgetFloor {
			return maximumInputBudget
		}
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

type delegatedCompactionStats struct {
	SnipCount        int
	DroppedCount     int
	BeforeTokens     int64
	AfterTokens      int64
	BeforeGroupCount int
	AfterGroupCount  int
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

const delegatedCompactionKeepTurns = 4

type delegatedMessageGroup struct {
	start     int
	end       int
	protected bool
	hasTools  bool
}

// buildDelegatedMessageWindow clones and windows delegated-worker messages without
// applying any replay normalization. Tool batches therefore either remain complete
// or are rejected/dropped as one unit.
func buildDelegatedMessageWindow(messages []modeladapter.Message, budget int64, stats *delegatedCompactionStats) ([]modeladapter.Message, bool, error) {
	working := cloneDelegatedMessages(messages)
	if budget <= 0 || len(working) == 0 {
		return working, false, nil
	}
	groups, err := groupDelegatedMessages(working)
	if err != nil {
		return nil, false, err
	}
	if stats != nil {
		stats.BeforeTokens = estimateModelMessagesTokens(working)
		stats.BeforeGroupCount = len(groups)
	}
	changed := false

	// Snip only optional older tool batches. The newest batch is protected as a
	// complete group so a retry never damages the currently active tool chain.
	for estimateModelMessagesTokens(working) > budget {
		target := -1
		for _, group := range groups {
			if group.protected || !group.hasTools {
				continue
			}
			for index := group.start; index <= group.end; index++ {
				message := working[index]
				if strings.TrimSpace(message.Role) == "tool" && len(message.Content) > delegatedSnipThresholdBytes {
					target = index
					break
				}
			}
			if target >= 0 {
				break
			}
		}
		if target < 0 {
			break
		}
		working[target].Content = working[target].Content[:delegatedSnipTargetBytes] + delegatedToolResultOmittedText(working[target].Name)
		changed = true
		if stats != nil {
			stats.SnipCount++
		}
	}

	keep := make([]bool, len(groups))
	for index := range keep {
		keep[index] = true
	}
	for estimateKeptDelegatedGroups(working, groups, keep) > budget {
		dropped := false
		for index, group := range groups {
			if !keep[index] || group.protected {
				continue
			}
			keep[index] = false
			changed = true
			dropped = true
			if stats != nil {
				stats.DroppedCount++
			}
			break
		}
		if !dropped {
			break
		}
	}
	out := make([]modeladapter.Message, 0, len(working))
	for index, group := range groups {
		if keep[index] {
			out = append(out, working[group.start:group.end+1]...)
		}
	}
	if err := validateDelegatedMessageStructure(out); err != nil {
		return nil, false, err
	}
	if stats != nil {
		stats.AfterTokens = estimateModelMessagesTokens(out)
		stats.AfterGroupCount = len(groups) - stats.DroppedCount
	}
	return out, changed, nil
}

func groupDelegatedMessages(messages []modeladapter.Message) ([]delegatedMessageGroup, error) {
	groups := make([]delegatedMessageGroup, 0, len(messages))
	callIDs := make(map[string]struct{})
	firstUser := -1
	lastUser := -1
	for index, message := range messages {
		if strings.TrimSpace(message.Role) == "user" {
			if firstUser < 0 {
				firstUser = index
			}
			lastUser = index
		}
	}
	for index := 0; index < len(messages); {
		message := messages[index]
		role := strings.TrimSpace(message.Role)
		switch role {
		case "tool":
			return nil, fmt.Errorf("orphan delegated tool result %q", strings.TrimSpace(message.ToolCallID))
		case "user":
			end := index
			// A non-tool turn starts at a user message and includes its assistant
			// reply. Keep the initial task and latest current prompt protected,
			// but never leave historical request/reply pairs half-visible.
			if index+1 < len(messages) && strings.TrimSpace(messages[index+1].Role) == "assistant" && len(messages[index+1].ToolCalls) == 0 {
				end = index + 1
			}
			groups = append(groups, delegatedMessageGroup{start: index, end: end, protected: index == firstUser || index == lastUser})
			index = end + 1
		case "assistant":
			if len(message.ToolCalls) == 0 {
				groups = append(groups, delegatedMessageGroup{start: index, end: index})
				index++
				continue
			}
			expected := make(map[string]struct{}, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				callID := strings.TrimSpace(call.ID)
				if callID == "" {
					return nil, fmt.Errorf("delegated assistant tool call is missing an id")
				}
				if _, exists := callIDs[callID]; exists {
					return nil, fmt.Errorf("duplicate delegated tool call id %q", callID)
				}
				callIDs[callID] = struct{}{}
				expected[callID] = struct{}{}
			}
			end := index
			if index+1 < len(messages) && strings.TrimSpace(messages[index+1].Role) != "tool" {
				return nil, fmt.Errorf("incomplete delegated tool batch after assistant message %d", index)
			}
			seen := make(map[string]struct{}, len(expected))
			for end+1 < len(messages) && strings.TrimSpace(messages[end+1].Role) == "tool" {
				end++
				callID := strings.TrimSpace(messages[end].ToolCallID)
				if _, exists := expected[callID]; !exists {
					return nil, fmt.Errorf("delegated tool result %q does not match its assistant batch", callID)
				}
				if _, exists := seen[callID]; exists {
					return nil, fmt.Errorf("duplicate delegated tool result %q", callID)
				}
				seen[callID] = struct{}{}
			}
			if len(seen) != len(expected) {
				return nil, fmt.Errorf("incomplete delegated tool batch after assistant message %d", index)
			}
			groups = append(groups, delegatedMessageGroup{start: index, end: end, hasTools: true})
			index = end + 1
		default:
			groups = append(groups, delegatedMessageGroup{start: index, end: index, protected: role == "system" || index == firstUser || index == lastUser})
			index++
		}
	}
	if len(groups) > 0 {
		groups[len(groups)-1].protected = true
	}
	return groups, nil
}

func estimateKeptDelegatedGroups(messages []modeladapter.Message, groups []delegatedMessageGroup, keep []bool) int64 {
	total := int64(0)
	for index, group := range groups {
		if keep[index] {
			total += estimateModelMessagesTokens(messages[group.start : group.end+1])
		}
	}
	return total
}

func validateDelegatedMessageStructure(messages []modeladapter.Message) error {
	_, err := groupDelegatedMessages(messages)
	return err
}

// dropDelegatedEarlyMessages 从最旧开始按轮成组丢弃 assistant 及其后所有连续
// role==tool 消息，直到预算内。一条 assistant 可能带多个 ToolCalls（并行工具调用），
// 对应其后多条连续 tool 消息，必须整组删除，否则会残留孤立 tool 消息导致
// provider 报 "tool result without matching tool_use"。
// 索引 0（system）与索引 1（首条 user）永不丢弃；保留最近 delegatedCompactionKeepTurns 轮。
// 注意：调用方必须使用返回的切片；底层数组可能已被原地修改（append 覆盖）。
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
			// 找到该 assistant 之后所有连续 role==tool 消息的最后一个索引 end
			// （一轮并行调用多个工具 = 1 条 assistant + N 条连续 tool）。
			end := i + 1
			for end < len(messages) && strings.TrimSpace(messages[end].Role) == "tool" {
				end++
			}
			end-- // 最后一个连续 tool 索引；若无 tool（end==i）则不构成可丢轮
			if end == i {
				continue
			}
			messages = append(messages[:i], messages[end+1:]...)
			dropped = true
			changed = true
			if stats != nil {
				stats.DroppedCount++
			}
			removed := end - i + 1
			if keepStart >= removed {
				keepStart -= removed // 删除发生在保留起点之前，起点前移 removed
			} else {
				keepStart = 0
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
	out, changed, err := buildDelegatedMessageWindow(messages, budget, stats)
	if err != nil {
		return messages, false
	}
	return out, changed
}

// ─── 子代理 LLM 摘要压缩（借鉴 aider 递归分治 + Cline findCutIndex tool 配对保护） ───

const (
	delegatedCompactionMaxDepth           = 3
	delegatedCompactionPreserveRatio      = 0.5 // 尾部保留 budget 的 50%
	delegatedCompactionSummaryMaxTokens   = 2048
	delegatedFallbackSummaryMaxChars      = 2000
	delegatedFallbackSnippetMaxChars      = 400
	delegatedAggressiveSnipThresholdBytes = 8 * 1024 // 增强 snip：非最新轮 8K 即截断
)

// isDelegatedSafeCutBoundary 判断给定索引是否是安全的压缩切割边界。
// 借鉴 Cline 的 findCutIndex：绝不在 tool_result-only user message 处切割，
// 否则会孤儿化前一个 assistant 的 tool_use（tool_use 在 assistant 中，tool_result 在 user 中，
// 切割在 user 处会留下孤立的 tool_result）。
func isDelegatedSafeCutBoundary(messages []modeladapter.Message, index int) bool {
	if index < 0 || index >= len(messages) {
		return false
	}
	msg := messages[index]
	role := strings.TrimSpace(msg.Role)
	// assistant 消息（无论是否有 ToolCalls）是安全切割点
	if role == "assistant" {
		return true
	}
	// user 消息：如果是纯 tool_result（有 ToolCallID）则不安全
	if role == "user" {
		return strings.TrimSpace(msg.ToolCallID) == ""
	}
	// system 消息安全
	return role == "system"
}

// findDelegatedCompactionCutIndex 找到安全切割点：保留尾部 preserveRecentTokens 的消息不压缩。
// 借鉴 Cline findCutIndex + aider summarize_real 的 tail 保留逻辑。
// 返回切割点索引（该索引及之前的消息将被压缩，之后的消息保留）。
func findDelegatedCompactionCutIndex(messages []modeladapter.Message, preserveRecentTokens int64) int {
	if len(messages) <= 2 {
		return -1
	}
	// 从末尾向前累积 token，直到达到 preserveRecentTokens
	total := int64(0)
	candidate := len(messages)
	for i := len(messages) - 1; i >= 1; i-- {
		total += estimateModelMessagesTokens(messages[i : i+1])
		if total >= preserveRecentTokens {
			candidate = i
			break
		}
	}
	if candidate >= len(messages) {
		candidate = len(messages) - 1
	}
	// 向前调整到安全切割边界（不在 tool_result-only user 处切割）
	for candidate > 1 && !isDelegatedSafeCutBoundary(messages, candidate) {
		candidate--
	}
	// 至少保留 system（索引 0）+ 首条 user（索引 1）不压缩
	if candidate <= 1 {
		return -1
	}
	return candidate
}

// delegatedCompactionSummaryPrompt 构建摘要 system prompt（借鉴 aider 的第一人称风格）。
func delegatedCompactionSummaryPrompt() string {
	return `你是一个对话摘要器。请将以下子代理工作历史压缩为简洁的摘要。
要求：
1. 以第一人称（"我"）书写，仿佛你在向自己回顾之前的工作
2. 保留关键信息：文件名、函数名、工具调用结果、发现的问题、已做的决策
3. 对较早的工作少写细节，对最近的工作多写细节
4. 不要包含代码块
5. 摘要长度不超过 2000 字`
}

// serializeDelegatedMessagesForSummary 把消息序列化为文本供摘要（借鉴 aider 的 # USER / # ASSISTANT 格式）。
func serializeDelegatedMessagesForSummary(messages []modeladapter.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		text := strings.TrimSpace(msg.Content)
		switch role {
		case "user":
			if strings.TrimSpace(msg.ToolCallID) != "" {
				// tool result
				toolName := strings.TrimSpace(msg.Name)
				if toolName == "" {
					toolName = "tool"
				}
				builder.WriteString("# TOOL (")
				builder.WriteString(toolName)
				builder.WriteString(")\n")
			} else {
				builder.WriteString("# USER\n")
			}
		case "assistant":
			builder.WriteString("# ASSISTANT\n")
		case "system":
			builder.WriteString("# SYSTEM\n")
		default:
			builder.WriteString("# ")
			builder.WriteString(strings.ToUpper(role))
			builder.WriteString("\n")
		}
		if len(text) > delegatedFallbackSnippetMaxChars*2 {
			text = text[:delegatedFallbackSnippetMaxChars*2] + "..."
		}
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// generateDelegatedCompactionSummary 调用 LLM 生成子代理工作历史摘要。
// 不依赖 ActiveStream（子代理没有），直接用 adapter.provider.StartStream。
func generateDelegatedCompactionSummary(ctx context.Context, adapter *localDelegatedAgentAdapter, request delegation.TaskRequest, messagesToSummarize []modeladapter.Message, modelCallID string) (string, error) {
	if adapter == nil || adapter.provider == nil || len(messagesToSummarize) == 0 {
		return "", fmt.Errorf("delegated compaction summary unavailable")
	}
	historyText := serializeDelegatedMessagesForSummary(messagesToSummarize)
	summaryMessages := []modeladapter.Message{
		{Role: "system", Content: delegatedCompactionSummaryPrompt()},
		{Role: "user", Content: "对话历史：\n\n" + historyText},
	}
	accumulated := ""
	err := adapter.provider.StartStream(ctx, ProviderRequest{
		RequestID:      strings.TrimSpace(request.ParentRequest),
		ConversationID: strings.TrimSpace(request.ConversationID),
		RunID:          strings.TrimSpace(request.ParentRequest),
		ModelCallID:    modelCallID,
		ModelID:        strings.TrimSpace(request.ModelID),
		ModelName:      strings.TrimSpace(request.ModelName),
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		Messages:       summaryMessages,
		MaxTokens:      delegatedCompactionSummaryMaxTokens,
		CompileSummary: "delegated compaction summary",
		ArtifactPaths:  &modeladapter.LLMArtifactPaths{},
	}, func(event modeladapter.ModelEvent) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			accumulated += event.Text
		case modeladapter.ModelEventKindToolLikeCompleted:
			return fmt.Errorf("compaction summary must not invoke tools")
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return fmt.Errorf("delegated compaction summary provider error")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(accumulated)
	if summary == "" {
		return "", fmt.Errorf("delegated compaction summary produced empty output")
	}
	return summary, nil
}

// buildDelegatedFallbackSummary 在 LLM 摘要失败时生成确定性摘要（不调 LLM）。
// 提取每轮的 user text + assistant text + tool name，截断到指定字符数。
func buildDelegatedFallbackSummary(messages []modeladapter.Message) string {
	var builder strings.Builder
	builder.WriteString("以下是之前工作的摘要（LLM 摘要失败，使用确定性回退）：\n\n")
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		if len(text) > delegatedFallbackSnippetMaxChars {
			text = text[:delegatedFallbackSnippetMaxChars] + "..."
		}
		switch role {
		case "user":
			if strings.TrimSpace(msg.ToolCallID) != "" {
				toolName := strings.TrimSpace(msg.Name)
				if toolName == "" {
					toolName = "tool"
				}
				builder.WriteString("[工具结果(")
				builder.WriteString(toolName)
				builder.WriteString("): ")
				builder.WriteString(text)
				builder.WriteString("]\n")
			} else {
				builder.WriteString("[用户: ")
				builder.WriteString(text)
				builder.WriteString("]\n")
			}
		case "assistant":
			builder.WriteString("[助手: ")
			builder.WriteString(text)
			builder.WriteString("]\n")
		}
	}
	result := builder.String()
	if len(result) > delegatedFallbackSummaryMaxChars {
		result = result[:delegatedFallbackSummaryMaxChars] + "\n...(截断)"
	}
	return result
}

// delegatedSummaryInputBudget 计算摘要 LLM 请求的安全输入预算。
// 摘要请求与主请求共享同一 provider 通道，输入不能超过窗口推导预算
// （delegatedContextBudgetForWindow）；同时不高于当前 pass 的压缩预算，
// 避免摘要输入比主请求本身还大。窗口信息缺失（<=0）时退回 pass 预算。
func delegatedSummaryInputBudget(adapter *localDelegatedAgentAdapter, request delegation.TaskRequest, passBudget int64) int64 {
	budget := int64(0)
	if adapter != nil && adapter.resolveContextWindow != nil {
		budget = delegatedContextBudgetForWindow(int64(adapter.resolveContextWindow(strings.TrimSpace(request.ModelID))))
	}
	if budget <= 0 || (passBudget > 0 && passBudget < budget) {
		budget = passBudget
	}
	return budget
}

// compactDelegatedMessagesWithSummary 递归分治压缩子代理消息（借鉴 aider summarize_real）。
// 流程：snip 超长 tool result -> findCutIndex 安全切割 -> LLM 摘要头部 -> 重建 -> 递归。
// depth 控制递归深度（最多 3 层），LLM 失败时降级为确定性摘要。
func compactDelegatedMessagesWithSummary(ctx context.Context, adapter *localDelegatedAgentAdapter, request delegation.TaskRequest, messages []modeladapter.Message, budget int64, depth int) ([]modeladapter.Message, bool, error) {
	if budget <= 0 || len(messages) <= 2 {
		return messages, false, nil
	}
	// 第 1 步：snip 超长 tool result（增强版：非最新轮 8K 即截断）
	working := cloneDelegatedMessages(messages)
	snipChanged := snipDelegatedAggressiveToolResults(working, budget)
	currentTokens := estimateModelMessagesTokens(working)
	if currentTokens <= budget {
		return working, snipChanged, nil
	}

	// 第 2 步：找安全切割点
	preserveTokens := int64(float64(budget) * delegatedCompactionPreserveRatio)
	cutIndex := findDelegatedCompactionCutIndex(working, preserveTokens)
	if cutIndex <= 1 {
		// 无法安全切割，降级到结构化裁剪
		return working, snipChanged, nil
	}

	// 第 3 步：对切割点之前的消息生成摘要
	messagesToSummarize := cloneDelegatedMessages(working[:cutIndex])
	tail := working[cutIndex:]
	// 摘要 LLM 与主请求共享同一 provider 通道，输入必须落在窗口内；
	// 配置窗口偏大时 working 可能未被压缩到真实窗口内，摘要请求会必失败。
	// 先把摘要输入压到摘要安全预算内（snip 超长 tool result，仍超则裁掉尾部）。
	summaryBudget := delegatedSummaryInputBudget(adapter, request, budget)
	if summaryBudget > 0 && estimateModelMessagesTokens(messagesToSummarize) > summaryBudget {
		snipDelegatedAggressiveToolResults(messagesToSummarize, summaryBudget)
		if estimateModelMessagesTokens(messagesToSummarize) > summaryBudget {
			cut := findDelegatedCompactionCutIndex(messagesToSummarize, int64(float64(summaryBudget)*delegatedCompactionPreserveRatio))
			if cut > 1 {
				messagesToSummarize = messagesToSummarize[:cut]
			}
		}
	}
	modelCallID := fmt.Sprintf("%s-compaction-%d", strings.TrimSpace(request.ID), depth+1)
	summary, err := generateDelegatedCompactionSummary(ctx, adapter, request, messagesToSummarize, modelCallID)
	if err != nil && len(messagesToSummarize) > 4 {
		// 摘要请求本身超窗（窗口/估算偏差兜底）：输入再减半重试一次，仍失败才走确定性回退。
		half := messagesToSummarize[:len(messagesToSummarize)/2]
		if retried, retryErr := generateDelegatedCompactionSummary(ctx, adapter, request, half, modelCallID+"-retry"); retryErr == nil {
			err = nil
			summary = retried
			logger.Infof("forwarder delegated compaction summary retried with halved input task_id=%s depth=%d", strings.TrimSpace(request.ID), depth)
		}
	}
	if err != nil {
		logger.Infof("forwarder delegated compaction summary LLM failed task_id=%s depth=%d err=%v, using fallback", strings.TrimSpace(request.ID), depth, err)
		summary = buildDelegatedFallbackSummary(messagesToSummarize)
	}

	// 第 4 步：重建消息 = [system?] + [摘要 user 消息] + [尾部消息]
	var rebuilt []modeladapter.Message
	if strings.TrimSpace(working[0].Role) == "system" {
		rebuilt = append(rebuilt, working[0])
	}
	rebuilt = append(rebuilt, modeladapter.Message{
		Role:    "user",
		Content: "[之前工作摘要]\n" + summary + "\n\n（以上是之前工作的摘要，以下是最近的对话）",
	})
	rebuilt = append(rebuilt, tail...)

	// 校验 tool chain 结构完整性
	if err := validateDelegatedMessageStructure(rebuilt); err != nil {
		logger.Infof("forwarder delegated compaction structure invalid after summary task_id=%s depth=%d err=%v, falling back to structural window", strings.TrimSpace(request.ID), depth, err)
		return working, snipChanged, nil
	}

	// 第 5 步：若仍超限且未达递归深度上限，递归
	rebuiltTokens := estimateModelMessagesTokens(rebuilt)
	if rebuiltTokens > budget && depth < delegatedCompactionMaxDepth {
		return compactDelegatedMessagesWithSummary(ctx, adapter, request, rebuilt, budget, depth+1)
	}
	return rebuilt, true, nil
}

// snipDelegatedAggressiveToolResults 更激进地截断非最新轮的 tool result。
// 比 snipDelegatedOversizedToolResults 更低的阈值（8K vs 16K），在 LLM 摘要前先省成本。
func snipDelegatedAggressiveToolResults(messages []modeladapter.Message, budget int64) bool {
	if budget <= 0 || len(messages) == 0 {
		return false
	}
	changed := false
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
			if len(messages[i].Content) > delegatedAggressiveSnipThresholdBytes {
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
	}
	return changed
}
